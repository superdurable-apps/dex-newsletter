//go:build integration

package techblog_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/render"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/unsubscribe"
	github "github.com/superdurable/dex-connectors-library/connectors/github"
	"github.com/superdurable/dex-connectors-library/connectors/google/gemini"
	"github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	testRepositoryOwner      = "superdurable"
	testRepositoryName       = "dex-connectors-library"
	testPullRequestURL       = "https://github.com/superdurable/dex-connectors-library/pull/101"
	testCommitURL            = "https://github.com/superdurable/dex-connectors-library/commit/abc123def456"
	testChannelID            = "C-REQUESTS"
	testSecondRepositoryName = "dex"
)

// TestHappyPathWithRevisionStaleGateAndWorkerReplacement drives the complete
// Process: Slack trigger (delivered twice), research, drafts, an editor
// revision, a stale-gate rejection, Worker replacement while waiting for the
// editor, approval, and per-subscriber Gmail delivery with one rejection.
func TestHappyPathWithRevisionStaleGateAndWorkerReplacement(t *testing.T) {
	ctx := integrationContext(t, 3*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	flowID := harness.deliverSlackRequest(ctx, "Write a blog post about connectors over the past two weeks", 2)

	display := harness.waitForStatus(ctx, flowID, techblog.StatusAwaitingEditorReview)
	if display["review-gate-key"] != "review-0" {
		t.Fatalf("review gate = %v, want review-0", display["review-gate-key"])
	}
	artifactPath, _ := display["blog-artifact-path"].(string)
	assertSelfContainedBlogArtifact(t, artifactPath, harness.artifactDirectory)
	if got := harness.providers.gemini.requestCount("interpret"); got != 1 {
		t.Fatalf("interpret requests = %d, want 1 (duplicate Slack delivery must not start a second run)", got)
	}
	if window, _ := display["change-window-description"].(string); !strings.Contains(window, "14 days") {
		t.Fatalf("change window = %q, want the 14-day window from the request", window)
	}

	if err := harness.invokeAction(ctx, flowID, harness.newsletter.RequestBlogRevision,
		techblog.RequestBlogRevisionInput{Feedback: "Add a section on retries.", GateKey: "review-0"}); err != nil {
		t.Fatalf("request revision: %v", err)
	}
	display = harness.waitForReviewGate(ctx, flowID, "review-1")
	if preview, _ := display["newsletter-text-preview"].(string); !strings.Contains(preview, "revised") {
		t.Fatalf("editor preview does not show the revised newsletter: %.200q", preview)
	}
	if newsletterPath, _ := display["newsletter-artifact-path"].(string); !strings.HasSuffix(newsletterPath, ".newsletter.html") {
		t.Fatalf("newsletter preview artifact = %q", newsletterPath)
	}
	if display["blog-revision-count"] != float64(1) {
		t.Fatalf("revision count = %v, want 1", display["blog-revision-count"])
	}
	if got := harness.providers.gemini.requestCount("blog"); got != 2 {
		t.Fatalf("blog drafts = %d, want 2", got)
	}
	if !harness.providers.gemini.lastPromptContains("blog", "Add a section on retries.") {
		t.Fatal("revision prompt does not carry the editor feedback")
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.ApproveNewsletterForDelivery,
		techblog.ApproveNewsletterInput{GateKey: "review-0", Subject: subjectOf(display)}); err == nil {
		t.Fatal("approval with a stale review gate was accepted")
	}

	harness.replaceWorker(ctx)
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.ApproveNewsletterForDelivery,
		techblog.ApproveNewsletterInput{GateKey: "review-1", Subject: "a subject the editor never saw"}); err == nil {
		t.Fatal("approval with a stale subject was accepted")
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.ApproveNewsletterForDelivery,
		techblog.ApproveNewsletterInput{GateKey: "review-1", Subject: subjectOf(display)}); err != nil {
		t.Fatalf("approve after Worker replacement: %v", err)
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.ApproveNewsletterForDelivery,
		techblog.ApproveNewsletterInput{GateKey: "review-1", Subject: subjectOf(display)}); err == nil {
		t.Fatal("duplicate approval was accepted")
	}

	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDelivered {
		t.Fatalf("result status = %q, want %q (closing reason %q)", result.Status, techblog.StatusDelivered, result.ClosingReason)
	}
	if result.RevisionCount != 1 || result.BlogArtifactPath == "" || !strings.Contains(result.BlogTitle, "revised") {
		t.Fatalf("unexpected result: %+v", result)
	}
	wantDelivery := struct{ recipients, sent, rejected int }{4, 3, 1}
	if result.Delivery.Recipients != wantDelivery.recipients || result.Delivery.Sent != wantDelivery.sent || result.Delivery.Rejected != wantDelivery.rejected {
		t.Fatalf("delivery = %+v, want %d recipients, %d sent, %d rejected", result.Delivery, wantDelivery.recipients, wantDelivery.sent, wantDelivery.rejected)
	}
	for _, recipient := range []string{"alice@example.com", "bob@example.com", "reject@example.com", "dana@example.org"} {
		if got := harness.providers.gmail.sendCount(recipient); got != 1 {
			t.Errorf("sends to %s = %d, want exactly 1", recipient, got)
		}
	}
	if got := harness.providers.gmail.totalSends(); got != 4 {
		t.Errorf("Gmail sends = %d, want 4: one per unique subscriber", got)
	}
	for _, recipient := range []string{"alice@example.com", "bob@example.com", "dana@example.org"} {
		body := harness.providers.gmail.bodyTo(recipient)
		if !strings.Contains(body, "https://news.example.com/newsletter?unsubscribe="+harness.unsubscribeKey.Token(recipient)) {
			t.Errorf("the newsletter to %s does not carry its own unsubscribe link", recipient)
		}
		if strings.Contains(body, render.UnsubscribeURLPlaceholder) {
			t.Errorf("the newsletter to %s still carries the unsubscribe placeholder", recipient)
		}
	}
	if strings.Contains(harness.providers.gmail.bodyTo("alice@example.com"), harness.unsubscribeKey.Token("bob@example.com")) {
		t.Error("alice's newsletter carries bob's unsubscribe link")
	}
	final, err := harness.display(ctx, flowID)
	if err != nil {
		t.Fatalf("display after delivery: %v", err)
	}
	if exceptions := deliveryExceptionsOf(t, final); len(exceptions) != 1 ||
		exceptions[0].Recipient != "reject@example.com" || exceptions[0].Status != "rejected" {
		t.Errorf("delivery exceptions = %+v, want only the rejected reject@example.com", exceptions)
	}
	if !harness.providers.gmail.lastBodyContains("Read on the web") {
		t.Error("newsletter does not link to the published blog post")
	}
	if !harness.providers.gmail.lastBodyContains("revised") {
		t.Error("newsletter does not carry the full revised post inline")
	}
	replies := harness.providers.slack.threadReplies(testChannelID)
	if len(replies) < 5 {
		t.Fatalf("Slack thread replies = %d (%q), want acknowledgement, plan, two drafts, and a delivery report", len(replies), replies)
	}
	if !strings.Contains(replies[len(replies)-1], "3") {
		t.Errorf("delivery report %q does not report the sent count", replies[len(replies)-1])
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.ApproveNewsletterForDelivery,
		techblog.ApproveNewsletterInput{GateKey: "review-1", Subject: subjectOf(display)}); err == nil {
		t.Fatal("approval after completion was accepted")
	}
}

