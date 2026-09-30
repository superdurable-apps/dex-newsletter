package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
	"github.com/superdurable-apps/dex-newsletter/internal/subscribers"
)

// Newsletter is the subscriber operation boundary the public pages use.
type Newsletter interface {
	Subscribe(ctx context.Context, address string) (alreadySubscribed bool, err error)
	Unsubscribe(ctx context.Context, address, token string) (removed bool, err error)
}

// ApplicationInfo is what the reader pages show about the publication.
type ApplicationInfo struct {
	Name      string
	DexWebURL string
}

type Handler struct {
	newsletter Newsletter
	drafts     Drafts
	info       ApplicationInfo
	logger     *slog.Logger
}

func NewHandler(newsletter Newsletter, drafts Drafts, info ApplicationInfo, logger *slog.Logger) (*generated.Server, error) {
	handler := &Handler{newsletter: newsletter, drafts: drafts, info: info, logger: logger}
	return generated.NewServer(handler,
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

func (handler *Handler) GetHealth(context.Context) (*generated.HealthResponse, error) {
	return &generated.HealthResponse{Status: generated.HealthResponseStatusOk}, nil
}

func (handler *Handler) GetApplicationInfo(context.Context) (*generated.ApplicationInfo, error) {
	info := &generated.ApplicationInfo{Name: handler.info.Name}
	if handler.info.DexWebURL != "" {
		info.DexWebUrl = generated.NewOptString(handler.info.DexWebURL)
	}
	return info, nil
}

func (handler *Handler) SubscribeToNewsletter(ctx context.Context, request *generated.SubscriptionRequest) (generated.SubscribeToNewsletterRes, error) {
	already, err := handler.newsletter.Subscribe(ctx, request.Email)
	switch {
	case err == nil && already:
		return &generated.SubscriptionResponse{Status: generated.SubscriptionResponseStatusAlreadySubscribed}, nil
	case err == nil:
		return &generated.SubscriptionResponse{Status: generated.SubscriptionResponseStatusSubscribed}, nil
	case errors.Is(err, subscribers.ErrInvalidAddress):
		response := generated.SubscribeToNewsletterBadRequest(errorResponse("invalid_email", subscribers.ErrInvalidAddress.Error()))
		return &response, nil
	case errors.Is(err, subscribers.ErrListFull):
		response := generated.SubscribeToNewsletterConflict(errorResponse("list_full", "The newsletter is not taking new subscribers right now."))
		return &response, nil
	}
	handler.logger.Error("subscribe failed", "error", err.Error())
	response := generated.SubscribeToNewsletterServiceUnavailable(errorResponse("subscription_unavailable", "Subscribing is unavailable right now. Try again in a moment."))
	return &response, nil
}

func (handler *Handler) UnsubscribeFromNewsletter(ctx context.Context, request *generated.UnsubscriptionRequest) (generated.UnsubscribeFromNewsletterRes, error) {
	removed, err := handler.newsletter.Unsubscribe(ctx, request.Email, request.Token)
	switch {
	case err == nil && removed:
		return &generated.UnsubscriptionResponse{Status: generated.UnsubscriptionResponseStatusUnsubscribed}, nil
	case err == nil:
		return &generated.UnsubscriptionResponse{Status: generated.UnsubscriptionResponseStatusNotSubscribed}, nil
	case errors.Is(err, subscribers.ErrInvalidLink):
		response := generated.UnsubscribeFromNewsletterBadRequest(errorResponse("invalid_link", "This unsubscribe link is invalid. Use the link from your newsletter email."))
		return &response, nil
	}
	handler.logger.Error("unsubscribe failed", "error", err.Error())
	response := generated.UnsubscribeFromNewsletterServiceUnavailable(errorResponse("unsubscription_unavailable", "Unsubscribing is unavailable right now. Try the link again in a moment."))
	return &response, nil
}

func errorResponse(code, message string) generated.ErrorResponse {
	return generated.ErrorResponse{Error: code, Message: message}
}

func writeGeneratedError(_ context.Context, w http.ResponseWriter, _ *http.Request, _ error) {
	writeError(w, http.StatusBadRequest, "invalid_request", "request does not match the OpenAPI contract")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.ErrorResponse{Error: code, Message: message})
}
