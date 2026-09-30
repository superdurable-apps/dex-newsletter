package blogpost

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
)

// ReviewCommand is what a Slack thread reply asks for.
type ReviewCommand string

const (
	ReviewApprove  ReviewCommand = "approve"
	ReviewReject   ReviewCommand = "reject"
	ReviewFeedback ReviewCommand = "feedback"
)

var (
	slackMention  = regexp.MustCompile(`<[@#!][^>]*>`)
	commandTrim   = regexp.MustCompile(`^[\s*_~` + "`" + `]+|[\s*_~.!` + "`" + `]+$`)
	approveWords  = map[string]bool{"approve": true, "approved": true}
	rejectWords   = map[string]bool{"reject": true, "rejected": true}
	feedbackLimit = 4000
)

// ParseReviewReply reads a whole reply as a command only when it is exactly approve or reject;
// anything longer, such as "approve but fix the title", is feedback.
func ParseReviewReply(text string) (ReviewCommand, string) {
	feedback := strings.TrimSpace(slackMention.ReplaceAllString(text, ""))
	word := strings.ToLower(commandTrim.ReplaceAllString(feedback, ""))
	switch {
	case approveWords[word]:
		return ReviewApprove, ""
	case rejectWords[word]:
		return ReviewReject, ""
	}
	if runes := []rune(feedback); len(runes) > feedbackLimit {
		feedback = string(runes[:feedbackLimit])
	}
	return ReviewFeedback, feedback
}

// SlackReviewReply is the ReceiveSlackReview input mapped from one thread reply.
type SlackReviewReply struct {
	EventID   string `json:"eventId"`
	UserID    string `json:"userId"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

// NewReviewTriggerFilter admits human replies from the configured reviewers in the review channel.
func NewReviewTriggerFilter(configuration slack.ThreadReplyCreatedTriggerConfiguration) (sdkgo.TriggerFilter[slack.MessageEvent], error) {
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	reviewers := map[string]bool{}
	for _, userID := range configuration.ThreadReplyMatcher.PosterUserIDs {
		reviewers[userID] = true
	}
	return func(event sdkgo.TriggerEvent[slack.MessageEvent]) bool {
		message := event.Payload
		isReply := message.ThreadTimestamp != "" && message.ThreadTimestamp != message.Timestamp
		return event.ID != "" && message.TeamID != "" && message.ChannelID == configuration.ChannelID && isReply &&
			reviewers[message.UserID] && strings.TrimSpace(message.Text) != ""
	}, nil
}

// ResolveReviewFlowID maps a thread reply to the BlogPost run its thread belongs to.
func ResolveReviewFlowID(event sdkgo.TriggerEvent[slack.MessageEvent]) string {
	message := event.Payload
	return "blog-post-" + message.TeamID + "-" + message.ChannelID + "-" + message.ThreadTimestamp
}

// MapToSlackReviewReply maps a thread reply to the ReceiveSlackReview input.
func MapToSlackReviewReply(event sdkgo.TriggerEvent[slack.MessageEvent]) SlackReviewReply {
	message := event.Payload
	return SlackReviewReply{EventID: event.ID, UserID: message.UserID, Timestamp: message.Timestamp, Text: message.Text}
}

// EditorLinks signs per-run editor links. Whoever holds a link can edit and approve that run's draft.
type EditorLinks struct {
	baseURL string
	key     []byte
}

func NewEditorLinks(baseURL string, key []byte) EditorLinks {
	return EditorLinks{baseURL: strings.TrimRight(baseURL, "/"), key: append([]byte(nil), key...)}
}

// Token is the hex HMAC-SHA256 of the run ID, truncated to 128 bits.
func (links EditorLinks) Token(flowID string) string {
	mac := hmac.New(sha256.New, links.key)
	mac.Write([]byte("editor\x00" + flowID))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// Verify reports whether token was issued for flowID.
func (links EditorLinks) Verify(flowID, token string) bool {
	expected, _ := hex.DecodeString(links.Token(flowID))
	actual, err := hex.DecodeString(strings.TrimSpace(token))
	return err == nil && len(links.key) > 0 && hmac.Equal(expected, actual)
}

// URL is the editor page for one run.
func (links EditorLinks) URL(flowID string) string {
	return links.baseURL + "/edit/" + url.PathEscape(flowID) + "?" + url.Values{"token": {links.Token(flowID)}}.Encode()
}
