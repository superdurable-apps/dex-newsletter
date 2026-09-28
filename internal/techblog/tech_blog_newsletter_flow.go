package techblog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/notices"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/prompts"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/render"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/subscribers"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/timewindow"
	"github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

// TechBlogNewsletterFlowType is the stable Flow type of the Process.
const TechBlogNewsletterFlowType = "TechBlogNewsletterFlow"

// Static Dex Web connection and Trigger binding names.
const (
	SlackConnectionName             = "slack-workspace"
	GmailConnectionName             = "newsletter-sender"
	NewsletterRequestTriggerBinding = "tech-blog-newsletter-request"
)

// PermissionManageNewsletter is the Work Queue permission of every human
// Action. One editor role reviews drafts and recovers failed requests, so
// Dex Web shows that editor every Action the run's current state allows.
const PermissionManageNewsletter = "newsletter.manage"

const (
	postRequestAcknowledgementStepType = "PostRequestAcknowledgement"
	postResearchPlanStepType           = "PostResearchPlan"
	postReviewRequestStepType          = "PostReviewRequest"
	postAttentionNoticeStepType        = "PostAttentionNotice"
	postFinalReplyStepType             = "PostFinalReply"
	loadNewsletterSubscribersStepType  = "LoadNewsletterSubscribers"
	sendNewsletterToSubscriberStepType = "SendNewsletterToSubscriber"

	attentionExpiry           = 7 * 24 * time.Hour
	maximumSubjectRunes       = 150
	maximumFeedbackRunes      = 4000
	maximumReasonRunes        = 500
	maximumPreviewRunes       = 60000
	subscriberListReadTimeout = 30 * time.Second
	deliveryExceptionKeyForm  = "recipient-%05d"
	// maximumShownDeliveryExceptions bounds delivery-exceptions-shown, the
	// copy of the first exceptions that Dex Web displays.
	maximumShownDeliveryExceptions = 20
)

// Request status values. Action eligibility is keyed on these values.
const (
	StatusReceived             = "received"
	StatusInterpreting         = "interpreting"
	StatusResearching          = "researching"
	StatusSynthesizing         = "synthesizing"
	StatusDraftingBlogPost     = "drafting-blog-post"
	StatusPublishingArtifact   = "publishing-blog-artifact"
	StatusDraftingNewsletter   = "drafting-newsletter"
	StatusAwaitingEditorReview = "awaiting-editor-review"
	StatusDeliveryApproved     = "delivery-approved"
	StatusRevisionRequested    = "revision-requested"
	StatusDiscardRequested     = "discard-requested"
	StatusLoadingSubscribers   = "loading-subscribers"
	StatusSendingNewsletter    = "sending-newsletter"
	StatusNeedsAttention       = "needs-attention"
	StatusRetryRequested       = "retry-requested"
	StatusAbandonRequested     = "abandon-requested"
	StatusDelivered            = "delivered"
	StatusDeliveryFailed       = "delivery-failed"
	StatusDeliveryStopped      = "delivery-stopped"
	StatusNeedsClarification   = "needs-clarification"
	StatusNoNotableChanges     = "no-notable-changes"
	StatusNoSubscribers        = "no-subscribers"
	StatusDiscarded            = "discarded"
	StatusReviewExpired        = "review-expired"
	StatusAbandoned            = "abandoned"
)

// Recoverable stages. An operator can retry the failed stage from Dex Web.
const (
	StageInterpretRequest    = "interpret-request"
	StageResearchRepos       = "research-repositories"
	StageSynthesizeResearch  = "synthesize-research"
	StageDraftBlogPost       = "draft-blog-post"
	StagePublishBlogArtifact = "publish-blog-artifact"
	StageDraftNewsletter     = "draft-newsletter"
	StageLoadSubscribers     = "load-subscribers"
	StageSendNewsletter      = "send-newsletter"
)

// Continuations name the Step each Slack reply leads to, so a reply whose
// Connector Step exhausts its retries still continues the Process.
const (
	ContinueToRequestInterpretation = "request-interpretation"
	ContinueToRepositoryResearch    = "repository-research"
	ContinueToEditorialReview       = "editorial-review"
	ContinueToOperatorRecovery      = "operator-recovery"
	ContinueToCompletion            = "completion"
)

var (
	// dex:indexed-attribute attribute-key:newsletter-request-status index-key:newsletter-request-status index-type:keyword value-type:string description:"Current request status"
	requestStatus = dex.DefineAttribute[string](
		"newsletter-request-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	// dex:indexed-attribute attribute-key:newsletter-request-topic index-key:newsletter-request-topic index-type:fulltext value-type:string description:"Requested blog topic"
	requestTopic = dex.DefineAttribute[string](
		"newsletter-request-topic",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexFullText}),
	)
	// dex:indexed-attribute attribute-key:newsletter-requester index-key:newsletter-requester index-type:keyword value-type:string description:"Slack user who requested the post"
	requesterUserID = dex.DefineAttribute[string](
		"newsletter-requester",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	newsletterRequest       = dex.DefineAttribute[model.NewsletterRequest]("newsletter-request")
	requestInterpretation   = dex.DefineAttribute[model.RequestInterpretation]("request-interpretation")
	changeWindow            = dex.DefineAttribute[model.ChangeWindow]("change-window")
	changeWindowDescription = dex.DefineAttribute[string]("change-window-description")
	repositoryDigests       = dex.DefineAttribute[[]model.RepositoryChangeDigest]("repository-digests")
	researchBrief           = dex.DefineAttribute[model.ResearchBrief]("research-brief")
	blogPost                = dex.DefineAttribute[model.BlogPost]("blog-post")
	blogPostTitle           = dex.DefineAttribute[string]("blog-post-title")
	blogHTML                = dex.DefineAttribute[string]("blog-html")
	blogArtifactPath        = dex.DefineAttribute[string]("blog-artifact-path")
	publishedBlogURL        = dex.DefineAttribute[string]("published-blog-url")
	newsletterDraft         = dex.DefineAttribute[model.NewsletterDraft]("newsletter-draft")
	renderedNewsletter      = dex.DefineAttribute[model.RenderedNewsletter]("rendered-newsletter")
	newsletterSubject       = dex.DefineAttribute[string]("newsletter-subject")
	deliveryNewsletter      = dex.DefineAttribute[model.RenderedNewsletter]("delivery-newsletter")
	blogRevisionCount       = dex.DefineAttribute[int64]("blog-revision-count")
	editorFeedback          = dex.DefineAttribute[string]("editor-feedback")
	reviewGateKey           = dex.DefineAttribute[string]("review-gate-key")
	reviewReminderCount     = dex.DefineAttribute[int64]("review-reminder-count")
	subscriberList          = dex.DefineAttribute[model.SubscriberList]("subscriber-list")
	subscriberCount         = dex.DefineAttribute[int64]("subscriber-count")
	deliveryCursor          = dex.DefineAttribute[int64]("delivery-cursor")
	deliverySummary         = dex.DefineAttribute[model.DeliverySummary]("delivery-summary")
	// deliveryExceptions keeps one instance per unconfirmed or rejected
	// recipient, keyed by its zero-padded delivery position, so recording one
	// never rewrites the others. It is the complete record.
	deliveryExceptions = dex.DefineAttributeMap[model.DeliveryException]("delivery-exceptions")
	// deliveryExceptionsShown copies the first maximumShownDeliveryExceptions
	// exceptions for GetDexDisplay. Dex Web v0.14.0 invokes Display RPCs
	// without the RPC's registered AttributeMap loads, so a Display RPC that
	// reads an AttributeMap fails there and hides the run's Actions.
	deliveryExceptionsShown   = dex.DefineAttribute[[]model.DeliveryException]("delivery-exceptions-shown")
	failedStage               = dex.DefineAttribute[string]("failed-stage")
	attentionReason           = dex.DefineAttribute[string]("attention-reason")
	attentionGateKey          = dex.DefineAttribute[string]("attention-gate-key")
	attentionCount            = dex.DefineAttribute[int64]("attention-count")
	closingReason             = dex.DefineAttribute[string]("closing-reason")
	approvedNewsletterSubject = dex.DefineAttribute[string]("approved-newsletter-subject")
	newsletterTextPreview     = dex.DefineAttribute[string]("newsletter-text-preview")
	newsletterArtifactPath    = dex.DefineAttribute[string]("newsletter-artifact-path")
	editorialDecisions        = dex.DefineChannelMap[EditorialDecision]("editorial-decisions")
	operatorRecoveryDecision  = dex.DefineChannelMap[RecoveryDecision]("operator-recovery-decisions")
)

// SlackThreadReply is the Step input of every Slack thread-reply Connector
// Step.
type SlackThreadReply struct {
	Thread       model.SlackThreadReference `json:"thread"`
	Text         string                     `json:"text"`
	Continuation string                     `json:"continuation"`
}

// EditorialDecisionKind is the editor's choice for one review gate.
type EditorialDecisionKind string

// Editorial decisions.
const (
	EditorialDecisionApprove EditorialDecisionKind = "approve"
	EditorialDecisionRevise  EditorialDecisionKind = "revise"
	EditorialDecisionDiscard EditorialDecisionKind = "discard"
)

// EditorialDecision is published by an editorial Action for one review gate.
type EditorialDecision struct {
	Kind     EditorialDecisionKind `json:"kind"`
	Feedback string                `json:"feedback,omitempty"`
	Reason   string                `json:"reason,omitempty"`
}

// EditorialReviewWait is the input of WaitForEditorialDecision.
type EditorialReviewWait struct {
	GateKey        string `json:"gateKey"`
	ReminderNumber int    `json:"reminderNumber"`
}

// RecoveryDecisionKind is the operator's choice for one attention gate.
type RecoveryDecisionKind string

// Operator recovery decisions.
const (
	RecoveryDecisionRetry   RecoveryDecisionKind = "retry"
	RecoveryDecisionAbandon RecoveryDecisionKind = "abandon"
)

// RecoveryDecision is published by a recovery Action for one attention gate.
type RecoveryDecision struct {
	Kind   RecoveryDecisionKind `json:"kind"`
	Reason string               `json:"reason,omitempty"`
}

// OperatorRecoveryWait is the input of WaitForOperatorRecovery.
type OperatorRecoveryWait struct {
	GateKey string `json:"gateKey"`
	Stage   string `json:"stage"`
}

// StageEntry is the input of application Steps that read all state from
// Attributes.
type StageEntry struct {
	Stage string `json:"stage"`
}

// NewsletterDelivery is the input of the per-subscriber Gmail Connector Step.
type NewsletterDelivery struct {
	RecipientIndex int    `json:"recipientIndex"`
	Recipient      string `json:"recipient"`
	Subject        string `json:"subject"`
	HTMLBody       string `json:"htmlBody"`
	TextBody       string `json:"textBody"`
}

// NewsletterResult is the typed completion output of TechBlogNewsletterFlow.
type NewsletterResult struct {
	Status           string                `json:"status"`
	Topic            string                `json:"topic"`
	BlogTitle        string                `json:"blogTitle"`
	BlogArtifactPath string                `json:"blogArtifactPath"`
	PublishedBlogURL string                `json:"publishedBlogUrl"`
	RevisionCount    int                   `json:"revisionCount"`
	ClosingReason    string                `json:"closingReason"`
	Delivery         model.DeliverySummary `json:"delivery"`
}

// ApproveNewsletterInput confirms the review gate the editor is approving.
type ApproveNewsletterInput struct {
	GateKey string `json:"gateKey"`
	Subject string `json:"subject"`
}

// RequestBlogRevisionInput carries the editor's revision feedback.
type RequestBlogRevisionInput struct {
	Feedback string `json:"feedback"`
	GateKey  string `json:"gateKey"`
}