// TestUnclearRequestAsksForClarification completes without research when the
// model cannot identify a topic.
func TestUnclearRequestAsksForClarification(t *testing.T) {
	ctx := integrationContext(t, time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: false})
	flowID := harness.deliverSlackRequest(ctx, "hey can you write something", 1)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusNeedsClarification {
		t.Fatalf("status = %q, want %q", result.Status, techblog.StatusNeedsClarification)
	}
	if got := harness.providers.github.requestCount(); got != 0 {
		t.Fatalf("GitHub requests = %d, want 0", got)
	}
	replies := harness.providers.slack.threadReplies(testChannelID)
	if len(replies) != 2 || !strings.Contains(replies[1], "Which area") {
		t.Fatalf("Slack replies = %q, want acknowledgement and the clarification question", replies)
	}
}

// TestInvalidSynthesisHoldsForOperatorRetryThenDiscard routes an unusable
// model output to operator recovery, retries the stage, then discards.
func TestInvalidSynthesisHoldsForOperatorRetryThenDiscard(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true, invalidSynthesisResponses: 1})
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors this week", 1)

	display := harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	if display["failed-stage"] != techblog.StageSynthesizeResearch || display["attention-gate-key"] != "attention-1" {
		t.Fatalf("attention display = %v", display)
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.RetryFailedStage, techblog.RetryFailedStageInput{GateKey: "attention-0"}); err == nil {
		t.Fatal("retry with a stale attention gate was accepted")
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.RetryFailedStage, techblog.RetryFailedStageInput{GateKey: "attention-1"}); err != nil {
		t.Fatalf("retry failed stage: %v", err)
	}
	display = harness.waitForStatus(ctx, flowID, techblog.StatusAwaitingEditorReview)
	if display["attention-reason"] != "" || display["failed-stage"] != "" {
		t.Fatalf("resolved attention still displayed: reason=%v stage=%v", display["attention-reason"], display["failed-stage"])
	}
	if got := harness.providers.gemini.requestCount("synthesis"); got != 2 {
		t.Fatalf("synthesis requests = %d, want 2", got)
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.DiscardNewsletterDraft,
		techblog.DiscardNewsletterDraftInput{Reason: stringPointer("Not this week."), GateKey: "review-0"}); err != nil {
		t.Fatalf("discard: %v", err)
	}
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDiscarded || result.ClosingReason != "Not this week." {
		t.Fatalf("result = %+v, want discarded with the editor's reason", result)
	}
	if got := harness.providers.gmail.totalSends(); got != 0 {
		t.Fatalf("Gmail sends = %d, want 0 after discard", got)
	}
}

// TestReviewReminderThenExpiry fires the reminder Timer and then the expiry
// Timer without an editorial decision.
func TestReviewReminderThenExpiry(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.waitForStatus(ctx, flowID, techblog.StatusAwaitingEditorReview)
	repliesBefore := len(harness.providers.slack.threadReplies(testChannelID))

	harness.skipEditorialTimer(ctx, flowID, 1)
	harness.waitFor(ctx, "review reminder", func(context.Context) bool {
		return len(harness.providers.slack.threadReplies(testChannelID)) == repliesBefore+1
	})
	harness.skipEditorialTimer(ctx, flowID, 2)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusReviewExpired {
		t.Fatalf("status = %q, want %q", result.Status, techblog.StatusReviewExpired)
	}
}

// TestNoNotableChangesCloses completes after synthesis finds nothing notable.
func TestNoNotableChangesCloses(t *testing.T) {
	ctx := integrationContext(t, time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: false})
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusNoNotableChanges {
		t.Fatalf("status = %q, want %q", result.Status, techblog.StatusNoNotableChanges)
	}
	if got := harness.providers.gemini.requestCount("blog"); got != 0 {
		t.Fatalf("blog drafts = %d, want 0", got)
	}
}

// TestGmailAuthenticationFailurePausesDeliveryAndRetryResumes holds an
// account-wide Gmail failure for an operator and resumes at the same
// subscriber after the connection is fixed, sending nobody twice.
func TestGmailAuthenticationFailurePausesDeliveryAndRetryResumes(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	harness.providers.gmail.setAuthenticationFailure(true)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)

	display := harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	if display["failed-stage"] != techblog.StageSendNewsletter {
		t.Fatalf("failed stage = %v, want %s", display["failed-stage"], techblog.StageSendNewsletter)
	}
	if got := harness.providers.gmail.totalAccepted(); got != 0 {
		t.Fatalf("accepted sends while unauthorized = %d", got)
	}
	harness.providers.gmail.setAuthenticationFailure(false)
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.RetryFailedStage,
		techblog.RetryFailedStageInput{GateKey: display["attention-gate-key"].(string)}); err != nil {
		t.Fatalf("retry delivery: %v", err)
	}
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDelivered || result.Delivery.Sent != 3 || result.Delivery.Rejected != 1 {
		t.Fatalf("result = %+v", result)
	}
	for _, recipient := range []string{"alice@example.com", "bob@example.com", "dana@example.org"} {
		if got := harness.providers.gmail.acceptedCount(recipient); got != 1 {
			t.Errorf("accepted sends to %s = %d, want exactly 1", recipient, got)
		}
	}
}

// TestUnconfirmedGmailSendIsNeverResent records a 503 as uncertain and moves on.
func TestUnconfirmedGmailSendIsNeverResent(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	harness.providers.gmail.setUnavailableRecipient("bob@example.com")
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDelivered || result.Delivery.Uncertain != 1 || result.Delivery.Sent != 2 {
		t.Fatalf("result = %+v", result)
	}
	if got := harness.providers.gmail.sendCount("bob@example.com"); got != 1 {
		t.Fatalf("attempts to the unconfirmed recipient = %d, want exactly 1 (never resent)", got)
	}
	display, err := harness.display(ctx, flowID)
	if err != nil {
		t.Fatalf("display after delivery: %v", err)
	}
	var unconfirmed []string
	for _, exception := range deliveryExceptionsOf(t, display) {
		if exception.Status == "uncertain" {
			unconfirmed = append(unconfirmed, exception.Recipient)
		}
	}
	if len(unconfirmed) != 1 || unconfirmed[0] != "bob@example.com" {
		t.Fatalf("unconfirmed recipients shown in Dex Web = %q, want [bob@example.com]", unconfirmed)
	}
}

// TestSlowGmailSendIsNotRepeated makes one send slower than the ASYNC local
// phase (about seven seconds). The send Step persists synchronously, so Gmail
// sees it once; under the connector's ASYNC default it would run again.
func TestSlowGmailSendIsNotRepeated(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	harness.providers.gmail.delaySendsTo("alice@example.com", 9*time.Second)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDelivered || result.Delivery.Sent != 3 {
		t.Fatalf("result = %+v", result)
	}
	if got := harness.providers.gmail.sendCount("alice@example.com"); got != 1 {
		t.Fatalf("sends to the slow recipient = %d, want exactly 1", got)
	}
}

// TestSubscriberListReadFailureHoldsThenRetrySucceeds exhausts the retries of
// the subscriber list read, holds for an operator, and completes after the
// retry.
func TestSubscriberListReadFailureHoldsThenRetrySucceeds(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	harness.subscriberReader.setFailing(true)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	display := harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	if display["failed-stage"] != techblog.StageLoadSubscribers {
		t.Fatalf("failed stage = %v", display["failed-stage"])
	}
	if reason, _ := display["attention-reason"].(string); !strings.Contains(reason, "subscriber list could not be read") {
		t.Fatalf("attention reason = %q", reason)
	}
	if got := harness.subscriberReader.attempts(); got < 2 {
		t.Fatalf("subscriber list reads = %d, want the read retried before holding", got)
	}
	harness.subscriberReader.setFailing(false)
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.RetryFailedStage,
		techblog.RetryFailedStageInput{GateKey: display["attention-gate-key"].(string)}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result := harness.waitForResult(ctx, flowID); result.Status != techblog.StatusDelivered || result.Delivery.Sent != 3 {
		t.Fatalf("result = %+v", result)
	}
}

