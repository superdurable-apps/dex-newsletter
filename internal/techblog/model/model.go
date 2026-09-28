// Package model defines the durable, provider-neutral payloads exchanged by the
// tech blog newsletter Flows. Every exported field is part of the open-Flow
// contract: add fields compatibly and never repurpose a persisted field.
package model

import "time"

// NewsletterRequest is the typed start input of TechBlogNewsletterFlow. It is
// mapped from one top-level Slack message in the designated request channel.
type NewsletterRequest struct {
	SlackEventID     string `json:"slackEventId"`
	TeamID           string `json:"teamId"`
	ChannelID        string `json:"channelId"`
	MessageTimestamp string `json:"messageTimestamp"`
	ThreadTimestamp  string `json:"threadTimestamp"`
	RequesterUserID  string `json:"requesterUserId"`
	RequestText      string `json:"requestText"`
}

// SlackThreadReference identifies the Slack thread that receives every status
// reply for one newsletter request.
type SlackThreadReference struct {
	ChannelID       string `json:"channelId"`
	ThreadTimestamp string `json:"threadTimestamp"`
}

// ChangeWindow is the half-open research interval [Since, Until). Until is the
// Slack message time, so retries always research the same interval.
type ChangeWindow struct {
	Since        time.Time `json:"since"`
	Until        time.Time `json:"until"`
	LookbackDays int       `json:"lookbackDays"`
}

// RepositoryReference names one GitHub repository.
type RepositoryReference struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// RepositorySelection is one catalog repository chosen for research, with the
// codebase areas most related to the requested topic.
type RepositorySelection struct {
	Repository RepositoryReference `json:"repository"`
	PathHints  []string            `json:"pathHints"`
	Reason     string              `json:"reason"`
}

// RequestInterpretation is the validated language-model reading of the Slack
// request. LookbackDays is zero when the request does not name a time range.
type RequestInterpretation struct {
	Understood            bool                  `json:"understood"`
	ClarificationQuestion string                `json:"clarificationQuestion"`
	Topic                 string                `json:"topic"`
	Instructions          string                `json:"instructions"`
	Audience              string                `json:"audience"`
	LookbackDays          int                   `json:"lookbackDays"`
	RepositorySelections  []RepositorySelection `json:"repositorySelections"`
}

// RepositoryResearchRequest is the typed start input of
// RepositoryChangeResearchFlow.
type RepositoryResearchRequest struct {
	Selection    RepositorySelection `json:"selection"`
	Topic        string              `json:"topic"`
	Instructions string              `json:"instructions"`
	Window       ChangeWindow        `json:"window"`
}

