package subscribers

import (
	"context"
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

// ErrListFull reports that MaxSubscribers is reached.
var ErrListFull = errors.New("the subscriber list is full")

// ErrInvalidLink reports an unsubscribe link whose token does not match its address.
var ErrInvalidLink = errors.New("this unsubscribe link is invalid")

// Service is the application boundary for the subscriber list Flow.
type Service struct {
	client *dex.Client
	links  UnsubscribeLinks
	flow   Flow
}

func NewService(client *dex.Client, links UnsubscribeLinks) *Service {
	return &Service{client: client, links: links}
}

// startRequestID is constant so every application start is the same logical request.
const startRequestID = "create-newsletter-subscribers"

// EnsureStarted starts the single list once; later starts attach to it.
func (service *Service) EnsureStarted(ctx context.Context) error {
	initial, err := InitialAttributes()
	if err != nil {
		return err
	}
	requestID := startRequestID
	_, err = service.client.StartFlow(ctx, service.flow, FlowID, nil, dex.StartFlowOptions{
		IDReusePolicy:  dex.IDReuseDisallow,
		RequestID:      &requestID,
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
		Attributes:     initial,
	})
	var started *dex.FlowAlreadyStartedError
	if errors.As(err, &started) {
		// An older deployment started the list with another request ID; the list is the same.
		return nil
	}
	if err != nil {
		return fmt.Errorf("start the subscriber list: %w", err)
	}
	return nil
}

// Subscribe adds address and reports whether it was already subscribed.
func (service *Service) Subscribe(ctx context.Context, raw string) (alreadySubscribed bool, err error) {
	address, err := CanonicalAddress(raw)
	if err != nil {
		return false, err
	}
	var result AddResult
	if err := service.client.InvokeRPC(ctx, FlowID, service.flow.AddNewsletterSubscriber, AddInput{Address: address}, &result); err != nil {
		return false, err
	}
	if result.ListFull {
		return false, ErrListFull
	}
	return result.AlreadySubscribed, nil
}

// Unsubscribe verifies the link token, then removes address.
func (service *Service) Unsubscribe(ctx context.Context, raw, token string) (removed bool, err error) {
	address, err := CanonicalAddress(raw)
	if err != nil || !service.links.Verify(address, token) {
		return false, ErrInvalidLink
	}
	var result RemoveResult
	if err := service.client.InvokeRPC(ctx, FlowID, service.flow.RemoveNewsletterSubscriber, RemoveInput{Address: address}, &result); err != nil {
		return false, err
	}
	return result.Removed, nil
}

// ListSubscribers returns every subscribed address.
func (service *Service) ListSubscribers(ctx context.Context) ([]string, error) {
	var addresses []string
	if err := service.client.InvokeRPC(ctx, FlowID, service.flow.ListNewsletterSubscribers, nil, &addresses); err != nil {
		return nil, err
	}
	return addresses, nil
}
