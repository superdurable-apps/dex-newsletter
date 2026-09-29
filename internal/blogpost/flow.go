// Package blogpost turns one Slack request into a researched tech blog post and
// newsletter: one top-level BlogPost Flow per Slack message, reviewed in Dex Web.
package blogpost

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/config"
	"github.com/superdurable-apps/dex-newsletter/internal/content"
	"github.com/superdurable-apps/dex-newsletter/internal/subscribers"

	github "github.com/superdurable/dex-connectors-library/connectors/github"
	"github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	"github.com/superdurable/dex-connectors-library/connectors/slack"
	llmrouter "github.com/superdurable/dex-connectors-library/connectors/superdurable/llm"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	FlowType = "BlogPost"

	SlackConnectionName  = "slack-workspace"
	GitHubConnectionName = "github"
	LLMConnectionName    = "llm"
	GmailConnectionName  = "newsletter-sender"
	// RequestTriggerBinding is the Slack channel binding that starts a BlogPost.
	RequestTriggerBinding = "blog-post-request"

	stepInterpretBlogRequest  = "InterpretBlogRequest"
	stepListOwnerRepositories = "ListOwnerRepositories"
	stepChooseRepositories    = "ChooseRepositories"
	stepListMergedPRs         = "ListMergedPullRequests"
	stepListPullRequestFiles  = "ListPullRequestFiles"
	stepListRepositoryCommits = "ListRepositoryCommits"
	stepWriteBlogPost         = "WriteBlogPost"
	stepWriteNewsletter       = "WriteNewsletter"
	stepSendNewsletterEmail   = "SendNewsletterEmail"
	stepPostSlackNotice       = "PostSlackNotice"

	maxDeliveryExceptions = 50
)

// Statuses drive Dex Web search, the status slot, and every Action condition.
const (
	StatusInterpreting   = "interpreting"
	StatusResearching    = "researching"
	StatusWriting        = "writing"
	StatusAwaitingReview = "awaiting-review"
	StatusNeedsAttention = "needs-attention"
	StatusRetrying       = "retrying"
	StatusDelivering     = "delivering"
	StatusSent           = "sent"
	StatusRejected       = "rejected"
	StatusNotARequest    = "not-a-blog-request"
	StatusNoChanges      = "no-changes-found"
	StatusStopped        = "delivery-stopped"
)

// Stages name where a run stopped, so Retry resumes the right Step.
const (
	StageInterpret       = "interpret"
	StageListOwners      = "list-repositories"
	StageChoose          = "choose-repositories"
	StageWriteBlog       = "write-blog"
	StageWriteNewsletter = "write-newsletter"
	StageDeliver         = "deliver"
)

const (
	decisionApprove = "approve"
	decisionRevise  = "revise"
	decisionRetry   = "retry"
	decisionReject  = "reject"
)

// SlackRequest is the typed start input mapped from one Slack root message.
type SlackRequest struct {
	EventID          string    `json:"eventId"`
	TeamID           string    `json:"teamId"`
	ChannelID        string    `json:"channelId"`
	MessageTimestamp string    `json:"messageTimestamp"`
	UserID           string    `json:"userId"`
	Text             string    `json:"text"`
	ReceivedAt       time.Time `json:"receivedAt"`
}

type SlackNotice struct {
	ChannelID       string `json:"channelId"`
	ThreadTimestamp string `json:"threadTimestamp"`
	Text            string `json:"text"`
}

type OwnerCursor struct {
	Owners     []string                      `json:"owners"`
	Index      int                           `json:"index"`
	Window     content.Window                `json:"window"`
	Candidates []content.RepositoryCandidate `json:"candidates"`
	Failures   []string                      `json:"failures,omitempty"`
}

type ResearchCursor struct {
	Repositories        []content.SelectedRepository `json:"repositories"`
	Index               int                          `json:"index"`
	Window              content.Window               `json:"window"`
	Current             content.RepositoryResearch   `json:"current"`
	PendingPullRequests []int                        `json:"pendingPullRequests,omitempty"`
}

type RevisionRequest struct {
	Notes string `json:"notes,omitempty"`
}

type NewsletterWritingInput struct {
	Draft           content.BlogDraft `json:"draft"`
	PublicationName string            `json:"publicationName"`
}

type OutgoingEmail struct {
	Address string `json:"address"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

type DeliveryProgress struct {
	Total     int `json:"total"`
	Next      int `json:"next"`
	Sent      int `json:"sent"`
	Failed    int `json:"failed"`
	Uncertain int `json:"uncertain"`
}

type DeliveryException struct {
	Address string `json:"address"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

type Attention struct {
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

type ReviewDecision struct {
	Kind  string `json:"kind"`
	Round int64  `json:"round"`
	Notes string `json:"notes,omitempty"`
}

type Outcome struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Result is the Flow's completion output.
type Result struct {
	Status   string           `json:"status"`
	Title    string           `json:"title,omitempty"`
	Message  string           `json:"message"`
	Delivery DeliveryProgress `json:"delivery"`
}

var (
	// dex:indexed-attribute attribute-key:blog-status index-key:blog-status index-type:keyword value-type:string description:"Blog post status"
	blogStatus = dex.DefineAttribute[string]("blog-status", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}))
	// dex:indexed-attribute attribute-key:blog-topic index-key:blog-topic index-type:fulltext value-type:string description:"Requested topic"
	blogTopic = dex.DefineAttribute[string]("blog-topic", dex.Indexed(dex.AttributeIndex{Type: dex.IndexFullText}))
	// dex:indexed-attribute attribute-key:blog-requester index-key:blog-requester index-type:keyword value-type:string description:"Slack user who asked"
	blogRequester = dex.DefineAttribute[string]("blog-requester", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}))

	blogRequest          = dex.DefineAttribute[SlackRequest]("blog-request")
	changeWindow         = dex.DefineAttribute[content.Window]("change-window")
	windowLabel          = dex.DefineAttribute[string]("window-label")
	blogTitle            = dex.DefineAttribute[string]("blog-title")
	attentionStage       = dex.DefineAttribute[string]("attention-stage")
	attentionReason      = dex.DefineAttribute[string]("attention-reason")
	reviewRound          = dex.DefineAttribute[int64]("review-round")
	revisionCount        = dex.DefineAttribute[int64]("revision-count")
	editorNotes          = dex.DefineAttribute[string]("editor-notes")
	ownerCursor          = dex.DefineAttribute[OwnerCursor]("owner-cursor")
	candidateRepos       = dex.DefineAttribute[[]content.RepositoryCandidate]("candidate-repositories")
	selectedRepos        = dex.DefineAttribute[[]content.SelectedRepository]("selected-repositories")
	researchCursor       = dex.DefineAttribute[ResearchCursor]("research-cursor")
	repositoryResearch   = dex.DefineAttribute[[]content.RepositoryResearch]("repository-research")
	researchSummary      = dex.DefineAttribute[string]("research-summary")
	blogDraft            = dex.DefineAttribute[content.BlogDraft]("blog-draft")
	blogPreview          = dex.DefineAttribute[string]("blog-preview")
	blogHTML             = dex.DefineAttribute[string]("blog-html")
	blogArtifactPath     = dex.DefineAttribute[string]("blog-artifact-path")
	newsletterDraft      = dex.DefineAttribute[content.NewsletterDraft]("newsletter-draft")
	newsletterSubject    = dex.DefineAttribute[string]("newsletter-subject")
	newsletterPreview    = dex.DefineAttribute[string]("newsletter-preview")
	deliveryRecipients   = dex.DefineAttribute[[]string]("delivery-recipients")
	deliveryProgress     = dex.DefineAttribute[DeliveryProgress]("delivery-progress")
	deliverySummary      = dex.DefineAttribute[string]("delivery-summary")
	deliveryExceptions   = dex.DefineAttribute[[]DeliveryException]("delivery-exceptions")
	slackNoticeStatus    = dex.DefineAttribute[string]("slack-notice-status")
	reviewDecisions      = dex.DefineChannel[ReviewDecision]("review-decisions")
	allBlogPostAttribute = []dex.AttributeDef{
		blogStatus, blogTopic, blogRequester, blogRequest, changeWindow, windowLabel, blogTitle, attentionStage,
		attentionReason, reviewRound, revisionCount, editorNotes, ownerCursor, candidateRepos, selectedRepos,
		researchCursor, repositoryResearch, researchSummary, blogDraft, blogPreview, blogHTML, blogArtifactPath,
		newsletterDraft, newsletterSubject, newsletterPreview, deliveryRecipients, deliveryProgress, deliverySummary,
		deliveryExceptions, slackNoticeStatus,
	}
)

// SubscriberDirectory reads the current subscriber list from its owning Flow.
type SubscriberDirectory interface {
	ListSubscribers(ctx context.Context) ([]string, error)
}

// Models are the Dex Web model picks per generation Step; empty uses the llm connection's default.
type Models struct {
	Interpret       string
	Choose          string
	WriteBlog       string
	WriteNewsletter string
}

