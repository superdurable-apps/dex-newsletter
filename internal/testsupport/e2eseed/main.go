// Command e2eseed prepares one BlogPost run for the browser end-to-end test of the draft editor.
//
// It reads the same configuration (BLOG_NEWSLETTER_CONFIG) and connection store
// (DEX_CONNECTOR_CONFIG_FILE) as the application under test, subscribes two readers through the
// application's subscriber list, and starts a run from a Slack-shaped request. cmd/server cannot
// point the llm connector at a loopback fake, so this command runs its own Worker, with the fake
// Gemini base URL, only while the run researches and writes. Once the draft awaits review it hands
// the run to the application's Worker with UpdateFlowConfig, stops its Worker, and confirms the
// application serves the editor view. Every editor read, preview, save, approval, Slack notice,
// and email after that runs in the application process. It then writes the seed JSON that the
// Playwright editor journey reads. It is test support only and never linked into the application.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/application"
	"github.com/superdurable-apps/dex-newsletter/internal/blogpost"
	"github.com/superdurable-apps/dex-newsletter/internal/config"
	"github.com/superdurable-apps/dex-newsletter/internal/content"
	"github.com/superdurable-apps/dex-newsletter/internal/testsupport/fakeproviders"

	llmrouter "github.com/superdurable/dex-connectors-library/connectors/superdurable/llm"
	"github.com/superdurable/dex-connectors-library/sdkgo/localconfig"
	"github.com/superdurable/dex/sdk-go/dex"
)

// Seed is the JSON the Playwright editor journey reads from E2E_SEED_FILE. There is one document:
// the email is the post, so the post's title is also the subject line.
type Seed struct {
	RunID      string `json:"runId"`
	EditorPath string `json:"editorPath"`
	FakeURL    string `json:"fakeUrl"`
	DexWebURL  string `json:"dexWebUrl"`
	Title      string `json:"title"`
	// Paragraph is the first paragraph of the first section as the model wrote it; the journey replaces it.
	Paragraph  string   `json:"paragraph"`
	Recipients []string `json:"recipients"`
}

type options struct {
	fakeURL            string
	output             string
	appWorkerTarget    string
	flowServiceAddress string
	blobCacheDirectory string
	timeout            time.Duration
}

