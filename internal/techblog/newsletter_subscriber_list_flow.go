package techblog

import (
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/subscribers"
	"github.com/superdurable/dex/sdk-go/dex"
)

// NewsletterSubscriberListFlowType is the stable Flow type of the subscriber
// list.
const NewsletterSubscriberListFlowType = "NewsletterSubscriberListFlow"

// NewsletterSubscriberListFlowID is the one long-lived Flow that owns the
// newsletter subscriber list. The application starts it at Worker startup.
const NewsletterSubscriberListFlowID = "newsletter-subscriber-list"

// newsletterSubscriberListStartRequestID is the constant start request ID.
// Dex returns an existing run as success only when its stored request ID
// matches, so every application start must send the same one.
const newsletterSubscriberListStartRequestID = "newsletter-subscriber-list-start"

// SubscriptionOutcome is the result of one AddNewsletterSubscriber call.
type SubscriptionOutcome string

// Subscription outcomes.
const (
	SubscriptionAdded             SubscriptionOutcome = "added"
	SubscriptionAlreadySubscribed SubscriptionOutcome = "already-subscribed"
	SubscriptionInvalidAddress    SubscriptionOutcome = "invalid-address"
	SubscriptionListFull          SubscriptionOutcome = "list-full"
)

// AddNewsletterSubscriberResult reports how one subscription was handled.
// Email is the canonical address when Outcome is added or already-subscribed.
type AddNewsletterSubscriberResult struct {
	Outcome SubscriptionOutcome `json:"outcome"`
	Email   string              `json:"email,omitempty"`
}

var (
	// newsletterSubscribers holds the canonical, unique subscriber addresses in
	// the order they subscribed. It is bounded by newsletter.maxRecipients
	// (at most 2000), and delivery always reads it whole, so one Attribute is
	// the whole store: no AttributeMap, partitions, or external database.
	newsletterSubscribers     = dex.DefineAttribute[[]string]("newsletter-subscribers")
	newsletterSubscriberCount = dex.DefineAttribute[int64]("newsletter-subscriber-count")
)

// NewsletterSubscriberListFlow is the durable owner of the newsletter
// subscriber list. It has no Steps: it stays open and serves typed RPCs. The
// application's subscription form adds addresses through
// AddNewsletterSubscriber, and TechBlogNewsletterFlow reads a snapshot through
// ListNewsletterSubscribers when an issue is approved for delivery.
type NewsletterSubscriberListFlow struct {
	dex.FlowDefaults
	maxSubscribers int
}

// NewNewsletterSubscriberListFlow caps the list at newsletter.maxRecipients.
func NewNewsletterSubscriberListFlow(configuration config.ProcessConfiguration) *NewsletterSubscriberListFlow {
	return &NewsletterSubscriberListFlow{maxSubscribers: configuration.Newsletter.MaxRecipients}
}

// NewsletterSubscriberListStartOptions starts the list once. With the
// constant request ID, a later start returns the existing run (open or
// stopped) as success, and the default Flow ID reuse policy never replaces a
// list that an operator stopped with a new, empty one. Nil Timeout keeps the
// list open indefinitely.
func NewsletterSubscriberListStartOptions() dex.StartFlowOptions {
	requestID := newsletterSubscriberListStartRequestID
	return dex.StartFlowOptions{AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true}, RequestID: &requestID}
}

// GetFlowType returns the stable Flow type.
func (*NewsletterSubscriberListFlow) GetFlowType() string { return NewsletterSubscriberListFlowType }

// GetSteps returns no Steps: the list is an entity Flow driven only by RPCs.
func (*NewsletterSubscriberListFlow) GetSteps() []dex.StepDef { return nil }

// GetRPCs registers the subscription and snapshot RPCs and the Dex Web read
// models. AddNewsletterSubscriber locks the list so concurrent subscriptions
// serialize instead of overwriting each other. ListNewsletterSubscribers also
// takes the list lock: a locked RPC needs an open run, so after an operator
// stops the list, deliveries hold instead of mailing its last snapshot.
func (flow *NewsletterSubscriberListFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.AddNewsletterSubscriber, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(newsletterSubscribers), dex.LockAttribute(newsletterSubscriberCount)},
		}),
		dex.DefineRPC(flow.ListNewsletterSubscribers, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(newsletterSubscribers)},
		}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