// TestSubscriberListKeepsOneCanonicalEntryPerAddress subscribes concurrently,
// repeats and rejects addresses, and reads the same list after a Worker
// replacement.
func TestSubscriberListKeepsOneCanonicalEntryPerAddress(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	var group sync.WaitGroup
	outcomes := make([]techblog.AddNewsletterSubscriberResult, 12)
	failures := make([]error, len(outcomes))
	for index := range outcomes {
		group.Add(1)
		go func() {
			defer group.Done()
			// Every other call repeats an earlier address in another case.
			address := fmt.Sprintf("reader-%d@example.com", index/2)
			if index%2 == 1 {
				address = strings.ToUpper(address)
			}
			addContext, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			outcomes[index], failures[index] = harness.subscriberList.AddNewsletterSubscriber(addContext, address)
		}()
	}
	group.Wait()
	added := 0
	for index, outcome := range outcomes {
		if failures[index] != nil {
			t.Fatalf("concurrent subscription %d: %v", index, failures[index])
		}
		if outcome.Email != fmt.Sprintf("reader-%d@example.com", index/2) {
			t.Fatalf("subscription %d = %+v, want the canonical address", index, outcome)
		}
		if outcome.Outcome == techblog.SubscriptionAdded {
			added++
		}
	}
	if added != len(outcomes)/2 {
		t.Fatalf("added = %d, want %d: one per address", added, len(outcomes)/2)
	}
	invalid, err := harness.subscriberList.AddNewsletterSubscriber(ctx, "Reader <reader-0@example.com>")
	if err != nil || invalid.Outcome != techblog.SubscriptionInvalidAddress {
		t.Fatalf("invalid subscription = %+v, %v", invalid, err)
	}
	harness.replaceWorker(ctx)
	addresses, err := harness.subscriberList.ListNewsletterSubscribers(ctx)
	if err != nil {
		t.Fatalf("list subscribers: %v", err)
	}
	if len(addresses) != defaultTestAudience+len(outcomes)/2 {
		t.Fatalf("subscribers after Worker replacement = %q", addresses)
	}
	seen := map[string]bool{}
	for _, address := range addresses {
		if seen[address] || address != strings.ToLower(address) {
			t.Fatalf("subscriber list %q has a duplicate or non-canonical address", addresses)
		}
		seen[address] = true
	}
}

// TestAbandonAfterPartialDeliveryWarnsAgainstReposting abandons a request
// whose delivery paused after one send and checks that the requester is told
// not to post it again.
func TestAbandonAfterPartialDeliveryWarnsAgainstReposting(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	harness.providers.gmail.failAuthenticationAfterAccepted(1)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	display := harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	if display["failed-stage"] != techblog.StageSendNewsletter {
		t.Fatalf("failed stage = %v, want %s", display["failed-stage"], techblog.StageSendNewsletter)
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.AbandonNewsletterRequest, techblog.AbandonNewsletterRequestInput{
		Reason: stringPointer("Gmail is down for the day."), GateKey: display["attention-gate-key"].(string),
	}); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDeliveryStopped || result.Delivery.Sent != 1 {
		t.Fatalf("result = %+v, want delivery-stopped after 1 send", result)
	}
	replies := harness.providers.slack.threadReplies(testChannelID)
	final := replies[len(replies)-1]
	if !strings.Contains(final, "Do not post this request again") || strings.Contains(final, "Post a new message") {
		t.Fatalf("final reply invites a repost that would email subscribers twice: %q", final)
	}
	if got := harness.providers.gmail.totalAccepted(); got != 1 {
		t.Fatalf("accepted sends = %d, want 1", got)
	}
}

// TestSubscriberListRestartKeepsTheListAndShowsItInDexWeb starts the list
// again, as every application restart does, and reads its Dex Web views.
func TestSubscriberListRestartKeepsTheListAndShowsItInDexWeb(t *testing.T) {
	ctx := integrationContext(t, time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	for restart := 1; restart <= 2; restart++ {
		if err := harness.subscriberList.StartList(ctx); err != nil {
			t.Fatalf("restart %d: StartList = %v, want the existing list returned as success", restart, err)
		}
	}
	addresses, err := harness.subscriberList.ListNewsletterSubscribers(ctx)
	if err != nil || len(addresses) != defaultTestAudience {
		t.Fatalf("subscribers after restarts = %q, %v", addresses, err)
	}
	var summary, display map[string]any
	if err := harness.client.InvokeRPC(ctx, harness.subscriberFlowID, harness.subscriberFlow.GetDexSummary, nil, &summary); err != nil {
		t.Fatalf("list summary: %v", err)
	}
	if err := harness.client.InvokeRPC(ctx, harness.subscriberFlowID, harness.subscriberFlow.GetDexDisplay, nil, &display); err != nil {
		t.Fatalf("list display: %v", err)
	}
	if summary["newsletter-subscriber-count"] != float64(defaultTestAudience) || display["newsletter-subscriber-count"] != float64(defaultTestAudience) {
		t.Fatalf("Dex Web count: summary %v, display %v; want %d", summary, display["newsletter-subscriber-count"], defaultTestAudience)
	}
	shown, _ := display["newsletter-subscribers"].([]any)
	if len(shown) != defaultTestAudience || shown[0] != "alice@example.com" || shown[1] != "bob@example.com" {
		t.Fatalf("Dex Web addresses = %v, want the canonical list in subscription order", display["newsletter-subscribers"])
	}
}

// TestUnsubscribeLinkRemovesTheReaderFromLaterIssues unsubscribes one reader
// with the token from their link, then delivers an issue.
func TestUnsubscribeLinkRemovesTheReaderFromLaterIssues(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	token := harness.unsubscribeKey.Token("bob@example.com")
	removed, err := harness.subscriberList.RemoveNewsletterSubscriber(ctx, token)
	if err != nil || removed.Outcome != techblog.UnsubscriptionRemoved {
		t.Fatalf("unsubscribe bob = %+v, %v", removed, err)
	}
	again, err := harness.subscriberList.RemoveNewsletterSubscriber(ctx, token)
	if err != nil || again.Outcome != techblog.UnsubscriptionNotSubscribed {
		t.Fatalf("opening the link again = %+v, %v; want not-subscribed", again, err)
	}
	unknown, err := harness.subscriberList.RemoveNewsletterSubscriber(ctx, harness.unsubscribeKey.Token("stranger@example.com"))
	if err != nil || unknown.Outcome != techblog.UnsubscriptionNotSubscribed {
		t.Fatalf("a stranger's token = %+v, %v; want not-subscribed", unknown, err)
	}
	addresses, err := harness.subscriberList.ListNewsletterSubscribers(ctx)
	if err != nil || len(addresses) != defaultTestAudience-1 {
		t.Fatalf("subscribers after unsubscribing bob = %q, %v", addresses, err)
	}
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDelivered || result.Delivery.Recipients != defaultTestAudience-1 {
		t.Fatalf("result = %+v, want delivery to the %d remaining subscribers", result, defaultTestAudience-1)
	}
	if got := harness.providers.gmail.sendCount("bob@example.com"); got != 0 {
		t.Fatalf("sends to the unsubscribed reader = %d, want 0", got)
	}
	resubscribed, err := harness.subscriberList.AddNewsletterSubscriber(ctx, "bob@example.com")
	if err != nil || resubscribed.Outcome != techblog.SubscriptionAdded {
		t.Fatalf("resubscribing bob = %+v, %v", resubscribed, err)
	}
}

// TestEmptySubscriberListClosesWithoutSending approves an issue while nobody
// has subscribed yet, the list's initial state.
func TestEmptySubscriberListClosesWithoutSending(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarnessWithSubscribers(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true}, nil)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusNoSubscribers {
		t.Fatalf("result = %+v, want %s", result, techblog.StatusNoSubscribers)
	}
	if got := harness.providers.gmail.totalSends(); got != 0 {
		t.Fatalf("Gmail sends = %d, want 0", got)
	}
}