type Dependencies struct {
	Slack       slack.Connection
	GitHub      github.Connection
	LLM         llmrouter.Connection
	Gmail       gmail.Connection
	Models      Models
	Config      config.Config
	Subscribers SubscriberDirectory
	Unsubscribe subscribers.UnsubscribeLinks
}

// ModelConfiguration is one generation Step's model pick from Dex Web.
type ModelConfiguration struct {
	Model string `json:"model"`
}

// ModelConfigurationRef identifies one generation Step's Dex Web model pick.
func ModelConfigurationRef(stepType string) sdkgo.ConnectorConfigurationRef {
	return sdkgo.ConnectorConfigurationRef{
		ConnectorID: llmrouter.ConnectorID, ConnectionName: LLMConnectionName, OperationID: "generateText",
		FlowType: FlowType, StepType: stepType,
	}
}

// ModelStepTypes lists the generation Steps whose model Dex Web can pick.
var ModelStepTypes = []string{stepInterpretBlogRequest, stepChooseRepositories, stepWriteBlogPost, stepWriteNewsletter}

type Flow struct {
	dex.FlowDefaults
	deps Dependencies
}

func NewFlow(deps Dependencies) *Flow { return &Flow{deps: deps} }

func (*Flow) GetFlowType() string { return FlowType }

func (flow *Flow) GetSteps() []dex.StepDef {
	research := flow.deps.Config.Research
	return []dex.StepDef{
		dex.DefineStartStep(RecordBlogRequest{flow: flow}),
		dex.DefineStep(llmrouter.NewGenerateTextStep(llmrouter.GenerateTextStepConfig[SlackRequest]{
			StepType: stepInterpretBlogRequest, ConnectionName: LLMConnectionName, Connection: flow.deps.LLM,
			Annotations: sdkgo.StepAnnotations{GroupID: "request", GroupLabel: "Request", Explanation: "Ask the model for the topic and time range in the Slack message."},
			ConfigurationUI: sdkgo.ConnectorConfigurationUI{Units: []sdkgo.ConnectorUIUnit{{
				ID: "model", UnitID: llmrouter.UIUnitModelPicker, Label: "Request reading model", Description: "Choose the provider/model that reads the Slack request; blank keeps the connection's default model.",
				Bindings: []sdkgo.ConnectorUIBinding{{Port: llmrouter.UIModelPickerPortModel, JSONPointer: "/model"}},
			}}},
			MapToOperationInput: func(request SlackRequest) llmrouter.GenerateTextRequest {
				return content.InterpretationRequest(flow.deps.Models.Interpret, request.Text, request.ReceivedAt)
			},
			Generated: sdkgo.GoTo(ApplyRequestInterpretation{}), Truncated: sdkgo.GoTo(ApplyRequestInterpretation{}),
			Blocked: sdkgo.GoTo(ApplyRequestInterpretation{}), ProviderRejected: sdkgo.GoTo(ApplyRequestInterpretation{}),
			InvalidResponse: sdkgo.GoTo(ApplyRequestInterpretation{}), Defect: sdkgo.GoTo(ApplyRequestInterpretation{}),
		})),
		dex.DefineStep(ApplyRequestInterpretation{flow: flow}),
		dex.DefineStep(github.NewListPublicRepositoriesStep(github.ListPublicRepositoriesStepConfig[OwnerCursor]{
			StepType: stepListOwnerRepositories, ConnectionName: GitHubConnectionName, Connection: flow.deps.GitHub,
			Annotations: sdkgo.StepAnnotations{GroupID: "research", GroupLabel: "Research", Explanation: "List one configured owner's public repositories."},
			MapToOperationInput: func(cursor OwnerCursor) github.ListPublicRepositoriesInput {
				return github.ListPublicRepositoriesInput{Login: cursor.Owners[cursor.Index], Limit: research.RepositoriesPerOwner}
			},
			RepositoriesLoaded: sdkgo.GoTo(RecordOwnerRepositories{}), InsufficientScope: sdkgo.GoTo(RecordOwnerRepositories{}),
			AuthorizationRevoked: sdkgo.GoTo(RecordOwnerRepositories{}), NotFound: sdkgo.GoTo(RecordOwnerRepositories{}),
			ProviderRejected: sdkgo.GoTo(RecordOwnerRepositories{}), InvalidResponse: sdkgo.GoTo(RecordOwnerRepositories{}),
			Defect: sdkgo.GoTo(RecordOwnerRepositories{}),
		})),
		dex.DefineStep(RecordOwnerRepositories{flow: flow}),
		dex.DefineStep(llmrouter.NewGenerateTextStep(llmrouter.GenerateTextStepConfig[content.RepositoryChoiceInput]{
			StepType: stepChooseRepositories, ConnectionName: LLMConnectionName, Connection: flow.deps.LLM,
			Annotations: sdkgo.StepAnnotations{GroupID: "research", GroupLabel: "Research", Explanation: "Ask the model which repositories and code areas relate to the topic."},
			ConfigurationUI: sdkgo.ConnectorConfigurationUI{Units: []sdkgo.ConnectorUIUnit{{
				ID: "model", UnitID: llmrouter.UIUnitModelPicker, Label: "Repository choice model", Description: "Choose the provider/model that picks repositories for the topic; blank keeps the connection's default model.",
				Bindings: []sdkgo.ConnectorUIBinding{{Port: llmrouter.UIModelPickerPortModel, JSONPointer: "/model"}},
			}}},
			MapToOperationInput: func(input content.RepositoryChoiceInput) llmrouter.GenerateTextRequest {
				return content.RepositoryChoiceRequest(flow.deps.Models.Choose, input)
			},
			Generated: sdkgo.GoTo(ApplyRepositoryChoice{}), Truncated: sdkgo.GoTo(ApplyRepositoryChoice{}),
			Blocked: sdkgo.GoTo(ApplyRepositoryChoice{}), ProviderRejected: sdkgo.GoTo(ApplyRepositoryChoice{}),
			InvalidResponse: sdkgo.GoTo(ApplyRepositoryChoice{}), Defect: sdkgo.GoTo(ApplyRepositoryChoice{}),
		})),
		dex.DefineStep(ApplyRepositoryChoice{flow: flow}),
		dex.DefineStep(github.NewListMergedPullRequestsStep(github.ListMergedPullRequestsStepConfig[ResearchCursor]{
			StepType: stepListMergedPRs, ConnectionName: GitHubConnectionName, Connection: flow.deps.GitHub,
			Annotations: sdkgo.StepAnnotations{GroupID: "research", GroupLabel: "Research", Explanation: "List pull requests merged into the current repository during the window."},
			MapToOperationInput: func(cursor ResearchCursor) github.ListMergedPullRequestsInput {
				repository := cursor.Repositories[cursor.Index]
				return github.ListMergedPullRequestsInput{
					Owner: repository.Owner, Repository: repository.Name, MergedAfter: cursor.Window.Since,
					MergedBefore: cursor.Window.Until, PageSize: research.PullRequestsPerRepository,
				}
			},
			Listed: sdkgo.GoTo(RecordMergedPullRequests{}), InsufficientScope: sdkgo.GoTo(RecordMergedPullRequests{}),
			AuthorizationRevoked: sdkgo.GoTo(RecordMergedPullRequests{}), NotFound: sdkgo.GoTo(RecordMergedPullRequests{}),
			ProviderRejected: sdkgo.GoTo(RecordMergedPullRequests{}), InvalidResponse: sdkgo.GoTo(RecordMergedPullRequests{}),
			Defect: sdkgo.GoTo(RecordMergedPullRequests{}),
		})),
		dex.DefineStep(RecordMergedPullRequests{flow: flow}),
		dex.DefineStep(github.NewListPullRequestFilesStep(github.ListPullRequestFilesStepConfig[ResearchCursor]{
			StepType: stepListPullRequestFiles, ConnectionName: GitHubConnectionName, Connection: flow.deps.GitHub,
			Annotations: sdkgo.StepAnnotations{GroupID: "research", GroupLabel: "Research", Explanation: "List the files one merged pull request changed."},
			MapToOperationInput: func(cursor ResearchCursor) github.ListPullRequestFilesInput {
				repository := cursor.Repositories[cursor.Index]
				return github.ListPullRequestFilesInput{Owner: repository.Owner, Repository: repository.Name, Number: cursor.PendingPullRequests[0], PageSize: 100}
			},
			Listed: sdkgo.GoTo(RecordPullRequestFiles{}), InsufficientScope: sdkgo.GoTo(RecordPullRequestFiles{}),
			AuthorizationRevoked: sdkgo.GoTo(RecordPullRequestFiles{}), NotFound: sdkgo.GoTo(RecordPullRequestFiles{}),
			ProviderRejected: sdkgo.GoTo(RecordPullRequestFiles{}), InvalidResponse: sdkgo.GoTo(RecordPullRequestFiles{}),
			Defect: sdkgo.GoTo(RecordPullRequestFiles{}),
		})),
		dex.DefineStep(RecordPullRequestFiles{}),
		dex.DefineStep(github.NewListCommitsStep(github.ListCommitsStepConfig[ResearchCursor]{
			StepType: stepListRepositoryCommits, ConnectionName: GitHubConnectionName, Connection: flow.deps.GitHub,
			Annotations: sdkgo.StepAnnotations{GroupID: "research", GroupLabel: "Research", Explanation: "List default-branch commits in the window, including work merged without a pull request."},
			MapToOperationInput: func(cursor ResearchCursor) github.ListCommitsInput {
				repository := cursor.Repositories[cursor.Index]
				return github.ListCommitsInput{
					Owner: repository.Owner, Repository: repository.Name, Since: cursor.Window.Since,
					Until: cursor.Window.Until, PageSize: research.CommitsPerRepository,
				}
			},
			Listed: sdkgo.GoTo(RecordRepositoryCommits{}), InsufficientScope: sdkgo.GoTo(RecordRepositoryCommits{}),
			AuthorizationRevoked: sdkgo.GoTo(RecordRepositoryCommits{}), NotFound: sdkgo.GoTo(RecordRepositoryCommits{}),
			ProviderRejected: sdkgo.GoTo(RecordRepositoryCommits{}), InvalidResponse: sdkgo.GoTo(RecordRepositoryCommits{}),
			Defect: sdkgo.GoTo(RecordRepositoryCommits{}),
		})),
		dex.DefineStep(RecordRepositoryCommits{flow: flow}),
		dex.DefineStep(PrepareBlogWriting{flow: flow}),
		dex.DefineStep(llmrouter.NewGenerateTextStep(llmrouter.GenerateTextStepConfig[content.BlogWritingInput]{
			StepType: stepWriteBlogPost, ConnectionName: LLMConnectionName, Connection: flow.deps.LLM,
			Annotations: sdkgo.StepAnnotations{GroupID: "writing", GroupLabel: "Writing", Explanation: "Ask the model for a structured blog post grounded in the research."},
			ConfigurationUI: sdkgo.ConnectorConfigurationUI{Units: []sdkgo.ConnectorUIUnit{{
				ID: "model", UnitID: llmrouter.UIUnitModelPicker, Label: "Blog writing model", Description: "Choose the provider/model that writes the blog post; blank keeps the connection's default model.",
				Bindings: []sdkgo.ConnectorUIBinding{{Port: llmrouter.UIModelPickerPortModel, JSONPointer: "/model"}},
			}}},
			MapToOperationInput: func(input content.BlogWritingInput) llmrouter.GenerateTextRequest {
				return content.BlogWritingRequest(flow.deps.Models.WriteBlog, input)
			},
			Generated: sdkgo.GoTo(RecordBlogDraft{}), Truncated: sdkgo.GoTo(RecordBlogDraft{}),
			Blocked: sdkgo.GoTo(RecordBlogDraft{}), ProviderRejected: sdkgo.GoTo(RecordBlogDraft{}),
			InvalidResponse: sdkgo.GoTo(RecordBlogDraft{}), Defect: sdkgo.GoTo(RecordBlogDraft{}),
		})),
		dex.DefineStep(RecordBlogDraft{flow: flow}),
		dex.DefineStep(llmrouter.NewGenerateTextStep(llmrouter.GenerateTextStepConfig[NewsletterWritingInput]{
			StepType: stepWriteNewsletter, ConnectionName: LLMConnectionName, Connection: flow.deps.LLM,
			Annotations: sdkgo.StepAnnotations{GroupID: "writing", GroupLabel: "Writing", Explanation: "Ask the model for the newsletter email based on the blog draft."},
			ConfigurationUI: sdkgo.ConnectorConfigurationUI{Units: []sdkgo.ConnectorUIUnit{{
				ID: "model", UnitID: llmrouter.UIUnitModelPicker, Label: "Newsletter writing model", Description: "Choose the provider/model that writes the newsletter email; blank keeps the connection's default model.",
				Bindings: []sdkgo.ConnectorUIBinding{{Port: llmrouter.UIModelPickerPortModel, JSONPointer: "/model"}},
			}}},
			MapToOperationInput: func(input NewsletterWritingInput) llmrouter.GenerateTextRequest {
				return content.NewsletterWritingRequest(flow.deps.Models.WriteNewsletter, input.Draft, input.PublicationName)
			},
			Generated: sdkgo.GoTo(RecordNewsletterDraft{}), Truncated: sdkgo.GoTo(RecordNewsletterDraft{}),
			Blocked: sdkgo.GoTo(RecordNewsletterDraft{}), ProviderRejected: sdkgo.GoTo(RecordNewsletterDraft{}),
			InvalidResponse: sdkgo.GoTo(RecordNewsletterDraft{}), Defect: sdkgo.GoTo(RecordNewsletterDraft{}),
		})),
		dex.DefineStep(RecordNewsletterDraft{flow: flow}),
		dex.DefineStep(EnterReview{flow: flow}),
		dex.DefineStep(EnterNeedsAttention{flow: flow}),
		dex.DefineStep(AwaitEditorDecision{flow: flow}),
		dex.DefineStep(StartNewsletterDelivery{flow: flow}),
		dex.DefineStep(gmail.NewSendMessageStep(gmail.SendMessageStepConfig[OutgoingEmail]{
			StepType: stepSendNewsletterEmail, ConnectionName: GmailConnectionName, Connection: flow.deps.Gmail,
			Annotations: sdkgo.StepAnnotations{GroupID: "delivery", GroupLabel: "Delivery", Explanation: "Send the newsletter to the next subscriber through Gmail."},
			MapToOperationInput: func(email OutgoingEmail) gmail.SendMessageInput {
				return gmail.SendMessageInput{To: []string{email.Address}, Subject: email.Subject, TextBody: email.Text, HTMLBody: email.HTML}
			},
			Sent: sdkgo.GoTo(RecordNewsletterEmailResult{}), ProviderRejected: sdkgo.GoTo(RecordNewsletterEmailResult{}),
			Uncertain: sdkgo.GoTo(RecordNewsletterEmailResult{}), Defect: sdkgo.GoTo(RecordNewsletterEmailResult{}),
			// An ASYNC send can replay after a lost acknowledgement and email the reader twice.
			StepOptionsOverride: &dex.StepOptions{ExecuteDurability: dex.StepDurabilitySync},
		})),
		dex.DefineStep(RecordNewsletterEmailResult{flow: flow}),
		dex.DefineStep(slack.NewPostThreadReplyStep(slack.PostThreadReplyStepConfig[SlackNotice]{
			StepType: stepPostSlackNotice, ConnectionName: SlackConnectionName, Connection: flow.deps.Slack,
			Annotations: sdkgo.StepAnnotations{GroupID: "slack", GroupLabel: "Slack", Explanation: "Reply in the requester's Slack thread."},
			MapToOperationInput: func(notice SlackNotice) slack.PostThreadReplyInput {
				return slack.PostThreadReplyInput{ChannelID: notice.ChannelID, ThreadTimestamp: notice.ThreadTimestamp, Text: notice.Text}
			},
			Sent: sdkgo.GoTo(RecordSlackNotice{}), ProviderRejected: sdkgo.GoTo(RecordSlackNotice{}),
			Uncertain: sdkgo.GoTo(RecordSlackNotice{}), Defect: sdkgo.GoTo(RecordSlackNotice{}),
		})),
		dex.DefineStep(RecordSlackNotice{}),
		dex.DefineStep(FinishBlogPost{flow: flow}),
	}
}

