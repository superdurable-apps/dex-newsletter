package blogpost

import (
	"net/url"
	"strings"
	"testing"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
)

func TestParseReviewReply(t *testing.T) {
	cases := []struct {
		text     string
		command  ReviewCommand
		feedback string
	}{
		{"approve", ReviewApprove, ""},
		{"Approve.", ReviewApprove, ""},
		{"APPROVED!", ReviewApprove, ""},
		{"<@U1> approve", ReviewApprove, ""},
		{"approve <@U1>", ReviewApprove, ""},
		{"  *approve*  ", ReviewApprove, ""},
		{"`approved`", ReviewApprove, ""},
		{"reject", ReviewReject, ""},
		{"Rejected.", ReviewReject, ""},
		{"<@U1> REJECT!", ReviewReject, ""},
		{"_reject_", ReviewReject, ""},
		{"approve but fix the title", ReviewFeedback, "approve but fix the title"},
		{"please approve this", ReviewFeedback, "please approve this"},
		{"reject the second section", ReviewFeedback, "reject the second section"},
		{"approve?", ReviewFeedback, "approve?"},
		{"", ReviewFeedback, ""},
		{"   ", ReviewFeedback, ""},
		{"<@U1>", ReviewFeedback, ""},
		{"<@U1> <!here> Shorten the intro.\n", ReviewFeedback, "Shorten the intro."},
		{"<#C1|general> Mention the retry fix", ReviewFeedback, "Mention the retry fix"},
	}
	for _, testCase := range cases {
		command, feedback := ParseReviewReply(testCase.text)
		if command != testCase.command || feedback != testCase.feedback {
			t.Errorf("ParseReviewReply(%q) = %q, %q; want %q, %q", testCase.text, command, feedback, testCase.command, testCase.feedback)
		}
	}
	command, feedback := ParseReviewReply("<@U1> " + strings.Repeat("é", 4500))
	if command != ReviewFeedback || feedback != strings.Repeat("é", 4000) {
		t.Fatalf("long feedback = %q with %d characters", command, len([]rune(feedback)))
	}
}

func reviewConfiguration() slack.ThreadReplyCreatedTriggerConfiguration {
	return slack.ThreadReplyCreatedTriggerConfiguration{
		ChannelID: "C1", ThreadReplyMatcher: slack.MessageMatcher{PosterUserIDs: []string{"U1", "U2"}},
	}
}