// FileChangeEvidence is one bounded file change from a merged pull request.
type FileChangeEvidence struct {
	Filename       string `json:"filename"`
	Status         string `json:"status"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Patch          string `json:"patch"`
	PatchTruncated bool   `json:"patchTruncated"`
}

// PullRequestEvidence is one merged pull request inside the change window.
type PullRequestEvidence struct {
	Number        int                  `json:"number"`
	Title         string               `json:"title"`
	Body          string               `json:"body"`
	BodyTruncated bool                 `json:"bodyTruncated"`
	URL           string               `json:"url"`
	AuthorLogin   string               `json:"authorLogin"`
	MergedAt      time.Time            `json:"mergedAt"`
	Labels        []string             `json:"labels"`
	Files         []FileChangeEvidence `json:"files"`
}

// CommitEvidence is one commit inside the change window.
type CommitEvidence struct {
	SHA         string    `json:"sha"`
	Message     string    `json:"message"`
	AuthorLogin string    `json:"authorLogin"`
	URL         string    `json:"url"`
	CommittedAt time.Time `json:"committedAt"`
}

// RepositoryChangeEvidence is the bounded raw research collected for one
// repository before it is summarized.
type RepositoryChangeEvidence struct {
	Repository   RepositoryReference   `json:"repository"`
	PullRequests []PullRequestEvidence `json:"pullRequests"`
	Commits      []CommitEvidence      `json:"commits"`
	Notes        []string              `json:"notes"`
	// PullRequestsListed and CommitsListed record whether each GitHub listing
	// succeeded at least once, so "no changes" is distinguishable from
	// "research could not run".
	PullRequestsListed bool `json:"pullRequestsListed"`
	CommitsListed      bool `json:"commitsListed"`
}

// SourceReference cites a pull request, commit, or document.
type SourceReference struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// ChangeHighlight is one meaningful capability, feature, or technical
// improvement found in the research.
type ChangeHighlight struct {
	Title        string            `json:"title"`
	WhatChanged  string            `json:"whatChanged"`
	HowItWorks   string            `json:"howItWorks"`
	WhyItMatters string            `json:"whyItMatters"`
	References   []SourceReference `json:"references"`
}

// RepositoryResearchStatus is the terminal research outcome for one repository.
type RepositoryResearchStatus string

const (
	RepositoryResearched         RepositoryResearchStatus = "researched"
	RepositoryNotFound           RepositoryResearchStatus = "repository-not-found"
	RepositoryResearchIncomplete RepositoryResearchStatus = "research-incomplete"
	RepositoryResearchFailed     RepositoryResearchStatus = "research-failed"
)

// RepositoryChangeDigest is the typed completion output of
// RepositoryChangeResearchFlow.
type RepositoryChangeDigest struct {
	Repository       RepositoryReference      `json:"repository"`
	Status           RepositoryResearchStatus `json:"status"`
	Relevant         bool                     `json:"relevant"`
	Summary          string                   `json:"summary"`
	Highlights       []ChangeHighlight        `json:"highlights"`
	PullRequestCount int                      `json:"pullRequestCount"`
	CommitCount      int                      `json:"commitCount"`
	Notes            []string                 `json:"notes"`
}

// ResearchBrief is the cross-repository synthesis that the blog post is
// written from.
type ResearchBrief struct {
	HasNotableChanges bool              `json:"hasNotableChanges"`
	Headline          string            `json:"headline"`
	Overview          string            `json:"overview"`
	Highlights        []ChangeHighlight `json:"highlights"`
	OpenQuestions     []string          `json:"openQuestions"`
}

// BlogBlockType is the closed set of blog body block kinds the renderer
// supports.
type BlogBlockType string

const (
	BlogBlockParagraph BlogBlockType = "paragraph"
	BlogBlockBullets   BlogBlockType = "bullets"
	BlogBlockCode      BlogBlockType = "code"
	BlogBlockQuote     BlogBlockType = "quote"
	BlogBlockCallout   BlogBlockType = "callout"
)

// BlogBlock is one block of blog body content. Text and Items support only
// inline `code` spans and [label](https://url) links; everything else is
// escaped by the renderer.
type BlogBlock struct {
	Type     BlogBlockType `json:"type"`
	Text     string        `json:"text"`
	Items    []string      `json:"items"`
	Language string        `json:"language"`
	Code     string        `json:"code"`
}

// BlogSection is one headed section of the blog post.
type BlogSection struct {
	Heading string      `json:"heading"`
	Blocks  []BlogBlock `json:"blocks"`
}

// BlogPost is the structured blog draft produced by the language model.
type BlogPost struct {
	Title      string            `json:"title"`
	Subtitle   string            `json:"subtitle"`
	Slug       string            `json:"slug"`
	Summary    string            `json:"summary"`
	Tags       []string          `json:"tags"`
	Sections   []BlogSection     `json:"sections"`
	References []SourceReference `json:"references"`
}

// NewsletterHighlight is one short highlight in the newsletter email.
type NewsletterHighlight struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// NewsletterDraft is the structured newsletter email copy produced by the
// language model from the approved-for-review blog post.
type NewsletterDraft struct {
	Subject    string                `json:"subject"`
	Preheader  string                `json:"preheader"`
	Intro      string                `json:"intro"`
	Highlights []NewsletterHighlight `json:"highlights"`
	Closing    string                `json:"closing"`
}

// RenderedNewsletter is the deliverable email: the draft copy, the full blog
// post inline, and an optional link to the published post.
type RenderedNewsletter struct {
	Subject  string `json:"subject"`
	HTMLBody string `json:"htmlBody"`
	TextBody string `json:"textBody"`
}

// GenerationStatus is the provider-neutral outcome of one language-model call.
type GenerationStatus string

const (
	GenerationSucceeded           GenerationStatus = "generated"
	GenerationTruncated           GenerationStatus = "truncated"
	GenerationBlocked             GenerationStatus = "blocked"
	GenerationRejected            GenerationStatus = "rejected"
	GenerationInvalidResponse     GenerationStatus = "invalid-response"
	GenerationDefect              GenerationStatus = "defect"
	GenerationUnsupportedProvider GenerationStatus = "unsupported-provider"
)

// GenerationRequest is the typed start input of LanguageModelGenerationFlow.
// It names a configured provider but carries no provider-specific fields, so a
// new provider can be added without changing the calling Flows.
type GenerationRequest struct {
	Purpose           string `json:"purpose"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	SystemInstruction string `json:"systemInstruction"`
	Prompt            string `json:"prompt"`
	// ResponseJSONSchema is canonical JSON Schema text (empty for free text).
	// It is text rather than a map so the SubFlow start input stays a typed,
	// form-drivable Dex Web payload.
	ResponseJSONSchema string   `json:"responseJsonSchema,omitempty"`
	Temperature        *float64 `json:"temperature,omitempty"`
	MaxOutputTokens    int      `json:"maxOutputTokens"`
	ThinkingBudget     *int     `json:"thinkingBudget,omitempty"`
}

