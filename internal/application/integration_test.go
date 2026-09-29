//go:build integration

package application_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/application"
	"github.com/superdurable-apps/dex-newsletter/internal/blogpost"
	"github.com/superdurable-apps/dex-newsletter/internal/config"
	"github.com/superdurable-apps/dex-newsletter/internal/subscribers"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	llmrouter "github.com/superdurable/dex-connectors-library/connectors/superdurable/llm"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/localconfig"
	"github.com/superdurable/dex/sdk-go/dex"
)

type harness struct {
	t         *testing.T
	fake      *providers
	options   application.Options
	app       *application.Application
	cancelRun context.CancelFunc
	runDone   chan error
	artifacts string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fake := newProviders(t)
	store, err := localconfig.LoadFile(writeConnectionStore(t, fake.URL))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	configuration, err := config.Parse([]byte(fmt.Sprintf(`{
		"github": {"owners": ["acme"]},
		"dexWebUrl": "http://127.0.0.1:8842",
		"blog": {"artifactDirectory": %q, "postUrlTemplate": "https://blog.acme.test/{slug}", "publicationName": "Acme Engineering"},
		"newsletter": {"publicBaseUrl": "https://news.acme.test", "unsubscribeKeyFile": %q}
	}`, filepath.Join(directory, "artifacts"), filepath.Join(directory, "unsubscribe.key"))))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, fake: fake, artifacts: filepath.Join(directory, "artifacts"), options: application.Options{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})), Config: configuration, Store: store,
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"), WorkerBindAddress: os.Getenv("DEX_WORKER_BIND_ADDRESS"),
		WorkerTarget: os.Getenv("DEX_WORKER_TARGET"), BlobCacheDirectory: filepath.Join(directory, "blobs"),
		LLMOptions:          []llmrouter.Option{llmrouter.WithProviderBaseURLForTest(llmrouter.ProviderGemini, fake.URL)},
		WithoutSlackTrigger: true,
	}}
	t.Cleanup(h.stop)
	h.start()
	return h
}