// TestStoppedSubscriberListHoldsDelivery stops the list, as an operator might
// after a flood of bad subscriptions, and checks that nothing is mailed from
// its last snapshot and that restarts still succeed.
func TestStoppedSubscriberListHoldsDelivery(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	if err := harness.client.StopFlow(ctx, harness.subscriberFlowID, dex.StopOptions{Reason: "flooded"}); err != nil {
		t.Fatalf("stop the subscriber list: %v", err)
	}
	harness.waitFor(ctx, "subscriber list closed", func(attemptContext context.Context) bool {
		_, err := harness.subscriberList.AddNewsletterSubscriber(attemptContext, "late@example.com")
		return err != nil
	})
	if err := harness.subscriberList.StartList(ctx); err != nil {
		t.Fatalf("StartList after a stop = %v, want success so the application still starts", err)
	}
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	display := harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	if display["failed-stage"] != techblog.StageLoadSubscribers {
		t.Fatalf("failed stage = %v, want %s", display["failed-stage"], techblog.StageLoadSubscribers)
	}
	if got := harness.providers.gmail.totalSends(); got != 0 {
		t.Fatalf("Gmail sends from a stopped list = %d, want 0", got)
	}
}

// TestUnrecoveredPausedDeliveryExpiresAsDeliveryStopped lets the 7-day
// attention wait expire after a partial delivery.
func TestUnrecoveredPausedDeliveryExpiresAsDeliveryStopped(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	harness.providers.gmail.failAuthenticationAfterAccepted(1)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	harness.approveCurrentDraft(ctx, flowID)
	harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	harness.skipTimer(ctx, flowID, "WaitForOperatorRecovery", 1)
	result := harness.waitForResult(ctx, flowID)
	if result.Status != techblog.StatusDeliveryStopped || result.Delivery.Sent != 1 {
		t.Fatalf("result = %+v, want delivery-stopped after 1 send", result)
	}
	replies := harness.providers.slack.threadReplies(testChannelID)
	if final := replies[len(replies)-1]; !strings.Contains(final, "Do not post this request again") {
		t.Fatalf("final reply = %q, want the do-not-repost warning", final)
	}
}

// TestOneFailedRepositoryContinuesWithACoverageNote researches two
// repositories where one is not accessible.
func TestOneFailedRepositoryContinuesWithACoverageNote(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true, selectSecondRepository: true})
	harness.providers.github.setForbidden(testSecondRepositoryName, true)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors and the SDK", 1)
	display := harness.waitForStatus(ctx, flowID, techblog.StatusAwaitingEditorReview)
	digests, _ := display["repository-digests"].([]any)
	if len(digests) != 2 {
		t.Fatalf("digests = %v", digests)
	}
	replies := harness.providers.slack.threadReplies(testChannelID)
	failedRepository := regexp.MustCompile(regexp.QuoteMeta(testRepositoryOwner+"/"+testSecondRepositoryName) + `([^-A-Za-z0-9]|$)`)
	if last := replies[len(replies)-1]; !failedRepository.MatchString(last) {
		t.Fatalf("draft-ready message %q does not name the repository whose research failed", last)
	}
}

// TestAllRepositoriesFailingHoldsForAttentionThenRetrySucceeds never reports
// "no notable changes" when research could not run.
func TestAllRepositoriesFailingHoldsForAttentionThenRetrySucceeds(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true})
	harness.providers.github.setForbidden(testRepositoryName, true)
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	display := harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	if display["failed-stage"] != techblog.StageResearchRepos {
		t.Fatalf("failed stage = %v", display["failed-stage"])
	}
	harness.providers.github.setForbidden(testRepositoryName, false)
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.RetryFailedStage,
		techblog.RetryFailedStageInput{GateKey: display["attention-gate-key"].(string)}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	harness.waitForStatus(ctx, flowID, techblog.StatusAwaitingEditorReview)
}

// TestBlockedGenerationHoldsForAttention routes a provider safety block to
// operator recovery instead of failing the run.
func TestBlockedGenerationHoldsForAttention(t *testing.T) {
	ctx := integrationContext(t, 2*time.Minute)
	harness := newProcessHarness(t, fakeGeminiBehavior{understood: true, hasNotableChanges: true, blockedSynthesisResponses: 1})
	flowID := harness.deliverSlackRequest(ctx, "Blog about connectors", 1)
	display := harness.waitForStatus(ctx, flowID, techblog.StatusNeedsAttention)
	if display["failed-stage"] != techblog.StageSynthesizeResearch {
		t.Fatalf("failed stage = %v", display["failed-stage"])
	}
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.RetryFailedStage,
		techblog.RetryFailedStageInput{GateKey: display["attention-gate-key"].(string)}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	harness.waitForStatus(ctx, flowID, techblog.StatusAwaitingEditorReview)
}

func (harness *processHarness) approveCurrentDraft(ctx context.Context, flowID string) {
	harness.t.Helper()
	display := harness.waitForStatus(ctx, flowID, techblog.StatusAwaitingEditorReview)
	if err := harness.invokeAction(ctx, flowID, harness.newsletter.ApproveNewsletterForDelivery, techblog.ApproveNewsletterInput{
		GateKey: display["review-gate-key"].(string), Subject: subjectOf(display),
	}); err != nil {
		harness.t.Fatalf("approve: %v", err)
	}
}

func subjectOf(display map[string]any) string {
	subject, _ := display["newsletter-subject"].(string)
	return subject
}

type processHarness struct {
	t                 *testing.T
	configuration     config.ProcessConfiguration
	providers         fakeProviders
	artifactDirectory string
	cache             *blobcache.Cache
	client            *dex.Client
	worker            *dex.Worker
	newsletter        *techblog.TechBlogNewsletterFlow
	subscriberList    techblog.NewsletterSubscriberListClient
	subscriberFlow    *techblog.NewsletterSubscriberListFlow
	subscriberFlowID  string
	subscriberReader  *failableSubscriberReader
	unsubscribeKey    unsubscribe.Key
	teamID            string
	messageSequence   int
}

// defaultTestSubscribers seed each harness's subscriber list. Bob repeats in
// another case and one entry is not an address, so the list keeps
// defaultTestAudience addresses.
var defaultTestSubscribers = []string{"alice@example.com", "Bob@Example.com", "reject@example.com", "not an address", "dana@example.org", "bob@example.com"}

const defaultTestAudience = 4

func newProcessHarness(t *testing.T, behavior fakeGeminiBehavior) *processHarness {
	t.Helper()
	return newProcessHarnessWithSubscribers(t, behavior, defaultTestSubscribers)
}

// newProcessHarnessWithSubscribers seeds the harness's own subscriber list
// with subscribers; nil leaves it empty.
func newProcessHarnessWithSubscribers(t *testing.T, behavior fakeGeminiBehavior, subscribers []string) *processHarness {
	t.Helper()
	if os.Getenv("DEX_FLOW_SERVICE_ADDRESS") == "" {
		t.Skip("DEX_FLOW_SERVICE_ADDRESS is not set; run through scripts/with-dex.sh")
	}
	harness := &processHarness{
		t: t, providers: newFakeProviders(t, behavior), artifactDirectory: t.TempDir(),
		teamID: "T" + strconv.FormatInt(time.Now().UnixNano(), 36),
	}
	harness.configuration = config.Default()
	harness.configuration.Blog.ArtifactDirectory = harness.artifactDirectory
	harness.configuration.Blog.PublicBaseURL = "https://blog.example.com/posts"
	harness.configuration.Review.ReminderInterval = config.Duration(time.Hour)
	harness.configuration.Review.MaxReminders = 1
	harness.configuration.Newsletter.SubscriptionPageURL = "https://news.example.com/newsletter"
	key, err := unsubscribe.NewKey([]byte("integration-unsubscribe-key-0123456789"))
	if err != nil {
		t.Fatalf("unsubscribe key: %v", err)
	}
	harness.unsubscribeKey = key
	if err := harness.configuration.Validate(); err != nil {
		t.Fatalf("test configuration: %v", err)
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 256 << 20})
	if err != nil {
		t.Fatalf("create blob cache: %v", err)
	}
	harness.cache = cache
	registry := harness.newRegistry()
	harness.client, err = dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
		WorkerTarget:       &dex.WorkerTarget{Address: os.Getenv("DEX_WORKER_TARGET")},
	})
	if err != nil {
		t.Fatalf("create Dex client: %v", err)
	}
	harness.startWorker(registry)
	t.Cleanup(func() {
		harness.stopWorker()
		_ = harness.client.Close()
		_ = harness.cache.Close()
	})
	harness.seedSubscribers(subscribers)
	return harness
}

