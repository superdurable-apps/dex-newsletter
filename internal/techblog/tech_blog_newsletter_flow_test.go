package techblog

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/unsubscribe"
	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
)

func TestNewsletterRequestTriggerFilterAdmitsOnlyTopLevelMessagesInTheChannel(t *testing.T) {
	filter, err := NewsletterRequestTriggerFilter(slack.ChannelThreadCreatedTriggerConfiguration{
		ChannelID:            "C1",
		ThreadTriggerMatcher: slack.MessageMatcher{MessageContains: "blog", PosterUserIDs: []string{"U1"}},
	})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	base := slack.MessageEvent{TeamID: "T1", ChannelID: "C1", Timestamp: "1.0", ThreadTimestamp: "1.0", UserID: "U1", Text: "Write a Blog post"}
	cases := []struct {
		name   string
		mutate func(*slack.MessageEvent)
		id     string
		admit  bool
	}{
		{"admits matching root message", func(*slack.MessageEvent) {}, "Ev1", true},
		{"rejects other channel", func(message *slack.MessageEvent) { message.ChannelID = "C2" }, "Ev1", false},
		{"rejects thread reply", func(message *slack.MessageEvent) { message.Timestamp = "2.0" }, "Ev1", false},
		{"rejects other poster", func(message *slack.MessageEvent) { message.UserID = "U2" }, "Ev1", false},
		{"rejects missing text match", func(message *slack.MessageEvent) { message.Text = "hello" }, "Ev1", false},
		{"rejects blank text", func(message *slack.MessageEvent) { message.Text = "   " }, "Ev1", false},
		{"rejects missing event ID", func(*slack.MessageEvent) {}, "", false},
		{"rejects missing team", func(message *slack.MessageEvent) { message.TeamID = "" }, "Ev1", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			message := base
			testCase.mutate(&message)
			event := sdkgo.TriggerEvent[slack.MessageEvent]{ID: testCase.id, OccurredAt: time.Unix(1, 0), Payload: message}
			if got := filter(event); got != testCase.admit {
				t.Fatalf("filter = %v, want %v", got, testCase.admit)
			}
		})
	}
}

func TestResolveNewsletterRequestFlowIDIsStablePerMessage(t *testing.T) {
	event := sdkgo.TriggerEvent[slack.MessageEvent]{ID: "Ev1", Payload: slack.MessageEvent{TeamID: "T1", ChannelID: "C1", Timestamp: "1700000000.000100"}}
	redelivered := event
	redelivered.ID = "Ev1-retry"
	if ResolveNewsletterRequestFlowID(event) != ResolveNewsletterRequestFlowID(redelivered) {
		t.Fatal("redelivery must resolve to the same Flow ID")
	}
	if got, want := ResolveNewsletterRequestFlowID(event), "tech-blog-newsletter-T1-C1-1700000000.000100"; got != want {
		t.Fatalf("Flow ID = %q, want %q", got, want)
	}
	request := MapSlackMessageToNewsletterRequest(sdkgo.TriggerEvent[slack.MessageEvent]{ID: "Ev2", Payload: slack.MessageEvent{
		TeamID: "T1", ChannelID: "C1", Timestamp: "5.0", UserID: "U1", Text: "blog",
	}})
	if request.ThreadTimestamp != "5.0" || request.SlackEventID != "Ev2" || request.RequestText != "blog" {
		t.Fatalf("mapped request = %+v", request)
	}
}

func TestAddSubscriberKeepsOneCanonicalEntryPerAddress(t *testing.T) {
	list, result := addSubscriber(nil, " Reader@Example.COM ", 2)
	if result != (AddNewsletterSubscriberResult{Outcome: SubscriptionAdded, Email: "reader@example.com"}) || !reflect.DeepEqual(list, []string{"reader@example.com"}) {
		t.Fatalf("first subscription = %q, %+v", list, result)
	}
	again, result := addSubscriber(list, "reader@example.com", 2)
	if result != (AddNewsletterSubscriberResult{Outcome: SubscriptionAlreadySubscribed, Email: "reader@example.com"}) || !reflect.DeepEqual(again, list) {
		t.Fatalf("repeat subscription = %q, %+v", again, result)
	}
	invalid, result := addSubscriber(list, "Reader <reader@example.com>", 2)
	if result != (AddNewsletterSubscriberResult{Outcome: SubscriptionInvalidAddress}) || !reflect.DeepEqual(invalid, list) {
		t.Fatalf("invalid subscription = %q, %+v", invalid, result)
	}
	full, result := addSubscriber([]string{"a@example.com", "b@example.com"}, "c@example.com", 2)
	if result != (AddNewsletterSubscriberResult{Outcome: SubscriptionListFull}) || len(full) != 2 {
		t.Fatalf("subscription to a full list = %q, %+v", full, result)
	}
	// A full list answers the same for a subscriber and a stranger, so the
	// response never reveals who is on it.
	if _, result := addSubscriber([]string{"a@example.com", "b@example.com"}, "B@example.com", 2); result != (AddNewsletterSubscriberResult{Outcome: SubscriptionListFull}) {
		t.Fatalf("an existing subscriber of a full list = %+v, want list-full", result)
	}
}