func (h *harness) start() {
	h.t.Helper()
	app, err := application.New(h.options)
	if err != nil {
		h.t.Fatalf("create application: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.app, h.cancelRun, h.runDone = app, cancel, make(chan error, 1)
	go func() { h.runDone <- app.Run(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := app.Subscribers.ListSubscribers(attemptCtx)
		attemptCancel()
		if err == nil {
			return
		}
		select {
		case runErr := <-h.runDone:
			h.runDone <- runErr
			h.t.Fatalf("the application stopped: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("the subscriber list never opened: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (h *harness) stop() {
	if h.app == nil {
		return
	}
	h.cancelRun()
	<-h.runDone
	if err := h.app.Close(); err != nil {
		h.t.Logf("close application: %v", err)
	}
	h.app = nil
}

// deliverSlackMessage routes one Slack root message through the same Trigger target main uses.
func (h *harness) deliverSlackMessage(ctx context.Context, eventID, timestamp, text string) string {
	h.t.Helper()
	filter, err := blogpost.NewRequestTriggerFilter(slack.ChannelThreadCreatedTriggerConfiguration{ChannelID: "CBLOG"})
	if err != nil {
		h.t.Fatal(err)
	}
	target := sdkgo.NewDexFlowTriggerTarget(h.app.Client, h.app.BlogPosts, filter, blogpost.ResolveRequestFlowID, blogpost.MapToSlackRequest)
	event := sdkgo.TriggerEvent[slack.MessageEvent]{ID: eventID, OccurredAt: time.Now().UTC(), Payload: slack.MessageEvent{
		TeamID: "T1", ChannelID: "CBLOG", Timestamp: timestamp, ThreadTimestamp: timestamp, UserID: "U1", Text: text,
	}}
	if err := target.HandleTrigger(ctx, event); err != nil {
		h.t.Fatalf("deliver Slack event: %v", err)
	}
	return blogpost.ResolveRequestFlowID(event)
}

func (h *harness) display(ctx context.Context, flowID string) map[string]any {
	h.t.Helper()
	var view map[string]any
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.GetDexDisplay, nil, &view); err != nil {
		h.t.Fatalf("read display of %s: %v", flowID, err)
	}
	return view
}

func (h *harness) waitForStatus(ctx context.Context, flowID, status string) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	last := map[string]any{}
	for time.Now().Before(deadline) {
		var view map[string]any
		if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.GetDexDisplay, nil, &view); err == nil {
			last = view
			if view["blog-status"] == status {
				return view
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never reached %q; last view: status=%v reason=%v", flowID, status, last["blog-status"], last["attention-reason"])
	return nil
}

func (h *harness) result(ctx context.Context, flowID string) blogpost.Result {
	h.t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := h.app.Client.WaitForFlow(waitCtx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	if err != nil {
		h.t.Fatalf("wait for %s: %v", flowID, err)
	}
	if result.Status != dex.FlowCompleted {
		h.t.Fatalf("%s ended %s", flowID, result.Status)
	}
	var output blogpost.Result
	if err := result.DecodeSingleOutput(&output); err != nil {
		h.t.Fatal(err)
	}
	return output
}

func TestBlogPostFromSlackToNewsletter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)

	for _, address := range []string{"Reader.One@Example.com", "reader.two@example.com"} {
		if already, err := h.app.Subscribers.Subscribe(ctx, address); err != nil || already {
			t.Fatalf("subscribe %s: already=%v err=%v", address, already, err)
		}
	}
	if already, err := h.app.Subscribers.Subscribe(ctx, "reader.one@example.com"); err != nil || !already {
		t.Fatalf("resubscribe: already=%v err=%v", already, err)
	}
	if _, err := h.app.Subscribers.Subscribe(ctx, "Reader <reader@example.com>"); !errors.Is(err, subscribers.ErrInvalidAddress) {
		t.Fatalf("display-name address error = %v", err)
	}

	stamp := fmt.Sprint(time.Now().UnixNano())
	flowID := h.deliverSlackMessage(ctx, "Ev"+stamp, stamp+".000100", "Write a blog post about connectors from the past 2 weeks")
	if again := h.deliverSlackMessage(ctx, "Ev"+stamp, stamp+".000100", "Write a blog post about connectors from the past 2 weeks"); again != flowID {
		t.Fatalf("redelivery resolved %s, want %s", again, flowID)
	}
	view := h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	if !strings.Contains(fmt.Sprint(view["research-summary"]), "2 merged pull requests and 1 other commits across acme/connectors") {
		t.Fatalf("research summary = %v", view["research-summary"])
	}
	artifact := fmt.Sprint(view["blog-artifact-path"])
	html, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read artifact %s: %v", artifact, err)
	}
	for _, want := range []string{"&lt;script&gt;", `href="https://github.com/acme/connectors/pull/11"`, "<code>stripe</code>", "<style>"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("artifact lacks %q", want)
		}
	}
	for _, unwanted := range []string{"<script>", "evil.example"} {
		if strings.Contains(string(html), unwanted) {
			t.Fatalf("artifact contains %q", unwanted)
		}
	}
	if !strings.HasPrefix(filepath.Base(artifact), "connectors-grow-up-r0") {
		t.Fatalf("artifact name = %s", filepath.Base(artifact))
	}

	// A stale form is refused; a revision reruns writing with the notes.
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.ReviseBlogPost, blogpost.ReviseBlogPostInput{Notes: "Shorter.", Round: 99}, nil); err == nil {
		t.Fatal("a stale revise was accepted")
	}
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.ReviseBlogPost, blogpost.ReviseBlogPostInput{Notes: "Lead with the model picker.", Round: 1}, nil); err != nil {
		t.Fatalf("revise: %v", err)
	}
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.ApproveBlogPost, blogpost.ApproveBlogPostInput{Round: 1}, nil); err == nil {
		t.Fatal("approve during revision was accepted")
	}
	view = h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	if view["blog-title"] != "Connectors, revised" || view["revision-count"] != float64(1) {
		t.Fatalf("after revision title=%v revisions=%v", view["blog-title"], view["revision-count"])
	}
	_, _, llmRequests := h.fake.snapshot()
	if !strings.Contains(llmRequests[len(llmRequests)-2], "Lead with the model picker.") {
		t.Fatal("the revision request did not carry the editor notes")
	}

	// Replace the Worker while the run waits for review.
	h.stop()
	h.start()
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.ApproveBlogPost, blogpost.ApproveBlogPostInput{Round: 2}, nil); err != nil {
		t.Fatalf("approve after Worker replacement: %v", err)
	}
	result := h.result(ctx, flowID)
	if result.Status != blogpost.StatusSent || result.Delivery.Sent != 2 || result.Delivery.Total != 2 {
		t.Fatalf("result = %+v", result)
	}
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.ApproveBlogPost, blogpost.ApproveBlogPostInput{Round: 2}, nil); err == nil {
		t.Fatal("approve after completion was accepted")
	}

	slackPosts, emails, _ := h.fake.snapshot()
	if len(emails) != 2 {
		t.Fatalf("sent %d emails, want 2", len(emails))
	}
	for _, email := range emails {
		if email.Subject != "Connectors grow up" || !strings.Contains(email.HTML, "https://blog.acme.test/connectors-grow-up") {
			t.Fatalf("email = %+v", email)
		}
		link := regexp.MustCompile(`https://news\.acme\.test/unsubscribe\?[^"\s]+`).FindString(email.Text)
		parsed, err := url.Parse(link)
		if err != nil || parsed.Query().Get("email") != email.To {
			t.Fatalf("unsubscribe link %q for %s", link, email.To)
		}
		if _, err := h.app.Subscribers.Unsubscribe(ctx, email.To, strings.Repeat("0", 32)); !errors.Is(err, subscribers.ErrInvalidLink) {
			t.Fatalf("forged token error = %v", err)
		}
		if removed, err := h.app.Subscribers.Unsubscribe(ctx, email.To, parsed.Query().Get("token")); err != nil || !removed {
			t.Fatalf("unsubscribe %s: removed=%v err=%v", email.To, removed, err)
		}
	}
	remaining, err := h.app.Subscribers.ListSubscribers(ctx)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining subscribers = %v, %v", remaining, err)
	}
	joined := strings.Join(slackPosts, "\n")
	for _, want := range []string{"On it.", "Draft ready for review", "/v2/run/BlogPost/" + url.PathEscape(flowID), "Newsletter sent: 2 of 2 sent"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Slack replies lack %q:\n%s", want, joined)
		}
	}
}

func TestNonRequestClosesWithoutResearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h := newHarness(t)
	stamp := fmt.Sprint(time.Now().UnixNano())
	flowID := h.deliverSlackMessage(ctx, "Ev"+stamp, stamp+".000200", "Who wants pizza on Friday?")
	if result := h.result(ctx, flowID); result.Status != blogpost.StatusNotARequest {
		t.Fatalf("result = %+v", result)
	}
}

func TestFailuresWaitForRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)
	h.fake.mutex.Lock()
	h.fake.failWriting = 1
	h.fake.rejectSendTo["second@example.com"] = 1
	h.fake.mutex.Unlock()
	for _, address := range []string{"first@example.com", "second@example.com", "third@example.com"} {
		if _, err := h.app.Subscribers.Subscribe(ctx, address); err != nil {
			t.Fatal(err)
		}
	}
	stamp := fmt.Sprint(time.Now().UnixNano())
	flowID := h.deliverSlackMessage(ctx, "Ev"+stamp, stamp+".000300", "Blog post about connectors please")

	view := h.waitForStatus(ctx, flowID, blogpost.StatusNeedsAttention)
	if !strings.Contains(fmt.Sprint(view["attention-reason"]), "Writing the blog post failed") {
		t.Fatalf("attention reason = %v", view["attention-reason"])
	}
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.RetryBlogPostStage, blogpost.RetryBlogPostStageInput{Round: 1}, nil); err != nil {
		t.Fatalf("retry writing: %v", err)
	}
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.ApproveBlogPost, blogpost.ApproveBlogPostInput{Round: 2}, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	view = h.waitForStatus(ctx, flowID, blogpost.StatusNeedsAttention)
	if !strings.Contains(fmt.Sprint(view["attention-reason"]), "subscriber 2 of 3") {
		t.Fatalf("delivery stop reason = %v", view["attention-reason"])
	}
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.RetryBlogPostStage, blogpost.RetryBlogPostStageInput{Round: 3}, nil); err != nil {
		t.Fatalf("retry delivery: %v", err)
	}
	result := h.result(ctx, flowID)
	if result.Status != blogpost.StatusSent || result.Delivery.Sent != 3 {
		t.Fatalf("result = %+v", result)
	}
	_, emails, _ := h.fake.snapshot()
	sent := map[string]int{}
	for _, email := range emails {
		sent[email.To]++
	}
	if len(emails) != 3 || sent["first@example.com"] != 1 || sent["second@example.com"] != 1 || sent["third@example.com"] != 1 {
		t.Fatalf("emails per recipient = %v", sent)
	}
}

func TestEditorRemovesSubscriberThroughAction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	h := newHarness(t)
	address := fmt.Sprintf("remove-%d@example.com", time.Now().UnixNano())
	if _, err := h.app.Subscribers.Subscribe(ctx, address); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Client.InvokeRPC(ctx, subscribers.FlowID, subscribers.Flow{}.RequestSubscriberRemoval, subscribers.RequestSubscriberRemovalInput{Address: address}, nil); err != nil {
		t.Fatalf("request removal: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		list, err := h.app.Subscribers.ListSubscribers(ctx)
		if err == nil && !contains(list, address) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the removal Action never applied")
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func TestEveryActionRequiresOnePermission(t *testing.T) {
	for _, source := range []string{"../blogpost/flow.go", "../subscribers/flow.go"} {
		contents, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range regexp.MustCompile(`ActionRequiresPermission\("([^"]+)"\)`).FindAllStringSubmatch(string(contents), -1) {
			if match[1] != "newsletter.manage" {
				t.Errorf("%s: Action requires %q, want newsletter.manage", source, match[1])
			}
		}
	}
}
