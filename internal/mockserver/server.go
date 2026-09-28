// Package mockserver is the in-memory contract test double for the application
// OpenAPI contract. It backs make mock and make test-mock-e2e without Dex and
// imports only the generated server; it never reaches the Flows, the runtime,
// or a provider.
package mockserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
)

const (
	// operationDelay keeps the submitting state visible in the mock UI.
	operationDelay = 300 * time.Millisecond
	// applicationName matches the default applicationName of the real process
	// configuration.
	applicationName = "Dex Tech Blog"
	// The messages repeat the real handler's reader-facing copy.
	invalidEmailMessage = "Enter a single email address, such as name@example.com."
	unavailableMessage  = "Subscriptions are unavailable right now. Try again in a minute."
)

type waitFunc func(context.Context, time.Duration) error

// Handler implements generated.Handler over a Store.
type Handler struct {
	store *Store
	wait  waitFunc
}

var _ generated.Handler = (*Handler)(nil)

// ControlRequest is the body of POST /__mock__/control.
type ControlRequest struct {
	// Action is "reset" or "fail-next".
	Action string `json:"action"`
}

// New returns the mock OpenAPI server under /api/ plus /__mock__/control.
func New(store *Store) (http.Handler, error) {
	return newWithWait(store, waitForDuration)
}

func newWithWait(store *Store, wait waitFunc) (http.Handler, error) {
	if store == nil {
		return nil, errors.New("mock store is required")
	}
	handler := &Handler{store: store, wait: wait}
	apiServer, err := generated.NewServer(handler,
		generated.WithErrorHandler(writeGeneratedError),
		generated.WithNotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "unknown API route")
		}),
		generated.WithMethodNotAllowed(func(w http.ResponseWriter, _ *http.Request, allowed string) {
			w.Header().Set("Allow", allowed)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create mock OpenAPI server: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", apiServer)
	mux.HandleFunc("/__mock__/control", handler.handleControl)
	return mux, nil
}

// GetApplicationInfo returns the application name. The mock runs without Dex,
// so it reports no Dex Web URL.
func (handler *Handler) GetApplicationInfo(context.Context) (*generated.ApplicationInfo, error) {
	return &generated.ApplicationInfo{Name: applicationName}, nil
}

// SubscribeToNewsletter adds the requested address to the in-memory list.
func (handler *Handler) SubscribeToNewsletter(ctx context.Context, request *generated.NewsletterSubscriptionRequest) (generated.SubscribeToNewsletterRes, error) {
	if err := handler.wait(ctx, operationDelay); err != nil {
		return nil, err
	}
	email, err := handler.store.Subscribe(request.Email)
	switch {
	case err == nil:
		return &generated.NewsletterSubscription{Email: email}, nil
	case errors.Is(err, ErrInvalidEmail):
		return &generated.SubscribeToNewsletterBadRequest{Error: "invalid_email", Message: invalidEmailMessage}, nil
	case errors.Is(err, ErrInjectedFailure):
		return &generated.SubscribeToNewsletterServiceUnavailable{Error: "unavailable", Message: unavailableMessage}, nil
	default:
		return nil, err
	}
}

func (handler *Handler) handleControl(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, handler.store.Control())
		return
	}
	if request.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}
	defer request.Body.Close()
	var control ControlRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&control); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_control", "mock control request is invalid")
		return
	}
	switch control.Action {
	case "reset":
		writeJSON(w, http.StatusOK, handler.store.Reset())
	case "fail-next":
		writeJSON(w, http.StatusOK, handler.store.FailNext())
	default:
		writeError(w, http.StatusBadRequest, "unknown_control", "mock control action is not supported")
	}
}

func waitForDuration(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeGeneratedError answers contract violations the way the real handler does.
func writeGeneratedError(_ context.Context, w http.ResponseWriter, _ *http.Request, err error) {
	if status := ogenerrors.ErrorCode(err); status >= 400 && status < 500 {
		writeError(w, status, "invalid_request", "request does not match the OpenAPI contract")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
