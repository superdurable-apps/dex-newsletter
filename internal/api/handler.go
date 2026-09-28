// Package api implements the application's OpenAPI contract.
//
// Dex Web v2 remains the only process-management surface. The API exposes the
// application identity for the home page and one reader-facing operation that
// adds an address to the newsletter subscriber list. Do not add
// process-management routes here.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
)

var (
	// ErrInvalidEmailAddress reports an address the subscriber list rejects.
	ErrInvalidEmailAddress = errors.New("email address is not a single, deliverable address")
	// ErrSubscriberListFull reports that the subscriber list is at capacity.
	ErrSubscriberListFull = errors.New("newsletter subscriber list is full")
)

// NewsletterSubscriptions adds addresses to the newsletter subscriber list.
type NewsletterSubscriptions interface {
	// Subscribe adds the address and returns its canonical form. Adding an
	// address that is already subscribed succeeds. It returns
	// ErrInvalidEmailAddress or ErrSubscriberListFull for a rejected address
	// and any other error when the list cannot be reached.
	Subscribe(ctx context.Context, email string) (string, error)
}

// ApplicationInfo is the non-business identity returned by GetApplicationInfo.
type ApplicationInfo struct {
	// Name is the human-readable application name.
	Name string
	// DexWebURL is the optional Dex Web v2 address where the process is managed.
	DexWebURL string
}

// Handler implements generated.Handler.
type Handler struct {
	info          ApplicationInfo
	subscriptions NewsletterSubscriptions
}

var _ generated.Handler = (*Handler)(nil)

// NewHandler returns the OpenAPI server with JSON 404, 405, and error responses.
func NewHandler(info ApplicationInfo, subscriptions NewsletterSubscriptions) (*generated.Server, error) {
	if subscriptions == nil {
		return nil, errors.New("newsletter subscriptions are required")
	}
	return generated.NewServer(&Handler{info: info, subscriptions: subscriptions},
		generated.WithErrorHandler(writeGeneratedError),
		generated.WithNotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "unknown API route")
		}),
		generated.WithMethodNotAllowed(func(w http.ResponseWriter, _ *http.Request, allowed string) {
			w.Header().Set("Allow", allowed)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		}),
	)
}

// GetApplicationInfo returns the application name and, when configured, the Dex Web URL.
func (handler *Handler) GetApplicationInfo(context.Context) (*generated.ApplicationInfo, error) {
	response := &generated.ApplicationInfo{Name: handler.info.Name}
	if handler.info.DexWebURL != "" {
		response.DexWebUrl = generated.NewOptString(handler.info.DexWebURL)
	}
	return response, nil
}

// SubscribeToNewsletter adds the requested address to the subscriber list.
func (handler *Handler) SubscribeToNewsletter(ctx context.Context, request *generated.NewsletterSubscriptionRequest) (generated.SubscribeToNewsletterRes, error) {
	email, err := handler.subscriptions.Subscribe(ctx, request.Email)
	switch {
	case err == nil:
		return &generated.NewsletterSubscription{Email: email}, nil
	case errors.Is(err, ErrInvalidEmailAddress):
		return &generated.SubscribeToNewsletterBadRequest{Error: "invalid_email", Message: "Enter a single email address, such as name@example.com."}, nil
	case errors.Is(err, ErrSubscriberListFull):
		return &generated.SubscribeToNewsletterConflict{Error: "subscriber_list_full", Message: "The newsletter is not accepting new subscribers right now."}, nil
	default:
		slog.WarnContext(ctx, "newsletter subscription failed", "error", err)
		return &generated.SubscribeToNewsletterServiceUnavailable{Error: "unavailable", Message: "Subscriptions are unavailable right now. Try again in a minute."}, nil
	}
}

func writeGeneratedError(_ context.Context, w http.ResponseWriter, _ *http.Request, err error) {
	if status := ogenerrors.ErrorCode(err); status >= 400 && status < 500 {
		writeError(w, status, "invalid_request", "request does not match the OpenAPI contract")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.Error{Error: code, Message: message})
}