// seedSubscribers starts this harness's subscriber list Flow and subscribes
// the addresses through its RPC, as the subscription form does. Each call is
// retried until it succeeds, because Dex can briefly fail to reach a Worker
// that just replaced the previous test's; both calls are idempotent.
func (harness *processHarness) seedSubscribers(addresses []string) {
	harness.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	harness.waitFor(ctx, "subscriber list start", func(attemptContext context.Context) bool {
		return harness.subscriberList.StartList(attemptContext) == nil
	})
	for _, address := range addresses {
		harness.waitFor(ctx, "seed subscriber "+strconv.Itoa(len(address)), func(attemptContext context.Context) bool {
			_, err := harness.subscriberList.AddNewsletterSubscriber(attemptContext, address)
			return err == nil
		})
	}
}

// newRegistry constructs fresh Flow definitions, as a replacement process would.
func (harness *processHarness) newRegistry() *dex.Registry {
	harness.t.Helper()
	connections := harness.providers.connections(harness.t)
	artifactStore, err := techblog.NewDirectoryBlogArtifactStore(harness.artifactDirectory)
	if err != nil {
		harness.t.Fatalf("artifact store: %v", err)
	}
	languageModel := techblog.NewLanguageModelGenerationFlow(connections.gemini)
	research := techblog.NewRepositoryChangeResearchFlow(harness.configuration, connections.github, languageModel)
	// Each harness owns its subscriber list, so tests sharing one Dex server
	// never see each other's subscribers.
	subscriberListFlow := techblog.NewNewsletterSubscriberListFlow(harness.configuration, harness.unsubscribeKey)
	unsubscribeLinks, err := unsubscribe.NewLinks(harness.unsubscribeKey, harness.configuration.Newsletter.SubscriptionPageURL)
	if err != nil {
		harness.t.Fatalf("unsubscribe links: %v", err)
	}
	harness.subscriberFlow = subscriberListFlow
	harness.subscriberFlowID = "newsletter-subscriber-list-" + harness.teamID
	harness.subscriberList = techblog.NewNewsletterSubscriberListClient(subscriberListFlow,
		harness.subscriberFlowID, func() *dex.Client { return harness.client })
	if harness.subscriberReader == nil {
		harness.subscriberReader = &failableSubscriberReader{}
	}
	harness.subscriberReader.setReader(harness.subscriberList)
	harness.newsletter = techblog.NewTechBlogNewsletterFlow(techblog.TechBlogNewsletterFlowDependencies{
		Configuration: harness.configuration, SlackConnection: connections.slack, GmailConnection: connections.gmail,
		SubscriberList:          harness.subscriberReader,
		UnsubscribeLinks:        unsubscribeLinks,
		SubscriberListReadRetry: &dex.RetryPolicy{InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 1, MaximumAttempts: 3},
		BlogArtifactStore:       artifactStore, LanguageModelGeneration: languageModel, RepositoryResearch: research,
	})
	registry, err := dex.NewRegistry([]dex.Flow{harness.newsletter, research, languageModel, subscriberListFlow})
	if err != nil {
		harness.t.Fatalf("register Flows: %v", err)
	}
	return registry
}

