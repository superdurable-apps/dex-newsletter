package techblog

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// maximumLockConflictDelay caps the pause between AddNewsletterSubscriber
// attempts while a concurrent subscription holds the list lock.
const maximumLockConflictDelay = 500 * time.Millisecond

// NewsletterSubscriberListClient invokes the typed RPCs of one subscriber list
// Flow. The Dex Client is resolved on each call because it is created after
// the Flows are registered.
type NewsletterSubscriberListClient struct {
	flow   *NewsletterSubscriberListFlow
	flowID string
	client func() *dex.Client
}

var _ SubscriberListReader = NewsletterSubscriberListClient{}

// NewNewsletterSubscriberListClient returns a client for the list Flow with
// flowID; the application uses NewsletterSubscriberListFlowID.
func NewNewsletterSubscriberListClient(flow *NewsletterSubscriberListFlow, flowID string, client func() *dex.Client) NewsletterSubscriberListClient {
	if flow == nil || flowID == "" || client == nil {
		panic("newsletter subscriber list client requires the list Flow, its Flow ID, and a Dex Client")
	}
	return NewsletterSubscriberListClient{flow: flow, flowID: flowID, client: client}
}

// StartList starts the subscriber list Flow unless it already exists. An
// existing list, open or stopped, is success: it was started earlier, perhaps
// by an older build with another start request ID.
func (listClient NewsletterSubscriberListClient) StartList(ctx context.Context) error {
	client, err := listClient.dexClient()
	if err != nil {
		return err
	}
	_, err = client.StartFlow(ctx, listClient.flow, listClient.flowID, nil, NewsletterSubscriberListStartOptions())
	var alreadyStarted *dex.FlowAlreadyStartedError
	if errors.As(err, &alreadyStarted) {
		return nil
	}
	return err
}

// AddNewsletterSubscriber adds one address. A retried call is harmless: an
// address that is already on the list is reported as already-subscribed.
func (listClient NewsletterSubscriberListClient) AddNewsletterSubscriber(ctx context.Context, email string) (AddNewsletterSubscriberResult, error) {
	var result AddNewsletterSubscriberResult
	err := listClient.invokeLocked(ctx, listClient.flow.AddNewsletterSubscriber, email, &result)
	return result, err
}

// RemoveNewsletterSubscriber removes the subscriber a link token names. Like
// AddNewsletterSubscriber it is called from the application API, so it waits
// out a concurrent holder of the list lock until ctx ends.
func (listClient NewsletterSubscriberListClient) RemoveNewsletterSubscriber(ctx context.Context, token string) (RemoveNewsletterSubscriberResult, error) {
	var result RemoveNewsletterSubscriberResult
	err := listClient.invokeLocked(ctx, listClient.flow.RemoveNewsletterSubscriber, token, &result)
	return result, err
}

// ListNewsletterSubscribers returns the current subscriber addresses in one
// attempt. It runs inside a Step, whose retry policy owns every retry: a
// concurrent subscription holding the list lock returns dex.RetryAfter, so
// Dex records and schedules the next attempt instead of an in-memory loop.
func (listClient NewsletterSubscriberListClient) ListNewsletterSubscribers(ctx context.Context) ([]string, error) {
	client, err := listClient.dexClient()
	if err != nil {
		return nil, err
	}
	var addresses []string
	err = client.InvokeRPC(ctx, listClient.flowID, listClient.flow.ListNewsletterSubscribers, nil, &addresses)
	var conflict *dex.RPCLockConflictError
	if errors.As(err, &conflict) {
		return nil, dex.RetryAfter(time.Second, err)
	}
	if err != nil {
		return nil, err
	}
	return addresses, nil
}

// invokeLocked invokes an RPC that takes the list lock outside a Step, from
// the subscription API. While another call holds the lock it retries with
// jittered backoff until ctx ends, so the caller's deadline bounds the wait.
func (listClient NewsletterSubscriberListClient) invokeLocked(ctx context.Context, rpc any, input any, output any) error {
	client, err := listClient.dexClient()
	if err != nil {
		return err
	}
	delay := 20 * time.Millisecond
	for {
		err := client.InvokeRPC(ctx, listClient.flowID, rpc, input, output)
		var conflict *dex.RPCLockConflictError
		if err == nil || !errors.As(err, &conflict) {
			return err
		}
		timer := time.NewTimer(delay/2 + rand.N(delay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		delay = min(2*delay, maximumLockConflictDelay)
	}
}

func (listClient NewsletterSubscriberListClient) dexClient() (*dex.Client, error) {
	client := listClient.client()
	if client == nil {
		return nil, errors.New("the Dex Client is not available yet")
	}
	return client, nil
}