func (*Flow) GetConnectorTriggerBindings() []sdkgo.TriggerBindingDefinition {
	return []sdkgo.TriggerBindingDefinition{
		slack.DefineChannelThreadCreatedTriggerBinding(slack.ChannelThreadCreatedTriggerBindingConfig{
			ConnectionName: SlackConnectionName, BindingName: RequestTriggerBinding,
			ConfigurationUI: sdkgo.ConnectorConfigurationUI{Units: []sdkgo.ConnectorUIUnit{
				{ID: "channel", UnitID: slack.UIUnitChannelPicker, Label: "Blog request channel", Required: true,
					Description: "Select the joined Slack channel where people ask for blog posts; every new top-level message there starts a BlogPost run.",
					Bindings:    []sdkgo.ConnectorUIBinding{{Port: slack.UIChannelPickerPortChannelID, JSONPointer: "/channelId"}}},
				{ID: "members", UnitID: slack.UIUnitMemberPicker, Label: "Members allowed to request",
					Description: "Select the members who may request posts; leave empty to accept anyone posting in the channel.",
					Bindings:    []sdkgo.ConnectorUIBinding{{Port: slack.UIMemberPickerPortMemberIDs, JSONPointer: "/threadTriggerMatcher/posterUserIds"}}},
			}},
		}),
	}
}

