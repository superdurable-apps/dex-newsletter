// Package api implements the application's OpenAPI contract.
//
// The product uses Dex Web v2 as its only process-management surface, so this
// API intentionally exposes only non-business application identity for the
// Hello World page. Do not add process-management routes here.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
)

// ApplicationInfo is the non-business identity returned by GetApplicationInfo.
type ApplicationInfo struct {
	// Name is the human-readable application name.
	Name string
	// DexWebURL is the optional Dex Web v2 address where the process is managed.
	DexWebURL string
}

// Handler implements generated.Handler.
type Handler struct{ info ApplicationInfo }

var _ generated.Handler = (*Handler)(nil)

// NewHandler returns the OpenAPI server with JSON 404, 405, and error responses.
func NewHandler(info ApplicationInfo) (*generated.Server, error) {
	return generated.NewServer(&Handler{info: info},
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

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
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
	_ = json.NewEncoder(w).Encode(errorResponse{Error: code, Message: message})
}
