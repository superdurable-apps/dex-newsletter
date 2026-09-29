package blogpost

import (
	"testing"
	"time"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
)

func TestRequestTriggerFilter(t *testing.T) {
	filter, err := NewRequestTriggerFilter(slack.ChannelThreadCreatedTriggerConfiguration{
		ChannelID: "C1", ThreadTriggerMatcher: slack.MessageMatcher{PosterUserIDs: []string{"U1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := slack.MessageEvent{TeamID: "T", ChannelID: "C1", Timestamp: "1.0", ThreadTimestamp: "1.0", UserID: "U1", Text: "Write a post about connectors"}
	cases := map[string]struct {
		mutate func(*slack.MessageEvent)
		want   bool
	}{
		"root message":       {func(*slack.MessageEvent) {}, true},
		"root without ts":    {func(event *slack.MessageEvent) { event.ThreadTimestamp = "" }, true},
		"thread reply":       {func(event *slack.MessageEvent) { event.Timestamp = "1.5" }, false},
		"other channel":      {func(event *slack.MessageEvent) { event.ChannelID = "C2" }, false},
		"member not allowed": {func(event *slack.MessageEvent) { event.UserID = "U9" }, false},
		"blank text":         {func(event *slack.MessageEvent) { event.Text = "  " }, false},
		"bot without user":   {func(event *slack.MessageEvent) { event.UserID = "" }, false},
	}
	for name, testCase := range cases {
		event := root
		testCase.mutate(&event)
		if got := filter(sdkgo.TriggerEvent[slack.MessageEvent]{ID: "Ev1", Payload: event}); got != testCase.want {
			t.Errorf("%s: admitted = %v, want %v", name, got, testCase.want)
		}
	}
	if _, err := NewRequestTriggerFilter(slack.ChannelThreadCreatedTriggerConfiguration{}); err == nil {
		t.Fatal("a binding without a channel was accepted")
	}
}

func TestRequestIdentityAndInput(t *testing.T) {
	occurred := time.Date(2026, 9, 28, 1, 2, 3, 0, time.FixedZone("x", 3600))
	event := sdkgo.TriggerEvent[slack.MessageEvent]{ID: "Ev1", OccurredAt: occurred, Payload: slack.MessageEvent{
		TeamID: "T", ChannelID: "C1", Timestamp: "1.0", UserID: "U1", Text: "hi",
	}}
	if id := ResolveRequestFlowID(event); id != "blog-post-T-C1-1.0" {
		t.Fatalf("Flow ID = %q", id)
	}
	if input := MapToSlackRequest(event); input.MessageTimestamp != "1.0" || input.EventID != "Ev1" || input.ReceivedAt.Location() != time.UTC {
		t.Fatalf("input = %+v", input)
	}
}
