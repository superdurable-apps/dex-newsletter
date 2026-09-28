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

// StartList starts the subscriber list Flow if it is not already running.
func (listClient NewsletterSubscriberListClient) StartList(ctx context.Context) error {
	client, err := listClient.dexClient()
	if err != nil {
		return err
	}
	_, err = client.StartFlow(ctx, listClient.flow, listClient.flowID, nil, NewsletterSubscriberListStartOptions())
	return err
}

// AddNewsletterSubscriber adds one address. While a concurrent subscription
// holds the list lock it retries with jittered backoff until ctx ends, so the
// caller's deadline bounds the wait. A retried call is harmless: an address
// that is already on the list is reported as already-subscribed.
func (listClient NewsletterSubscriberListClient) AddNewsletterSubscriber(ctx context.Context, email string) (AddNewsletterSubscriberResult, error) {
	client, err := listClient.dexClient()
	if err != nil {
		return AddNewsletterSubscriberResult{}, err
	}
	delay := 20 * time.Millisecond
	for {
		var result AddNewsletterSubscriberResult
		err := client.InvokeRPC(ctx, listClient.flowID, listClient.flow.AddNewsletterSubscriber, email, &result)
		var conflict *dex.RPCLockConflictError
		if err == nil || !errors.As(err, &conflict) {
			return result, err
		}
		timer := time.NewTimer(delay/2 + rand.N(delay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return AddNewsletterSubscriberResult{}, errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		delay = min(2*delay, maximumLockConflictDelay)
	}
}

// ListNewsletterSubscribers returns the current subscriber addresses.
func (listClient NewsletterSubscriberListClient) ListNewsletterSubscribers(ctx context.Context) ([]string, error) {
	client, err := listClient.dexClient()
	if err != nil {
		return nil, err
	}
	var addresses []string
	if err := client.InvokeRPC(ctx, listClient.flowID, listClient.flow.ListNewsletterSubscribers, nil, &addresses); err != nil {
		return nil, err
	}
	return addresses, nil
}

func (listClient NewsletterSubscriberListClient) dexClient() (*dex.Client, error) {
	client := listClient.client()
	if client == nil {
		return nil, errors.New("the Dex Client is not available yet")
	}
	return client, nil
}