func (harness *processHarness) startWorker(registry *dex.Registry) {
	harness.t.Helper()
	worker, err := dex.NewWorker(registry, harness.cache, dex.WorkerOptions{
		BindAddress:        os.Getenv("DEX_WORKER_BIND_ADDRESS"),
		WorkerTarget:       dex.WorkerTarget{Address: os.Getenv("DEX_WORKER_TARGET")},
		FlowServiceAddress: os.Getenv("DEX_FLOW_SERVICE_ADDRESS"),
	})
	if err != nil {
		harness.t.Fatalf("create Worker: %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- worker.Start() }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", os.Getenv("DEX_WORKER_TARGET"), 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		select {
		case err := <-result:
			harness.t.Fatalf("start Worker: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			harness.t.Fatalf("Worker listener did not open: %v", dialErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	harness.worker = worker
}

func (harness *processHarness) stopWorker() {
	if harness.worker == nil {
		return
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = harness.worker.Stop(stopContext)
	harness.worker = nil
}

func (harness *processHarness) replaceWorker(ctx context.Context) {
	harness.t.Helper()
	harness.stopWorker()
	harness.startWorker(harness.newRegistry())
	harness.t.Log("Worker replaced")
}

func (harness *processHarness) deliverSlackRequest(ctx context.Context, text string, deliveries int) string {
	harness.t.Helper()
	harness.messageSequence++
	timestamp := fmt.Sprintf("%d.%06d", time.Now().Unix(), harness.messageSequence)
	filter, err := techblog.NewsletterRequestTriggerFilter(slack.ChannelThreadCreatedTriggerConfiguration{ChannelID: testChannelID})
	if err != nil {
		harness.t.Fatalf("trigger filter: %v", err)
	}
	target := sdkgo.NewDexFlowTriggerTarget(harness.client, harness.newsletter, filter,
		techblog.ResolveNewsletterRequestFlowID, techblog.MapSlackMessageToNewsletterRequest)
	event := sdkgo.TriggerEvent[slack.MessageEvent]{
		ID: "Ev-" + harness.teamID + "-" + strconv.Itoa(harness.messageSequence), OccurredAt: time.Now().UTC(),
		Payload: slack.MessageEvent{
			TeamID: harness.teamID, ChannelID: testChannelID, Timestamp: timestamp, ThreadTimestamp: timestamp,
			UserID: "U-REQUESTER", Text: text,
		},
	}
	for delivery := 0; delivery < deliveries; delivery++ {
		if err := target.HandleTrigger(ctx, event); err != nil {
			harness.t.Fatalf("deliver Slack request %d: %v", delivery+1, err)
		}
	}
	ignoredReply := event
	ignoredReply.ID += "-reply"
	ignoredReply.Payload.Timestamp = timestamp + "1"
	if filter(ignoredReply) {
		harness.t.Fatal("the request filter admitted a thread reply")
	}
	return techblog.ResolveNewsletterRequestFlowID(event)
}

func (harness *processHarness) display(ctx context.Context, flowID string) (map[string]any, error) {
	var output map[string]any
	err := harness.client.InvokeRPC(ctx, flowID, harness.newsletter.GetDexDisplay, nil, &output)
	return output, err
}

func (harness *processHarness) waitForStatus(ctx context.Context, flowID string, status string) map[string]any {
	harness.t.Helper()
	var display map[string]any
	harness.waitFor(ctx, "status "+status, func(attemptContext context.Context) bool {
		current, err := harness.display(attemptContext, flowID)
		if err != nil {
			return false
		}
		display = current
		return current["newsletter-request-status"] == status
	})
	return display
}

func (harness *processHarness) waitForReviewGate(ctx context.Context, flowID string, gateKey string) map[string]any {
	harness.t.Helper()
	var display map[string]any
	harness.waitFor(ctx, "review gate "+gateKey, func(attemptContext context.Context) bool {
		current, err := harness.display(attemptContext, flowID)
		if err != nil {
			return false
		}
		display = current
		return current["newsletter-request-status"] == techblog.StatusAwaitingEditorReview && current["review-gate-key"] == gateKey
	})
	return display
}

func (harness *processHarness) waitForResult(ctx context.Context, flowID string) techblog.NewsletterResult {
	harness.t.Helper()
	flowResult, err := harness.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	if err != nil {
		harness.t.Fatalf("wait for Flow %s: %v", flowID, err)
	}
	if flowResult.Status != dex.FlowCompleted {
		display, _ := harness.display(ctx, flowID)
		harness.t.Fatalf("Flow %s ended with %s (%s): display %v", flowID, flowResult.Status, flowResult.ErrorMessage, display)
	}
	var result techblog.NewsletterResult
	if err := flowResult.DecodeSingleOutput(&result); err != nil {
		harness.t.Fatalf("decode result: %v", err)
	}
	return result
}

func (harness *processHarness) invokeAction(ctx context.Context, flowID string, action any, input any) error {
	var output dex.None
	return harness.client.InvokeRPC(ctx, flowID, action, input, &output)
}

func (harness *processHarness) skipEditorialTimer(ctx context.Context, flowID string, executionNumber int32) {
	harness.t.Helper()
	harness.skipTimer(ctx, flowID, "WaitForEditorialDecision", executionNumber)
}

// skipTimer fires the first Timer of one execution of a waiting Step.
func (harness *processHarness) skipTimer(ctx context.Context, flowID string, stepType string, executionNumber int32) {
	harness.t.Helper()
	timerIndex := int32(0)
	harness.waitFor(ctx, fmt.Sprintf("skip %s Timer %d", stepType, executionNumber), func(attemptContext context.Context) bool {
		return harness.client.SkipTimer(attemptContext, flowID,
			dex.StepExecutionID{StepType: stepType, ExecutionNumber: &executionNumber},
			dex.TimerID{Index: &timerIndex}) == nil
	})
}

func (harness *processHarness) waitFor(ctx context.Context, description string, condition func(context.Context) bool) {
	harness.t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		attemptContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		satisfied := condition(attemptContext)
		cancel()
		if satisfied {
			return
		}
		select {
		case <-ctx.Done():
			harness.t.Fatalf("timed out waiting for %s", description)
		case <-ticker.C:
		}
	}
}

func assertSelfContainedBlogArtifact(t *testing.T, path string, root string) {
	t.Helper()
	if !strings.HasPrefix(path, root) {
		t.Fatalf("artifact path %q is outside %q", path, root)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read blog artifact: %v", err)
	}
	html := strings.ToLower(string(contents))
	if !strings.HasPrefix(strings.TrimSpace(html), "<!doctype html") {
		t.Fatalf("artifact is not an HTML document: %.80s", html)
	}
	for _, forbidden := range []string{"<script", "<link", "<iframe", "<img src=\"http"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("artifact contains %q; it must be self-contained", forbidden)
		}
	}
	if filepath.Ext(path) != ".html" {
		t.Fatalf("artifact extension = %q", filepath.Ext(path))
	}
}

func stringPointer(value string) *string { return &value }

// failableSubscriberReader reads the harness's subscriber list and can be told
// to fail every read, as an unreachable Dex server would.
type failableSubscriberReader struct {
	mutex    sync.Mutex
	reader   techblog.SubscriberListReader
	failing  bool
	readings int
}

func (reader *failableSubscriberReader) setReader(next techblog.SubscriberListReader) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	reader.reader = next
}

func (reader *failableSubscriberReader) setFailing(failing bool) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	reader.failing = failing
}

func (reader *failableSubscriberReader) attempts() int {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	return reader.readings
}

func (reader *failableSubscriberReader) ListNewsletterSubscribers(ctx context.Context) ([]string, error) {
	reader.mutex.Lock()
	reader.readings++
	failing, next := reader.failing, reader.reader
	reader.mutex.Unlock()
	if failing {
		return nil, fmt.Errorf("the subscriber list Flow is unreachable")
	}
	return next.ListNewsletterSubscribers(ctx)
}

// deliveryExceptionsOf decodes the Dex Web display of delivery exceptions.
func deliveryExceptionsOf(t *testing.T, display map[string]any) []struct{ Recipient, Status string } {
	t.Helper()
	encoded, err := json.Marshal(display["delivery-exceptions"])
	if err != nil {
		t.Fatalf("encode delivery exceptions: %v", err)
	}
	var exceptions []struct{ Recipient, Status string }
	if err := json.Unmarshal(encoded, &exceptions); err != nil {
		t.Fatalf("decode delivery exceptions %s: %v", encoded, err)
	}
	return exceptions
}

func integrationContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

type fakeProviders struct {
	slack  *fakeSlack
	github *fakeGitHub
	gemini *fakeGemini
	gmail  *fakeGmail
}

type testConnections struct {
	slack  slack.Connection
	github github.Connection
	gemini gemini.Connection
	gmail  gmail.Connection
}

func newFakeProviders(t *testing.T, behavior fakeGeminiBehavior) fakeProviders {
	t.Helper()
	providers := fakeProviders{
		slack: newFakeSlack(t), github: newFakeGitHub(t), gemini: newFakeGemini(t, behavior), gmail: newFakeGmail(t),
	}
	return providers
}

func (providers fakeProviders) connections(t *testing.T) testConnections {
	t.Helper()
	var connections testConnections
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("create test connection: %v", err)
		}
	}
	slackReference := sdkgo.ConnectionRef{Provider: "slack", Name: techblog.SlackConnectionName}
	slackConfig := slack.DefaultConfig()
	slackConfig.Endpoint = providers.slack.URL
	slackClient, err := slack.New(slackConfig, sdkgo.StaticCredentialProvider[slack.Credentials]{slackReference: {
		BotToken: sdkgo.NewSecretString("xoxb-test"), UserToken: sdkgo.NewSecretString("xoxp-test"), AppToken: sdkgo.NewSecretString("xapp-test"),
	}})
	must(err)
	connections.slack, err = slack.NewConnection(slackClient, slackReference)
	must(err)

	githubReference := sdkgo.ConnectionRef{Provider: "github", Name: techblog.GitHubConnectionName}
	githubConfig := github.DefaultConfig()
	githubConfig.BaseURL = providers.github.URL
	githubClient, err := github.New(githubConfig, sdkgo.StaticCredentialProvider[github.Credentials]{githubReference: {AccessToken: sdkgo.NewSecretString("gho-test")}})
	must(err)
	connections.github, err = github.NewConnection(githubClient, githubReference)
	must(err)

	geminiReference := sdkgo.ConnectionRef{Provider: "google", Name: techblog.GeminiConnectionName}
	geminiConfig := gemini.DefaultConfig()
	geminiConfig.Endpoint = providers.gemini.URL
	geminiClient, err := gemini.New(geminiConfig, sdkgo.StaticCredentialProvider[gemini.Credentials]{geminiReference: {APIKey: sdkgo.NewSecretString("gemini-test-key")}})
	must(err)
	connections.gemini, err = gemini.NewConnection(geminiClient, geminiReference)
	must(err)

	gmailReference := sdkgo.ConnectionRef{Provider: "google", Name: techblog.GmailConnectionName}
	gmailConfig := gmail.DefaultConfig()
	gmailConfig.Endpoint = providers.gmail.URL
	gmailClient, err := gmail.New(gmailConfig, sdkgo.StaticCredentialProvider[gmail.Credentials]{gmailReference: {
		AccessToken: sdkgo.NewSecretString("ya29-gmail"), PrimaryEmail: "newsletter@example.com",
	}})
	must(err)
	connections.gmail, err = gmail.NewConnection(gmailClient, gmailReference)
	must(err)
	return connections
}

