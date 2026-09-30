//go:build integration

package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/blogpost"
	"github.com/superdurable-apps/dex-newsletter/internal/content"
	"github.com/superdurable-apps/dex-newsletter/internal/subscribers"
	"github.com/superdurable-apps/dex-newsletter/internal/testsupport/fakeproviders"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	llmrouter "github.com/superdurable/dex-connectors-library/connectors/superdurable/llm"
	"github.com/superdurable/dex-connectors-library/sdkgo"
)

func TestSlackThreadReviewLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)
	gate := h.gateRevisions()
	reader := h.subscribeReader(ctx, "slack-review")

	flowID, thread := h.startSlackThread(ctx, "Write a blog post about connectors from the past 2 weeks")
	view := h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	firstTitle := fmt.Sprint(view["blog-title"])
	post := h.waitForSlackPost(ctx, "*Draft 1 ready for review*: "+content.SlackText(firstTitle))
	for _, want := range []string{"Stripe checkout", "*Newsletter email*", "Connectors grow up", "Reply in this thread",
		fmt.Sprint(view["editor-url"]), "/v2/run/BlogPost/" + url.PathEscape(flowID)} {
		if !strings.Contains(post, want) {
			t.Fatalf("the review post lacks %q:\n%s", want, post)
		}
	}

	// A reply from someone who is not a reviewer never reaches the run.
	h.deliverSlackReply(ctx, slackReply(thread, "UOUTSIDER", "approve"))
	if view := h.display(ctx, flowID); view["blog-status"] != blogpost.StatusAwaitingReview || len(reviewEvents(t, view)) != 0 {
		t.Fatalf("after a non-reviewer reply status=%v history=%v", view["blog-status"], view["review-history"])
	}

	// Feedback revises the draft; the model call is held so the run stays in writing.
	feedback := slackReply(thread, "UREVIEWER", "<@UBOT> Lead with the model picker, please.")
	h.deliverSlackReply(ctx, feedback)
	gate.waitForRevision(t)
	h.waitForSlackPost(ctx, "Thanks <@UREVIEWER>. Revising the draft")
	if view := h.display(ctx, flowID); view["blog-status"] != blogpost.StatusWriting || view["revision-count"] != float64(1) {
		t.Fatalf("while revising status=%v revisions=%v", view["blog-status"], view["revision-count"])
	}

	// Slack redelivers the feedback event: it is recognized and changes nothing.
	h.deliverSlackReply(ctx, feedback)
	if outcome := h.invokeSlackReview(ctx, feedback); outcome != blogpost.OutcomeDuplicate {
		t.Fatalf("redelivered feedback outcome = %q", outcome)
	}

	// A reply while the run is writing gets a hint instead of a decision.
	if outcome := h.invokeSlackReview(ctx, slackReply(thread, "UREVIEWER", "Is the new draft ready?")); outcome != blogpost.OutcomeIgnored {
		t.Fatalf("reply while writing outcome = %q", outcome)
	}
	h.waitForSlackPost(ctx, "I'm not waiting for a review right now ("+blogpost.StatusWriting+")")

	gate.open()
	view = h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	if view["blog-title"] != "Connectors, revised" || view["revision-count"] != float64(1) || view["draft-version"] != float64(2) {
		t.Fatalf("after revision title=%v revisions=%v version=%v", view["blog-title"], view["revision-count"], view["draft-version"])
	}
	h.waitForSlackPost(ctx, "*Draft 2 ready for review*: Connectors, revised")
	_, _, llmRequests := h.fake.Snapshot()
	revisions := revisionRequests(llmRequests)
	if len(revisions) != 1 || !strings.Contains(revisions[0], "Lead with the model picker, please.") || strings.Contains(revisions[0], "UBOT") {
		t.Fatalf("want one revision request carrying the feedback without its mention, got %d:\n%s", len(revisions), strings.Join(revisions, "\n---\n"))
	}
	// The email is revised too: the newsletter writer gets the feedback and the current email.
	var emailRevisions []string
	for _, request := range llmRequests {
		if strings.Contains(request, "Revise the previous email") {
			emailRevisions = append(emailRevisions, request)
		}
	}
	if len(emailRevisions) != 1 || !strings.Contains(emailRevisions[0], "Lead with the model picker, please.") || !strings.Contains(emailRevisions[0], "Previous email:") {
		t.Fatalf("want one email revision carrying the feedback and the previous email, got %d:\n%s", len(emailRevisions), strings.Join(emailRevisions, "\n---\n"))
	}

	// Approve with any case and punctuation sends the revised newsletter.
	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "Approve."))
	h.waitForSlackPost(ctx, "Approved by <@UREVIEWER>. Sending the newsletter.")
	result := h.result(ctx, flowID)
	if result.Status != blogpost.StatusSent || result.Title != "Connectors, revised" || result.Delivery.Total == 0 || result.Delivery.Sent != result.Delivery.Total {
		t.Fatalf("result = %+v", result)
	}
	_, emails, _ := h.fake.Snapshot()
	if received := emailsTo(emails, reader); len(received) != 1 || received[0].Subject != "Connectors grow up" {
		t.Fatalf("emails to %s = %+v", reader, received)
	}
	h.waitForSlackPost(ctx, "Newsletter sent: ")

	slackPosts, _, _ := h.fake.Snapshot()
	for fragment, want := range map[string]int{"Thanks <@UREVIEWER>": 1, "Approved by": 1, "I'm not waiting for a review": 1, "UOUTSIDER": 0, "ready for review*": 2} {
		if got := countContaining(slackPosts, fragment); got != want {
			t.Fatalf("%d Slack posts contain %q, want %d:\n%s", got, fragment, want, strings.Join(slackPosts, "\n---\n"))
		}
	}
	history := reviewEvents(t, h.display(ctx, flowID))
	if len(history) != 2 {
		t.Fatalf("review history = %+v", history)
	}
	for index, want := range []blogpost.ReviewEvent{
		{Source: "slack", Actor: "UREVIEWER", Action: blogpost.OutcomeRevising, Detail: "Lead with the model picker, please."},
		{Source: "slack", Actor: "UREVIEWER", Action: blogpost.OutcomeApproved},
	} {
		got := history[index]
		if got.Source != want.Source || got.Actor != want.Actor || got.Action != want.Action || got.Detail != want.Detail || got.At.IsZero() {
			t.Fatalf("review history[%d] = %+v, want %+v", index, got, want)
		}
	}
}

