// Package subscribers owns the newsletter subscriber list: one long-lived
// top-level Flow whose Attributes are the only record of who subscribed.
package subscribers

import (
	"errors"
	"sort"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	FlowType = "NewsletterSubscribers"
	// FlowID is the single subscriber list.
	FlowID = "newsletter-subscribers"
	// MaxSubscribers bounds the one list Attribute. Dex Web does not yet pass RPC loads
	// (superdurable/dex#562), so the list stays a plain Attribute instead of an AttributeMap.
	MaxSubscribers = 10000
	recentShown    = 20

	statusOpen = "open"
)

type Subscriber struct {
	Address      string    `json:"address"`
	SubscribedAt time.Time `json:"subscribedAt"`
}

type RemovalRequest struct {
	Address string `json:"address"`
}

var (
	// dex:indexed-attribute attribute-key:subscriber-list-status index-key:subscriber-list-status index-type:keyword value-type:string description:"Subscriber list status"
	listStatus = dex.DefineAttribute[string](
		"subscriber-list-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	subscriberList    = dex.DefineAttribute[[]Subscriber]("subscriber-list")
	subscriberCount   = dex.DefineAttribute[int64]("subscriber-count")
	recentSubscribers = dex.DefineAttribute[[]string]("recent-subscribers")
	lastChange        = dex.DefineAttribute[string]("last-subscriber-change")
	removalRequests   = dex.DefineChannel[RemovalRequest]("subscriber-removal-requests")
)

type Flow struct{ dex.FlowDefaults }

func (Flow) GetFlowType() string { return FlowType }

// dex:group group-id:subscribers group-label:"Subscribers"
// dex:explanation text:"Keep the subscriber list open and apply each removal an editor requests from Dex Web."
type HoldSubscriberList struct{ dex.StepDefaults }

func (HoldSubscriberList) GetStepType() string { return "HoldSubscriberList" }

func (HoldSubscriberList) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{dex.LockAttribute(subscriberList)}}
}

func (HoldSubscriberList) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	return dex.Until(removalRequests.ForOne()), nil
}

func (HoldSubscriberList) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	requests, err := removalRequests.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	list, err := subscriberList.Get(ctx)
	if err != nil && !isMissing(err) {
		return nil, err
	}
	for _, request := range requests {
		address, err := CanonicalAddress(request.Address)
		if err != nil {
			continue
		}
		if next, removed := without(list, address); removed {
			list = next
			if err := lastChange.Set(ctx, "Removed "+address+" from Dex Web"); err != nil {
				return nil, err
			}
		}
	}
	if err := subscriberList.Set(ctx, list); err != nil {
		return nil, err
	}
	if err := subscriberCount.Set(ctx, int64(len(list))); err != nil {
		return nil, err
	}
	if err := recentSubscribers.Set(ctx, recent(list)); err != nil {
		return nil, err
	}
	return dex.GoTo(HoldSubscriberList{}, nil), nil
}

func (Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(HoldSubscriberList{})}
}

func (flow Flow) GetRPCs() []dex.RPCDef {
	listLock := []dex.AttributeLock{dex.LockAttribute(subscriberList)}
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.AddNewsletterSubscriber, &dex.RPCOptions{LockAttributes: listLock}),
		dex.DefineRPC(flow.RemoveNewsletterSubscriber, &dex.RPCOptions{LockAttributes: listLock}),
		dex.DefineRPC(flow.ListNewsletterSubscribers, nil),
		dex.DefineRPC(flow.RequestSubscriberRemoval, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Remove subscriber",
				dex.WhenAttributeMatches(listStatus, dex.AttributeMatchEqual(statusOpen)),
				dex.ActionRequiresPermission("newsletter.manage"),
			),
		}),
	}
}

func (Flow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{listStatus, subscriberList, subscriberCount, recentSubscribers, lastChange},
		Channels:   []dex.ChannelDef{removalRequests},
	}
}