type fakeSlack struct {
	*httptest.Server
	mutex   sync.Mutex
	replies map[string][]string
	nextTS  int
}

func newFakeSlack(t *testing.T) *fakeSlack {
	provider := &fakeSlack{replies: map[string][]string{}}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat.postMessage" {
			http.NotFound(writer, request)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		provider.mutex.Lock()
		provider.nextTS++
		timestamp := fmt.Sprintf("%d.000100", 2000000000+provider.nextTS)
		provider.replies[payload["channel"]] = append(provider.replies[payload["channel"]], payload["text"])
		provider.mutex.Unlock()
		writeJSON(writer, http.StatusOK, map[string]any{
			"ok": true, "channel": payload["channel"], "ts": timestamp,
			"message": map[string]any{"ts": timestamp, "text": payload["text"], "thread_ts": payload["thread_ts"], "user": "U-BOT"},
		})
	}))
	t.Cleanup(provider.Close)
	return provider
}

func (provider *fakeSlack) threadReplies(channelID string) []string {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return append([]string(nil), provider.replies[channelID]...)
}

type fakeGitHub struct {
	*httptest.Server
	mutex     sync.Mutex
	requests  int
	forbidden map[string]bool
}

func (provider *fakeGitHub) setForbidden(repositoryName string, forbidden bool) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.forbidden[repositoryName] = forbidden
}

func (provider *fakeGitHub) isForbidden(request *http.Request) bool {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	for name, forbidden := range provider.forbidden {
		if !forbidden {
			continue
		}
		fullName := testRepositoryOwner + "/" + name
		if strings.Contains(request.URL.Query().Get("q"), "repo:"+fullName+" ") ||
			strings.HasSuffix(request.URL.Query().Get("q"), "repo:"+fullName) ||
			strings.HasPrefix(request.URL.Path, "/repos/"+fullName+"/") {
			return true
		}
	}
	return false
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	provider := &fakeGitHub{forbidden: map[string]bool{}}
	mergedAt := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	repositoryPath := "/repos/" + testRepositoryOwner + "/" + testRepositoryName
	provider.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provider.mutex.Lock()
		provider.requests++
		provider.mutex.Unlock()
		// The connector enforces the exact OAuth grant from this header.
		writer.Header().Set("X-OAuth-Scopes", "read:user, user:email")
		if provider.isForbidden(request) {
			writer.Header().Set("X-RateLimit-Remaining", "4999")
			writeJSON(writer, http.StatusForbidden, map[string]any{"message": "Resource not accessible by integration"})
			return
		}
		switch {
		case request.URL.Path == "/search/issues":
			if !strings.Contains(request.URL.Query().Get("q"), "repo:"+testRepositoryOwner+"/"+testRepositoryName) {
				writeJSON(writer, http.StatusOK, map[string]any{"total_count": 0, "incomplete_results": false, "items": []any{}})
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{
				"total_count": 1, "incomplete_results": false,
				"items": []any{map[string]any{
					"number": 101, "title": "Add a Gemini connector", "html_url": testPullRequestURL,
					"body": "Adds connectors/google/gemini with generateContent and structured JSON output.",
					"user": map[string]any{"login": "octocat"}, "labels": []any{map[string]any{"name": "connector"}},
					"pull_request": map[string]any{"merged_at": mergedAt},
				}},
			})
		case request.URL.Path == repositoryPath+"/pulls/101/files":
			writeJSON(writer, http.StatusOK, []any{map[string]any{
				"filename": "connectors/google/gemini/client.go", "status": "added", "additions": 300, "deletions": 0, "changes": 300,
				"patch": "@@ -0,0 +1,3 @@\n+package gemini\n+// generateContent\n+",
			}})
		case request.URL.Path == repositoryPath+"/commits":
			writeJSON(writer, http.StatusOK, []any{map[string]any{
				"sha": "abc123def456", "html_url": testCommitURL, "author": map[string]any{"login": "octocat"},
				"commit": map[string]any{
					"message":   "connector(google/gemini): add generateContent",
					"author":    map[string]any{"name": "octocat", "date": mergedAt},
					"committer": map[string]any{"name": "octocat", "date": mergedAt},
				},
			}})
		default:
			writeJSON(writer, http.StatusNotFound, map[string]any{"message": "Not Found"})
		}
	}))
	t.Cleanup(provider.Close)
	return provider
}

func (provider *fakeGitHub) requestCount() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.requests
}

type fakeGeminiBehavior struct {
	understood                bool
	hasNotableChanges         bool
	invalidSynthesisResponses int
	blockedSynthesisResponses int
	selectSecondRepository    bool
}

type fakeGemini struct {
	*httptest.Server
	behavior    fakeGeminiBehavior
	mutex       sync.Mutex
	counts      map[string]int
	lastPrompts map[string]string
}

func newFakeGemini(t *testing.T, behavior fakeGeminiBehavior) *fakeGemini {
	provider := &fakeGemini{behavior: behavior, counts: map[string]int{}, lastPrompts: map[string]string{}}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-goog-api-key") != "gemini-test-key" || request.URL.Query().Get("key") != "" {
			writeJSON(writer, http.StatusForbidden, map[string]any{"error": map[string]any{"code": 403, "status": "PERMISSION_DENIED", "message": "bad key"}})
			return
		}
		if !strings.HasSuffix(request.URL.Path, ":generateContent") {
			http.NotFound(writer, request)
			return
		}
		body, _ := io.ReadAll(request.Body)
		stage := classifyGeminiStage(body)
		provider.mutex.Lock()
		provider.counts[stage]++
		count := provider.counts[stage]
		provider.lastPrompts[stage] = string(body)
		provider.mutex.Unlock()
		if stage == "synthesis" && count <= provider.behavior.blockedSynthesisResponses {
			writeJSON(writer, http.StatusOK, map[string]any{
				"candidates":    []any{map[string]any{"finishReason": "SAFETY", "content": map[string]any{"role": "model", "parts": []any{}}}},
				"usageMetadata": map[string]any{"promptTokenCount": 100, "totalTokenCount": 100},
				"modelVersion":  "gemini-2.5-flash-test",
			})
			return
		}
		text := provider.responseText(stage, count, string(body))
		writeJSON(writer, http.StatusOK, map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 100, "candidatesTokenCount": 50, "totalTokenCount": 150},
			"modelVersion":  "gemini-2.5-flash-test", "responseId": fmt.Sprintf("%s-%d", stage, count),
		})
	}))
	t.Cleanup(provider.Close)
	return provider
}