// DiscardNewsletterDraftInput carries the editor's optional discard reason.
type DiscardNewsletterDraftInput struct {
	Reason  *string `json:"reason,omitempty"`
	GateKey string  `json:"gateKey"`
}

// RetryFailedStageInput confirms the attention gate the operator is retrying.
type RetryFailedStageInput struct {
	GateKey string `json:"gateKey"`
}

// AbandonNewsletterRequestInput carries the operator's optional reason.
type AbandonNewsletterRequestInput struct {
	Reason  *string `json:"reason,omitempty"`
	GateKey string  `json:"gateKey"`
}

// SubscriberListReader reads the newsletter subscriber list from the Flow that
// owns it.
type SubscriberListReader interface {
	ListNewsletterSubscribers(ctx context.Context) ([]string, error)
}

// UnsubscribeLinks builds the unsubscribe link each newsletter email carries.
type UnsubscribeLinks interface {
	// URL returns the unsubscribe link for one canonical address.
	URL(canonicalAddress string) string
	// ExampleURL returns a link of the real shape that unsubscribes nobody,
	// for the previews editors review.
	ExampleURL() string
}

// TechBlogNewsletterFlowDependencies are the constructor-injected
// collaborators of TechBlogNewsletterFlow.
type TechBlogNewsletterFlowDependencies struct {
	Configuration    config.ProcessConfiguration
	SlackConnection  slack.Connection
	GmailConnection  gmail.Connection
	SubscriberList   SubscriberListReader
	UnsubscribeLinks UnsubscribeLinks
	// SubscriberListReadRetry overrides how long a failed subscriber list read
	// is retried before the request holds for attention; nil retries for
	// about ten minutes.
	SubscriberListReadRetry *dex.RetryPolicy
	BlogArtifactStore       BlogArtifactStore
	LanguageModelGeneration *LanguageModelGenerationFlow
	RepositoryResearch      *RepositoryChangeResearchFlow
}

// TechBlogNewsletterFlow turns one Slack request into a researched blog post,
// an HTML artifact, and an editor-approved newsletter delivered by Gmail.
type TechBlogNewsletterFlow struct {
	dex.FlowDefaults
	dependencies TechBlogNewsletterFlowDependencies
	stages       *newsletterStages
}

// NewTechBlogNewsletterFlow validates and wires the Flow.
func NewTechBlogNewsletterFlow(dependencies TechBlogNewsletterFlowDependencies) *TechBlogNewsletterFlow {
	if dependencies.BlogArtifactStore == nil || dependencies.LanguageModelGeneration == nil || dependencies.RepositoryResearch == nil ||
		dependencies.SubscriberList == nil || dependencies.UnsubscribeLinks == nil {
		panic("tech blog newsletter Flow requires an artifact store, a subscriber list reader, unsubscribe links, and both child Flows")
	}
	return &TechBlogNewsletterFlow{
		dependencies: dependencies,
		stages:       &newsletterStages{configuration: dependencies.Configuration, unsubscribeLinks: dependencies.UnsubscribeLinks},
	}
}

// GetFlowType returns the stable Flow type.
func (*TechBlogNewsletterFlow) GetFlowType() string { return TechBlogNewsletterFlowType }

// GetSteps registers every reachable Step.
func (flow *TechBlogNewsletterFlow) GetSteps() []dex.StepDef {
	stages := flow.stages
	dependencies := flow.dependencies
	return []dex.StepDef{
		dex.DefineStartStep(ReceiveNewsletterRequest{}),
		dex.DefineStep(slack.NewPostThreadReplyStep(slack.PostThreadReplyStepConfig[SlackThreadReply]{
			StepType:       postRequestAcknowledgementStepType,
			ConnectionName: SlackConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "intake", GroupLabel: "Intake", Explanation: "Acknowledge the request in its Slack thread.",
			},
			Connection:          dependencies.SlackConnection,
			MapToOperationInput: mapSlackThreadReplyToOperationInput,
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(ContinueAfterSlackReplyFailure{stages: stages}, nil),
			},
			Sent:             sdkgo.GoTo(BeginRequestInterpretation{stages: stages}),
			ProviderRejected: sdkgo.GoTo(BeginRequestInterpretation{stages: stages}),
			Uncertain:        sdkgo.GoTo(BeginRequestInterpretation{stages: stages}),
			Defect:           sdkgo.GoTo(BeginRequestInterpretation{stages: stages}),
		})),
		dex.DefineStep(BeginRequestInterpretation{stages: stages}),
		dex.DefineStep(InterpretNewsletterRequest{stages: stages, languageModel: dependencies.LanguageModelGeneration}),
		dex.DefineStep(slack.NewPostThreadReplyStep(slack.PostThreadReplyStepConfig[SlackThreadReply]{
			StepType:       postResearchPlanStepType,
			ConnectionName: SlackConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "research", GroupLabel: "Research",
				Explanation: "Post the interpreted topic, time range, and repositories in the Slack thread.",
			},
			Connection:          dependencies.SlackConnection,
			MapToOperationInput: mapSlackThreadReplyToOperationInput,
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(ContinueAfterSlackReplyFailure{stages: stages}, nil),
			},
			Sent:             sdkgo.GoTo(BeginRepositoryResearch{stages: stages}),
			ProviderRejected: sdkgo.GoTo(BeginRepositoryResearch{stages: stages}),
			Uncertain:        sdkgo.GoTo(BeginRepositoryResearch{stages: stages}),
			Defect:           sdkgo.GoTo(BeginRepositoryResearch{stages: stages}),
		})),
		dex.DefineStep(BeginRepositoryResearch{stages: stages}),
		dex.DefineStep(ResearchSelectedRepositories{stages: stages, repositoryResearch: dependencies.RepositoryResearch}),
		dex.DefineStep(SynthesizeResearchBrief{stages: stages, languageModel: dependencies.LanguageModelGeneration}),
		dex.DefineStep(DraftBlogPost{stages: stages, languageModel: dependencies.LanguageModelGeneration}),
		dex.DefineStep(PublishBlogArtifact{stages: stages, artifactStore: dependencies.BlogArtifactStore}),
		dex.DefineStep(DraftNewsletter{stages: stages, languageModel: dependencies.LanguageModelGeneration, artifactStore: dependencies.BlogArtifactStore}),
		dex.DefineStep(slack.NewPostThreadReplyStep(slack.PostThreadReplyStepConfig[SlackThreadReply]{
			StepType:       postReviewRequestStepType,
			ConnectionName: SlackConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "review", GroupLabel: "Review",
				Explanation: "Tell the Slack thread that a draft is ready or still awaiting editor review.",
			},
			Connection:          dependencies.SlackConnection,
			MapToOperationInput: mapSlackThreadReplyToOperationInput,
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(ContinueAfterSlackReplyFailure{stages: stages}, nil),
			},
			Sent:             sdkgo.GoTo(AwaitEditorialDecision{}),
			ProviderRejected: sdkgo.GoTo(AwaitEditorialDecision{}),
			Uncertain:        sdkgo.GoTo(AwaitEditorialDecision{}),
			Defect:           sdkgo.GoTo(AwaitEditorialDecision{}),
		})),
		dex.DefineStep(AwaitEditorialDecision{}),
		dex.DefineStep(WaitForEditorialDecision{stages: stages}),
		dex.DefineStep(LoadNewsletterSubscribers{stages: stages, subscriberList: dependencies.SubscriberList, retry: dependencies.SubscriberListReadRetry}),
		dex.DefineStep(gmail.NewSendMessageStep(gmail.SendMessageStepConfig[NewsletterDelivery]{
			StepType:       sendNewsletterToSubscriberStepType,
			ConnectionName: GmailConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "delivery", GroupLabel: "Delivery", Explanation: "Send the newsletter to one subscriber with Gmail.",
			},
			Connection:          dependencies.GmailConnection,
			MapToOperationInput: mapNewsletterDeliveryToOperationInput,
			// Gmail has no server-side idempotency, so a send must not replay.
			// The connector's ASYNC default lets a send slower than the local
			// phase run Execute again; SYNC persists before the handler
			// returns, so only a Worker lost mid-call can repeat a send. The
			// FDG analyzer requires this override to stay a static literal.
			StepOptionsOverride: &dex.StepOptions{
				ExecuteDurability: dex.StepDurabilitySync,
				ExecuteFailure:    dex.ProceedToOnExecuteFailure(HoldNewsletterDeliveryAfterRetries{stages: stages}, nil),
			},
			Sent:             sdkgo.GoTo(RecordNewsletterDelivery{stages: stages}),
			ProviderRejected: sdkgo.GoTo(RecordNewsletterDelivery{stages: stages}),
			Uncertain:        sdkgo.GoTo(RecordNewsletterDelivery{stages: stages}),
			Defect:           sdkgo.GoTo(RecordNewsletterDelivery{stages: stages}),
		})),
		dex.DefineStep(RecordNewsletterDelivery{stages: stages}),
		dex.DefineStep(slack.NewPostThreadReplyStep(slack.PostThreadReplyStepConfig[SlackThreadReply]{
			StepType:       postAttentionNoticeStepType,
			ConnectionName: SlackConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "recovery", GroupLabel: "Recovery", Explanation: "Tell the Slack thread that a stage needs operator attention.",
			},
			Connection:          dependencies.SlackConnection,
			MapToOperationInput: mapSlackThreadReplyToOperationInput,
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(ContinueAfterSlackReplyFailure{stages: stages}, nil),
			},
			Sent:             sdkgo.GoTo(AwaitOperatorRecovery{}),
			ProviderRejected: sdkgo.GoTo(AwaitOperatorRecovery{}),
			Uncertain:        sdkgo.GoTo(AwaitOperatorRecovery{}),
			Defect:           sdkgo.GoTo(AwaitOperatorRecovery{}),
		})),
		dex.DefineStep(AwaitOperatorRecovery{}),
		dex.DefineStep(WaitForOperatorRecovery{stages: stages}),
		dex.DefineStep(slack.NewPostThreadReplyStep(slack.PostThreadReplyStepConfig[SlackThreadReply]{
			StepType:       postFinalReplyStepType,
			ConnectionName: SlackConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "close", GroupLabel: "Close", Explanation: "Post the final outcome in the Slack thread.",
			},
			Connection:          dependencies.SlackConnection,
			MapToOperationInput: mapSlackThreadReplyToOperationInput,
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(ContinueAfterSlackReplyFailure{stages: stages}, nil),
			},
			Sent:             sdkgo.GoTo(CompleteNewsletterRequest{}),
			ProviderRejected: sdkgo.GoTo(CompleteNewsletterRequest{}),
			Uncertain:        sdkgo.GoTo(CompleteNewsletterRequest{}),
			Defect:           sdkgo.GoTo(CompleteNewsletterRequest{}),
		})),
		dex.DefineStep(CompleteNewsletterRequest{}),
		dex.DefineStep(ContinueAfterSlackReplyFailure{stages: stages}),
		dex.DefineStep(HoldSubscriberListAfterRetries{stages: stages}),
		dex.DefineStep(HoldNewsletterDeliveryAfterRetries{stages: stages}),
	}
}