func (flow *Flow) GetRPCs() []dex.RPCDef {
	statusLock := []dex.AttributeLock{dex.LockAttribute(blogStatus)}
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.ApproveBlogPost, &dex.RPCOptions{
			Action: dex.DefineAction("Approve and send",
				dex.WhenAttributeMatches(blogStatus, dex.AttributeMatchEqual(StatusAwaitingReview)),
				dex.ActionRequiresPermission("newsletter.manage")),
			LockAttributes: statusLock,
		}),
		dex.DefineRPC(flow.ReviseBlogPost, &dex.RPCOptions{
			Action: dex.DefineAction("Revise",
				dex.WhenAttributeMatches(blogStatus, dex.AttributeMatchEqual(StatusAwaitingReview)),
				dex.ActionRequiresPermission("newsletter.manage")),
			LockAttributes: statusLock,
		}),
		dex.DefineRPC(flow.RetryBlogPostStage, &dex.RPCOptions{
			Action: dex.DefineAction("Retry",
				dex.WhenAttributeMatches(blogStatus, dex.AttributeMatchEqual(StatusNeedsAttention)),
				dex.ActionRequiresPermission("newsletter.manage")),
			LockAttributes: statusLock,
		}),
		dex.DefineRPC(flow.RejectBlogPost, &dex.RPCOptions{
			Action: dex.DefineAction("Reject",
				dex.WhenAttributeMatches(blogStatus, dex.AttributeMatchEqual(StatusAwaitingReview), dex.AttributeMatchEqual(StatusNeedsAttention)),
				dex.ActionRequiresPermission("newsletter.manage")),
			LockAttributes: statusLock,
		}),
	}
}

func (*Flow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: allBlogPostAttribute, Channels: []dex.ChannelDef{reviewDecisions}}
}

// dex:group group-id:request group-label:"Request"
// dex:explanation text:"Store the Slack request, initialize the run's state, and acknowledge it in the thread."
type RecordBlogRequest struct {
	dex.StepDefaults
	flow *Flow
}

func (RecordBlogRequest) GetStepType() string { return "RecordBlogRequest" }

// WaitFor skips at once; Dex Web Start Flow calls the start Step's WaitFor.
func (RecordBlogRequest) WaitFor(dex.Context, SlackRequest) (*dex.Wait, error) {
	return dex.SkipWaitImmediately(), nil
}

func (step RecordBlogRequest) Execute(ctx dex.Context, request SlackRequest) (*dex.StepDecision, error) {
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" || request.ChannelID == "" || request.MessageTimestamp == "" {
		return dex.ForceFail("a blog request needs Slack message text, a channel, and a message timestamp"), nil
	}
	if request.ReceivedAt.IsZero() {
		request.ReceivedAt = time.Now().UTC()
	}
	if err := initializeBlogPost(ctx, request); err != nil {
		return nil, err
	}
	if err := blogRequest.Set(ctx, request); err != nil {
		return nil, err
	}
	if err := blogStatus.Set(ctx, StatusInterpreting); err != nil {
		return nil, err
	}
	if err := blogRequester.Set(ctx, request.UserID); err != nil {
		return nil, err
	}
	if err := blogTopic.Set(ctx, ""); err != nil {
		return nil, err
	}
	notice := noticeFor(request, "On it. I'm reading your request and researching recent changes. Follow along in Dex Web: "+step.flow.runLink(ctx))
	return dex.GoToMany(
		dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), notice),
		dex.MovementOf(sdkgo.StepRef[SlackRequest](stepInterpretBlogRequest), request),
	), nil
}

// dex:group group-id:request group-label:"Request"
// dex:explanation text:"Validate the topic and time range, or close the run when the message is not a blog request."
type ApplyRequestInterpretation struct {
	dex.StepDefaultsNoWaitFor[llmrouter.GenerateTextResult]
	flow *Flow
}

func (ApplyRequestInterpretation) GetStepType() string { return "ApplyRequestInterpretation" }

func (step ApplyRequestInterpretation) Execute(ctx dex.Context, result llmrouter.GenerateTextResult) (*dex.StepDecision, error) {
	request, err := blogRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	interpretation, reason := parseGenerated(result, content.ParseInterpretation)
	if reason != "" {
		return dex.GoTo(EnterNeedsAttention{}, Attention{Stage: StageInterpret, Reason: "Reading the request failed: " + reason}), nil
	}
	if !interpretation.IsBlogRequest {
		message := "This doesn't look like a blog request, so I'm skipping it. Try: \"Write a blog post about connectors from the past 2 weeks.\""
		return dex.GoToMany(
			dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
			dex.MovementOf(FinishBlogPost{}, Outcome{Status: StatusNotARequest, Message: interpretation.Explanation}),
		), nil
	}
	research := step.flow.deps.Config.Research
	window := content.ResolveWindow(interpretation, request.ReceivedAt, research.DefaultWindowDays, research.MaxWindowDays)
	if err := blogTopic.Set(ctx, interpretation.Topic); err != nil {
		return nil, err
	}
	if err := changeWindow.Set(ctx, window); err != nil {
		return nil, err
	}
	if err := windowLabel.Set(ctx, window.Label); err != nil {
		return nil, err
	}
	if err := blogStatus.Set(ctx, StatusResearching); err != nil {
		return nil, err
	}
	cursor := OwnerCursor{Owners: step.flow.deps.Config.GitHub.Owners, Window: window, Candidates: []content.RepositoryCandidate{}}
	if err := ownerCursor.Set(ctx, cursor); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[OwnerCursor](stepListOwnerRepositories), cursor), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Keep each owner's active repositories, then list the next owner or ask which repositories fit the topic."
type RecordOwnerRepositories struct {
	dex.StepDefaultsNoWaitFor[github.ListPublicRepositoriesResult]
	flow *Flow
}

func (RecordOwnerRepositories) GetStepType() string { return "RecordOwnerRepositories" }

func (step RecordOwnerRepositories) Execute(ctx dex.Context, result github.ListPublicRepositoriesResult) (*dex.StepDecision, error) {
	cursor, err := ownerCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	owner := cursor.Owners[cursor.Index]
	if result.Branch == github.ListPublicRepositoriesBranchRepositoriesLoaded {
		cursor.Candidates = content.MergeCandidates(cursor.Candidates, content.CandidatesFromRepositories(owner, result.Value.Repositories, cursor.Window))
	} else {
		cursor.Failures = append(cursor.Failures, owner+": "+failureText(result.Branch, result.Failure))
	}
	cursor.Index++
	if err := ownerCursor.Set(ctx, cursor); err != nil {
		return nil, err
	}
	if cursor.Index < len(cursor.Owners) {
		return dex.GoTo(sdkgo.StepRef[OwnerCursor](stepListOwnerRepositories), cursor), nil
	}
	if len(cursor.Candidates) == 0 && len(cursor.Failures) > 0 {
		return dex.GoTo(EnterNeedsAttention{}, Attention{Stage: StageListOwners, Reason: "Listing GitHub repositories failed: " + strings.Join(cursor.Failures, "; ")}), nil
	}
	request, err := blogRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(cursor.Candidates) == 0 {
		message := "No public repository under the configured GitHub owners changed during " + cursor.Window.Label + ", so there is nothing to write about."
		return dex.GoToMany(
			dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
			dex.MovementOf(FinishBlogPost{}, Outcome{Status: StatusNoChanges, Message: message}),
		), nil
	}
	if err := candidateRepos.Set(ctx, cursor.Candidates); err != nil {
		return nil, err
	}
	input, err := step.flow.repositoryChoiceInput(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[content.RepositoryChoiceInput](stepChooseRepositories), input), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Keep the chosen repositories and start researching the first one."
type ApplyRepositoryChoice struct {
	dex.StepDefaultsNoWaitFor[llmrouter.GenerateTextResult]
	flow *Flow
}

func (ApplyRepositoryChoice) GetStepType() string { return "ApplyRepositoryChoice" }

func (step ApplyRepositoryChoice) Execute(ctx dex.Context, result llmrouter.GenerateTextResult) (*dex.StepDecision, error) {
	candidates, err := candidateRepos.Get(ctx)
	if err != nil {
		return nil, err
	}
	limit := step.flow.deps.Config.Research.MaxRepositories
	selected, reason := parseGenerated(result, func(text string) ([]content.SelectedRepository, error) {
		return content.ParseRepositoryChoice(text, candidates, limit)
	})
	if reason != "" {
		return dex.GoTo(EnterNeedsAttention{}, Attention{Stage: StageChoose, Reason: "Choosing repositories failed: " + reason}), nil
	}
	window, err := changeWindow.Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		request, err := blogRequest.Get(ctx)
		if err != nil {
			return nil, err
		}
		topic, err := blogTopic.Get(ctx)
		if err != nil {
			return nil, err
		}
		message := fmt.Sprintf("None of the %d repositories that changed during %s look related to %q.", len(candidates), window.Label, topic)
		return dex.GoToMany(
			dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
			dex.MovementOf(FinishBlogPost{}, Outcome{Status: StatusNoChanges, Message: message}),
		), nil
	}
	if err := selectedRepos.Set(ctx, selected); err != nil {
		return nil, err
	}
	if err := repositoryResearch.Set(ctx, []content.RepositoryResearch{}); err != nil {
		return nil, err
	}
	cursor := ResearchCursor{Repositories: selected, Window: window, Current: content.RepositoryResearch{Repository: selected[0]}}
	if err := researchCursor.Set(ctx, cursor); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[ResearchCursor](stepListMergedPRs), cursor), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Keep the merged pull requests, then read the largest ones' changed files or the repository's commits."