// classifyGeminiStage identifies the stage by a property unique to its
// response schema; the test asserts every expected stage was reached.
func classifyGeminiStage(body []byte) string {
	var request struct {
		GenerationConfig struct {
			ResponseJSONSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"responseJsonSchema"`
		} `json:"generationConfig"`
	}
	_ = json.Unmarshal(body, &request)
	properties := request.GenerationConfig.ResponseJSONSchema.Properties
	for _, candidate := range []struct{ property, stage string }{
		{"repositorySelections", "interpret"}, {"hasNotableChanges", "synthesis"}, {"preheader", "newsletter"},
		{"sections", "blog"}, {"relevant", "summary"},
	} {
		if _, found := properties[candidate.property]; found {
			return candidate.stage
		}
	}
	return "unknown"
}

func (provider *fakeGemini) responseText(stage string, count int, body string) string {
	highlight := map[string]any{
		"title": "Gemini connector", "whatChanged": "A new gemini connector with generateContent.",
		"howItWorks":   "It maps provider-neutral requests to models.generateContent with JSON schema output.",
		"whyItMatters": "Processes can use Gemini without provider code.",
		"references":   []any{map[string]any{"label": "PR #101", "url": testPullRequestURL}},
	}
	encode := func(value any) string {
		contents, _ := json.Marshal(value)
		return string(contents)
	}
	switch stage {
	case "interpret":
		if !provider.behavior.understood {
			return encode(map[string]any{
				"understood": false, "clarificationQuestion": "Which area of the codebase should the post cover?",
				"topic": "", "instructions": "", "audience": "", "lookbackDays": 0, "repositorySelections": []any{},
			})
		}
		return encode(map[string]any{
			"understood": true, "clarificationQuestion": "", "topic": "Connectors", "instructions": "Focus on new connectors.",
			"audience": "Engineers", "lookbackDays": 14,
			"repositorySelections": provider.repositorySelections(),
		})
	case "summary":
		return encode(map[string]any{"relevant": true, "summary": "A Gemini connector landed.", "highlights": []any{highlight}})
	case "synthesis":
		if count <= provider.behavior.invalidSynthesisResponses {
			return "this is not json"
		}
		if !provider.behavior.hasNotableChanges {
			return encode(map[string]any{"hasNotableChanges": false, "headline": "", "overview": "Nothing notable.", "highlights": []any{}, "openQuestions": []any{}})
		}
		return encode(map[string]any{
			"hasNotableChanges": true, "headline": "Connectors speak Gemini", "overview": "One notable connector change.",
			"highlights": []any{highlight}, "openQuestions": []any{},
		})
	case "blog":
		title := "Connectors now speak Gemini"
		if count > 1 {
			title += " (revised)"
		}
		return encode(map[string]any{
			"title": title, "subtitle": "What changed in the connector library", "slug": "connectors-speak-gemini",
			"summary": "A new Gemini connector.", "tags": []any{"connectors"},
			"sections": []any{map[string]any{"heading": "What changed", "blocks": []any{
				map[string]any{"type": "paragraph", "text": "The `gemini` connector landed in [PR #101](" + testPullRequestURL + ")."},
				map[string]any{"type": "code", "language": "go", "code": "gemini.NewGenerateContentStep(config)"},
			}}},
			"references": []any{map[string]any{"label": "PR #101", "url": testPullRequestURL}},
		})
	case "newsletter":
		return encode(map[string]any{
			"subject": "This week in Dex: Gemini connector", "preheader": "Connectors now speak Gemini.",
			"intro": "Here is what changed in the connector library.", "closing": "Thanks for reading.",
			"highlights": []any{map[string]any{"title": "Gemini connector", "text": "Generate content from Processes."}},
		})
	default:
		return "{}"
	}
}

func (provider *fakeGemini) repositorySelections() []any {
	selections := []any{map[string]any{
		"repository": map[string]any{"owner": testRepositoryOwner, "name": testRepositoryName},
		"pathHints":  []any{"connectors/", "sdkgo/"}, "reason": "Connectors live here.",
	}}
	if provider.behavior.selectSecondRepository {
		selections = append(selections, map[string]any{
			"repository": map[string]any{"owner": testRepositoryOwner, "name": testSecondRepositoryName},
			"pathHints":  []any{"sdk-go/"}, "reason": "The SDK consumes connectors.",
		})
	}
	return selections
}

func (provider *fakeGemini) requestCount(stage string) int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.counts[stage]
}

func (provider *fakeGemini) lastPromptContains(stage string, text string) bool {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return strings.Contains(provider.lastPrompts[stage], text)
}

type fakeGmail struct {
	*httptest.Server
	mutex                 sync.Mutex
	sends                 map[string]int
	accepted              map[string]int
	lastBody              string
	bodies                map[string]string
	next                  int
	authenticationFailure bool
	// failAfterAccepted, when positive, fails authentication once that many
	// sends were accepted.
	failAfterAccepted     int
	unavailableRecipients map[string]bool
	sendDelays            map[string]time.Duration
}

// delaySendsTo makes every send to recipient answer only after delay.
func (provider *fakeGmail) delaySendsTo(recipient string, delay time.Duration) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.sendDelays[strings.ToLower(recipient)] = delay
}

func (provider *fakeGmail) failAuthenticationAfterAccepted(accepted int) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.failAfterAccepted = accepted
}

func (provider *fakeGmail) setAuthenticationFailure(failing bool) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.authenticationFailure = failing
}

func (provider *fakeGmail) setUnavailableRecipient(recipient string) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.unavailableRecipients[strings.ToLower(recipient)] = true
}

func (provider *fakeGmail) acceptedCount(recipient string) int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.accepted[strings.ToLower(recipient)]
}

func (provider *fakeGmail) totalAccepted() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	total := 0
	for _, count := range provider.accepted {
		total += count
	}
	return total
}

func newFakeGmail(t *testing.T) *fakeGmail {
	provider := &fakeGmail{sends: map[string]int{}, accepted: map[string]int{}, unavailableRecipients: map[string]bool{}, sendDelays: map[string]time.Duration{}, bodies: map[string]string{}}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/users/me/messages/send" {
			http.NotFound(writer, request)
			return
		}
		var payload struct {
			Raw string `json:"raw"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(payload.Raw)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		recipient := ""
		for _, line := range strings.Split(string(raw), "\r\n") {
			if strings.HasPrefix(line, "To: ") {
				recipient = strings.Trim(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "To: "))), "<>")
				break
			}
		}
		provider.mutex.Lock()
		provider.sends[recipient]++
		provider.next++
		identifier := provider.next
		authenticationFailure := provider.authenticationFailure
		if provider.failAfterAccepted > 0 {
			acceptedSoFar := 0
			for _, count := range provider.accepted {
				acceptedSoFar += count
			}
			authenticationFailure = authenticationFailure || acceptedSoFar >= provider.failAfterAccepted
		}
		unavailable := provider.unavailableRecipients[recipient]
		if !authenticationFailure && !unavailable && !strings.HasPrefix(recipient, "reject@") {
			provider.accepted[recipient]++
			provider.lastBody = string(raw)
			provider.bodies[recipient] = string(raw) + decodeMIMEBodies(string(raw))
		}
		delay := provider.sendDelays[recipient]
		provider.mutex.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-request.Context().Done():
				return
			}
		}
		if authenticationFailure {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": 401, "status": "UNAUTHENTICATED", "message": "Invalid Credentials"}})
			return
		}
		if unavailable {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": 503, "status": "UNAVAILABLE", "message": "backend error"}})
			return
		}
		if strings.HasPrefix(recipient, "reject@") {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT", "message": "Invalid To header"}})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"id": fmt.Sprintf("message-%d", identifier), "threadId": fmt.Sprintf("thread-%d", identifier)})
	}))
	t.Cleanup(provider.Close)
	return provider
}

// bodyTo returns the decoded message last accepted for recipient.
func (provider *fakeGmail) bodyTo(recipient string) string {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.bodies[strings.ToLower(recipient)]
}

func (provider *fakeGmail) sendCount(recipient string) int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.sends[strings.ToLower(recipient)]
}

func (provider *fakeGmail) totalSends() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	total := 0
	for _, count := range provider.sends {
		total += count
	}
	return total
}

func (provider *fakeGmail) lastBodyContains(text string) bool {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	body := provider.lastBody
	if decoded := decodeMIMEBodies(body); decoded != "" {
		body += decoded
	}
	return strings.Contains(body, text)
}

// decodeMIMEBodies decodes base64 MIME parts so assertions can read them.
func decodeMIMEBodies(message string) string {
	var decoded strings.Builder
	for _, part := range strings.Split(message, "\r\n\r\n") {
		compact := strings.ReplaceAll(strings.TrimSpace(part), "\r\n", "")
		if compact == "" {
			continue
		}
		if contents, err := base64.StdEncoding.DecodeString(compact); err == nil {
			decoded.Write(contents)
		}
	}
	return decoded.String()
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