// GetRPCs registers the Dex Web read models and human Actions.
func (flow *TechBlogNewsletterFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.ApproveNewsletterForDelivery, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Approve and send newsletter",
				dex.WhenAttributeMatches(requestStatus, dex.AttributeMatchEqual(StatusAwaitingEditorReview)),
				dex.ActionRequiresPermission(PermissionManageNewsletter),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(requestStatus), dex.LockAttribute(reviewGateKey), dex.LockAttribute(newsletterSubject)},
		}),
		dex.DefineRPC(flow.RequestBlogRevision, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Request revision",
				dex.WhenAttributeMatches(requestStatus, dex.AttributeMatchEqual(StatusAwaitingEditorReview)),
				dex.ActionRequiresPermission(PermissionManageNewsletter),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(requestStatus), dex.LockAttribute(reviewGateKey), dex.LockAttribute(blogRevisionCount)},
		}),
		dex.DefineRPC(flow.DiscardNewsletterDraft, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Discard draft",
				dex.WhenAttributeMatches(requestStatus, dex.AttributeMatchEqual(StatusAwaitingEditorReview)),
				dex.ActionRequiresPermission(PermissionManageNewsletter),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(requestStatus), dex.LockAttribute(reviewGateKey)},
		}),
		dex.DefineRPC(flow.RetryFailedStage, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Retry failed stage",
				dex.WhenAttributeMatches(requestStatus, dex.AttributeMatchEqual(StatusNeedsAttention)),
				dex.ActionRequiresPermission(PermissionManageNewsletter),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(requestStatus), dex.LockAttribute(attentionGateKey)},
		}),
		dex.DefineRPC(flow.AbandonNewsletterRequest, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Abandon request",
				dex.WhenAttributeMatches(requestStatus, dex.AttributeMatchEqual(StatusNeedsAttention)),
				dex.ActionRequiresPermission(PermissionManageNewsletter),
			),
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(requestStatus), dex.LockAttribute(attentionGateKey)},
		}),
	}
}

// GetPersistenceSchema registers every Attribute and Channel.
func (*TechBlogNewsletterFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			requestStatus, requestTopic, requesterUserID, newsletterRequest, requestInterpretation, changeWindow,
			changeWindowDescription, repositoryDigests, researchBrief, blogPost, blogPostTitle, blogHTML,
			blogArtifactPath, publishedBlogURL, newsletterDraft, renderedNewsletter, newsletterSubject,
			deliveryNewsletter, blogRevisionCount, editorFeedback, reviewGateKey, reviewReminderCount,
			subscriberList, subscriberCount, deliveryCursor, deliverySummary, deliveryExceptions, deliveryExceptionsShown, failedStage,
			attentionReason, attentionGateKey, attentionCount, closingReason, approvedNewsletterSubject,
			newsletterTextPreview, newsletterArtifactPath,
		},
		Channels: []dex.ChannelDef{editorialDecisions, operatorRecoveryDecision},
	}
}

// GetConnectorTriggerBindings declares the Slack request Trigger binding.
func (*TechBlogNewsletterFlow) GetConnectorTriggerBindings() []sdkgo.TriggerBindingDefinition {
	return []sdkgo.TriggerBindingDefinition{
		slack.DefineChannelThreadCreatedTriggerBinding(slack.ChannelThreadCreatedTriggerBindingConfig{
			ConnectionName: SlackConnectionName, BindingName: NewsletterRequestTriggerBinding,
			ConfigurationUI: sdkgo.ConnectorConfigurationUI{Units: []sdkgo.ConnectorUIUnit{
				{ID: "channel", UnitID: slack.UIUnitChannelPicker, Label: "Blog request channel", Required: true, Bindings: []sdkgo.ConnectorUIBinding{{Port: slack.UIChannelPickerPortChannelID, JSONPointer: "/channelId"}}},
				{ID: "message", UnitID: slack.UIUnitTextInput, Label: "Request message contains (optional)", Bindings: []sdkgo.ConnectorUIBinding{{Port: slack.UITextInputPortText, JSONPointer: "/threadTriggerMatcher/messageContains"}}},
				{ID: "members", UnitID: slack.UIUnitMemberPicker, Label: "Members allowed to request posts (optional)", Bindings: []sdkgo.ConnectorUIBinding{{Port: slack.UIMemberPickerPortMemberIDs, JSONPointer: "/threadTriggerMatcher/posterUserIds"}}},
			}},
		}),
	}
}

// dex:field attribute-key:blog-post-title value-type:string editable:false description:"Blog post"
// dex:field attribute-key:change-window-description value-type:string editable:false description:"Change window"
// dex:field attribute-key:blog-revision-count value-type:int64 editable:false description:"Revisions"
func (*TechBlogNewsletterFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	title, err := optionalValue(blogPostTitle.Get(ctx))
	if err != nil {
		return nil, err
	}
	window, err := optionalValue(changeWindowDescription.Get(ctx))
	if err != nil {
		return nil, err
	}
	revisions, err := optionalValue(blogRevisionCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"blog-post-title":           title,
		"change-window-description": window,
		"blog-revision-count":       revisions,
	}}, nil
}

// dex:field attribute-key:blog-post-title value-type:string editable:false description:"Blog post" ui-slot:title
// dex:field attribute-key:newsletter-request-topic value-type:string editable:false description:"Requested topic" ui-slot:subtitle
// dex:field attribute-key:newsletter-request-status value-type:string editable:false description:"Status" ui-slot:status
// dex:field attribute-key:attention-reason value-type:string editable:false description:"Needs attention because" ui-slot:reason
// dex:field attribute-key:change-window-description value-type:string editable:false description:"Change window"
// dex:field attribute-key:newsletter-requester value-type:string editable:false description:"Requested by (Slack user)"
// dex:field attribute-key:blog-artifact-path value-type:string editable:false description:"Blog HTML artifact"
// dex:field attribute-key:published-blog-url value-type:string editable:false description:"Published blog URL"
// dex:field attribute-key:newsletter-subject value-type:string editable:true description:"Newsletter subject"
// dex:field attribute-key:blog-revision-count value-type:int64 editable:false description:"Revisions"
// dex:field attribute-key:editor-feedback value-type:string editable:false description:"Latest editor feedback"
// dex:field attribute-key:review-gate-key value-type:string editable:false description:"Review gate"
// dex:field attribute-key:attention-gate-key value-type:string editable:false description:"Attention gate"
// dex:field attribute-key:failed-stage value-type:string editable:false description:"Failed stage"
// dex:field attribute-key:subscriber-count value-type:int64 editable:false description:"Subscribers"
// dex:field attribute-key:delivery-summary value-type:json editable:false description:"Delivery outcomes"
// dex:field attribute-key:delivery-exceptions-shown value-type:array editable:false description:"Recipients to check before resending (first 20)"
// dex:field attribute-key:research-brief value-type:json editable:false description:"Research brief"
// dex:field attribute-key:repository-digests value-type:array editable:false description:"Repository research"
// dex:field attribute-key:closing-reason value-type:string editable:false description:"Closing reason"
// dex:field attribute-key:approved-newsletter-subject value-type:string editable:false description:"Approved subject"
// dex:field attribute-key:newsletter-artifact-path value-type:string editable:false description:"Newsletter HTML preview file"
// dex:field attribute-key:newsletter-text-preview value-type:string editable:false description:"Newsletter as it will be sent (text)"
func (*TechBlogNewsletterFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	title, titleErr := optionalValue(blogPostTitle.Get(ctx))
	topic, topicErr := optionalValue(requestTopic.Get(ctx))
	status, statusErr := optionalValue(requestStatus.Get(ctx))
	reason, reasonErr := optionalValue(attentionReason.Get(ctx))
	window, windowErr := optionalValue(changeWindowDescription.Get(ctx))
	requester, requesterErr := optionalValue(requesterUserID.Get(ctx))
	artifact, artifactErr := optionalValue(blogArtifactPath.Get(ctx))
	publishedURL, publishedErr := optionalValue(publishedBlogURL.Get(ctx))
	subject, subjectErr := optionalValue(newsletterSubject.Get(ctx))
	revisions, revisionsErr := optionalValue(blogRevisionCount.Get(ctx))
	feedback, feedbackErr := optionalValue(editorFeedback.Get(ctx))
	reviewGate, reviewGateErr := optionalValue(reviewGateKey.Get(ctx))
	attentionGate, attentionGateErr := optionalValue(attentionGateKey.Get(ctx))
	stage, stageErr := optionalValue(failedStage.Get(ctx))
	subscribersFound, subscribersErr := optionalValue(subscriberCount.Get(ctx))
	summary, summaryErr := optionalValue(deliverySummary.Get(ctx))
	exceptions, exceptionsErr := optionalValue(deliveryExceptionsShown.Get(ctx))
	brief, briefErr := optionalValue(researchBrief.Get(ctx))
	digests, digestsErr := optionalValue(repositoryDigests.Get(ctx))
	closing, closingErr := optionalValue(closingReason.Get(ctx))
	approvedSubject, approvedSubjectErr := optionalValue(approvedNewsletterSubject.Get(ctx))
	newsletterPath, newsletterPathErr := optionalValue(newsletterArtifactPath.Get(ctx))
	newsletterPreview, newsletterPreviewErr := optionalValue(newsletterTextPreview.Get(ctx))
	if err := errors.Join(approvedSubjectErr, newsletterPathErr, newsletterPreviewErr, titleErr, topicErr, statusErr, reasonErr, windowErr, requesterErr, artifactErr, publishedErr,
		subjectErr, revisionsErr, feedbackErr, reviewGateErr, attentionGateErr, stageErr, subscribersErr, summaryErr,
		exceptionsErr, briefErr, digestsErr, closingErr); err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"blog-post-title":             title,
		"newsletter-request-topic":    topic,
		"newsletter-request-status":   status,
		"attention-reason":            reason,
		"change-window-description":   window,
		"newsletter-requester":        requester,
		"blog-artifact-path":          artifact,
		"published-blog-url":          publishedURL,
		"newsletter-subject":          subject,
		"blog-revision-count":         revisions,
		"editor-feedback":             feedback,
		"review-gate-key":             reviewGate,
		"attention-gate-key":          attentionGate,
		"failed-stage":                stage,
		"subscriber-count":            subscribersFound,
		"delivery-summary":            summary,
		"delivery-exceptions-shown":   exceptions,
		"research-brief":              brief,
		"repository-digests":          digests,
		"closing-reason":              closing,
		"approved-newsletter-subject": approvedSubject,
		"newsletter-artifact-path":    newsletterPath,
		"newsletter-text-preview":     newsletterPreview,
	}}, nil
}