type RecordMergedPullRequests struct {
	dex.StepDefaultsNoWaitFor[github.ListMergedPullRequestsResult]
	flow *Flow
}

func (RecordMergedPullRequests) GetStepType() string { return "RecordMergedPullRequests" }

func (step RecordMergedPullRequests) Execute(ctx dex.Context, result github.ListMergedPullRequestsResult) (*dex.StepDecision, error) {
	cursor, err := researchCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	research := step.flow.deps.Config.Research
	if result.Branch == github.ListMergedPullRequestsBranchListed {
		cursor.Current.PullRequests = content.PullRequestsFromPage(result.Value, research.PullRequestsPerRepository)
		cursor.PendingPullRequests = []int{}
		for _, pullRequest := range cursor.Current.PullRequests {
			if len(cursor.PendingPullRequests) < research.PullRequestsWithChangedFiles {
				cursor.PendingPullRequests = append(cursor.PendingPullRequests, pullRequest.Number)
			}
		}
	} else {
		cursor.Current.Notes = append(cursor.Current.Notes, "Merged pull requests unavailable: "+failureText(result.Branch, result.Failure))
	}
	if err := researchCursor.Set(ctx, cursor); err != nil {
		return nil, err
	}
	if len(cursor.PendingPullRequests) > 0 {
		return dex.GoTo(sdkgo.StepRef[ResearchCursor](stepListPullRequestFiles), cursor), nil
	}
	return dex.GoTo(sdkgo.StepRef[ResearchCursor](stepListRepositoryCommits), cursor), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Attach one pull request's changed files, then read the next pull request or the repository's commits."
type RecordPullRequestFiles struct {
	dex.StepDefaultsNoWaitFor[github.ListPullRequestFilesResult]
}

func (RecordPullRequestFiles) GetStepType() string { return "RecordPullRequestFiles" }

func (RecordPullRequestFiles) Execute(ctx dex.Context, result github.ListPullRequestFilesResult) (*dex.StepDecision, error) {
	cursor, err := researchCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	number := cursor.PendingPullRequests[0]
	cursor.PendingPullRequests = cursor.PendingPullRequests[1:]
	if result.Branch == github.ListPullRequestFilesBranchListed {
		for index := range cursor.Current.PullRequests {
			if cursor.Current.PullRequests[index].Number == number {
				cursor.Current.PullRequests[index].Files = content.FilesFromPage(result.Value)
			}
		}
	} else {
		cursor.Current.Notes = append(cursor.Current.Notes, fmt.Sprintf("Files of pull request #%d unavailable: %s", number, failureText(result.Branch, result.Failure)))
	}
	if err := researchCursor.Set(ctx, cursor); err != nil {
		return nil, err
	}
	if len(cursor.PendingPullRequests) > 0 {
		return dex.GoTo(sdkgo.StepRef[ResearchCursor](stepListPullRequestFiles), cursor), nil
	}
	return dex.GoTo(sdkgo.StepRef[ResearchCursor](stepListRepositoryCommits), cursor), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Finish one repository, then research the next repository or start writing."
type RecordRepositoryCommits struct {
	dex.StepDefaultsNoWaitFor[github.ListCommitsResult]
	flow *Flow
}

func (RecordRepositoryCommits) GetStepType() string { return "RecordRepositoryCommits" }

func (step RecordRepositoryCommits) Execute(ctx dex.Context, result github.ListCommitsResult) (*dex.StepDecision, error) {
	cursor, err := researchCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	if result.Branch == github.ListCommitsBranchListed {
		cursor.Current.Commits = content.CommitsFromPage(result.Value, cursor.Current.PullRequests, step.flow.deps.Config.Research.CommitsPerRepository)
	} else {
		cursor.Current.Notes = append(cursor.Current.Notes, "Commits unavailable: "+failureText(result.Branch, result.Failure))
	}
	research, err := repositoryResearch.Get(ctx)
	if err != nil {
		return nil, err
	}
	research = append(research, cursor.Current)
	if err := repositoryResearch.Set(ctx, research); err != nil {
		return nil, err
	}
	cursor.Index++
	cursor.PendingPullRequests = nil
	if cursor.Index < len(cursor.Repositories) {
		cursor.Current = content.RepositoryResearch{Repository: cursor.Repositories[cursor.Index]}
		if err := researchCursor.Set(ctx, cursor); err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[ResearchCursor](stepListMergedPRs), cursor), nil
	}
	if err := researchCursor.Set(ctx, cursor); err != nil {
		return nil, err
	}
	if err := researchSummary.Set(ctx, content.ResearchSummary(research)); err != nil {
		return nil, err
	}
	if pullRequests, commits := content.ResearchTotals(research); pullRequests+commits == 0 {
		request, err := blogRequest.Get(ctx)
		if err != nil {
			return nil, err
		}
		message := "The chosen repositories had no merged pull requests or commits during " + cursor.Window.Label + "."
		for _, repository := range research {
			if len(repository.Notes) > 0 {
				message += " Some GitHub queries failed: " + strings.Join(repository.Notes, "; ")
				break
			}
		}
		return dex.GoToMany(
			dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
			dex.MovementOf(FinishBlogPost{}, Outcome{Status: StatusNoChanges, Message: message}),
		), nil
	}
	return dex.GoTo(PrepareBlogWriting{}, RevisionRequest{}), nil
}

// dex:group group-id:writing group-label:"Writing"
// dex:explanation text:"Assemble the research, and any editor notes, into the blog writing request."
type PrepareBlogWriting struct {
	dex.StepDefaultsNoWaitFor[RevisionRequest]
	flow *Flow
}

func (PrepareBlogWriting) GetStepType() string { return "PrepareBlogWriting" }

func (step PrepareBlogWriting) Execute(ctx dex.Context, revision RevisionRequest) (*dex.StepDecision, error) {
	topic, err := blogTopic.Get(ctx)
	if err != nil {
		return nil, err
	}
	window, err := changeWindow.Get(ctx)
	if err != nil {
		return nil, err
	}
	research, err := repositoryResearch.Get(ctx)
	if err != nil {
		return nil, err
	}
	input := content.BlogWritingInput{
		Topic: topic, Window: window, PublicationName: step.flow.deps.Config.Blog.PublicationName,
		Research: research, EditorNotes: strings.TrimSpace(revision.Notes),
	}
	if input.EditorNotes != "" {
		previous, err := blogDraft.Get(ctx)
		if err != nil {
			return nil, err
		}
		if previous.Title != "" {
			input.PreviousDraft = &previous
		}
	}
	if err := blogStatus.Set(ctx, StatusWriting); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[content.BlogWritingInput](stepWriteBlogPost), input), nil
}

// dex:group group-id:writing group-label:"Writing"
// dex:explanation text:"Ground the draft's links in the research, render the self-contained HTML artifact, and ask for the newsletter."
type RecordBlogDraft struct {
	dex.StepDefaultsNoWaitFor[llmrouter.GenerateTextResult]
	flow *Flow
}

func (RecordBlogDraft) GetStepType() string { return "RecordBlogDraft" }

func (step RecordBlogDraft) Execute(ctx dex.Context, result llmrouter.GenerateTextResult) (*dex.StepDecision, error) {
	research, err := repositoryResearch.Get(ctx)
	if err != nil {
		return nil, err
	}
	draft, reason := parseGenerated(result, func(text string) (content.BlogDraft, error) {
		return content.ParseBlogDraft(text, content.EvidenceURLs(research))
	})
	if reason != "" {
		return dex.GoTo(EnterNeedsAttention{}, Attention{Stage: StageWriteBlog, Reason: "Writing the blog post failed: " + reason}), nil
	}
	window, err := changeWindow.Get(ctx)
	if err != nil {
		return nil, err
	}
	repositories, err := selectedRepos.Get(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := revisionCount.Get(ctx)
	if err != nil {
		return nil, err
	}
	blog := step.flow.deps.Config.Blog
	html, err := content.RenderBlogHTML(content.BlogPage{Draft: draft, PublicationName: blog.PublicationName, Window: window, Repositories: repositories})
	if err != nil {
		return nil, err
	}
	// The path is derived from the run and revision, so a retried attempt overwrites the same file.
	path, err := writeArtifact(blog.ArtifactDirectory, ctx.FlowID(), fmt.Sprintf("%s-r%d.html", draft.Slug, revision), html)
	if err != nil {
		return nil, err
	}
	if err := blogDraft.Set(ctx, draft); err != nil {
		return nil, err
	}
	if err := blogTitle.Set(ctx, draft.Title); err != nil {
		return nil, err
	}
	if err := blogPreview.Set(ctx, content.RenderBlogText(draft)); err != nil {
		return nil, err
	}
	if err := blogHTML.Set(ctx, html); err != nil {
		return nil, err
	}
	if err := blogArtifactPath.Set(ctx, path); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[NewsletterWritingInput](stepWriteNewsletter), NewsletterWritingInput{Draft: draft, PublicationName: blog.PublicationName}), nil
}