func TestSlackRejectAndApprovalWithConditions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)
	flowID, thread := h.startSlackThread(ctx, "Write a blog post about connectors from the past 2 weeks")
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)

	// An approval with a condition is feedback, not an approval.
	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "Approve, but fix the subtitle first."))
	h.waitForSlackPost(ctx, "Thanks <@UREVIEWER>. Revising the draft")
	view := h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	if view["blog-title"] != "Connectors, revised" || view["revision-count"] != float64(1) {
		t.Fatalf("after conditional approval title=%v revisions=%v", view["blog-title"], view["revision-count"])
	}
	_, _, llmRequests := h.fake.Snapshot()
	if revisions := revisionRequests(llmRequests); len(revisions) != 1 || !strings.Contains(revisions[0], "Approve, but fix the subtitle first.") {
		t.Fatalf("the revision requests do not carry the conditional approval as feedback:\n%s", strings.Join(revisions, "\n---\n"))
	}
	h.waitForSlackPost(ctx, "*Draft 2 ready for review*: Connectors, revised")

	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "REJECT"))
	result := h.result(ctx, flowID)
	if result.Status != blogpost.StatusRejected || !strings.Contains(result.Message, "Rejected in Slack by <@UREVIEWER>.") {
		t.Fatalf("result = %+v", result)
	}
	h.waitForSlackPost(ctx, "An editor rejected the draft. Rejected in Slack by <@UREVIEWER>.")
	slackPosts, emails, _ := h.fake.Snapshot()
	if len(emails) != 0 || countContaining(slackPosts, "Approved by") != 0 {
		t.Fatalf("a rejected run sent %d emails; Slack posts:\n%s", len(emails), strings.Join(slackPosts, "\n---\n"))
	}
	history := reviewEvents(t, h.display(ctx, flowID))
	if len(history) != 2 || history[0].Action != blogpost.OutcomeRevising || history[0].Detail != "Approve, but fix the subtitle first." ||
		history[1].Action != blogpost.OutcomeRejected || history[1].Actor != "UREVIEWER" {
		t.Fatalf("review history = %+v", history)
	}
}