// GenerationUsage is provider-neutral token accounting.
type GenerationUsage struct {
	PromptTokens   int `json:"promptTokens"`
	OutputTokens   int `json:"outputTokens"`
	ThoughtsTokens int `json:"thoughtsTokens"`
	TotalTokens    int `json:"totalTokens"`
}

// GenerationResult is the typed completion output of
// LanguageModelGenerationFlow. Text is set only when Status is
// GenerationSucceeded or GenerationTruncated.
type GenerationResult struct {
	Purpose        string           `json:"purpose"`
	Status         GenerationStatus `json:"status"`
	Text           string           `json:"text"`
	Provider       string           `json:"provider"`
	Model          string           `json:"model"`
	FinishReason   string           `json:"finishReason"`
	FailureMessage string           `json:"failureMessage"`
	Usage          GenerationUsage  `json:"usage"`
}

// SubscriberList is the validated newsletter audience snapshot taken when the
// editor approves sending.
type SubscriberList struct {
	Recipients       []string `json:"recipients"`
	InvalidAddresses int      `json:"invalidAddresses"`
	DuplicateCount   int      `json:"duplicateCount"`
	TruncatedCount   int      `json:"truncatedCount"`
}

// DeliveryStatus is the recorded outcome of sending the newsletter to one
// subscriber.
type DeliveryStatus string

const (
	DeliverySent      DeliveryStatus = "sent"
	DeliveryRejected  DeliveryStatus = "rejected"
	DeliveryUncertain DeliveryStatus = "uncertain"
	DeliveryDefect    DeliveryStatus = "defect"
)

// DeliveryException records one subscriber whose send was not confirmed, so
// an operator can check it in Dex Web before sending again.
type DeliveryException struct {
	Recipient   string         `json:"recipient"`
	Status      DeliveryStatus `json:"status"`
	FailureKind string         `json:"failureKind,omitempty"`
}

// DeliverySummary counts per-subscriber delivery outcomes.
type DeliverySummary struct {
	Recipients int `json:"recipients"`
	Sent       int `json:"sent"`
	Rejected   int `json:"rejected"`
	Uncertain  int `json:"uncertain"`
	Defect     int `json:"defect"`
	// SkippedOverLimit counts valid subscribed addresses dropped by
	// newsletter.maxRecipients; InvalidAddresses counts unusable stored
	// addresses.
	SkippedOverLimit int `json:"skippedOverLimit"`
	InvalidAddresses int `json:"invalidAddresses"`
}