// dex:group group-id:writing group-label:"Writing"
// dex:explanation text:"Keep the newsletter draft and its plain-text preview, then open the review."
type RecordNewsletterDraft struct {
	dex.StepDefaultsNoWaitFor[llmrouter.GenerateTextResult]
	flow *Flow
}

func (RecordNewsletterDraft) GetStepType() string { return "RecordNewsletterDraft" }

func (step RecordNewsletterDraft) Execute(ctx dex.Context, result llmrouter.GenerateTextResult) (*dex.StepDecision, error) {
	draft, reason := parseGenerated(result, content.ParseNewsletterDraft)
	if reason != "" {
		return dex.GoTo(EnterNeedsAttention{}, Attention{Stage: StageWriteNewsletter, Reason: "Writing the newsletter failed: " + reason}), nil
	}
	blog, err := blogDraft.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := newsletterDraft.Set(ctx, draft); err != nil {
		return nil, err
	}
	if err := newsletterSubject.Set(ctx, draft.Subject); err != nil {
		return nil, err
	}
	preview := content.RenderNewsletterText(step.flow.newsletterEmail(draft, blog.Slug, "reader@example.com"))
	if err := newsletterPreview.Set(ctx, preview); err != nil {
		return nil, err
	}
	return dex.GoTo(EnterReview{}, nil), nil
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Open a new review round and tell the requester the draft is ready in Dex Web."
type EnterReview struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	flow *Flow
}

func (EnterReview) GetStepType() string { return "EnterReview" }

func (step EnterReview) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	round, err := reviewRound.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := reviewRound.Set(ctx, round+1); err != nil {
		return nil, err
	}
	if err := attentionStage.Set(ctx, ""); err != nil {
		return nil, err
	}
	if err := attentionReason.Set(ctx, ""); err != nil {
		return nil, err
	}
	if err := blogStatus.Set(ctx, StatusAwaitingReview); err != nil {
		return nil, err
	}
	request, err := blogRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	title, err := blogTitle.Get(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := researchSummary.Get(ctx)
	if err != nil {
		return nil, err
	}
	message := fmt.Sprintf("Draft ready for review: *%s*\nBased on %s.\nApprove and send, revise, or reject it in Dex Web: %s", title, summary, step.flow.runLink(ctx))
	return dex.GoToMany(
		dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
		dex.MovementOf(AwaitEditorDecision{}, nil),
	), nil
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Record why the run stopped and wait for an editor to retry or reject it."
type EnterNeedsAttention struct {
	dex.StepDefaultsNoWaitFor[Attention]
	flow *Flow
}

func (EnterNeedsAttention) GetStepType() string { return "EnterNeedsAttention" }

func (step EnterNeedsAttention) Execute(ctx dex.Context, attention Attention) (*dex.StepDecision, error) {
	round, err := reviewRound.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := reviewRound.Set(ctx, round+1); err != nil {
		return nil, err
	}
	if err := attentionStage.Set(ctx, attention.Stage); err != nil {
		return nil, err
	}
	if err := attentionReason.Set(ctx, attention.Reason); err != nil {
		return nil, err
	}
	if err := blogStatus.Set(ctx, StatusNeedsAttention); err != nil {
		return nil, err
	}
	request, err := blogRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	message := "I'm stuck. " + attention.Reason + "\nRetry or reject it in Dex Web: " + step.flow.runLink(ctx)
	return dex.GoToMany(
		dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
		dex.MovementOf(AwaitEditorDecision{}, nil),
	), nil
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Wait for an editor's Approve, Revise, Retry, or Reject Action in Dex Web."
type AwaitEditorDecision struct {
	dex.StepDefaults
	flow *Flow
}

func (AwaitEditorDecision) GetStepType() string { return "AwaitEditorDecision" }

func (AwaitEditorDecision) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(reviewDecisions.ForOne()), nil
}

func (step AwaitEditorDecision) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	decisions, err := reviewDecisions.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	round, err := reviewRound.Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(decisions) != 1 || decisions[0].Round != round {
		// A decision from an earlier round raced its own Action; keep waiting for this round.
		return dex.GoTo(AwaitEditorDecision{}, nil), nil
	}
	decision := decisions[0]
	stage, err := attentionStage.Get(ctx)
	if err != nil {
		return nil, err
	}
	request, err := blogRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	switch decision.Kind {
	case decisionApprove:
		return dex.GoTo(StartNewsletterDelivery{}, nil), nil
	case decisionRevise:
		count, err := revisionCount.Get(ctx)
		if err != nil {
			return nil, err
		}
		if err := revisionCount.Set(ctx, count+1); err != nil {
			return nil, err
		}
		if err := editorNotes.Set(ctx, decision.Notes); err != nil {
			return nil, err
		}
		return dex.GoTo(PrepareBlogWriting{}, RevisionRequest{Notes: decision.Notes}), nil
	case decisionReject:
		outcome := Outcome{Status: StatusRejected, Message: "An editor rejected the draft."}
		if stage == StageDeliver {
			outcome = Outcome{Status: StatusStopped, Message: "An editor stopped the newsletter delivery."}
		}
		if decision.Notes != "" {
			outcome.Message += " " + decision.Notes
		}
		return dex.GoToMany(
			dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, outcome.Message)),
			dex.MovementOf(FinishBlogPost{}, outcome),
		), nil
	}
	// decisionRetry resumes the stage that stopped.
	switch stage {
	case StageInterpret:
		return dex.GoTo(sdkgo.StepRef[SlackRequest](stepInterpretBlogRequest), request), nil
	case StageListOwners:
		window, err := changeWindow.Get(ctx)
		if err != nil {
			return nil, err
		}
		cursor := OwnerCursor{Owners: step.flow.deps.Config.GitHub.Owners, Window: window, Candidates: []content.RepositoryCandidate{}}
		if err := ownerCursor.Set(ctx, cursor); err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[OwnerCursor](stepListOwnerRepositories), cursor), nil
	case StageChoose:
		input, err := step.flow.repositoryChoiceInput(ctx)
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[content.RepositoryChoiceInput](stepChooseRepositories), input), nil
	case StageWriteNewsletter:
		draft, err := blogDraft.Get(ctx)
		if err != nil {
			return nil, err
		}
		input := NewsletterWritingInput{Draft: draft, PublicationName: step.flow.deps.Config.Blog.PublicationName}
		return dex.GoTo(sdkgo.StepRef[NewsletterWritingInput](stepWriteNewsletter), input), nil
	case StageDeliver:
		email, err := step.flow.nextEmail(ctx)
		if err != nil {
			return nil, err
		}
		if err := blogStatus.Set(ctx, StatusDelivering); err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[OutgoingEmail](stepSendNewsletterEmail), email), nil
	}
	notes, err := editorNotes.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(PrepareBlogWriting{}, RevisionRequest{Notes: notes}), nil
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Snapshot the subscriber list and send the first email."
type StartNewsletterDelivery struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	flow *Flow
}

func (StartNewsletterDelivery) GetStepType() string { return "StartNewsletterDelivery" }

func (step StartNewsletterDelivery) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	recipients, err := step.flow.deps.Subscribers.ListSubscribers(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the subscriber list: %w", err)
	}
	if err := deliveryRecipients.Set(ctx, recipients); err != nil {
		return nil, err
	}
	progress := DeliveryProgress{Total: len(recipients)}
	if err := deliveryProgress.Set(ctx, progress); err != nil {
		return nil, err
	}
	if err := deliverySummary.Set(ctx, deliveryText(progress)); err != nil {
		return nil, err
	}
	if err := attentionStage.Set(ctx, StageDeliver); err != nil {
		return nil, err
	}
	if err := blogStatus.Set(ctx, StatusDelivering); err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		request, err := blogRequest.Get(ctx)
		if err != nil {
			return nil, err
		}
		message := "Approved. The newsletter has no subscribers yet, so no email was sent."
		return dex.GoToMany(
			dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
			dex.MovementOf(FinishBlogPost{}, Outcome{Status: StatusSent, Message: message}),
		), nil
	}
	email, err := step.flow.nextEmail(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[OutgoingEmail](stepSendNewsletterEmail), email), nil
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Count one send, stop for an editor when Gmail rejects the account, then send the next email or finish."
type RecordNewsletterEmailResult struct {
	dex.StepDefaultsNoWaitFor[gmail.SendMessageResult]
	flow *Flow
}

func (RecordNewsletterEmailResult) GetStepType() string { return "RecordNewsletterEmailResult" }