func TestAddSubscriberNeverModifiesTheCurrentList(t *testing.T) {
	current := make([]string, 1, 4)
	current[0] = "a@example.com"
	updated, _ := addSubscriber(current, "b@example.com", 10)
	updated[0] = "changed@example.com"
	if current[0] != "a@example.com" || len(current) != 1 {
		t.Fatalf("addSubscriber aliased the current list: %q", current)
	}
}

func TestSubscriberListStartsWithAConstantRequestID(t *testing.T) {
	first, second := NewsletterSubscriberListStartOptions(), NewsletterSubscriberListStartOptions()
	if first.RequestID == nil || second.RequestID == nil || *first.RequestID != *second.RequestID {
		t.Fatalf("start request IDs = %v and %v; every start must send the same ID so Dex returns the existing list", first.RequestID, second.RequestID)
	}
	if first.AlreadyStarted == nil || !first.AlreadyStarted.IgnoreError || first.Timeout != nil {
		t.Fatalf("start options = %+v; want AlreadyStarted.IgnoreError and no timeout", first)
	}
}

func TestRemoveSubscriberRemovesOnlyTheAddressATokenNames(t *testing.T) {
	key, err := unsubscribe.NewKey([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	current := []string{"a@example.com", "b@example.com", "c@example.com"}
	updated, result := removeSubscriber(current, key.Token("b@example.com"), key)
	if result.Outcome != UnsubscriptionRemoved || !reflect.DeepEqual(updated, []string{"a@example.com", "c@example.com"}) {
		t.Fatalf("remove b = %q, %+v", updated, result)
	}
	if !reflect.DeepEqual(current, []string{"a@example.com", "b@example.com", "c@example.com"}) {
		t.Fatalf("removeSubscriber modified its input: %q", current)
	}
	again, result := removeSubscriber(updated, key.Token("b@example.com"), key)
	if result.Outcome != UnsubscriptionNotSubscribed || !reflect.DeepEqual(again, updated) {
		t.Fatalf("second remove of b = %q, %+v, want not-subscribed", again, result)
	}
	otherKey, _ := unsubscribe.NewKey([]byte(strings.Repeat("o", 32)))
	if _, result := removeSubscriber(current, otherKey.Token("b@example.com"), key); result.Outcome != UnsubscriptionNotSubscribed {
		t.Fatalf("a token from another key = %+v, want not-subscribed", result)
	}
	for _, token := range []string{"", "short", strings.Repeat("a", 21) + "="} {
		if _, result := removeSubscriber(current, token, key); result.Outcome != UnsubscriptionInvalidToken {
			t.Errorf("token %q = %+v, want invalid-token", token, result)
		}
	}
	if emptied, result := removeSubscriber([]string{"a@example.com"}, key.Token("a@example.com"), key); result.Outcome != UnsubscriptionRemoved || len(emptied) != 0 {
		t.Fatalf("removing the last subscriber = %q, %+v", emptied, result)
	}
}

func TestSanitizeSubjectRemovesHeaderInjectionAndBoundsLength(t *testing.T) {
	if got := sanitizeSubject("Weekly\r\nBcc: attacker@example.com"); got != "Weekly Bcc: attacker@example.com" {
		t.Fatalf("sanitized subject = %q", got)
	}
	long := ""
	for len(long) < 400 {
		long += "subject "
	}
	if got := []rune(sanitizeSubject(long)); len(got) > maximumSubjectRunes {
		t.Fatalf("subject length = %d, want <= %d", len(got), maximumSubjectRunes)
	}
	if sanitizeSubject(" \t ") != "" {
		t.Fatal("blank subject must sanitize to empty so the drafted subject is kept")
	}
}

func TestValidateOpenGateRejectsStaleOrClosedGates(t *testing.T) {
	if err := validateOpenGate(StatusAwaitingEditorReview, StatusAwaitingEditorReview, "review-1", "review-1"); err != nil {
		t.Fatalf("open gate rejected: %v", err)
	}
	if err := validateOpenGate(StatusAwaitingEditorReview, StatusAwaitingEditorReview, "review-1", "review-0"); err == nil {
		t.Fatal("stale gate accepted")
	}
	if err := validateOpenGate(StatusDeliveryApproved, StatusAwaitingEditorReview, "review-1", "review-1"); err == nil {
		t.Fatal("closed gate accepted")
	}
}

func TestDirectoryBlogArtifactStoreWritesIdempotentlyInsideItsRoot(t *testing.T) {
	root := t.TempDir()
	store, err := NewDirectoryBlogArtifactStore(root)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	first, err := store.WriteBlogArtifact("flow/../../escape", "../post.html", "<!doctype html>one")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	second, err := store.WriteBlogArtifact("flow/../../escape", "../post.html", "<!doctype html>two")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if first != second {
		t.Fatalf("rewrite changed the path: %q vs %q", first, second)
	}
	if len(first) <= len(root) || first[:len(root)] != root {
		t.Fatalf("artifact %q escaped root %q", first, root)
	}
}