// dex:input field-name:gateKey value-type:string source:attribute attribute-key:review-gate-key required:true description:"Review gate"
// dex:input field-name:subject value-type:string source:attribute attribute-key:newsletter-subject required:true description:"Newsletter subject being approved"
func (*TechBlogNewsletterFlow) ApproveNewsletterForDelivery(ctx dex.Context, input ApproveNewsletterInput) (*dex.RPCResult[dex.None], error) {
	status, err := requestStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	currentGate, err := reviewGateKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateOpenGate(status, StatusAwaitingEditorReview, currentGate, input.GateKey); err != nil {
		return nil, err
	}
	currentSubject, err := optionalValue(newsletterSubject.Get(ctx))
	if err != nil {
		return nil, err
	}
	approvedSubject := sanitizeSubject(currentSubject)
	if approvedSubject == "" || sanitizeSubject(input.Subject) != approvedSubject {
		return nil, fmt.Errorf("the newsletter subject changed since this page loaded; refresh and review it before approving")
	}
	if err := approvedNewsletterSubject.Set(ctx, approvedSubject); err != nil {
		return nil, err
	}
	if err := requestStatus.Set(ctx, StatusDeliveryApproved); err != nil {
		return nil, err
	}
	if err := editorialDecisions.Publish(ctx, currentGate, EditorialDecision{Kind: EditorialDecisionApprove}); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:input field-name:feedback value-type:string source:user required:true description:"What should change in the next draft"
// dex:input field-name:gateKey value-type:string source:attribute attribute-key:review-gate-key required:true description:"Review gate"
func (flow *TechBlogNewsletterFlow) RequestBlogRevision(ctx dex.Context, input RequestBlogRevisionInput) (*dex.RPCResult[dex.None], error) {
	status, err := requestStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	currentGate, err := reviewGateKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateOpenGate(status, StatusAwaitingEditorReview, currentGate, input.GateKey); err != nil {
		return nil, err
	}
	feedback, _ := truncateRunes(strings.TrimSpace(input.Feedback), maximumFeedbackRunes)
	if feedback == "" {
		return nil, fmt.Errorf("revision feedback is required")
	}
	revisions, err := optionalValue(blogRevisionCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	maximumRevisions := flow.dependencies.Configuration.Review.MaxRevisions
	if int(revisions) >= maximumRevisions {
		return nil, fmt.Errorf("the revision limit of %d has been reached; approve or discard this draft", maximumRevisions)
	}
	if err := requestStatus.Set(ctx, StatusRevisionRequested); err != nil {
		return nil, err
	}
	if err := editorialDecisions.Publish(ctx, currentGate, EditorialDecision{Kind: EditorialDecisionRevise, Feedback: feedback}); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:input field-name:reason value-type:string source:user required:false description:"Why the draft is discarded"
// dex:input field-name:gateKey value-type:string source:attribute attribute-key:review-gate-key required:true description:"Review gate"
func (*TechBlogNewsletterFlow) DiscardNewsletterDraft(ctx dex.Context, input DiscardNewsletterDraftInput) (*dex.RPCResult[dex.None], error) {
	status, err := requestStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	currentGate, err := reviewGateKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateOpenGate(status, StatusAwaitingEditorReview, currentGate, input.GateKey); err != nil {
		return nil, err
	}
	if err := requestStatus.Set(ctx, StatusDiscardRequested); err != nil {
		return nil, err
	}
	if err := editorialDecisions.Publish(ctx, currentGate, EditorialDecision{Kind: EditorialDecisionDiscard, Reason: boundedOptionalText(input.Reason)}); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:input field-name:gateKey value-type:string source:attribute attribute-key:attention-gate-key required:true description:"Attention gate"
func (*TechBlogNewsletterFlow) RetryFailedStage(ctx dex.Context, input RetryFailedStageInput) (*dex.RPCResult[dex.None], error) {
	status, err := requestStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	currentGate, err := attentionGateKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateOpenGate(status, StatusNeedsAttention, currentGate, input.GateKey); err != nil {
		return nil, err
	}
	if err := requestStatus.Set(ctx, StatusRetryRequested); err != nil {
		return nil, err
	}
	if err := operatorRecoveryDecision.Publish(ctx, currentGate, RecoveryDecision{Kind: RecoveryDecisionRetry}); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:input field-name:reason value-type:string source:user required:false description:"Why the request is abandoned"
// dex:input field-name:gateKey value-type:string source:attribute attribute-key:attention-gate-key required:true description:"Attention gate"
func (*TechBlogNewsletterFlow) AbandonNewsletterRequest(ctx dex.Context, input AbandonNewsletterRequestInput) (*dex.RPCResult[dex.None], error) {
	status, err := requestStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	currentGate, err := attentionGateKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateOpenGate(status, StatusNeedsAttention, currentGate, input.GateKey); err != nil {
		return nil, err
	}
	if err := requestStatus.Set(ctx, StatusAbandonRequested); err != nil {
		return nil, err
	}
	if err := operatorRecoveryDecision.Publish(ctx, currentGate, RecoveryDecision{Kind: RecoveryDecisionAbandon, Reason: boundedOptionalText(input.Reason)}); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Store the Slack request and acknowledge it in its thread."
type ReceiveNewsletterRequest struct {
	dex.StepDefaults
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (ReceiveNewsletterRequest) GetStepType() string { return "ReceiveNewsletterRequest" }

// WaitFor returns at once. The start Step keeps a WaitFor because Dex Web
// Start Flow (Dex CLI v0.13.8) invokes WaitFor on the start Step, which an
// execute-only Step rejects.
func (ReceiveNewsletterRequest) WaitFor(dex.Context, model.NewsletterRequest) (*dex.Wait, error) {
	return dex.SkipWaitImmediately(), nil
}

// Execute initializes durable request state.
func (ReceiveNewsletterRequest) Execute(ctx dex.Context, request model.NewsletterRequest) (*dex.StepDecision, error) {
	if strings.TrimSpace(request.ChannelID) == "" || strings.TrimSpace(request.ThreadTimestamp) == "" {
		return nil, fmt.Errorf("the newsletter request is missing its Slack thread")
	}
	if err := newsletterRequest.Set(ctx, request); err != nil {
		return nil, err
	}
	if err := requesterUserID.Set(ctx, request.RequesterUserID); err != nil {
		return nil, err
	}
	if err := blogRevisionCount.Set(ctx, 0); err != nil {
		return nil, err
	}
	if err := attentionCount.Set(ctx, 0); err != nil {
		return nil, err
	}
	if err := requestStatus.Set(ctx, StatusReceived); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postRequestAcknowledgementStepType), SlackThreadReply{
		Thread: threadOf(request), Text: notices.RequestAcknowledged(), Continuation: ContinueToRequestInterpretation,
	}), nil
}

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Build the request-interpretation prompt after the acknowledgement."
type BeginRequestInterpretation struct {
	dex.StepDefaultsNoWaitFor[slack.PostThreadReplyResult]
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (BeginRequestInterpretation) GetStepType() string { return "BeginRequestInterpretation" }

// Execute enters the interpretation stage.
func (step BeginRequestInterpretation) Execute(ctx dex.Context, _ slack.PostThreadReplyResult) (*dex.StepDecision, error) {
	request, err := step.stages.prepareRequestInterpretation(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(InterpretNewsletterRequest{}, request), nil
}

// dex:group group-id:intake group-label:"Intake"
// dex:explanation text:"Wait for the language model to interpret the topic, instructions, time range, and repositories."
type InterpretNewsletterRequest struct {
	dex.StepDefaults
	stages        *newsletterStages
	languageModel *LanguageModelGenerationFlow
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (InterpretNewsletterRequest) GetStepType() string { return "InterpretNewsletterRequest" }

// WaitFor starts the generation SubFlow.
func (step InterpretNewsletterRequest) WaitFor(_ dex.Context, request model.GenerationRequest) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.languageModel, request)), nil
}

// Execute validates the interpretation and resolves the change window.
func (step InterpretNewsletterRequest) Execute(ctx dex.Context, _ model.GenerationRequest) (*dex.StepDecision, error) {
	request, err := newsletterRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	configuration := step.stages.configuration
	generation, err := decodeGenerationSubFlowResult(ctx, 0)
	var interpretation model.RequestInterpretation
	if err == nil {
		interpretation, err = prompts.ParseRequestInterpretation(generation, request, configuration)
	}
	if err != nil {
		reply, recordErr := step.stages.recordAttention(ctx, StageInterpretRequest, err.Error())
		if recordErr != nil {
			return nil, recordErr
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	if !interpretation.Understood {
		if err := requestInterpretation.Set(ctx, interpretation); err != nil {
			return nil, err
		}
		reply, err := step.stages.recordClosing(ctx, StatusNeedsClarification, "The request needs clarification.",
			notices.ClarificationNeeded(interpretation.ClarificationQuestion))
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
	}
	if interpretation.LookbackDays == 0 {
		if days, found := timewindow.ParseLookbackPhrase(request.RequestText); found {
			interpretation.LookbackDays = days
		}
	}
	window := timewindow.ResolveChangeWindow(
		requestTime(ctx, request), interpretation.LookbackDays,
		configuration.Research.DefaultLookbackDays, configuration.Research.MaxLookbackDays,
	)
	description := timewindow.DescribeChangeWindow(window)
	if err := requestInterpretation.Set(ctx, interpretation); err != nil {
		return nil, err
	}
	if err := changeWindow.Set(ctx, window); err != nil {
		return nil, err
	}
	if err := changeWindowDescription.Set(ctx, description); err != nil {
		return nil, err
	}
	if err := requestTopic.Set(ctx, interpretation.Topic); err != nil {
		return nil, err
	}
	repositories := make([]model.RepositoryReference, 0, len(interpretation.RepositorySelections))
	for _, selection := range interpretation.RepositorySelections {
		repositories = append(repositories, selection.Repository)
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postResearchPlanStepType), SlackThreadReply{
		Thread: threadOf(request), Text: notices.ResearchStarted(interpretation.Topic, description, repositories),
		Continuation: ContinueToRepositoryResearch,
	}), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Start repository research after the plan is posted."
type BeginRepositoryResearch struct {
	dex.StepDefaultsNoWaitFor[slack.PostThreadReplyResult]
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (BeginRepositoryResearch) GetStepType() string { return "BeginRepositoryResearch" }

// Execute enters the research stage.
func (step BeginRepositoryResearch) Execute(ctx dex.Context, _ slack.PostThreadReplyResult) (*dex.StepDecision, error) {
	requests, err := step.stages.prepareRepositoryResearch(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(ResearchSelectedRepositories{}, requests), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Research every selected repository in parallel SubFlows and collect their digests."
type ResearchSelectedRepositories struct {
	dex.StepDefaults
	stages             *newsletterStages
	repositoryResearch *RepositoryChangeResearchFlow
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (ResearchSelectedRepositories) GetStepType() string { return "ResearchSelectedRepositories" }

// WaitFor starts one bounded research SubFlow per selected repository.
func (step ResearchSelectedRepositories) WaitFor(_ dex.Context, requests []model.RepositoryResearchRequest) (*dex.Wait, error) {
	if len(requests) == 0 {
		return dex.SkipWaitImmediately(), nil
	}
	conditions := make([]dex.Condition, 0, len(requests))
	for _, request := range requests {
		conditions = append(conditions, dex.SubFlow(step.repositoryResearch, request))
	}
	return dex.AllOf(conditions...), nil
}

// Execute stores the digests; one failed repository does not fail the request.
func (step ResearchSelectedRepositories) Execute(ctx dex.Context, requests []model.RepositoryResearchRequest) (*dex.StepDecision, error) {
	digests := make([]model.RepositoryChangeDigest, 0, len(requests))
	usable := 0
	for index, request := range requests {
		digest := model.RepositoryChangeDigest{Repository: request.Selection.Repository, Status: model.RepositoryResearchFailed}
		flowResult, err := dex.SubFlowResult(ctx, index)
		if err != nil {
			return nil, err
		}
		if flowResult.Status == dex.FlowCompleted {
			if err := flowResult.DecodeSingleOutput(&digest); err != nil {
				return nil, fmt.Errorf("decode repository digest %d: %w", index, err)
			}
		} else {
			digest.Summary = fmt.Sprintf("Repository research ended with status %s.", flowResult.Status)
		}
		if digest.Status == model.RepositoryResearched || digest.Status == model.RepositoryResearchIncomplete {
			usable++
		}
		digests = append(digests, digest)
	}
	if err := repositoryDigests.Set(ctx, digests); err != nil {
		return nil, err
	}
	if usable == 0 {
		reply, err := step.stages.recordAttention(ctx, StageResearchRepos, "Research did not complete for any selected repository.")
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	request, err := step.stages.prepareResearchSynthesis(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(SynthesizeResearchBrief{}, request), nil
}

// dex:group group-id:research group-label:"Research"
// dex:explanation text:"Wait for the language model to synthesize the repository digests into a research brief."
type SynthesizeResearchBrief struct {
	dex.StepDefaults
	stages        *newsletterStages
	languageModel *LanguageModelGenerationFlow
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (SynthesizeResearchBrief) GetStepType() string { return "SynthesizeResearchBrief" }

// WaitFor starts the generation SubFlow.
func (step SynthesizeResearchBrief) WaitFor(_ dex.Context, request model.GenerationRequest) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.languageModel, request)), nil
}

// Execute stores the brief or closes when nothing notable changed.
func (step SynthesizeResearchBrief) Execute(ctx dex.Context, _ model.GenerationRequest) (*dex.StepDecision, error) {
	digests, err := repositoryDigests.Get(ctx)
	if err != nil {
		return nil, err
	}
	generation, err := decodeGenerationSubFlowResult(ctx, 0)
	var brief model.ResearchBrief
	if err == nil {
		brief, err = prompts.ParseResearchBrief(generation, digests)
	}
	if err != nil {
		reply, recordErr := step.stages.recordAttention(ctx, StageSynthesizeResearch, err.Error())
		if recordErr != nil {
			return nil, recordErr
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	if err := researchBrief.Set(ctx, brief); err != nil {
		return nil, err
	}
	if !brief.HasNotableChanges {
		topic, err := requestTopic.Get(ctx)
		if err != nil {
			return nil, err
		}
		description, err := changeWindowDescription.Get(ctx)
		if err != nil {
			return nil, err
		}
		reply, err := step.stages.recordClosing(ctx, StatusNoNotableChanges, "No notable changes were found in the change window.",
			appendCoverageNote(notices.NoNotableChanges(topic, description), digests))
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
	}
	request, err := step.stages.prepareBlogDraft(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(DraftBlogPost{}, request), nil
}

// dex:group group-id:writing group-label:"Writing"
// dex:explanation text:"Wait for the language model to draft or revise the blog post."
type DraftBlogPost struct {
	dex.StepDefaults
	stages        *newsletterStages
	languageModel *LanguageModelGenerationFlow
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (DraftBlogPost) GetStepType() string { return "DraftBlogPost" }

// WaitFor starts the generation SubFlow.
func (step DraftBlogPost) WaitFor(_ dex.Context, request model.GenerationRequest) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.languageModel, request)), nil
}

// Execute validates and stores the normalized blog post.
func (step DraftBlogPost) Execute(ctx dex.Context, _ model.GenerationRequest) (*dex.StepDecision, error) {
	brief, err := researchBrief.Get(ctx)
	if err != nil {
		return nil, err
	}
	generation, err := decodeGenerationSubFlowResult(ctx, 0)
	var post model.BlogPost
	if err == nil {
		post, err = prompts.ParseBlogPost(generation, brief)
	}
	if err == nil {
		post, err = render.NormalizeBlogPost(post)
	}
	if err != nil {
		reply, recordErr := step.stages.recordAttention(ctx, StageDraftBlogPost, err.Error())
		if recordErr != nil {
			return nil, recordErr
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	if err := blogPost.Set(ctx, post); err != nil {
		return nil, err
	}
	if err := blogPostTitle.Set(ctx, post.Title); err != nil {
		return nil, err
	}
	if err := requestStatus.Set(ctx, StatusPublishingArtifact); err != nil {
		return nil, err
	}
	return dex.GoTo(PublishBlogArtifact{}, StageEntry{Stage: StagePublishBlogArtifact}), nil
}

// dex:group group-id:writing group-label:"Writing"
// dex:explanation text:"Render the self-contained blog HTML and write it as a publishable artifact."
type PublishBlogArtifact struct {
	dex.StepDefaultsNoWaitFor[StageEntry]
	stages        *newsletterStages
	artifactStore BlogArtifactStore
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (PublishBlogArtifact) GetStepType() string { return "PublishBlogArtifact" }

// Execute renders and writes the artifact idempotently.
func (step PublishBlogArtifact) Execute(ctx dex.Context, _ StageEntry) (*dex.StepDecision, error) {
	post, err := blogPost.Get(ctx)
	if err != nil {
		return nil, err
	}
	window, err := changeWindow.Get(ctx)
	if err != nil {
		return nil, err
	}
	presentation := render.BlogPresentationFromConfiguration(step.stages.configuration.Blog)
	html, err := render.RenderBlogHTML(post, presentation, window.Until)
	path := ""
	if err == nil {
		path, err = step.artifactStore.WriteBlogArtifact(ctx.FlowID(), render.BlogArtifactFileName(post), html)
	}
	if err != nil {
		reply, recordErr := step.stages.recordAttention(ctx, StagePublishBlogArtifact, err.Error())
		if recordErr != nil {
			return nil, recordErr
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	if err := blogHTML.Set(ctx, html); err != nil {
		return nil, err
	}
	if err := blogArtifactPath.Set(ctx, path); err != nil {
		return nil, err
	}
	if err := publishedBlogURL.Set(ctx, render.PublishedBlogURL(post, presentation)); err != nil {
		return nil, err
	}
	request, err := step.stages.prepareNewsletterDraft(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(DraftNewsletter{}, request), nil
}

// dex:group group-id:writing group-label:"Writing"
// dex:explanation text:"Wait for the language model to write the newsletter email from the blog post."
type DraftNewsletter struct {
	dex.StepDefaults
	stages        *newsletterStages
	languageModel *LanguageModelGenerationFlow
	artifactStore BlogArtifactStore
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (DraftNewsletter) GetStepType() string { return "DraftNewsletter" }

// WaitFor starts the generation SubFlow.
func (step DraftNewsletter) WaitFor(_ dex.Context, request model.GenerationRequest) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.languageModel, request)), nil
}

// Execute renders the newsletter and opens editorial review.
func (step DraftNewsletter) Execute(ctx dex.Context, _ model.GenerationRequest) (*dex.StepDecision, error) {
	post, err := blogPost.Get(ctx)
	if err != nil {
		return nil, err
	}
	configuration := step.stages.configuration
	generation, err := decodeGenerationSubFlowResult(ctx, 0)
	var draft model.NewsletterDraft
	var newsletter model.RenderedNewsletter
	if err == nil {
		draft, err = prompts.ParseNewsletterDraft(generation)
	}
	if err == nil {
		newsletter, err = render.RenderNewsletter(draft, post, render.BlogPresentationFromConfiguration(configuration.Blog), configuration.Newsletter.Footer)
	}
	if err != nil {
		reply, recordErr := step.stages.recordAttention(ctx, StageDraftNewsletter, err.Error())
		if recordErr != nil {
			return nil, recordErr
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	// Editors review the newsletter with an example unsubscribe link; each
	// recipient's own link is filled in at send time.
	preview, err := render.PersonalizeNewsletter(newsletter, step.stages.unsubscribeLinks.ExampleURL())
	newsletterPath := ""
	if err == nil {
		newsletterPath, err = step.artifactStore.WriteBlogArtifact(ctx.FlowID(), newsletterArtifactFileName(post), preview.HTMLBody)
	}
	if err != nil {
		reply, recordErr := step.stages.recordAttention(ctx, StageDraftNewsletter, err.Error())
		if recordErr != nil {
			return nil, recordErr
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	previewText, _ := truncateRunes(preview.TextBody, maximumPreviewRunes)
	if err := newsletterTextPreview.Set(ctx, previewText); err != nil {
		return nil, err
	}
	if err := newsletterArtifactPath.Set(ctx, newsletterPath); err != nil {
		return nil, err
	}
	if err := newsletterDraft.Set(ctx, draft); err != nil {
		return nil, err
	}
	if err := renderedNewsletter.Set(ctx, newsletter); err != nil {
		return nil, err
	}
	if err := newsletterSubject.Set(ctx, newsletter.Subject); err != nil {
		return nil, err
	}
	if !configuration.Review.Required {
		entry, err := step.stages.prepareSubscriberLoad(ctx)
		if err != nil {
			return nil, err
		}
		return dex.GoTo(LoadNewsletterSubscribers{}, entry), nil
	}
	reply, err := step.stages.recordEditorialReviewOpened(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postReviewRequestStepType), reply), nil
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Resume the editorial wait for the current review gate."
type AwaitEditorialDecision struct {
	dex.StepDefaultsNoWaitFor[slack.PostThreadReplyResult]
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (AwaitEditorialDecision) GetStepType() string { return "AwaitEditorialDecision" }

// Execute moves to the durable editorial wait.
func (AwaitEditorialDecision) Execute(ctx dex.Context, _ slack.PostThreadReplyResult) (*dex.StepDecision, error) {
	gateKey, err := reviewGateKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	reminders, err := optionalValue(reviewReminderCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	return dex.GoTo(WaitForEditorialDecision{}, EditorialReviewWait{GateKey: gateKey, ReminderNumber: int(reminders)}), nil
}

// dex:group group-id:review group-label:"Review"
// dex:explanation text:"Wait for an editor to approve, revise, or discard the draft, reminding the thread on schedule."
type WaitForEditorialDecision struct {
	dex.StepDefaults
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (WaitForEditorialDecision) GetStepType() string { return "WaitForEditorialDecision" }

// GetStepOptions serializes Execute with editorial Actions invoked through the
// Go Client, so such an Action that overlaps expiry is either applied or
// rejected. Dex Web v0.14.0 sends Actions without their locks
// (superdurable/dex#562); one that lands during the final expiry Execute
// reports success and is then dropped.
func (WaitForEditorialDecision) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{
		dex.LockAttribute(requestStatus), dex.LockAttribute(reviewGateKey),
	}}
}

// WaitFor races the review gate's decision Channel against the reminder Timer.
func (step WaitForEditorialDecision) WaitFor(_ dex.Context, wait EditorialReviewWait) (*dex.Wait, error) {
	return dex.AnyOf(
		editorialDecisions.ForOne(wait.GateKey),
		dex.Timer(time.Duration(step.stages.configuration.Review.ReminderInterval)),
	), nil
}

// Execute applies the decision or the reminder schedule.
func (step WaitForEditorialDecision) Execute(ctx dex.Context, wait EditorialReviewWait) (*dex.StepDecision, error) {
	decisions, err := editorialDecisions.GetConditionResults(ctx, wait.GateKey)
	if err != nil {
		return nil, err
	}
	if len(decisions) == 1 {
		decision := decisions[0]
		switch decision.Kind {
		case EditorialDecisionApprove:
			entry, err := step.stages.prepareSubscriberLoad(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(LoadNewsletterSubscribers{}, entry), nil
		case EditorialDecisionRevise:
			revisions, err := optionalValue(blogRevisionCount.Get(ctx))
			if err != nil {
				return nil, err
			}
			if err := blogRevisionCount.Set(ctx, revisions+1); err != nil {
				return nil, err
			}
			if err := editorFeedback.Set(ctx, decision.Feedback); err != nil {
				return nil, err
			}
			request, err := step.stages.prepareBlogDraft(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(DraftBlogPost{}, request), nil
		case EditorialDecisionDiscard:
			post, err := blogPost.Get(ctx)
			if err != nil {
				return nil, err
			}
			reply, err := step.stages.recordClosing(ctx, StatusDiscarded, firstNonBlank(decision.Reason, "Discarded by the editor."),
				notices.DraftDiscarded(post, decision.Reason))
			if err != nil {
				return nil, err
			}
			return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
		default:
			return nil, fmt.Errorf("unknown editorial decision %q", decision.Kind)
		}
	}
	if !ctx.HasTimerFired() {
		return nil, fmt.Errorf("editorial wait completed without a decision or a reminder")
	}
	status, err := requestStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != StatusAwaitingEditorReview {
		// An Action already published a decision and Timers win ties; consume it on the next wait.
		return dex.GoTo(WaitForEditorialDecision{}, wait), nil
	}
	post, err := blogPost.Get(ctx)
	if err != nil {
		return nil, err
	}
	if wait.ReminderNumber >= step.stages.configuration.Review.MaxReminders {
		reply, err := step.stages.recordClosing(ctx, StatusReviewExpired, "No editorial decision arrived before the review expired.",
			notices.ReviewExpired(post))
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
	}
	request, err := newsletterRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	reminderNumber := wait.ReminderNumber + 1
	if err := reviewReminderCount.Set(ctx, int64(reminderNumber)); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postReviewRequestStepType), SlackThreadReply{
		Thread: threadOf(request), Text: notices.ReviewReminder(post, step.stages.runReference(ctx), reminderNumber),
		Continuation: ContinueToEditorialReview,
	}), nil
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Read the subscriber list from Dex, snapshot this issue's audience, and send to the first subscriber."
type LoadNewsletterSubscribers struct {
	dex.StepDefaultsNoWaitFor[StageEntry]
	stages         *newsletterStages
	subscriberList SubscriberListReader
	retry          *dex.RetryPolicy
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (LoadNewsletterSubscribers) GetStepType() string { return loadNewsletterSubscribersStepType }

// GetStepOptions retries a failed read of the subscriber list Flow, for about
// ten minutes by default, then holds the request for operator attention.
func (step LoadNewsletterSubscribers) GetStepOptions() *dex.StepOptions {
	retry := step.retry
	if retry == nil {
		retry = &dex.RetryPolicy{InitialInterval: 2 * time.Second, BackoffCoefficient: 2, MaximumInterval: time.Minute, TotalDuration: 10 * time.Minute}
	}
	return &dex.StepOptions{
		ExecuteRetry:   retry,
		ExecuteFailure: dex.ProceedToOnExecuteFailure(HoldSubscriberListAfterRetries{stages: step.stages}, nil),
	}
}

// Execute snapshots the audience and the approved newsletter. The read is an
// external effect through the Dex Client, so it lives here; a failed read is
// retried and the snapshot is taken once.
func (step LoadNewsletterSubscribers) Execute(ctx dex.Context, _ StageEntry) (*dex.StepDecision, error) {
	configuration := step.stages.configuration
	readContext, cancel := context.WithTimeout(ctx, subscriberListReadTimeout)
	defer cancel()
	addresses, err := step.subscriberList.ListNewsletterSubscribers(readContext)
	if err != nil {
		return nil, fmt.Errorf("read the newsletter subscriber list: %w", err)
	}
	list, err := subscribers.BuildDeliveryList(addresses, configuration.Newsletter.MaxRecipients)
	if err != nil {
		reply, recordErr := step.stages.recordAttention(ctx, StageLoadSubscribers, err.Error())
		if recordErr != nil {
			return nil, recordErr
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	if err := subscriberList.Set(ctx, list); err != nil {
		return nil, err
	}
	if err := subscriberCount.Set(ctx, int64(len(list.Recipients))); err != nil {
		return nil, err
	}
	if len(list.Recipients) == 0 {
		post, err := blogPost.Get(ctx)
		if err != nil {
			return nil, err
		}
		reply, err := step.stages.recordClosing(ctx, StatusNoSubscribers, "The newsletter subscriber list is empty.", notices.NoSubscribers(post))
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
	}
	newsletter, err := renderedNewsletter.Get(ctx)
	if err != nil {
		return nil, err
	}
	approvedSubject, err := optionalValue(approvedNewsletterSubject.Get(ctx))
	if err != nil {
		return nil, err
	}
	if subject := sanitizeSubject(approvedSubject); subject != "" {
		newsletter.Subject = subject
	}
	if err := deliveryNewsletter.Set(ctx, newsletter); err != nil {
		return nil, err
	}
	if err := deliveryCursor.Set(ctx, 0); err != nil {
		return nil, err
	}
	if err := deliverySummary.Set(ctx, model.DeliverySummary{
		Recipients: len(list.Recipients), SkippedOverLimit: list.TruncatedCount, InvalidAddresses: list.InvalidAddresses,
	}); err != nil {
		return nil, err
	}
	if err := requestStatus.Set(ctx, StatusSendingNewsletter); err != nil {
		return nil, err
	}
	delivery, err := step.stages.deliveryFor(newsletter, list, 0)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[NewsletterDelivery](sendNewsletterToSubscriberStepType), delivery), nil
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Record one subscriber's delivery outcome and send to the next subscriber or report."
type RecordNewsletterDelivery struct {
	dex.StepDefaultsNoWaitFor[gmail.SendMessageResult]
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordNewsletterDelivery) GetStepType() string { return "RecordNewsletterDelivery" }

// Execute records the outcome without ever resending an uncertain delivery.
func (step RecordNewsletterDelivery) Execute(ctx dex.Context, result gmail.SendMessageResult) (*dex.StepDecision, error) {
	cursor, err := deliveryCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := deliverySummary.Get(ctx)
	if err != nil {
		return nil, err
	}
	if isConnectionLevelDeliveryFailure(result) {
		// The account, not this address, failed: nothing was sent, so keep the
		// cursor and let an operator retry from this subscriber.
		reply, err := step.stages.recordDeliveryAttention(ctx, "Gmail could not send with the newsletter-sender connection ("+
			describeQueryFailure(string(result.Branch), result.Failure)+"). Reauthorize it in Dex Web, restart the application, then retry.")
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
	}
	list, err := subscriberList.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cursor < 0 || int(cursor) >= len(list.Recipients) {
		return nil, fmt.Errorf("delivery cursor %d is outside %d subscribers", cursor, len(list.Recipients))
	}
	var status model.DeliveryStatus
	switch result.Branch {
	case gmail.SendMessageBranchSent:
		status = model.DeliverySent
		summary.Sent++
	case gmail.SendMessageBranchProviderRejected:
		status = model.DeliveryRejected
		summary.Rejected++
	case gmail.SendMessageBranchUncertain:
		status = model.DeliveryUncertain
		summary.Uncertain++
	default:
		status = model.DeliveryDefect
		summary.Defect++
	}
	if status != model.DeliverySent {
		// Operators check these recipients in Dex Web before any resend;
		// addresses never go to Slack.
		exception := model.DeliveryException{Recipient: list.Recipients[cursor], Status: status}
		if result.Failure != nil {
			exception.FailureKind = string(result.Failure.Kind)
		}
		if err := deliveryExceptions.Set(ctx, fmt.Sprintf(deliveryExceptionKeyForm, cursor), exception); err != nil {
			return nil, err
		}
		shown, err := optionalValue(deliveryExceptionsShown.Get(ctx))
		if err != nil {
			return nil, err
		}
		if len(shown) < maximumShownDeliveryExceptions {
			if err := deliveryExceptionsShown.Set(ctx, append(shown, exception)); err != nil {
				return nil, err
			}
		}
	}
	if err := deliverySummary.Set(ctx, summary); err != nil {
		return nil, err
	}
	next := cursor + 1
	if err := deliveryCursor.Set(ctx, next); err != nil {
		return nil, err
	}
	if int(next) < len(list.Recipients) {
		newsletter, err := deliveryNewsletter.Get(ctx)
		if err != nil {
			return nil, err
		}
		delivery, err := step.stages.deliveryFor(newsletter, list, int(next))
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[NewsletterDelivery](sendNewsletterToSubscriberStepType), delivery), nil
	}
	post, err := blogPost.Get(ctx)
	if err != nil {
		return nil, err
	}
	publishedURL, err := optionalValue(publishedBlogURL.Get(ctx))
	if err != nil {
		return nil, err
	}
	artifactPath, err := optionalValue(blogArtifactPath.Get(ctx))
	if err != nil {
		return nil, err
	}
	closingStatus, closingText := StatusDelivered, ""
	if summary.Sent == 0 {
		closingStatus, closingText = StatusDeliveryFailed, "The newsletter was not delivered to any subscriber."
	}
	reply, err := step.stages.recordClosing(ctx, closingStatus, closingText, notices.DeliveryReport(post, summary, publishedURL, artifactPath))
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
}

// dex:group group-id:recovery group-label:"Recovery"
// dex:explanation text:"Resume the operator-recovery wait for the current attention gate."
type AwaitOperatorRecovery struct {
	dex.StepDefaultsNoWaitFor[slack.PostThreadReplyResult]
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (AwaitOperatorRecovery) GetStepType() string { return "AwaitOperatorRecovery" }

// Execute moves to the durable recovery wait.
func (AwaitOperatorRecovery) Execute(ctx dex.Context, _ slack.PostThreadReplyResult) (*dex.StepDecision, error) {
	gateKey, err := attentionGateKey.Get(ctx)
	if err != nil {
		return nil, err
	}
	stage, err := failedStage.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(WaitForOperatorRecovery{}, OperatorRecoveryWait{GateKey: gateKey, Stage: stage}), nil
}

// dex:group group-id:recovery group-label:"Recovery"
// dex:explanation text:"Wait for an operator to retry the failed stage or abandon the request."
type WaitForOperatorRecovery struct {
	dex.StepDefaults
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (WaitForOperatorRecovery) GetStepType() string { return "WaitForOperatorRecovery" }

// GetStepOptions serializes Execute with recovery Actions invoked through the
// Go Client. Dex Web v0.14.0 sends Actions without their locks
// (superdurable/dex#562), so one that lands during the final expiry Execute
// reports success and is then dropped.
func (WaitForOperatorRecovery) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{
		dex.LockAttribute(requestStatus), dex.LockAttribute(attentionGateKey),
	}}
}

// WaitFor races the attention gate's decision Channel against expiry.
func (WaitForOperatorRecovery) WaitFor(_ dex.Context, wait OperatorRecoveryWait) (*dex.Wait, error) {
	return dex.AnyOf(operatorRecoveryDecision.ForOne(wait.GateKey), dex.Timer(attentionExpiry)), nil
}

// Execute retries the failed stage or closes the request.
func (step WaitForOperatorRecovery) Execute(ctx dex.Context, wait OperatorRecoveryWait) (*dex.StepDecision, error) {
	decisions, err := operatorRecoveryDecision.GetConditionResults(ctx, wait.GateKey)
	if err != nil {
		return nil, err
	}
	if len(decisions) == 1 && decisions[0].Kind == RecoveryDecisionAbandon {
		reply, err := step.stages.recordAbandonment(ctx, wait.Stage, firstNonBlank(decisions[0].Reason, "Abandoned by an operator."))
		if err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
	}
	if len(decisions) == 1 && decisions[0].Kind == RecoveryDecisionRetry {
		// The attention is resolved; clear it so Dex Web stops showing a stale reason.
		if err := errors.Join(attentionReason.Set(ctx, ""), failedStage.Set(ctx, "")); err != nil {
			return nil, err
		}
		switch wait.Stage {
		case StageInterpretRequest:
			request, err := step.stages.prepareRequestInterpretation(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(InterpretNewsletterRequest{}, request), nil
		case StageResearchRepos:
			requests, err := step.stages.prepareRepositoryResearch(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(ResearchSelectedRepositories{}, requests), nil
		case StageSynthesizeResearch:
			request, err := step.stages.prepareResearchSynthesis(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(SynthesizeResearchBrief{}, request), nil
		case StageDraftBlogPost:
			request, err := step.stages.prepareBlogDraft(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(DraftBlogPost{}, request), nil
		case StagePublishBlogArtifact:
			if err := requestStatus.Set(ctx, StatusPublishingArtifact); err != nil {
				return nil, err
			}
			return dex.GoTo(PublishBlogArtifact{}, StageEntry{Stage: StagePublishBlogArtifact}), nil
		case StageDraftNewsletter:
			request, err := step.stages.prepareNewsletterDraft(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(DraftNewsletter{}, request), nil
		case StageLoadSubscribers:
			entry, err := step.stages.prepareSubscriberLoad(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(LoadNewsletterSubscribers{}, entry), nil
		case StageSendNewsletter:
			delivery, err := step.stages.prepareDeliveryResumption(ctx)
			if err != nil {
				return nil, err
			}
			return dex.GoTo(sdkgo.StepRef[NewsletterDelivery](sendNewsletterToSubscriberStepType), delivery), nil
		default:
			return nil, fmt.Errorf("unknown newsletter stage %q", wait.Stage)
		}
	}
	if len(decisions) == 1 {
		return nil, fmt.Errorf("unknown recovery decision %q", decisions[0].Kind)
	}
	if !ctx.HasTimerFired() {
		return nil, fmt.Errorf("recovery wait completed without a decision or expiry")
	}
	status, err := requestStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != StatusNeedsAttention {
		return dex.GoTo(WaitForOperatorRecovery{}, wait), nil
	}
	reply, err := step.stages.recordAbandonment(ctx, wait.Stage, "No operator recovered the request within 7 days.")
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postFinalReplyStepType), reply), nil
}

// dex:group group-id:recovery group-label:"Recovery"
// dex:explanation text:"Continue the Process when a Slack status reply exhausts its retries; Slack replies are best effort."
type ContinueAfterSlackReplyFailure struct {
	dex.StepDefaultsNoWaitFor[SlackThreadReply]
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (ContinueAfterSlackReplyFailure) GetStepType() string { return "ContinueAfterSlackReplyFailure" }

// Execute routes to the Step the reply would have led to.
func (ContinueAfterSlackReplyFailure) Execute(_ dex.Context, reply SlackThreadReply) (*dex.StepDecision, error) {
	skipped := slack.PostThreadReplyResult{}
	switch reply.Continuation {
	case ContinueToRequestInterpretation:
		return dex.GoTo(BeginRequestInterpretation{}, skipped), nil
	case ContinueToRepositoryResearch:
		return dex.GoTo(BeginRepositoryResearch{}, skipped), nil
	case ContinueToEditorialReview:
		return dex.GoTo(AwaitEditorialDecision{}, skipped), nil
	case ContinueToOperatorRecovery:
		return dex.GoTo(AwaitOperatorRecovery{}, skipped), nil
	case ContinueToCompletion:
		return dex.GoTo(CompleteNewsletterRequest{}, skipped), nil
	default:
		return nil, fmt.Errorf("unknown Slack reply continuation %q", reply.Continuation)
	}
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Hold for operator attention when reading the subscriber list exhausts its retries."
type HoldSubscriberListAfterRetries struct {
	dex.StepDefaultsNoWaitFor[StageEntry]
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (HoldSubscriberListAfterRetries) GetStepType() string { return "HoldSubscriberListAfterRetries" }

// Execute routes to operator recovery.
func (step HoldSubscriberListAfterRetries) Execute(ctx dex.Context, _ StageEntry) (*dex.StepDecision, error) {
	reply, err := step.stages.recordAttention(ctx, StageLoadSubscribers,
		"The newsletter subscriber list could not be read after every retry ("+recoveryErrorText(ctx)+"). Check that the application is running, then retry.")
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
}

// dex:group group-id:delivery group-label:"Delivery"
// dex:explanation text:"Pause delivery for operator attention when a Gmail send exhausts its retries, keeping the position."
type HoldNewsletterDeliveryAfterRetries struct {
	dex.StepDefaultsNoWaitFor[NewsletterDelivery]
	stages *newsletterStages
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (HoldNewsletterDeliveryAfterRetries) GetStepType() string {
	return "HoldNewsletterDeliveryAfterRetries"
}

// Execute routes to operator recovery; a retry resumes at this subscriber.
func (step HoldNewsletterDeliveryAfterRetries) Execute(ctx dex.Context, _ NewsletterDelivery) (*dex.StepDecision, error) {
	reply, err := step.stages.recordDeliveryAttention(ctx,
		"Gmail kept failing after every retry, for example because the daily sending limit was reached ("+recoveryErrorText(ctx)+"). Retry later from Dex Web.")
	if err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[SlackThreadReply](postAttentionNoticeStepType), reply), nil
}

// dex:group group-id:close group-label:"Close"
// dex:explanation text:"Complete with the final typed newsletter result."
type CompleteNewsletterRequest struct {
	dex.StepDefaultsNoWaitFor[slack.PostThreadReplyResult]
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (CompleteNewsletterRequest) GetStepType() string { return "CompleteNewsletterRequest" }

// Execute completes the Flow.
func (CompleteNewsletterRequest) Execute(ctx dex.Context, _ slack.PostThreadReplyResult) (*dex.StepDecision, error) {
	status, statusErr := requestStatus.Get(ctx)
	topic, topicErr := optionalValue(requestTopic.Get(ctx))
	title, titleErr := optionalValue(blogPostTitle.Get(ctx))
	artifactPath, artifactErr := optionalValue(blogArtifactPath.Get(ctx))
	publishedURL, publishedErr := optionalValue(publishedBlogURL.Get(ctx))
	reason, reasonErr := optionalValue(closingReason.Get(ctx))
	revisions, revisionsErr := optionalValue(blogRevisionCount.Get(ctx))
	delivery, deliveryErr := optionalValue(deliverySummary.Get(ctx))
	if err := errors.Join(statusErr, topicErr, titleErr, artifactErr, publishedErr, reasonErr, revisionsErr, deliveryErr); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(NewsletterResult{
		Status: status, Topic: topic, BlogTitle: title, BlogArtifactPath: artifactPath, PublishedBlogURL: publishedURL,
		RevisionCount: int(revisions), ClosingReason: reason, Delivery: delivery,
	}), nil
}

// newsletterStages prepares stage inputs and records attention, review, and
// closing state. It never returns Dex decisions: every transition stays in a
// Step's Execute so the Flow Definition Graph shows it.
type newsletterStages struct {
	configuration    config.ProcessConfiguration
	unsubscribeLinks UnsubscribeLinks
}

func (stages *newsletterStages) prepareRequestInterpretation(ctx dex.Context) (model.GenerationRequest, error) {
	request, err := newsletterRequest.Get(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	if err := requestStatus.Set(ctx, StatusInterpreting); err != nil {
		return model.GenerationRequest{}, err
	}
	return prompts.BuildRequestInterpretationRequest(stages.configuration, request, requestTime(ctx, request)), nil
}

func (stages *newsletterStages) prepareRepositoryResearch(ctx dex.Context) ([]model.RepositoryResearchRequest, error) {
	interpretation, window, err := loadInterpretationAndWindow(ctx)
	if err != nil {
		return nil, err
	}
	requests := make([]model.RepositoryResearchRequest, 0, len(interpretation.RepositorySelections))
	for _, selection := range interpretation.RepositorySelections {
		if len(requests) >= stages.configuration.Research.MaxRepositoriesPerRequest {
			break
		}
		requests = append(requests, model.RepositoryResearchRequest{
			Selection: selection, Topic: interpretation.Topic, Instructions: interpretation.Instructions, Window: window,
		})
	}
	if err := requestStatus.Set(ctx, StatusResearching); err != nil {
		return nil, err
	}
	return requests, nil
}

func (stages *newsletterStages) prepareResearchSynthesis(ctx dex.Context) (model.GenerationRequest, error) {
	interpretation, window, err := loadInterpretationAndWindow(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	digests, err := repositoryDigests.Get(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	if err := requestStatus.Set(ctx, StatusSynthesizing); err != nil {
		return model.GenerationRequest{}, err
	}
	return prompts.BuildResearchSynthesisRequest(stages.configuration, interpretation, window, digests), nil
}

func (stages *newsletterStages) prepareBlogDraft(ctx dex.Context) (model.GenerationRequest, error) {
	interpretation, window, err := loadInterpretationAndWindow(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	brief, err := researchBrief.Get(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	revision, err := currentBlogRevision(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	if err := requestStatus.Set(ctx, StatusDraftingBlogPost); err != nil {
		return model.GenerationRequest{}, err
	}
	return prompts.BuildBlogDraftRequest(stages.configuration, interpretation, window, brief, revision), nil
}

func (stages *newsletterStages) prepareNewsletterDraft(ctx dex.Context) (model.GenerationRequest, error) {
	interpretation, _, err := loadInterpretationAndWindow(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	post, err := blogPost.Get(ctx)
	if err != nil {
		return model.GenerationRequest{}, err
	}
	if err := requestStatus.Set(ctx, StatusDraftingNewsletter); err != nil {
		return model.GenerationRequest{}, err
	}
	return prompts.BuildNewsletterDraftRequest(stages.configuration, interpretation, post), nil
}

func (stages *newsletterStages) prepareSubscriberLoad(ctx dex.Context) (StageEntry, error) {
	if err := requestStatus.Set(ctx, StatusLoadingSubscribers); err != nil {
		return StageEntry{}, err
	}
	return StageEntry{Stage: StageLoadSubscribers}, nil
}

func (stages *newsletterStages) recordAttention(ctx dex.Context, stage string, reason string) (SlackThreadReply, error) {
	count, err := optionalValue(attentionCount.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	count++
	gateKey := fmt.Sprintf("attention-%d", count)
	boundedReason, _ := truncateRunes(reason, maximumReasonRunes)
	if err := errors.Join(
		attentionCount.Set(ctx, count), attentionGateKey.Set(ctx, gateKey), failedStage.Set(ctx, stage),
		attentionReason.Set(ctx, boundedReason), requestStatus.Set(ctx, StatusNeedsAttention),
	); err != nil {
		return SlackThreadReply{}, err
	}
	request, err := newsletterRequest.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	return SlackThreadReply{
		Thread: threadOf(request), Text: notices.NeedsAttention(stage, boundedReason, stages.runReference(ctx)),
		Continuation: ContinueToOperatorRecovery,
	}, nil
}

func (stages *newsletterStages) recordEditorialReviewOpened(ctx dex.Context) (SlackThreadReply, error) {
	revisions, err := optionalValue(blogRevisionCount.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	if err := errors.Join(
		reviewGateKey.Set(ctx, fmt.Sprintf("review-%d", revisions)), reviewReminderCount.Set(ctx, 0),
		requestStatus.Set(ctx, StatusAwaitingEditorReview),
	); err != nil {
		return SlackThreadReply{}, err
	}
	post, err := blogPost.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	artifactPath, err := optionalValue(blogArtifactPath.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	request, err := newsletterRequest.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	digests, err := optionalValue(repositoryDigests.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	return SlackThreadReply{
		Thread:       threadOf(request),
		Text:         appendCoverageNote(notices.DraftReadyForReview(post, stages.runReference(ctx), artifactPath, int(revisions)), digests),
		Continuation: ContinueToEditorialReview,
	}, nil
}

func (stages *newsletterStages) recordClosing(ctx dex.Context, status string, reason string, text string) (SlackThreadReply, error) {
	if err := errors.Join(requestStatus.Set(ctx, status), closingReason.Set(ctx, reason)); err != nil {
		return SlackThreadReply{}, err
	}
	request, err := newsletterRequest.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	return SlackThreadReply{Thread: threadOf(request), Text: text, Continuation: ContinueToCompletion}, nil
}

// recordAbandonment closes a request an operator abandoned or nobody
// recovered. When delivery had paused, it reports what was sent and warns
// against reposting, which would email every subscriber again.
func (stages *newsletterStages) recordAbandonment(ctx dex.Context, stage string, reason string) (SlackThreadReply, error) {
	if stage != StageSendNewsletter {
		return stages.recordClosing(ctx, StatusAbandoned, reason, notices.RequestFailed(reason))
	}
	post, err := blogPost.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	summary, err := optionalValue(deliverySummary.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	publishedURL, err := optionalValue(publishedBlogURL.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	artifactPath, err := optionalValue(blogArtifactPath.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	return stages.recordClosing(ctx, StatusDeliveryStopped, reason, notices.DeliveryStopped(post, summary, reason, publishedURL, artifactPath))
}

func (stages *newsletterStages) runReference(ctx dex.Context) notices.RunReference {
	return notices.RunReference{FlowType: TechBlogNewsletterFlowType, FlowID: ctx.FlowID(), DexWebURL: stages.configuration.DexWebURL}
}

func (stages *newsletterStages) recordDeliveryAttention(ctx dex.Context, reason string) (SlackThreadReply, error) {
	count, err := optionalValue(attentionCount.Get(ctx))
	if err != nil {
		return SlackThreadReply{}, err
	}
	count++
	boundedReason, _ := truncateRunes(reason, maximumReasonRunes)
	if err := errors.Join(
		attentionCount.Set(ctx, count), attentionGateKey.Set(ctx, fmt.Sprintf("attention-%d", count)),
		failedStage.Set(ctx, StageSendNewsletter), attentionReason.Set(ctx, boundedReason),
		requestStatus.Set(ctx, StatusNeedsAttention),
	); err != nil {
		return SlackThreadReply{}, err
	}
	post, err := blogPost.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	summary, err := deliverySummary.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	request, err := newsletterRequest.Get(ctx)
	if err != nil {
		return SlackThreadReply{}, err
	}
	return SlackThreadReply{
		Thread:       threadOf(request),
		Text:         notices.DeliveryHeldForAttention(post, summary, boundedReason, stages.runReference(ctx)),
		Continuation: ContinueToOperatorRecovery,
	}, nil
}

// prepareDeliveryResumption rebuilds the send for the subscriber at the
// durable cursor; subscribers before it were already recorded.
func (stages *newsletterStages) prepareDeliveryResumption(ctx dex.Context) (NewsletterDelivery, error) {
	cursor, err := deliveryCursor.Get(ctx)
	if err != nil {
		return NewsletterDelivery{}, err
	}
	list, err := subscriberList.Get(ctx)
	if err != nil {
		return NewsletterDelivery{}, err
	}
	newsletter, err := deliveryNewsletter.Get(ctx)
	if err != nil {
		return NewsletterDelivery{}, err
	}
	if cursor < 0 || int(cursor) >= len(list.Recipients) {
		return NewsletterDelivery{}, fmt.Errorf("delivery cursor %d is outside %d subscribers", cursor, len(list.Recipients))
	}
	if err := requestStatus.Set(ctx, StatusSendingNewsletter); err != nil {
		return NewsletterDelivery{}, err
	}
	return stages.deliveryFor(newsletter, list, int(cursor))
}

// isConnectionLevelDeliveryFailure reports a failure of the sending account
// rather than of one address. Subscriber addresses are validated before
// delivery, so a Defect (credentials unavailable, message rejected locally)
// applies to every remaining subscriber.
func isConnectionLevelDeliveryFailure(result gmail.SendMessageResult) bool {
	if result.Branch == gmail.SendMessageBranchDefect {
		return true
	}
	if result.Branch == gmail.SendMessageBranchProviderRejected && result.Failure != nil {
		return result.Failure.Kind == sdkgo.FailureAuthentication || result.Failure.Kind == sdkgo.FailureAuthorization
	}
	return false
}

func appendCoverageNote(text string, digests []model.RepositoryChangeDigest) string {
	var incomplete []model.RepositoryReference
	for _, digest := range digests {
		if digest.Status != model.RepositoryResearched {
			incomplete = append(incomplete, digest.Repository)
		}
	}
	if note := notices.PartialResearchCoverage(incomplete); note != "" {
		return text + "\n" + note
	}
	return text
}

func newsletterArtifactFileName(post model.BlogPost) string {
	return strings.TrimSuffix(render.BlogArtifactFileName(post), ".html") + ".newsletter.html"
}

func recoveryErrorText(ctx dex.Context) string {
	if info := ctx.RecoveryError(); info != nil && strings.TrimSpace(info.Detail) != "" {
		text, _ := truncateRunes(info.Detail, 200)
		return text
	}
	return "no error detail"
}

func validateOpenGate(status string, expectedStatus string, currentGate string, requestedGate string) error {
	if status != expectedStatus {
		return fmt.Errorf("this Action is not available while the request is %s", status)
	}
	if requestedGate != currentGate {
		return fmt.Errorf("the request changed since this page loaded; refresh and try again")
	}
	return nil
}

func loadInterpretationAndWindow(ctx dex.Context) (model.RequestInterpretation, model.ChangeWindow, error) {
	interpretation, err := requestInterpretation.Get(ctx)
	if err != nil {
		return model.RequestInterpretation{}, model.ChangeWindow{}, err
	}
	window, err := changeWindow.Get(ctx)
	if err != nil {
		return model.RequestInterpretation{}, model.ChangeWindow{}, err
	}
	return interpretation, window, nil
}

func currentBlogRevision(ctx dex.Context) (*prompts.BlogRevision, error) {
	revisions, err := optionalValue(blogRevisionCount.Get(ctx))
	if err != nil || revisions == 0 {
		return nil, err
	}
	feedback, err := optionalValue(editorFeedback.Get(ctx))
	if err != nil {
		return nil, err
	}
	previous, err := blogPost.Get(ctx)
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &prompts.BlogRevision{RevisionNumber: int(revisions), EditorFeedback: feedback, PreviousDraft: previous}, nil
}

func mapSlackThreadReplyToOperationInput(reply SlackThreadReply) slack.PostThreadReplyInput {
	return slack.PostThreadReplyInput{ChannelID: reply.Thread.ChannelID, ThreadTimestamp: reply.Thread.ThreadTimestamp, Text: reply.Text}
}

func mapNewsletterDeliveryToOperationInput(delivery NewsletterDelivery) gmail.SendMessageInput {
	return gmail.SendMessageInput{
		To: []string{delivery.Recipient}, Subject: delivery.Subject, TextBody: delivery.TextBody, HTMLBody: delivery.HTMLBody,
	}
}

func requestTime(ctx dex.Context, request model.NewsletterRequest) time.Time {
	if parsed, err := timewindow.ParseSlackTimestamp(request.MessageTimestamp); err == nil {
		return parsed
	}
	return ctx.FlowStartedAt().UTC()
}

func threadOf(request model.NewsletterRequest) model.SlackThreadReference {
	return model.SlackThreadReference{ChannelID: request.ChannelID, ThreadTimestamp: request.ThreadTimestamp}
}

// deliveryFor builds the send for one recipient, with that recipient's own
// unsubscribe link in both bodies.
func (stages *newsletterStages) deliveryFor(newsletter model.RenderedNewsletter, list model.SubscriberList, index int) (NewsletterDelivery, error) {
	recipient := list.Recipients[index]
	personalized, err := render.PersonalizeNewsletter(newsletter, stages.unsubscribeLinks.URL(recipient))
	if err != nil {
		return NewsletterDelivery{}, err
	}
	return NewsletterDelivery{
		RecipientIndex: index, Recipient: recipient, Subject: personalized.Subject,
		HTMLBody: personalized.HTMLBody, TextBody: personalized.TextBody,
	}, nil
}

func sanitizeSubject(subject string) string {
	cleaned := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return ' '
		}
		return character
	}, subject)
	cleaned, _ = truncateRunes(strings.Join(strings.Fields(cleaned), " "), maximumSubjectRunes)
	return cleaned
}

func boundedOptionalText(text *string) string {
	if text == nil {
		return ""
	}
	bounded, _ := truncateRunes(strings.TrimSpace(*text), maximumReasonRunes)
	return bounded
}

// NewsletterRequestTriggerFilter is the application admission rule for Slack
// request messages: top-level messages in the configured channel, optionally
// containing text and posted by allowed members.
func NewsletterRequestTriggerFilter(configuration slack.ChannelThreadCreatedTriggerConfiguration) (sdkgo.TriggerFilter[slack.MessageEvent], error) {
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	matcher := configuration.ThreadTriggerMatcher
	return func(event sdkgo.TriggerEvent[slack.MessageEvent]) bool {
		message := event.Payload
		isReply := message.ThreadTimestamp != "" && message.ThreadTimestamp != message.Timestamp
		if event.ID == "" || message.TeamID == "" || message.ChannelID != configuration.ChannelID ||
			message.Timestamp == "" || message.UserID == "" || isReply || strings.TrimSpace(message.Text) == "" {
			return false
		}
		if matcher.MessageContains != "" && !strings.Contains(strings.ToLower(message.Text), strings.ToLower(matcher.MessageContains)) {
			return false
		}
		if len(matcher.PosterUserIDs) == 0 {
			return true
		}
		for _, allowedUserID := range matcher.PosterUserIDs {
			if message.UserID == allowedUserID {
				return true
			}
		}
		return false
	}, nil
}

// ResolveNewsletterRequestFlowID derives one stable Flow ID per Slack request
// message, so redelivered events attach to the existing run.
func ResolveNewsletterRequestFlowID(event sdkgo.TriggerEvent[slack.MessageEvent]) string {
	message := event.Payload
	return fmt.Sprintf("tech-blog-newsletter-%s-%s-%s", message.TeamID, message.ChannelID, message.Timestamp)
}

// MapSlackMessageToNewsletterRequest maps the Slack event to the Flow input.
func MapSlackMessageToNewsletterRequest(event sdkgo.TriggerEvent[slack.MessageEvent]) model.NewsletterRequest {
	message := event.Payload
	thread := message.ThreadTimestamp
	if thread == "" {
		thread = message.Timestamp
	}
	return model.NewsletterRequest{
		SlackEventID: event.ID, TeamID: message.TeamID, ChannelID: message.ChannelID,
		MessageTimestamp: message.Timestamp, ThreadTimestamp: thread,
		RequesterUserID: message.UserID, RequestText: message.Text,
	}
}

var _ dex.Flow = (*TechBlogNewsletterFlow)(nil)
var _ dex.Step[model.NewsletterRequest] = ReceiveNewsletterRequest{}
var _ dex.Step[slack.PostThreadReplyResult] = BeginRequestInterpretation{}
var _ dex.Step[model.GenerationRequest] = InterpretNewsletterRequest{}
var _ dex.Step[slack.PostThreadReplyResult] = BeginRepositoryResearch{}
var _ dex.Step[[]model.RepositoryResearchRequest] = ResearchSelectedRepositories{}
var _ dex.Step[model.GenerationRequest] = SynthesizeResearchBrief{}
var _ dex.Step[model.GenerationRequest] = DraftBlogPost{}
var _ dex.Step[StageEntry] = PublishBlogArtifact{}
var _ dex.Step[model.GenerationRequest] = DraftNewsletter{}
var _ dex.Step[slack.PostThreadReplyResult] = AwaitEditorialDecision{}
var _ dex.Step[EditorialReviewWait] = WaitForEditorialDecision{}
var _ dex.Step[StageEntry] = LoadNewsletterSubscribers{}
var _ dex.Step[StageEntry] = HoldSubscriberListAfterRetries{}
var _ dex.Step[gmail.SendMessageResult] = RecordNewsletterDelivery{}
var _ dex.Step[slack.PostThreadReplyResult] = AwaitOperatorRecovery{}
var _ dex.Step[OperatorRecoveryWait] = WaitForOperatorRecovery{}
var _ dex.Step[slack.PostThreadReplyResult] = CompleteNewsletterRequest{}
var _ dex.RPC[dex.None, map[string]any] = (*TechBlogNewsletterFlow)(nil).GetDexSummary
var _ dex.RPC[dex.None, map[string]any] = (*TechBlogNewsletterFlow)(nil).GetDexDisplay
var _ dex.RPC[ApproveNewsletterInput, dex.None] = (*TechBlogNewsletterFlow)(nil).ApproveNewsletterForDelivery
var _ dex.RPC[RequestBlogRevisionInput, dex.None] = (*TechBlogNewsletterFlow)(nil).RequestBlogRevision
var _ dex.RPC[DiscardNewsletterDraftInput, dex.None] = (*TechBlogNewsletterFlow)(nil).DiscardNewsletterDraft
var _ dex.RPC[RetryFailedStageInput, dex.None] = (*TechBlogNewsletterFlow)(nil).RetryFailedStage
var _ dex.RPC[AbandonNewsletterRequestInput, dex.None] = (*TechBlogNewsletterFlow)(nil).AbandonNewsletterRequest
