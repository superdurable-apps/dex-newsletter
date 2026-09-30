//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/application"
	"github.com/superdurable-apps/dex-newsletter/internal/blogpost"
	"github.com/superdurable-apps/dex-newsletter/internal/testsupport/fakeproviders"

	"github.com/superdurable/dex-connectors-library/sdkgo/localconfig"
)

// A Slack approve counts only for the draft version in the thread, sent after it was posted.
func TestSlackApproveCountsOnlyForThePostedVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	h := newHarness(t)
	reader := h.subscribeReader(ctx, "posted-version")
	flowID, thread := h.startSlackThread(ctx, "Write a blog post about connectors")
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	h.waitForPostedDraft(ctx, flowID, 1)
	// A reviewer reads Draft 1 and composes an approve...
	early := slackReply(thread, "UREVIEWER", "approve")

	// ...while someone saves an edit in the editor.
	token := h.editorToken(ctx, flowID)
	draft, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil {
		t.Fatal(err)
	}
	blog, newsletter := clone(t, draft.Blog), clone(t, draft.Newsletter)
	newsletter.Subject = "Edited after the reviewer read it"
	if saved, err := h.app.Drafts.Save(ctx, flowID, token, blogpost.SaveDraftEditsInput{BaseVersion: 1, Blog: blog, Newsletter: newsletter}); err != nil || saved.Outcome != blogpost.OutcomeSaved {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	// The run records the version it posted; the fake echoes placeholder text, so nothing may parse it.
	h.waitForPostedDraft(ctx, flowID, 2)

	if outcome := h.invokeSlackReview(ctx, early); outcome != blogpost.OutcomeIgnored {
		t.Fatalf("an approve sent before Draft 2 was posted = %s", outcome)
	}
	h.waitForSlackPost(ctx, "Draft 2 was posted after your reply, so I didn't send anything.")
	if view := h.display(ctx, flowID); view["blog-status"] != blogpost.StatusAwaitingReview {
		t.Fatalf("status after the stale approve = %v", view["blog-status"])
	}

	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "approve"))
	h.waitForSlackPost(ctx, "Draft 2 approved by <@UREVIEWER>. Sending the newsletter.")
	if result := h.result(ctx, flowID); result.Status != blogpost.StatusSent {
		t.Fatalf("result = %+v", result)
	}
	_, emails, _ := h.fake.Snapshot()
	for _, email := range emails {
		if email.To == reader && email.Subject != "Edited after the reviewer read it" {
			t.Fatalf("the reader got subject %q", email.Subject)
		}
	}
}

// A Slack reject stops a delivery that Gmail stopped, closing the run as delivery-stopped.
func TestSlackRejectStopsAStoppedDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	h := newHarness(t)
	reader := h.subscribeReader(ctx, "stopped-delivery")
	h.fake.RejectSendTo(reader, 1)
	flowID, thread := h.startSlackThread(ctx, "Write a blog post about connectors")
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	h.waitForPostedDraft(ctx, flowID, 1)
	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "approve"))
	view := h.waitForStatus(ctx, flowID, blogpost.StatusNeedsAttention)
	if !strings.Contains(fmt.Sprint(view["attention-reason"]), "Gmail stopped the delivery") {
		t.Fatalf("attention reason = %v", view["attention-reason"])
	}
	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "reject"))
	if result := h.result(ctx, flowID); result.Status != blogpost.StatusStopped {
		t.Fatalf("result = %+v", result)
	}
}

// Rapid editor saves leave the latest version as the blog artifact; an older save never rolls it back.
func TestRapidEditorSavesKeepTheLatestArtifact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	h := newHarness(t)
	flowID, _ := h.startSlackThread(ctx, "Write a blog post about connectors")
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	token := h.editorToken(ctx, flowID)
	for version, title := range []string{"", "Second title", "Third title"} {
		if version == 0 {
			continue
		}
		draft, err := h.app.Drafts.Get(ctx, flowID, token)
		if err != nil {
			t.Fatal(err)
		}
		blog := clone(t, draft.Blog)
		blog.Title = title
		if saved, err := h.app.Drafts.Save(ctx, flowID, token, blogpost.SaveDraftEditsInput{BaseVersion: draft.DraftVersion, Blog: blog, Newsletter: draft.Newsletter}); err != nil || saved.Outcome != blogpost.OutcomeSaved {
			t.Fatalf("save %d = %+v, %v", version+1, saved, err)
		}
	}
	h.waitForPostedDraft(ctx, flowID, 3)
	view := h.display(ctx, flowID)
	path := fmt.Sprint(view["blog-artifact-path"])
	if !strings.HasSuffix(path, "-v3.html") || view["blog-title"] != "Third title" {
		t.Fatalf("artifact = %s, title = %v", path, view["blog-title"])
	}
	html, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(html), "Third title") {
		t.Fatalf("artifact content (%v) lacks the latest title", err)
	}
	slackPosts, _, _ := h.fake.Snapshot()
	if last := lastContaining(slackPosts, "edited in the editor*"); !strings.HasPrefix(last, "*Draft 3, edited in the editor*") {
		t.Fatalf("the last edited-draft post is %q", firstLine(last))
	}
}

// The review binding must listen to the channel whose threads hold the drafts.
func TestReviewBindingMustShareTheRequestChannel(t *testing.T) {
	h := newHarness(t)
	storePath, err := fakeproviders.WriteConnectionStore(t.TempDir(), h.fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	for _, binding := range document["triggerBindings"].([]any) {
		entry := binding.(map[string]any)
		if entry["bindingName"] == blogpost.ReviewTriggerBinding {
			entry["configuration"].(map[string]any)["channelId"] = "COTHER"
		}
	}
	contents, _ = json.Marshal(document)
	if err := os.WriteFile(storePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := localconfig.LoadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	options := h.options
	options.Store, options.WithoutSlackTrigger = store, false
	options.BlobCacheDirectory = filepath.Join(t.TempDir(), "blobs")
	if app, err := application.New(options); err == nil {
		_ = app.Close()
		t.Fatal("a review binding on another channel was accepted")
	} else if !strings.Contains(err.Error(), "must use the same channel") {
		t.Fatalf("error = %v", err)
	}
}

func lastContaining(posts []string, fragment string) string {
	for index := len(posts) - 1; index >= 0; index-- {
		if strings.Contains(posts[index], fragment) {
			return posts[index]
		}
	}
	return ""
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