func TestReviewTriggerFilter(t *testing.T) {
	filter, err := NewReviewTriggerFilter(reviewConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	reply := slack.MessageEvent{TeamID: "T", ChannelID: "C1", Timestamp: "1.5", ThreadTimestamp: "1.0", UserID: "U1", Text: "approve"}
	cases := map[string]struct {
		eventID string
		mutate  func(*slack.MessageEvent)
		want    bool
	}{
		"reviewer reply":   {"Ev1", func(*slack.MessageEvent) {}, true},
		"second reviewer":  {"Ev1", func(event *slack.MessageEvent) { event.UserID = "U2" }, true},
		"root message":     {"Ev1", func(event *slack.MessageEvent) { event.Timestamp = "1.0" }, false},
		"root without ts":  {"Ev1", func(event *slack.MessageEvent) { event.ThreadTimestamp = "" }, false},
		"other channel":    {"Ev1", func(event *slack.MessageEvent) { event.ChannelID = "C2" }, false},
		"not a reviewer":   {"Ev1", func(event *slack.MessageEvent) { event.UserID = "U9" }, false},
		"bot without user": {"Ev1", func(event *slack.MessageEvent) { event.UserID = "" }, false},
		"blank text":       {"Ev1", func(event *slack.MessageEvent) { event.Text = " \n " }, false},
		"missing team":     {"Ev1", func(event *slack.MessageEvent) { event.TeamID = "" }, false},
		"missing event ID": {"", func(*slack.MessageEvent) {}, false},
		"feedback reply":   {"Ev1", func(event *slack.MessageEvent) { event.Text = "Shorten the intro" }, true},
	}
	for name, testCase := range cases {
		event := reply
		testCase.mutate(&event)
		if got := filter(sdkgo.TriggerEvent[slack.MessageEvent]{ID: testCase.eventID, Payload: event}); got != testCase.want {
			t.Errorf("%s: admitted = %v, want %v", name, got, testCase.want)
		}
	}

	invalid := map[string]slack.ThreadReplyCreatedTriggerConfiguration{
		"no reviewers":       {ChannelID: "C1"},
		"no channel":         {ThreadReplyMatcher: slack.MessageMatcher{PosterUserIDs: []string{"U1"}}},
		"duplicate reviewer": {ChannelID: "C1", ThreadReplyMatcher: slack.MessageMatcher{PosterUserIDs: []string{"U1", "U1"}}},
	}
	for name, configuration := range invalid {
		if filter, err := NewReviewTriggerFilter(configuration); err == nil || filter != nil {
			t.Errorf("%s: configuration was accepted", name)
		}
	}
}

func TestReviewIdentityAndInput(t *testing.T) {
	root := sdkgo.TriggerEvent[slack.MessageEvent]{ID: "Ev1", Payload: slack.MessageEvent{
		TeamID: "T", ChannelID: "C1", Timestamp: "1.0", UserID: "U7", Text: "Write a post about connectors",
	}}
	reply := sdkgo.TriggerEvent[slack.MessageEvent]{ID: "Ev2", Payload: slack.MessageEvent{
		TeamID: "T", ChannelID: "C1", Timestamp: "1.5", ThreadTimestamp: "1.0", UserID: "U1", Text: "approve",
	}}
	if id := ResolveReviewFlowID(reply); id != "blog-post-T-C1-1.0" || id != ResolveRequestFlowID(root) {
		t.Fatalf("reply Flow ID = %q, request Flow ID = %q", id, ResolveRequestFlowID(root))
	}
	want := SlackReviewReply{EventID: "Ev2", UserID: "U1", Timestamp: "1.5", Text: "approve"}
	if input := MapToSlackReviewReply(reply); input != want {
		t.Fatalf("input = %+v, want %+v", input, want)
	}
}

func TestEditorLinks(t *testing.T) {
	const runID = "blog-post-T-C1-1.0"
	key := []byte("editor key of sixteen+ bytes")
	links := NewEditorLinks("https://news.example.com/", key)
	token := links.Token(runID)
	if len(token) != 32 || strings.Trim(token, "0123456789abcdef") != "" {
		t.Fatalf("token = %q, want 32 lowercase hex characters", token)
	}
	if NewEditorLinks("https://other.example.com", []byte("editor key of sixteen+ bytes")).Token(runID) != token {
		t.Fatal("the same key signs the same run differently")
	}
	key[0] = 'X'
	if links.Token(runID) != token {
		t.Fatal("changing the caller's key slice changed the links' key")
	}
	if links.Token("blog-post-T-C1-2.0") == token {
		t.Fatal("two runs share a token")
	}
	other := NewEditorLinks("https://news.example.com", []byte("another editor key, sixteen+"))
	if other.Token(runID) == token || other.Verify(runID, token) {
		t.Fatal("a token verified under another key")
	}

	if !links.Verify(runID, token) || !links.Verify(runID, " "+token+"\n") {
		t.Fatal("the issued token did not verify")
	}
	lastDigit := "0"
	if strings.HasSuffix(token, "0") {
		lastDigit = "1"
	}
	forged := token[:len(token)-1] + lastDigit
	for name, candidate := range map[string]string{
		"other run": links.Token("blog-post-T-C1-2.0"), "forged": forged, "truncated": token[:16], "extended": token + "00",
		"not hex": strings.Repeat("z", 32), "odd length": token[:31], "empty": "",
	} {
		if links.Verify(runID, candidate) {
			t.Errorf("%s token %q verified", name, candidate)
		}
	}
	for name, unsigned := range map[string]EditorLinks{"nil key": NewEditorLinks("https://news.example.com", nil), "empty key": NewEditorLinks("https://news.example.com", []byte{})} {
		if unsigned.Verify(runID, unsigned.Token(runID)) {
			t.Errorf("%s: a token verified without a signing key", name)
		}
	}

	parsed, err := url.Parse(links.URL(runID))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "news.example.com" || parsed.Path != "/edit/"+runID || parsed.Query().Get("token") != token {
		t.Fatalf("URL = %v, %v", parsed, err)
	}
	const oddRunID = "run/with space?and#hash"
	escaped := links.URL(oddRunID)
	if want := "https://news.example.com/edit/run%2Fwith%20space%3Fand%23hash?token=" + links.Token(oddRunID); escaped != want {
		t.Fatalf("URL = %q, want %q", escaped, want)
	}
	if parsed, err := url.Parse(escaped); err != nil || parsed.Query().Get("token") != links.Token(oddRunID) || parsed.Fragment != "" {
		t.Fatalf("escaped URL parsed as %+v, %v", parsed, err)
	}
}