// GetPersistenceSchema registers the subscriber list Attributes.
func (*NewsletterSubscriberListFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{newsletterSubscribers, newsletterSubscriberCount}}
}

// AddNewsletterSubscriber adds one address. Adding an address that is already
// on the list succeeds without a write, so a retried call is harmless. An
// invalid address or a full list is a typed outcome, not an error.
func (flow *NewsletterSubscriberListFlow) AddNewsletterSubscriber(ctx dex.Context, email string) (*dex.RPCResult[AddNewsletterSubscriberResult], error) {
	current, err := optionalValue(newsletterSubscribers.Get(ctx))
	if err != nil {
		return nil, err
	}
	updated, result := addSubscriber(current, email, flow.maxSubscribers)
	if result.Outcome == SubscriptionAdded {
		if err := newsletterSubscribers.Set(ctx, updated); err != nil {
			return nil, err
		}
		if err := newsletterSubscriberCount.Set(ctx, int64(len(updated))); err != nil {
			return nil, err
		}
	}
	return &dex.RPCResult[AddNewsletterSubscriberResult]{Output: result}, nil
}

// ListNewsletterSubscribers returns the current subscriber addresses in
// subscription order. It writes nothing; its lock only requires an open list.
func (*NewsletterSubscriberListFlow) ListNewsletterSubscribers(ctx dex.Context, _ dex.None) (*dex.RPCResult[[]string], error) {
	current, err := optionalValue(newsletterSubscribers.Get(ctx))
	if err != nil {
		return nil, err
	}
	if current == nil {
		current = []string{}
	}
	return &dex.RPCResult[[]string]{Output: current}, nil
}

// dex:field attribute-key:newsletter-subscriber-count value-type:int64 editable:false description:"Subscribers"
func (*NewsletterSubscriberListFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	count, err := optionalValue(newsletterSubscriberCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"newsletter-subscriber-count": count}}, nil
}

// dex:field attribute-key:newsletter-subscriber-count value-type:int64 editable:false description:"Subscribers" ui-slot:title
// dex:field attribute-key:newsletter-subscribers value-type:string-array editable:false description:"Subscriber addresses"
func (*NewsletterSubscriberListFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	count, err := optionalValue(newsletterSubscriberCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	addresses, err := optionalValue(newsletterSubscribers.Get(ctx))
	if err != nil {
		return nil, err
	}
	if addresses == nil {
		addresses = []string{}
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"newsletter-subscriber-count": count,
		"newsletter-subscribers":      addresses,
	}}, nil
}

// addSubscriber returns the list with the canonical address appended, or the
// unchanged list with the reason nothing was added. It never modifies current.
// A full list answers list-full for every valid address, subscribed or not,
// so the outcome never reveals who is on it.
func addSubscriber(current []string, email string, maxSubscribers int) ([]string, AddNewsletterSubscriberResult) {
	address, valid := subscribers.CanonicalAddress(email)
	if !valid {
		return current, AddNewsletterSubscriberResult{Outcome: SubscriptionInvalidAddress}
	}
	if len(current) >= maxSubscribers {
		return current, AddNewsletterSubscriberResult{Outcome: SubscriptionListFull}
	}
	for _, existing := range current {
		if existing == address {
			return current, AddNewsletterSubscriberResult{Outcome: SubscriptionAlreadySubscribed, Email: address}
		}
	}
	updated := make([]string, len(current), len(current)+1)
	copy(updated, current)
	return append(updated, address), AddNewsletterSubscriberResult{Outcome: SubscriptionAdded, Email: address}
}

var _ dex.Flow = (*NewsletterSubscriberListFlow)(nil)
var _ dex.RPC[string, AddNewsletterSubscriberResult] = (*NewsletterSubscriberListFlow)(nil).AddNewsletterSubscriber
var _ dex.RPC[dex.None, []string] = (*NewsletterSubscriberListFlow)(nil).ListNewsletterSubscribers
var _ dex.RPC[dex.None, map[string]any] = (*NewsletterSubscriberListFlow)(nil).GetDexSummary
var _ dex.RPC[dex.None, map[string]any] = (*NewsletterSubscriberListFlow)(nil).GetDexDisplay