func (step RecordNewsletterEmailResult) Execute(ctx dex.Context, result gmail.SendMessageResult) (*dex.StepDecision, error) {
	progress, err := deliveryProgress.Get(ctx)
	if err != nil {
		return nil, err
	}
	recipients, err := deliveryRecipients.Get(ctx)
	if err != nil {
		return nil, err
	}
	address := recipients[progress.Next]
	if stopsDelivery(result) {
		reason := fmt.Sprintf("Gmail stopped the delivery at subscriber %d of %d: %s", progress.Next+1, progress.Total, failureText(result.Branch, result.Failure))
		return dex.GoTo(EnterNeedsAttention{}, Attention{Stage: StageDeliver, Reason: reason}), nil
	}
	exceptions, err := deliveryExceptions.Get(ctx)
	if err != nil {
		return nil, err
	}
	switch result.Branch {
	case gmail.SendMessageBranchSent:
		progress.Sent++
	case gmail.SendMessageBranchUncertain:
		progress.Uncertain++
		exceptions = appendException(exceptions, DeliveryException{Address: address, Outcome: "uncertain", Detail: "Gmail may have sent it; check the Sent folder before resending."})
	default:
		progress.Failed++
		exceptions = appendException(exceptions, DeliveryException{Address: address, Outcome: "rejected", Detail: failureText(result.Branch, result.Failure)})
	}
	progress.Next++
	if err := deliveryProgress.Set(ctx, progress); err != nil {
		return nil, err
	}
	if err := deliverySummary.Set(ctx, deliveryText(progress)); err != nil {
		return nil, err
	}
	if err := deliveryExceptions.Set(ctx, exceptions); err != nil {
		return nil, err
	}
	if progress.Next < progress.Total {
		email, err := step.flow.nextEmail(ctx)
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[OutgoingEmail](stepSendNewsletterEmail), email), nil
	}
	request, err := blogRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	message := "Newsletter sent: " + deliveryText(progress) + "."
	return dex.GoToMany(
		dex.MovementOf(sdkgo.StepRef[SlackNotice](stepPostSlackNotice), noticeFor(request, message)),
		dex.MovementOf(FinishBlogPost{}, Outcome{Status: StatusSent, Message: message}),
	), nil
}

// dex:group group-id:slack group-label:"Slack"
// dex:explanation text:"Record whether the Slack reply was posted; a lost reply never blocks the post."
type RecordSlackNotice struct {
	dex.StepDefaultsNoWaitFor[slack.PostThreadReplyResult]
}

func (RecordSlackNotice) GetStepType() string { return "RecordSlackNotice" }

func (RecordSlackNotice) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(slackNoticeStatus)}}
}

func (RecordSlackNotice) Execute(ctx dex.Context, result slack.PostThreadReplyResult) (*dex.StepDecision, error) {
	status := "Last Slack reply posted"
	if result.Branch != slack.PostThreadReplyBranchSent {
		status = "Last Slack reply not posted: " + failureText(result.Branch, result.Failure)
	}
	if err := slackNoticeStatus.Set(ctx, status); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Record the final outcome and complete the run."
type FinishBlogPost struct {
	dex.StepDefaultsNoWaitFor[Outcome]
	flow *Flow
}

func (FinishBlogPost) GetStepType() string { return "FinishBlogPost" }

func (FinishBlogPost) Execute(ctx dex.Context, outcome Outcome) (*dex.StepDecision, error) {
	if err := blogStatus.Set(ctx, outcome.Status); err != nil {
		return nil, err
	}
	if outcome.Status != StatusSent {
		if err := attentionReason.Set(ctx, outcome.Message); err != nil {
			return nil, err
		}
	}
	title, err := blogTitle.Get(ctx)
	if err != nil {
		return nil, err
	}
	progress, err := deliveryProgress.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(Result{Status: outcome.Status, Title: title, Message: outcome.Message, Delivery: progress}), nil
}

type ApproveBlogPostInput struct {
	Round int64 `json:"round"`
}

// ApproveBlogPost approves the current draft; the Round comes from the form the editor opened.
//
// dex:input field-name:round value-type:int64 source:attribute attribute-key:review-round required:true description:"Review round"
func (*Flow) ApproveBlogPost(ctx dex.Context, input ApproveBlogPostInput) (*dex.RPCResult[dex.None], error) {
	return decide(ctx, []string{StatusAwaitingReview}, ReviewDecision{Kind: decisionApprove, Round: input.Round}, StatusDelivering)
}

type ReviseBlogPostInput struct {
	Notes string `json:"notes"`
	Round int64  `json:"round"`
}

// ReviseBlogPost rewrites the blog and newsletter with the editor's notes.
//
// dex:input field-name:notes value-type:string source:user required:true description:"What should change in the next draft"
// dex:input field-name:round value-type:int64 source:attribute attribute-key:review-round required:true description:"Review round"
func (*Flow) ReviseBlogPost(ctx dex.Context, input ReviseBlogPostInput) (*dex.RPCResult[dex.None], error) {
	notes := strings.TrimSpace(input.Notes)
	if notes == "" {
		return nil, errors.New("describe what should change")
	}
	if len(notes) > 4000 {
		return nil, errors.New("keep revision notes under 4000 characters")
	}
	return decide(ctx, []string{StatusAwaitingReview}, ReviewDecision{Kind: decisionRevise, Round: input.Round, Notes: notes}, StatusWriting)
}

type RetryBlogPostStageInput struct {
	Round int64 `json:"round"`
}

// RetryBlogPostStage resumes the stage that stopped.
//
// dex:input field-name:round value-type:int64 source:attribute attribute-key:review-round required:true description:"Review round"
func (*Flow) RetryBlogPostStage(ctx dex.Context, input RetryBlogPostStageInput) (*dex.RPCResult[dex.None], error) {
	return decide(ctx, []string{StatusNeedsAttention}, ReviewDecision{Kind: decisionRetry, Round: input.Round}, StatusRetrying)
}

type RejectBlogPostInput struct {
	Reason *string `json:"reason,omitempty"`
	Round  int64   `json:"round"`
}

// RejectBlogPost closes the run without sending, or stops a delivery in progress.
//
// dex:input field-name:reason value-type:string source:user required:false description:"Why the draft is rejected"
// dex:input field-name:round value-type:int64 source:attribute attribute-key:review-round required:true description:"Review round"
func (*Flow) RejectBlogPost(ctx dex.Context, input RejectBlogPostInput) (*dex.RPCResult[dex.None], error) {
	notes := ""
	if input.Reason != nil {
		notes = strings.TrimSpace(*input.Reason)
	}
	return decide(ctx, []string{StatusAwaitingReview, StatusNeedsAttention}, ReviewDecision{Kind: decisionReject, Round: input.Round, Notes: notes}, StatusRejected)
}

// decide rechecks the status and round, moves the status so a repeated Action is refused, and queues the decision.
func decide(ctx dex.Context, allowed []string, decision ReviewDecision, next string) (*dex.RPCResult[dex.None], error) {
	status, err := blogStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	permitted := false
	for _, candidate := range allowed {
		permitted = permitted || status == candidate
	}
	if !permitted {
		return nil, fmt.Errorf("this blog post is %s, so the action no longer applies", status)
	}
	round, err := reviewRound.Get(ctx)
	if err != nil {
		return nil, err
	}
	if decision.Round != round {
		return nil, errors.New("the draft changed since you opened it; reopen the run and try again")
	}
	if err := blogStatus.Set(ctx, next); err != nil {
		return nil, err
	}
	if err := reviewDecisions.Publish(ctx, decision); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:field attribute-key:blog-title value-type:string editable:false description:"Blog post"
// dex:field attribute-key:window-label value-type:string editable:false description:"Changes from"
// dex:field attribute-key:delivery-summary value-type:string editable:false description:"Delivery"
func (*Flow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	title, err := blogTitle.Get(ctx)
	if err != nil {
		return nil, err
	}
	window, err := windowLabel.Get(ctx)
	if err != nil {
		return nil, err
	}
	delivery, err := deliverySummary.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"blog-title":       title,
		"window-label":     window,
		"delivery-summary": delivery,
	}}, nil
}