func TestSlackReviewWhileNeedsAttention(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)
	h.fake.FailBlogWriting(1)
	flowID, thread := h.startSlackThread(ctx, "Blog post about connectors please")
	h.waitForStatus(ctx, flowID, blogpost.StatusNeedsAttention)

	// The editor refuses changes while nothing is in review.
	token := h.editorToken(ctx, flowID)
	draft, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil || draft.Editable || draft.Status != blogpost.StatusNeedsAttention || draft.DraftVersion != 0 || draft.BlogHTML != "" {
		t.Fatalf("draft while stuck = %+v, %v", draft, err)
	}
	if saved, err := h.app.Drafts.Save(ctx, flowID, token, blogpost.SaveDraftEditsInput{}); err != nil || saved.Outcome != blogpost.OutcomeNotInReview {
		t.Fatalf("save while stuck = %+v, %v", saved, err)
	}
	if approved, err := h.app.Drafts.Approve(ctx, flowID, token, 0); err != nil || approved.Outcome != blogpost.OutcomeNotInReview {
		t.Fatalf("approve while stuck = %+v, %v", approved, err)
	}

	// Feedback and approval only get a hint; reject closes the run.
	for _, text := range []string{"Any news on this one?", "approve"} {
		if outcome := h.invokeSlackReview(ctx, slackReply(thread, "UREVIEWER", text)); outcome != blogpost.OutcomeIgnored {
			t.Fatalf("%q while stuck outcome = %q", text, outcome)
		}
	}
	h.waitForSlackPosts(ctx, "This run is stuck: Writing the blog post failed", 2)
	if view := h.display(ctx, flowID); view["blog-status"] != blogpost.StatusNeedsAttention || len(reviewEvents(t, view)) != 0 {
		t.Fatalf("after hints status=%v history=%v", view["blog-status"], view["review-history"])
	}
	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "Rejected!"))
	if result := h.result(ctx, flowID); result.Status != blogpost.StatusRejected || !strings.Contains(result.Message, "Rejected in Slack by <@UREVIEWER>.") {
		t.Fatalf("result = %+v", result)
	}
	if slackPosts, _, _ := h.fake.Snapshot(); countContaining(slackPosts, "Approved by") != 0 {
		t.Fatalf("an approval was acknowledged while stuck:\n%s", strings.Join(slackPosts, "\n---\n"))
	}
}

func TestEditorSavesAndApprovesEditedDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)
	reader := h.subscribeReader(ctx, "editor-approve")
	flowID, _ := h.startSlackThread(ctx, "Write a blog post about connectors from the past 2 weeks")
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)

	// The Dex Web editor link carries this run's signed token; nothing else opens the editor.
	token := h.editorToken(ctx, flowID)
	links := h.editorLinks()
	if token != links.Token(flowID) || !links.Verify(flowID, token) {
		t.Fatalf("editor token %q is not the run's signed token", token)
	}
	for name, forged := range map[string]string{"zeros": strings.Repeat("0", 32), "another run": links.Token(flowID + "0"), "empty": "", "not hex": "edit-me"} {
		if _, err := h.app.Drafts.Get(ctx, flowID, forged); !errors.Is(err, blogpost.ErrInvalidEditorLink) {
			t.Fatalf("get with %s token error = %v", name, err)
		}
	}
	// A signed link to a run that does not exist passes the token check and then fails.
	missing := "blog-post-T1-CBLOG-1.000001"
	if _, err := h.app.Drafts.Get(ctx, missing, links.Token(missing)); !errors.Is(err, blogpost.ErrUnknownRun) {
		t.Fatalf("get of a missing run error = %v", err)
	}

	draft, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != blogpost.StatusAwaitingReview || !draft.Editable || draft.DraftVersion != 1 || draft.RevisionCount != 0 ||
		draft.Blog.Slug != "connectors-grow-up" || draft.Newsletter.Subject != "Connectors grow up" {
		t.Fatalf("draft = %+v", draft)
	}
	if !strings.Contains(draft.BlogHTML, "<h1>"+template.HTMLEscapeString(draft.Blog.Title)+"</h1>") || strings.Contains(draft.BlogHTML, "<script>") ||
		!strings.Contains(draft.NewsletterHTML, "Connectors grow up") {
		t.Fatalf("draft rendering lacks the escaped title or the subject:\n%s", draft.BlogHTML)
	}

	blog, newsletter := clone(t, draft.Blog), clone(t, draft.Newsletter)
	blog.Title = "   "
	preview, err := h.app.Drafts.Preview(ctx, flowID, token, blog, newsletter)
	if err != nil || preview.Valid || !strings.Contains(preview.Message, "Title is required") || preview.BlogHTML != "" {
		t.Fatalf("preview without a title = %+v, %v", preview, err)
	}
	blog.Title = "Connectors, edited by hand"
	blog.Sections[0].Paragraphs = []string{"An editor rewrote this paragraph before it shipped."}
	newsletter.Subject = "Connectors, edited in the editor"
	if _, err := h.app.Drafts.Preview(ctx, flowID, strings.Repeat("0", 32), blog, newsletter); !errors.Is(err, blogpost.ErrInvalidEditorLink) {
		t.Fatalf("preview with a forged token error = %v", err)
	}
	preview, err = h.app.Drafts.Preview(ctx, flowID, token, blog, newsletter)
	if err != nil || !preview.Valid || !strings.Contains(preview.BlogHTML, "<h1>Connectors, edited by hand</h1>") ||
		!strings.Contains(preview.BlogHTML, "An editor rewrote this paragraph") || !strings.Contains(preview.NewsletterHTML, "Connectors, edited in the editor") {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if view := h.display(ctx, flowID); view["blog-title"] != draft.Blog.Title || view["draft-version"] != float64(1) {
		t.Fatalf("preview changed the run: title=%v version=%v", view["blog-title"], view["draft-version"])
	}

	edits := blogpost.SaveDraftEditsInput{BaseVersion: 1, Blog: blog, Newsletter: newsletter}
	if _, err := h.app.Drafts.Save(ctx, flowID, links.Token(flowID+"0"), edits); !errors.Is(err, blogpost.ErrInvalidEditorLink) {
		t.Fatalf("save with a forged token error = %v", err)
	}
	stale := edits
	stale.BaseVersion = 0
	if saved, err := h.app.Drafts.Save(ctx, flowID, token, stale); err != nil || saved.Outcome != blogpost.OutcomeDraftChanged || saved.DraftVersion != 1 {
		t.Fatalf("save from a stale version = %+v, %v", saved, err)
	}
	saved, err := h.app.Drafts.Save(ctx, flowID, token, edits)
	if err != nil || saved.Outcome != blogpost.OutcomeSaved || saved.DraftVersion != 2 {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	view := h.display(ctx, flowID)
	if view["blog-status"] != blogpost.StatusAwaitingReview || view["blog-title"] != "Connectors, edited by hand" ||
		view["newsletter-subject"] != "Connectors, edited in the editor" || view["draft-version"] != float64(2) {
		t.Fatalf("after save status=%v title=%v subject=%v version=%v", view["blog-status"], view["blog-title"], view["newsletter-subject"], view["draft-version"])
	}
	h.waitForSlackPost(ctx, "*Draft 2, edited in the editor*: Connectors, edited by hand")
	view = h.waitForDisplay(ctx, flowID, "the version 2 artifact", func(view map[string]any) bool {
		return strings.HasSuffix(fmt.Sprint(view["blog-artifact-path"]), "-v2.html")
	})
	artifact := fmt.Sprint(view["blog-artifact-path"])
	if filepath.Base(artifact) != "connectors-grow-up-r0-v2.html" || !strings.HasPrefix(artifact, h.artifacts) {
		t.Fatalf("edited artifact path = %s", artifact)
	}
	html, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1>Connectors, edited by hand</h1>", "An editor rewrote this paragraph before it shipped."} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("edited artifact lacks %q", want)
		}
	}
	reloaded, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil || reloaded.DraftVersion != 2 || reloaded.Blog.Title != blog.Title || reloaded.Blog.Slug != draft.Blog.Slug ||
		reloaded.Newsletter.Subject != newsletter.Subject || reloaded.Blog.Sections[0].Paragraphs[0] != blog.Sections[0].Paragraphs[0] {
		t.Fatalf("reloaded draft = %+v, %v", reloaded, err)
	}

	// A Dex Web Approve form opened before the edit is stale, and so is an editor on version 1.
	if err := h.app.Client.InvokeRPC(ctx, flowID, h.app.BlogPosts.ApproveBlogPost, blogpost.ApproveBlogPostInput{Round: 1}, nil); err == nil {
		t.Fatal("a Dex Web approval of the pre-edit round was accepted")
	}
	if _, err := h.app.Drafts.Approve(ctx, flowID, strings.Repeat("0", 32), 2); !errors.Is(err, blogpost.ErrInvalidEditorLink) {
		t.Fatalf("approve with a forged token error = %v", err)
	}
	if approved, err := h.app.Drafts.Approve(ctx, flowID, token, 1); err != nil || approved.Outcome != blogpost.OutcomeDraftChanged || approved.DraftVersion != 2 {
		t.Fatalf("approve version 1 = %+v, %v", approved, err)
	}
	if view := h.display(ctx, flowID); view["blog-status"] != blogpost.StatusAwaitingReview {
		t.Fatalf("a refused approval moved the run to %v", view["blog-status"])
	}
	approved, err := h.app.Drafts.Approve(ctx, flowID, token, 2)
	if err != nil || approved.Outcome != blogpost.OutcomeApproved || approved.DraftVersion != 2 {
		t.Fatalf("approve version 2 = %+v, %v", approved, err)
	}
	h.waitForSlackPost(ctx, "Draft 2 approved in the editor. Sending the newsletter.")
	result := h.result(ctx, flowID)
	if result.Status != blogpost.StatusSent || result.Title != "Connectors, edited by hand" || result.Delivery.Total == 0 || result.Delivery.Sent != result.Delivery.Total {
		t.Fatalf("result = %+v", result)
	}
	_, emails, _ := h.fake.Snapshot()
	received := emailsTo(emails, reader)
	if len(received) != 1 || received[0].Subject != "Connectors, edited in the editor" || !strings.Contains(received[0].HTML, "Connectors, edited in the editor") ||
		!strings.HasPrefix(received[0].Text, "Connectors, edited in the editor") {
		t.Fatalf("emails to %s = %+v", reader, received)
	}

	// The closed run stays readable in the editor but no longer changes.
	if saved, err := h.app.Drafts.Save(ctx, flowID, token, blogpost.SaveDraftEditsInput{BaseVersion: 2, Blog: blog, Newsletter: newsletter}); err != nil ||
		saved.Outcome != blogpost.OutcomeNotInReview || saved.DraftVersion != 2 {
		t.Fatalf("save after sending = %+v, %v", saved, err)
	}
	if approved, err := h.app.Drafts.Approve(ctx, flowID, token, 2); err != nil || approved.Outcome != blogpost.OutcomeNotInReview {
		t.Fatalf("approve after sending = %+v, %v", approved, err)
	}
	closed, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil || closed.Editable || closed.Status != blogpost.StatusSent || closed.DraftVersion != 2 || closed.Blog.Title != blog.Title ||
		!strings.Contains(closed.BlogHTML, "<h1>Connectors, edited by hand</h1>") {
		t.Fatalf("draft after sending = %+v, %v", closed, err)
	}
	if _, err := h.app.Drafts.Get(ctx, flowID, strings.Repeat("0", 32)); !errors.Is(err, blogpost.ErrInvalidEditorLink) {
		t.Fatalf("get of a closed run with a forged token error = %v", err)
	}
	history := reviewEvents(t, h.display(ctx, flowID))
	if len(history) != 2 || history[0].Source != "editor" || history[0].Action != "edited" || history[0].Detail != "version 2" ||
		history[1].Source != "editor" || history[1].Action != blogpost.OutcomeApproved || history[1].Detail != "version 2" {
		t.Fatalf("review history = %+v", history)
	}
}

func TestSlackApprovalSendsEditorVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)
	reader := h.subscribeReader(ctx, "editor-then-slack")
	flowID, thread := h.startSlackThread(ctx, "Write a blog post about connectors from the past 2 weeks")
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	token := h.editorToken(ctx, flowID)
	draft, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil {
		t.Fatal(err)
	}
	blog, newsletter := clone(t, draft.Blog), clone(t, draft.Newsletter)
	blog.Title = "Connectors, tightened in the editor"
	newsletter.Subject = "Connectors, tightened"
	saved, err := h.app.Drafts.Save(ctx, flowID, token, blogpost.SaveDraftEditsInput{BaseVersion: draft.DraftVersion, Blog: blog, Newsletter: newsletter})
	if err != nil || saved.Outcome != blogpost.OutcomeSaved || saved.DraftVersion != 2 {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	h.waitForSlackPost(ctx, "*Draft 2, edited in the editor*: Connectors, tightened in the editor")

	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "approved"))
	h.waitForSlackPost(ctx, "Approved by <@UREVIEWER>. Sending the newsletter.")
	result := h.result(ctx, flowID)
	if result.Status != blogpost.StatusSent || result.Title != "Connectors, tightened in the editor" || result.Delivery.Sent != result.Delivery.Total {
		t.Fatalf("result = %+v", result)
	}
	_, emails, _ := h.fake.Snapshot()
	if received := emailsTo(emails, reader); len(received) != 1 || received[0].Subject != "Connectors, tightened" {
		t.Fatalf("emails to %s = %+v", reader, received)
	}
	history := reviewEvents(t, h.display(ctx, flowID))
	if len(history) != 2 || history[0].Source != "editor" || history[0].Action != "edited" ||
		history[1].Source != "slack" || history[1].Action != blogpost.OutcomeApproved || history[1].Actor != "UREVIEWER" {
		t.Fatalf("review history = %+v", history)
	}
}

func TestEditorKeepsResearchLinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newHarness(t)
	flowID, thread := h.startSlackThread(ctx, "Write a blog post about connectors from the past 2 weeks")
	h.waitForStatus(ctx, flowID, blogpost.StatusAwaitingReview)
	token := h.editorToken(ctx, flowID)
	draft, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil {
		t.Fatal(err)
	}
	const stripeLink = "https://github.com/acme/connectors/pull/11"
	if len(draft.Blog.Highlights) != 2 || draft.Blog.Highlights[0].URL != stripeLink || draft.Blog.Highlights[1].URL != "" {
		t.Fatalf("model highlights = %+v", draft.Blog.Highlights)
	}

	// Links outside the draft's own highlights are refused, including the one the model invented.
	for name, change := range map[string]func(*content.BlogDraft){
		"changed link":  func(blog *content.BlogDraft) { blog.Highlights[0].URL = "https://github.com/acme/connectors/pull/99" },
		"invented link": func(blog *content.BlogDraft) { blog.Highlights[1].URL = "https://evil.example/phish" },
		"added highlight": func(blog *content.BlogDraft) {
			blog.Highlights = append(blog.Highlights, content.BlogHighlight{Title: "Elsewhere", Description: "Another page.", URL: "https://blog.acme.test/elsewhere"})
		},
	} {
		blog := clone(t, draft.Blog)
		change(&blog)
		saved, err := h.app.Drafts.Save(ctx, flowID, token, blogpost.SaveDraftEditsInput{BaseVersion: 1, Blog: blog, Newsletter: draft.Newsletter})
		if err != nil || saved.Outcome != blogpost.OutcomeInvalid || saved.DraftVersion != 1 || !strings.Contains(saved.Message, "links to a page the draft did not cite") {
			t.Fatalf("save with %s = %+v, %v", name, saved, err)
		}
		preview, err := h.app.Drafts.Preview(ctx, flowID, token, blog, draft.Newsletter)
		if err != nil || preview.Valid || !strings.Contains(preview.Message, "links to a page the draft did not cite") {
			t.Fatalf("preview with %s = %+v, %v", name, preview, err)
		}
	}
	if view := h.display(ctx, flowID); view["draft-version"] != float64(1) || view["blog-title"] != draft.Blog.Title {
		t.Fatalf("a refused save changed the run: version=%v title=%v", view["draft-version"], view["blog-title"])
	}

	// Renaming a highlight keeps its link; the slug stays the model's.
	blog := clone(t, draft.Blog)
	blog.Highlights[0].Title = "Stripe checkout connector"
	blog.Highlights[0].Description = "Hosted ACH checkout sessions."
	blog.Slug = "a-different-slug"
	saved, err := h.app.Drafts.Save(ctx, flowID, token, blogpost.SaveDraftEditsInput{BaseVersion: 1, Blog: blog, Newsletter: draft.Newsletter})
	if err != nil || saved.Outcome != blogpost.OutcomeSaved || saved.DraftVersion != 2 {
		t.Fatalf("save a renamed highlight = %+v, %v", saved, err)
	}
	reloaded, err := h.app.Drafts.Get(ctx, flowID, token)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Blog.Slug != "connectors-grow-up" || len(reloaded.Blog.Highlights) != 2 ||
		reloaded.Blog.Highlights[0] != (content.BlogHighlight{Title: "Stripe checkout connector", Description: "Hosted ACH checkout sessions.", URL: stripeLink}) ||
		reloaded.Blog.Highlights[1].URL != "" {
		t.Fatalf("reloaded draft = %+v", reloaded.Blog)
	}
	if !strings.Contains(reloaded.BlogHTML, `<a href="`+stripeLink+`">Stripe checkout connector</a>`) {
		t.Fatal("the renamed highlight lost its link in the rendering")
	}
	view := h.waitForDisplay(ctx, flowID, "the version 2 artifact", func(view map[string]any) bool {
		return strings.HasSuffix(fmt.Sprint(view["blog-artifact-path"]), "-v2.html")
	})
	if filepath.Base(fmt.Sprint(view["blog-artifact-path"])) != "connectors-grow-up-r0-v2.html" {
		t.Fatalf("edited artifact path = %v", view["blog-artifact-path"])
	}

	h.deliverSlackReply(ctx, slackReply(thread, "UREVIEWER", "reject"))
	if result := h.result(ctx, flowID); result.Status != blogpost.StatusRejected {
		t.Fatalf("result = %+v", result)
	}
}