// InitialAttributes gives a new list its first state so views and Actions work before any change.
func InitialAttributes() ([]dex.InitialAttributeDef, error) {
	definitions := []dex.InitialAttributeDef{}
	for _, build := range []func() (dex.InitialAttributeDef, error){
		func() (dex.InitialAttributeDef, error) { return dex.InitialAttribute(listStatus, statusOpen) },
		func() (dex.InitialAttributeDef, error) { return dex.InitialAttribute(subscriberList, []Subscriber{}) },
		func() (dex.InitialAttributeDef, error) { return dex.InitialAttribute(subscriberCount, int64(0)) },
		func() (dex.InitialAttributeDef, error) { return dex.InitialAttribute(recentSubscribers, []string{}) },
		func() (dex.InitialAttributeDef, error) { return dex.InitialAttribute(lastChange, "List created") },
	} {
		definition, err := build()
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

type AddResult struct {
	AlreadySubscribed bool `json:"alreadySubscribed"`
	ListFull          bool `json:"listFull"`
}

type AddInput struct {
	Address string `json:"address"`
}

// AddNewsletterSubscriber adds one canonical address; callers canonicalize it first.
func (Flow) AddNewsletterSubscriber(ctx dex.Context, input AddInput) (*dex.RPCResult[AddResult], error) {
	address, err := CanonicalAddress(input.Address)
	if err != nil {
		return nil, err
	}
	list, err := subscriberList.Get(ctx)
	if err != nil {
		return nil, err
	}
	if contains(list, address) {
		return &dex.RPCResult[AddResult]{Output: AddResult{AlreadySubscribed: true}}, nil
	}
	if len(list) >= MaxSubscribers {
		return &dex.RPCResult[AddResult]{Output: AddResult{ListFull: true}}, nil
	}
	list = append(list, Subscriber{Address: address, SubscribedAt: time.Now().UTC()})
	if err := subscriberList.Set(ctx, list); err != nil {
		return nil, err
	}
	if err := subscriberCount.Set(ctx, int64(len(list))); err != nil {
		return nil, err
	}
	if err := recentSubscribers.Set(ctx, recent(list)); err != nil {
		return nil, err
	}
	if err := lastChange.Set(ctx, "Subscribed "+address); err != nil {
		return nil, err
	}
	return &dex.RPCResult[AddResult]{Output: AddResult{}}, nil
}

type RemoveResult struct {
	Removed bool `json:"removed"`
}

type RemoveInput struct {
	Address string `json:"address"`
}

// RemoveNewsletterSubscriber removes one address after the caller verified its unsubscribe link.
func (Flow) RemoveNewsletterSubscriber(ctx dex.Context, input RemoveInput) (*dex.RPCResult[RemoveResult], error) {
	address, err := CanonicalAddress(input.Address)
	if err != nil {
		return nil, err
	}
	list, err := subscriberList.Get(ctx)
	if err != nil {
		return nil, err
	}
	next, removed := without(list, address)
	if !removed {
		return &dex.RPCResult[RemoveResult]{Output: RemoveResult{}}, nil
	}
	if err := subscriberList.Set(ctx, next); err != nil {
		return nil, err
	}
	if err := subscriberCount.Set(ctx, int64(len(next))); err != nil {
		return nil, err
	}
	if err := recentSubscribers.Set(ctx, recent(next)); err != nil {
		return nil, err
	}
	if err := lastChange.Set(ctx, "Unsubscribed "+address); err != nil {
		return nil, err
	}
	return &dex.RPCResult[RemoveResult]{Output: RemoveResult{Removed: true}}, nil
}

// ListNewsletterSubscribers returns every address in subscription order.
func (Flow) ListNewsletterSubscribers(ctx dex.Context, _ dex.None) (*dex.RPCResult[[]string], error) {
	list, err := subscriberList.Get(ctx)
	if err != nil {
		return nil, err
	}
	addresses := make([]string, 0, len(list))
	for _, subscriber := range list {
		addresses = append(addresses, subscriber.Address)
	}
	return &dex.RPCResult[[]string]{Output: addresses}, nil
}

type RequestSubscriberRemovalInput struct {
	Address string `json:"address"`
}

// RequestSubscriberRemoval queues a removal; HoldSubscriberList applies it under the list lock,
// because Dex Web invokes Actions without their registered locks (superdurable/dex#562).
//
// dex:input field-name:address value-type:string source:user required:true description:"Email address to remove"
func (Flow) RequestSubscriberRemoval(ctx dex.Context, input RequestSubscriberRemovalInput) (*dex.RPCResult[dex.None], error) {
	status, err := listStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != statusOpen {
		return nil, errors.New("the subscriber list is not open")
	}
	address, err := CanonicalAddress(input.Address)
	if err != nil {
		return nil, err
	}
	if err := removalRequests.Publish(ctx, RemovalRequest{Address: address}); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:field attribute-key:subscriber-count value-type:int64 editable:false description:"Subscribers"
func (Flow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	count, err := subscriberCount.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"subscriber-count": count,
	}}, nil
}

// dex:field attribute-key:subscriber-count value-type:int64 editable:false description:"Subscribers" ui-slot:title
// dex:field attribute-key:subscriber-list-status value-type:string editable:false description:"Status" ui-slot:status
// dex:field attribute-key:last-subscriber-change value-type:string editable:false description:"Last change"
// dex:field attribute-key:recent-subscribers value-type:string-array editable:false description:"Newest subscribers (up to 20)"
func (Flow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	count, err := subscriberCount.Get(ctx)
	if err != nil {
		return nil, err
	}
	status, err := listStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	change, err := lastChange.Get(ctx)
	if err != nil {
		return nil, err
	}
	newest, err := recentSubscribers.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"subscriber-count":       count,
		"subscriber-list-status": status,
		"last-subscriber-change": change,
		"recent-subscribers":     newest,
	}}, nil
}

func contains(list []Subscriber, address string) bool {
	for _, subscriber := range list {
		if subscriber.Address == address {
			return true
		}
	}
	return false
}

func without(list []Subscriber, address string) ([]Subscriber, bool) {
	next := make([]Subscriber, 0, len(list))
	for _, subscriber := range list {
		if subscriber.Address != address {
			next = append(next, subscriber)
		}
	}
	return next, len(next) != len(list)
}

func recent(list []Subscriber) []string {
	sorted := append([]Subscriber(nil), list...)
	sort.SliceStable(sorted, func(left, right int) bool { return sorted[left].SubscribedAt.After(sorted[right].SubscribedAt) })
	addresses := []string{}
	for _, subscriber := range sorted {
		if len(addresses) == recentShown {
			break
		}
		addresses = append(addresses, subscriber.Address)
	}
	return addresses
}

func isMissing(err error) bool {
	var missing *dex.AttributeNotFoundError
	return errors.As(err, &missing)
}

var _ dex.Flow = Flow{}
var _ dex.Step[dex.None] = HoldSubscriberList{}