// dex:field attribute-key:blog-title value-type:string editable:false description:"Blog post" ui-slot:title
// dex:field attribute-key:blog-topic value-type:string editable:false description:"Topic" ui-slot:subtitle
// dex:field attribute-key:blog-status value-type:string editable:false description:"Status" ui-slot:status
// dex:field attribute-key:attention-reason value-type:string editable:false description:"Needs attention because" ui-slot:reason
// dex:field attribute-key:window-label value-type:string editable:false description:"Changes from"
// dex:field attribute-key:research-summary value-type:string editable:false description:"Research"
// dex:field attribute-key:selected-repositories value-type:array editable:false description:"Repositories and focus areas"
// dex:field attribute-key:blog-preview value-type:string editable:false description:"Blog draft"
// dex:field attribute-key:blog-artifact-path value-type:string editable:false description:"Blog HTML artifact"
// dex:field attribute-key:newsletter-subject value-type:string editable:false description:"Newsletter subject"
// dex:field attribute-key:newsletter-preview value-type:string editable:false description:"Newsletter email"
// dex:field attribute-key:revision-count value-type:int64 editable:false description:"Revisions"
// dex:field attribute-key:editor-notes value-type:string editable:false description:"Latest revision notes"
// dex:field attribute-key:delivery-summary value-type:string editable:false description:"Delivery"
// dex:field attribute-key:delivery-exceptions value-type:array editable:false description:"Recipients to check (first 50)"
// dex:field attribute-key:slack-notice-status value-type:string editable:false description:"Slack"
func (*Flow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	title, err := blogTitle.Get(ctx)
	if err != nil {
		return nil, err
	}
	topic, err := blogTopic.Get(ctx)
	if err != nil {
		return nil, err
	}
	status, err := blogStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	reason, err := attentionReason.Get(ctx)
	if err != nil {
		return nil, err
	}
	window, err := windowLabel.Get(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := researchSummary.Get(ctx)
	if err != nil {
		return nil, err
	}
	repositories, err := selectedRepos.Get(ctx)
	if err != nil {
		return nil, err
	}
	preview, err := blogPreview.Get(ctx)
	if err != nil {
		return nil, err
	}
	path, err := blogArtifactPath.Get(ctx)
	if err != nil {
		return nil, err
	}
	subject, err := newsletterSubject.Get(ctx)
	if err != nil {
		return nil, err
	}
	email, err := newsletterPreview.Get(ctx)
	if err != nil {
		return nil, err
	}
	revisions, err := revisionCount.Get(ctx)
	if err != nil {
		return nil, err
	}
	notes, err := editorNotes.Get(ctx)
	if err != nil {
		return nil, err
	}
	delivery, err := deliverySummary.Get(ctx)
	if err != nil {
		return nil, err
	}
	exceptions, err := deliveryExceptions.Get(ctx)
	if err != nil {
		return nil, err
	}
	notice, err := slackNoticeStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"blog-title":            title,
		"blog-topic":            topic,
		"blog-status":           status,
		"attention-reason":      reason,
		"window-label":          window,
		"research-summary":      summary,
		"selected-repositories": repositories,
		"blog-preview":          preview,
		"blog-artifact-path":    path,
		"newsletter-subject":    subject,
		"newsletter-preview":    email,
		"revision-count":        revisions,
		"editor-notes":          notes,
		"delivery-summary":      delivery,
		"delivery-exceptions":   exceptions,
		"slack-notice-status":   notice,
	}}, nil
}

// initializeBlogPost writes every displayed Attribute once so views never read a missing value.
func initializeBlogPost(ctx dex.Context, request SlackRequest) error {
	for _, write := range []func() error{
		func() error { return changeWindow.Set(ctx, content.Window{}) },
		func() error { return windowLabel.Set(ctx, "") },
		func() error { return blogTitle.Set(ctx, "") },
		func() error { return attentionStage.Set(ctx, "") },
		func() error { return attentionReason.Set(ctx, "") },
		func() error { return reviewRound.Set(ctx, 0) },
		func() error { return revisionCount.Set(ctx, 0) },
		func() error { return editorNotes.Set(ctx, "") },
		func() error { return candidateRepos.Set(ctx, []content.RepositoryCandidate{}) },
		func() error { return selectedRepos.Set(ctx, []content.SelectedRepository{}) },
		func() error { return repositoryResearch.Set(ctx, []content.RepositoryResearch{}) },
		func() error { return researchSummary.Set(ctx, "") },
		func() error { return blogDraft.Set(ctx, content.BlogDraft{}) },
		func() error { return blogPreview.Set(ctx, "") },
		func() error { return blogHTML.Set(ctx, "") },
		func() error { return blogArtifactPath.Set(ctx, "") },
		func() error { return newsletterDraft.Set(ctx, content.NewsletterDraft{}) },
		func() error { return newsletterSubject.Set(ctx, "") },
		func() error { return newsletterPreview.Set(ctx, "") },
		func() error { return deliveryRecipients.Set(ctx, []string{}) },
		func() error { return deliveryProgress.Set(ctx, DeliveryProgress{}) },
		func() error { return deliverySummary.Set(ctx, "") },
		func() error { return deliveryExceptions.Set(ctx, []DeliveryException{}) },
		func() error { return slackNoticeStatus.Set(ctx, "") },
		func() error { return ownerCursor.Set(ctx, OwnerCursor{}) },
		func() error { return researchCursor.Set(ctx, ResearchCursor{}) },
	} {
		if err := write(); err != nil {
			return err
		}
	}
	return nil
}

func (flow *Flow) repositoryChoiceInput(ctx dex.Context) (content.RepositoryChoiceInput, error) {
	topic, err := blogTopic.Get(ctx)
	if err != nil {
		return content.RepositoryChoiceInput{}, err
	}
	window, err := changeWindow.Get(ctx)
	if err != nil {
		return content.RepositoryChoiceInput{}, err
	}
	candidates, err := candidateRepos.Get(ctx)
	if err != nil {
		return content.RepositoryChoiceInput{}, err
	}
	return content.RepositoryChoiceInput{Topic: topic, Window: window, Candidates: candidates, Limit: flow.deps.Config.Research.MaxRepositories}, nil
}

// nextEmail renders the email for the subscriber at the delivery cursor.
func (flow *Flow) nextEmail(ctx dex.Context) (OutgoingEmail, error) {
	progress, err := deliveryProgress.Get(ctx)
	if err != nil {
		return OutgoingEmail{}, err
	}
	recipients, err := deliveryRecipients.Get(ctx)
	if err != nil {
		return OutgoingEmail{}, err
	}
	draft, err := newsletterDraft.Get(ctx)
	if err != nil {
		return OutgoingEmail{}, err
	}
	blog, err := blogDraft.Get(ctx)
	if err != nil {
		return OutgoingEmail{}, err
	}
	address := recipients[progress.Next]
	email := flow.newsletterEmail(draft, blog.Slug, address)
	html, err := content.RenderNewsletterHTML(email)
	if err != nil {
		return OutgoingEmail{}, err
	}
	return OutgoingEmail{Address: address, Subject: draft.Subject, Text: content.RenderNewsletterText(email), HTML: html}, nil
}

func (flow *Flow) newsletterEmail(draft content.NewsletterDraft, slug, address string) content.NewsletterEmail {
	blog := flow.deps.Config.Blog
	postURL := ""
	if blog.PostURLTemplate != "" && slug != "" {
		postURL = strings.ReplaceAll(blog.PostURLTemplate, "{slug}", url.PathEscape(slug))
	}
	return content.NewsletterEmail{Draft: draft, PublicationName: blog.PublicationName, PostURL: postURL, UnsubscribeURL: flow.deps.Unsubscribe.URL(address)}
}

func (flow *Flow) runLink(ctx dex.Context) string {
	return flow.deps.Config.DexWebURL + "/v2/run/" + FlowType + "/" + url.PathEscape(ctx.FlowID())
}

func noticeFor(request SlackRequest, text string) SlackNotice {
	return SlackNotice{ChannelID: request.ChannelID, ThreadTimestamp: request.MessageTimestamp, Text: text}
}

// parseGenerated returns the parsed reply, or a reason when generation or parsing failed.
func parseGenerated[T any](result llmrouter.GenerateTextResult, parse func(string) (T, error)) (T, string) {
	var zero T
	if result.Branch != llmrouter.GenerateTextBranchGenerated {
		return zero, failureText(result.Branch, result.Failure)
	}
	parsed, err := parse(result.Value.Text)
	if err != nil {
		return zero, err.Error()
	}
	return parsed, ""
}

func failureText(branch sdkgo.BranchID, failure *sdkgo.Failure) string {
	if failure == nil {
		return string(branch)
	}
	text := string(branch)
	if failure.Kind != "" {
		text += " (" + string(failure.Kind) + ")"
	}
	if failure.Message != "" {
		text += ": " + failure.Message
	}
	return text
}

// stopsDelivery reports failures that would repeat for every remaining subscriber.
func stopsDelivery(result gmail.SendMessageResult) bool {
	if result.Branch == gmail.SendMessageBranchDefect {
		return true
	}
	if result.Branch != gmail.SendMessageBranchProviderRejected || result.Failure == nil {
		return false
	}
	switch result.Failure.Kind {
	case sdkgo.FailureAuthentication, sdkgo.FailureAuthorization, sdkgo.FailureQuotaExhausted, sdkgo.FailureRateLimit, sdkgo.FailureLocalDefect:
		return true
	}
	return false
}

func appendException(exceptions []DeliveryException, exception DeliveryException) []DeliveryException {
	if len(exceptions) >= maxDeliveryExceptions {
		return exceptions
	}
	return append(exceptions, exception)
}

func deliveryText(progress DeliveryProgress) string {
	text := fmt.Sprintf("%d of %d sent", progress.Sent, progress.Total)
	if progress.Failed > 0 {
		text += fmt.Sprintf(", %d rejected", progress.Failed)
	}
	if progress.Uncertain > 0 {
		text += fmt.Sprintf(", %d uncertain", progress.Uncertain)
	}
	return text
}

func writeArtifact(directory, flowID, name, contents string) (string, error) {
	folder := filepath.Join(directory, strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(flowID))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", fmt.Errorf("create blog artifact folder: %w", err)
	}
	path := filepath.Join(folder, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return "", fmt.Errorf("write blog artifact: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return absolute, nil
}

var _ dex.Flow = (*Flow)(nil)