func main() {
	var opts options
	flag.StringVar(&opts.fakeURL, "fake-url", "", "base URL of e2eproviders (required)")
	flag.StringVar(&opts.output, "out", "", "file that receives the seed JSON (required)")
	flag.StringVar(&opts.appWorkerTarget, "app-worker-target", os.Getenv("DEX_WORKER_TARGET"), "the application's advertised Worker target")
	flag.StringVar(&opts.flowServiceAddress, "flow-service", os.Getenv("DEX_FLOW_SERVICE_ADDRESS"), "Dex FlowService address")
	flag.StringVar(&opts.blobCacheDirectory, "blob-cache-dir", "", "blob cache directory for this command, separate from the application's (required)")
	flag.DurationVar(&opts.timeout, "timeout", 3*time.Minute, "overall deadline")
	flag.Parse()
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "e2eseed:", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	if opts.fakeURL == "" || opts.output == "" || opts.appWorkerTarget == "" || opts.flowServiceAddress == "" || opts.blobCacheDirectory == "" {
		return errors.New("-fake-url, -out, -app-worker-target, -flow-service, and -blob-cache-dir are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	store, err := localconfig.LoadFromEnvironment()
	if err != nil {
		return err
	}
	// New binds nothing; the Worker listens only while Run is active.
	workerAddress, err := freeLoopbackAddress()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	app, err := application.New(application.Options{
		Logger: logger, Config: configuration, Store: store,
		FlowServiceAddress: opts.flowServiceAddress, WorkerBindAddress: workerAddress, WorkerTarget: workerAddress,
		BlobCacheDirectory:  opts.blobCacheDirectory,
		LLMOptions:          []llmrouter.Option{llmrouter.WithProviderBaseURLForTest(llmrouter.ProviderGemini, opts.fakeURL)},
		WithoutSlackTrigger: true,
	})
	if err != nil {
		return fmt.Errorf("create the seeding application: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = app.Close()
		}
	}()

	// The application under test starts the subscriber list. Wait until it answers, so this
	// command's Run only attaches to that list and never becomes the Worker that owns it.
	if err := poll(ctx, "the application's subscriber list", func() (bool, error) {
		_, err := app.Subscribers.ListSubscribers(ctx)
		return err == nil, err
	}); err != nil {
		return err
	}
	stamp := fmt.Sprint(time.Now().UnixNano())
	recipients := []string{"editor-journey-" + stamp + "-a@example.com", "editor-journey-" + stamp + "-b@example.com"}
	for _, address := range recipients {
		if already, err := app.Subscribers.Subscribe(ctx, address); err != nil || already {
			return fmt.Errorf("subscribe %s: already=%v err=%v", address, already, err)
		}
	}

	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(runCtx) }()
	if err := poll(ctx, "the seeding Worker at "+workerAddress, func() (bool, error) {
		select {
		case err := <-runDone:
			runDone <- err
			return false, fmt.Errorf("the seeding application stopped: %v", err)
		default:
		}
		connection, err := net.DialTimeout("tcp", workerAddress, time.Second)
		if err != nil {
			return false, err
		}
		return true, connection.Close()
	}); err != nil {
		return err
	}

	now := time.Now().UTC()
	messageTimestamp := fmt.Sprintf("%d.%06d", now.Unix(), now.Nanosecond()/1000)
	flowID := "blog-post-T1-CBLOG-" + messageTimestamp
	request := blogpost.SlackRequest{
		EventID: "Ev" + stamp, TeamID: "T1", ChannelID: "CBLOG", MessageTimestamp: messageTimestamp, UserID: "UREQ",
		Text: "Write a blog post about connectors from the past 2 weeks", ReceivedAt: now,
	}
	if _, err := app.Client.StartFlow(ctx, app.BlogPosts, flowID, request, dex.StartFlowOptions{}); err != nil {
		return fmt.Errorf("start %s: %w", flowID, err)
	}
	var view map[string]any
	if err := poll(ctx, flowID+" awaiting review", func() (bool, error) {
		var current map[string]any
		if err := app.Client.InvokeRPC(ctx, flowID, app.BlogPosts.GetDexDisplay, nil, &current); err != nil {
			return false, err
		}
		view = current
		switch current["blog-status"] {
		case blogpost.StatusAwaitingReview:
			return true, nil
		case blogpost.StatusNeedsAttention, blogpost.StatusNotARequest, blogpost.StatusNoChanges, blogpost.StatusRejected:
			return false, stopPolling{fmt.Errorf("%s stopped at %v: %v", flowID, current["blog-status"], current["attention-reason"])}
		}
		return false, fmt.Errorf("status %v", current["blog-status"])
	}); err != nil {
		return err
	}
	title, editorURL := fmt.Sprint(view["blog-title"]), fmt.Sprint(view["editor-url"])
	// The review post is the last provider effect before the run waits for an editor.
	reviewPost := "*Draft 1 ready for review*: " + content.SlackText(title)
	if err := poll(ctx, "the Slack review post", func() (bool, error) {
		state, err := fakeState(ctx, opts.fakeURL)
		if err != nil {
			return false, err
		}
		for _, post := range state.SlackPosts {
			if strings.Contains(post, reviewPost) {
				return true, nil
			}
		}
		return false, fmt.Errorf("%d Slack posts, none with %q", len(state.SlackPosts), reviewPost)
	}); err != nil {
		return err
	}

	// Hand the waiting run to the application's Worker, then stop this one.
	target := dex.WorkerTarget{Address: opts.appWorkerTarget}
	if err := app.Client.UpdateFlowConfig(ctx, flowID, dex.FlowConfig{WorkerTarget: &target}); err != nil {
		return fmt.Errorf("hand %s to the application's Worker: %w", flowID, err)
	}
	stopRun()
	if err := <-runDone; err != nil {
		return fmt.Errorf("stop the seeding Worker: %w", err)
	}
	closed = true
	if err := app.Close(); err != nil {
		return fmt.Errorf("close the seeding application: %w", err)
	}

	editor, err := url.Parse(editorURL)
	if err != nil || !strings.HasPrefix(editor.Path, "/edit/") || editor.Query().Get("token") == "" {
		return fmt.Errorf("the run's editor-url %q is not an editor link", editorURL)
	}
	// Only the application's Worker can answer now; this proves the handover.
	draftURL := editor.Scheme + "://" + editor.Host + "/api/drafts/" + url.PathEscape(flowID) + "?" + url.Values{"token": {editor.Query().Get("token")}}.Encode()
	paragraph := ""
	if err := poll(ctx, "the application's editor view", func() (bool, error) {
		var draft struct {
			Status       string `json:"status"`
			Editable     bool   `json:"editable"`
			DraftVersion int64  `json:"draftVersion"`
			Blog         struct {
				Title    string `json:"title"`
				Sections []struct {
					Paragraphs []string `json:"paragraphs"`
				} `json:"sections"`
			} `json:"blog"`
		}
		if err := getJSON(ctx, draftURL, &draft); err != nil {
			return false, err
		}
		if draft.Status != blogpost.StatusAwaitingReview || !draft.Editable || draft.DraftVersion != 1 {
			return false, stopPolling{fmt.Errorf("the application serves status=%s editable=%v version=%d", draft.Status, draft.Editable, draft.DraftVersion)}
		}
		// The journey edits the title and the first paragraph, so the draft must have both.
		if draft.Blog.Title != title || len(draft.Blog.Sections) == 0 || len(draft.Blog.Sections[0].Paragraphs) == 0 {
			return false, stopPolling{fmt.Errorf("the editor serves title %q and %d sections, want title %q and a first paragraph", draft.Blog.Title, len(draft.Blog.Sections), title)}
		}
		paragraph = draft.Blog.Sections[0].Paragraphs[0]
		return true, nil
	}); err != nil {
		return err
	}

	seed := Seed{
		RunID: flowID, EditorPath: editor.RequestURI(), FakeURL: opts.fakeURL, DexWebURL: configuration.DexWebURL,
		Title: title, Paragraph: paragraph, Recipients: recipients,
	}
	contents, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomically(opts.output, append(contents, '\n')); err != nil {
		return err
	}
	fmt.Printf("seeded %s awaiting review; editor %s\n", flowID, seed.EditorPath)
	return nil
}

// stopPolling ends poll at once with its error.
type stopPolling struct{ error }

// poll calls check every 100ms until it succeeds, returns stopPolling, or ctx ends.
func poll(ctx context.Context, what string, check func() (bool, error)) error {
	var last error
	for {
		done, err := check()
		if done {
			return nil
		}
		var stop stopPolling
		if errors.As(err, &stop) {
			return fmt.Errorf("waiting for %s: %w", what, stop.error)
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w (last: %v)", what, ctx.Err(), last)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func fakeState(ctx context.Context, fakeURL string) (fakeproviders.State, error) {
	var state fakeproviders.State
	err := getJSON(ctx, strings.TrimRight(fakeURL, "/")+fakeproviders.StatePath, &state)
	return state, err
}

func getJSON(ctx context.Context, target string, value any) error {
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d: %s", response.Request.URL.Path, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, value)
}

func freeLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

func writeFileAtomically(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".e2eseed-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(contents); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
