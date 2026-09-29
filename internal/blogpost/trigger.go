package blogpost

import (
	"strings"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
)

// NewRequestTriggerFilter admits only top-level human messages in the configured channel.
// Replies, including this application's own thread replies, never start a run.
func NewRequestTriggerFilter(configuration slack.ChannelThreadCreatedTriggerConfiguration) (sdkgo.TriggerFilter[slack.MessageEvent], error) {
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, userID := range configuration.ThreadTriggerMatcher.PosterUserIDs {
		allowed[userID] = true
	}
	contains := strings.ToLower(strings.TrimSpace(configuration.ThreadTriggerMatcher.MessageContains))
	return func(event sdkgo.TriggerEvent[slack.MessageEvent]) bool {
		message := event.Payload
		isRoot := message.ThreadTimestamp == "" || message.ThreadTimestamp == message.Timestamp
		switch {
		case event.ID == "" || message.TeamID == "" || message.UserID == "" || message.Timestamp == "":
			return false
		case message.ChannelID != configuration.ChannelID || !isRoot || strings.TrimSpace(message.Text) == "":
			return false
		case len(allowed) > 0 && !allowed[message.UserID]:
			return false
		case contains != "" && !strings.Contains(strings.ToLower(message.Text), contains):
			return false
		}
		return true
	}, nil
}

// ResolveRequestFlowID names one BlogPost run per Slack message.
func ResolveRequestFlowID(event sdkgo.TriggerEvent[slack.MessageEvent]) string {
	message := event.Payload
	return "blog-post-" + message.TeamID + "-" + message.ChannelID + "-" + message.Timestamp
}

// MapToSlackRequest maps the Slack message to the BlogPost start input.
func MapToSlackRequest(event sdkgo.TriggerEvent[slack.MessageEvent]) SlackRequest {
	message := event.Payload
	return SlackRequest{
		EventID: event.ID, TeamID: message.TeamID, ChannelID: message.ChannelID, MessageTimestamp: message.Timestamp,
		UserID: message.UserID, Text: message.Text, ReceivedAt: event.OccurredAt.UTC(),
	}
}