// replySequence keeps Slack event IDs and reply timestamps unique within a test run.
var replySequence atomic.Int64

// startSlackThread posts a root request message and returns its run and thread timestamp.
func (h *harness) startSlackThread(ctx context.Context, text string) (flowID, thread string) {
	h.t.Helper()
	stamp := fmt.Sprint(time.Now().UnixNano())
	thread = stamp + ".000400"
	return h.deliverSlackMessage(ctx, "Ev"+stamp, thread, text), thread
}

// slackReply is one reply in the request's thread.
func slackReply(thread, userID, text string) sdkgo.TriggerEvent[slack.MessageEvent] {
	sequence := replySequence.Add(1)
	return sdkgo.TriggerEvent[slack.MessageEvent]{ID: fmt.Sprintf("EvReply%d-%d", time.Now().UnixNano(), sequence), OccurredAt: time.Now().UTC(), Payload: slack.MessageEvent{
		TeamID: "T1", ChannelID: "CBLOG", Timestamp: fmt.Sprintf("%d.%06d", time.Now().Unix(), sequence), ThreadTimestamp: thread, UserID: userID, Text: text,
	}}
}

// deliverSlackReply routes one thread reply through the same review Trigger target main uses.
func (h *harness) deliverSlackReply(ctx context.Context, event sdkgo.TriggerEvent[slack.MessageEvent]) {
	h.t.Helper()
	filter, err := blogpost.NewReviewTriggerFilter(slack.ThreadReplyCreatedTriggerConfiguration{
		ChannelID: "CBLOG", ThreadReplyMatcher: slack.MessageMatcher{PosterUserIDs: []string{"UREVIEWER"}},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	target := sdkgo.NewDexRPCTriggerTarget(h.app.Client, h.app.BlogPosts.ReceiveSlackReview, filter, blogpost.ResolveReviewFlowID, blogpost.MapToSlackReviewReply)
	if err := target.HandleTrigger(ctx, event); err != nil {
		h.t.Fatalf("deliver Slack reply %q: %v", event.Payload.Text, err)
	}
}

// invokeSlackReview sends the reply's RPC input directly, to read the outcome the Trigger target discards.
func (h *harness) invokeSlackReview(ctx context.Context, event sdkgo.TriggerEvent[slack.MessageEvent]) string {
	h.t.Helper()
	var result blogpost.SlackReviewResult
	if err := h.app.Client.InvokeRPC(ctx, blogpost.ResolveReviewFlowID(event), h.app.BlogPosts.ReceiveSlackReview, blogpost.MapToSlackReviewReply(event), &result); err != nil {
		h.t.Fatalf("review reply %q: %v", event.Payload.Text, err)
	}
	return result.Outcome
}

func (h *harness) waitForSlackPost(ctx context.Context, fragment string) string {
	h.t.Helper()
	posts := h.waitForSlackPosts(ctx, fragment, 1)
	return posts[0]
}

// waitForSlackPosts waits until count Slack posts contain fragment and returns them.
func (h *harness) waitForSlackPosts(ctx context.Context, fragment string, count int) []string {
	h.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var posts []string
	for time.Now().Before(deadline) && ctx.Err() == nil {
		posts, _, _ = h.fake.Snapshot()
		var matching []string
		for _, post := range posts {
			if strings.Contains(post, fragment) {
				matching = append(matching, post)
			}
		}
		if len(matching) >= count {
			return matching
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("fewer than %d Slack posts contain %q:\n%s", count, fragment, strings.Join(posts, "\n---\n"))
	return nil
}

func (h *harness) waitForDisplay(ctx context.Context, flowID, what string, done func(map[string]any) bool) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if view := h.display(ctx, flowID); done(view) {
			return view
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("%s never showed %s", flowID, what)
	return nil
}

// editorToken reads the token from the editor link Dex Web shows for the run.
func (h *harness) editorToken(ctx context.Context, flowID string) string {
	h.t.Helper()
	raw := fmt.Sprint(h.display(ctx, flowID)["editor-url"])
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "https" || link.Host != "news.acme.test" || link.Path != "/edit/"+flowID || link.Query().Get("token") == "" {
		h.t.Fatalf("editor URL = %q (%v)", raw, err)
	}
	return link.Query().Get("token")
}

// editorLinks signs editor links with the harness's key, as the application does.
func (h *harness) editorLinks() blogpost.EditorLinks {
	h.t.Helper()
	key, err := subscribers.LoadSigningKey(h.options.Config.Newsletter.UnsubscribeKeyFile)
	if err != nil {
		h.t.Fatal(err)
	}
	return blogpost.NewEditorLinks(h.options.Config.Newsletter.PublicBaseURL, key)
}

// subscribeReader subscribes a unique address and removes it when the test ends,
// so the shared subscriber list is left as the test found it.
func (h *harness) subscribeReader(ctx context.Context, name string) string {
	h.t.Helper()
	address := fmt.Sprintf("%s-%d@example.com", name, time.Now().UnixNano())
	if already, err := h.app.Subscribers.Subscribe(ctx, address); err != nil || already {
		h.t.Fatalf("subscribe %s: already=%v err=%v", address, already, err)
	}
	h.t.Cleanup(func() {
		if h.app == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var removed subscribers.RemoveResult
		if err := h.app.Client.InvokeRPC(cleanupCtx, subscribers.FlowID, subscribers.Flow{}.RemoveNewsletterSubscriber, subscribers.RemoveInput{Address: address}, &removed); err != nil || !removed.Removed {
			h.t.Errorf("remove %s: removed=%v err=%v", address, removed.Removed, err)
		}
	})
	return address
}

// revisionGate sits between the application and the fake model and holds every revision
// request until opened, so a test can act while a revision is being written.
type revisionGate struct {
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

// gateRevisions restarts the application with its model calls routed through a revisionGate.
func (h *harness) gateRevisions() *revisionGate {
	h.t.Helper()
	target, err := url.Parse(h.fake.URL)
	if err != nil {
		h.t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	gate := &revisionGate{arrived: make(chan struct{}, 1), release: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.Contains(string(body), "Revise the previous draft") {
			select {
			case gate.arrived <- struct{}{}:
			default:
			}
			select {
			case <-gate.release:
			case <-request.Context().Done():
				return
			}
		}
		request.Body, request.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		proxy.ServeHTTP(response, request)
	}))
	h.t.Cleanup(func() {
		gate.open()
		server.Close()
	})
	h.stop()
	h.options.LLMOptions = []llmrouter.Option{llmrouter.WithProviderBaseURLForTest(llmrouter.ProviderGemini, server.URL)}
	h.start()
	return gate
}

func (gate *revisionGate) waitForRevision(t *testing.T) {
	t.Helper()
	select {
	case <-gate.arrived:
	case <-time.After(60 * time.Second):
		t.Fatal("the revision request never reached the model")
	}
}

func (gate *revisionGate) open() { gate.once.Do(func() { close(gate.release) }) }

func reviewEvents(t *testing.T, view map[string]any) []blogpost.ReviewEvent {
	t.Helper()
	return decodeAs[[]blogpost.ReviewEvent](t, view["review-history"])
}

// revisionRequests returns the model requests that asked for a revised draft.
func revisionRequests(llmRequests []string) []string {
	var revisions []string
	for _, request := range llmRequests {
		if strings.Contains(request, "Revise the previous draft") {
			revisions = append(revisions, request)
		}
	}
	return revisions
}

// decodeAs converts a display value decoded as generic JSON into T.
func decodeAs[T any](t *testing.T, value any) T {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded T
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	return decoded
}

// clone deep-copies a draft so edits never alias the view they came from.
func clone[T any](t *testing.T, value T) T {
	t.Helper()
	return decodeAs[T](t, value)
}

func emailsTo(emails []fakeproviders.SentEmail, address string) []fakeproviders.SentEmail {
	var matching []fakeproviders.SentEmail
	for _, email := range emails {
		if email.To == address {
			matching = append(matching, email)
		}
	}
	return matching
}

func countContaining(texts []string, fragment string) int {
	count := 0
	for _, text := range texts {
		if strings.Contains(text, fragment) {
			count++
		}
	}
	return count
}
