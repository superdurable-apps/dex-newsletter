package mockserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := newWithWait(NewStore(), func(context.Context, time.Duration) error { return nil })
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return handler
}

func TestNewRequiresStore(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) succeeded")
	}
}

func TestServerCoversApplicationAndControlRoutes(t *testing.T) {
	handler := newTestHandler(t)

	var info struct {
		Name      string  `json:"name"`
		DexWebURL *string `json:"dexWebUrl"`
	}
	decode(t, assertStatus(t, request(t, handler, http.MethodGet, "/api/application-info", nil), http.StatusOK), &info)
	if info.Name != applicationName || info.DexWebURL != nil {
		t.Fatalf("application info = %+v", info)
	}

	var subscribed map[string]any
	decode(t, assertStatus(t, request(t, handler, http.MethodPost, "/api/newsletter/subscriptions", map[string]any{"email": " Reader@Example.com "}), http.StatusOK), &subscribed)
	if len(subscribed) != 1 || subscribed["email"] != "reader@example.com" {
		t.Fatalf("subscription = %v", subscribed)
	}
	// Re-subscribing answers with the same response.
	decode(t, assertStatus(t, request(t, handler, http.MethodPost, "/api/newsletter/subscriptions", map[string]any{"email": "reader@example.com"}), http.StatusOK), &subscribed)
	if subscribed["email"] != "reader@example.com" {
		t.Fatalf("repeated subscription = %v", subscribed)
	}

	var view ControlView
	decode(t, assertStatus(t, request(t, handler, http.MethodGet, "/__mock__/control", nil), http.StatusOK), &view)
	if view.Mode != "mock" || len(view.Subscribers) != 1 || view.Subscribers[0] != "reader@example.com" || view.FailNext {
		t.Fatalf("control view = %+v", view)
	}

	decode(t, assertStatus(t, request(t, handler, http.MethodPost, "/__mock__/control", ControlRequest{Action: "fail-next"}), http.StatusOK), &view)
	if !view.FailNext {
		t.Fatalf("fail-next view = %+v", view)
	}
	decode(t, assertStatus(t, request(t, handler, http.MethodPost, "/__mock__/control", ControlRequest{Action: "reset"}), http.StatusOK), &view)
	if len(view.Subscribers) != 0 || view.FailNext {
		t.Fatalf("reset view = %+v", view)
	}
	// Reset also discards the pending failure.
	assertStatus(t, request(t, handler, http.MethodPost, "/api/newsletter/subscriptions", map[string]any{"email": "reader@example.com"}), http.StatusOK).Body.Close()
}

func TestServerMatchesEveryApplicationResponseStatus(t *testing.T) {
	handler := newTestHandler(t)
	subscribe := func(body any) *http.Response {
		return request(t, handler, http.MethodPost, "/api/newsletter/subscriptions", body)
	}

	assertStatus(t, subscribe(map[string]any{"email": "reader@example.com"}), http.StatusOK).Body.Close()
	assertError(t, subscribe(map[string]any{"email": "reader@example"}), http.StatusBadRequest, "invalid_email", invalidEmailMessage)
	assertError(t, subscribe(map[string]any{"email": "not-an-address"}), http.StatusBadRequest, "invalid_email", invalidEmailMessage)

	assertStatus(t, request(t, handler, http.MethodPost, "/__mock__/control", ControlRequest{Action: "fail-next"}), http.StatusOK).Body.Close()
	assertError(t, subscribe(map[string]any{"email": "reader@example.com"}), http.StatusServiceUnavailable, "unavailable", unavailableMessage)
	assertStatus(t, subscribe(map[string]any{"email": "reader@example.com"}), http.StatusOK).Body.Close()

	for name, body := range map[string]any{
		"empty email":      map[string]any{"email": ""},
		"missing email":    map[string]any{},
		"unknown property": map[string]any{"email": "reader@example.com", "topics": []string{"go"}},
		"too long":         map[string]any{"email": strings.Repeat("a", 243) + "@example.com"},
		"not an object":    []string{"reader@example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			assertError(t, subscribe(body), http.StatusBadRequest, "invalid_request", "request does not match the OpenAPI contract")
		})
	}
	assertError(t, request(t, handler, http.MethodPost, "/api/newsletter/subscriptions", nil), http.StatusBadRequest, "invalid_request", "request does not match the OpenAPI contract")
}

func TestServerRejectsRoutesOutsideTheContract(t *testing.T) {
	handler := newTestHandler(t)
	cases := []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodGet, "/api/newsletter/subscriptions", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodDelete, "/api/newsletter/subscriptions", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodPost, "/api/application-info", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/api/newsletter/subscriptions/reader@example.com", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/api/flows", http.StatusNotFound, "not_found"},
		{http.MethodPost, "/api/flows", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/api/health", http.StatusNotFound, "not_found"},
		{http.MethodDelete, "/__mock__/control", http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := assertStatus(t, request(t, handler, tc.method, tc.path, map[string]any{}), tc.status)
			var body errorResponse
			decode(t, response, &body)
			if body.Error != tc.code {
				t.Fatalf("error code = %q, want %q", body.Error, tc.code)
			}
		})
	}
}

func TestServerRejectsUnknownControls(t *testing.T) {
	handler := newTestHandler(t)
	assertError(t, request(t, handler, http.MethodPost, "/__mock__/control", ControlRequest{Action: "advance"}), http.StatusBadRequest, "unknown_control", "mock control action is not supported")
	assertError(t, request(t, handler, http.MethodPost, "/__mock__/control", map[string]any{"action": "reset", "extra": true}), http.StatusBadRequest, "invalid_control", "mock control request is invalid")
	response := request(t, handler, http.MethodDelete, "/__mock__/control", nil)
	defer response.Body.Close()
	if allow := response.Header.Get("Allow"); allow != "GET, POST" {
		t.Fatalf("Allow = %q", allow)
	}
}

func TestSubscribeWaitsForTheOperationDelay(t *testing.T) {
	var waited []time.Duration
	handler, err := newWithWait(NewStore(), func(_ context.Context, duration time.Duration) error {
		waited = append(waited, duration)
		return nil
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	assertStatus(t, request(t, handler, http.MethodPost, "/api/newsletter/subscriptions", map[string]any{"email": "reader@example.com"}), http.StatusOK).Body.Close()
	if len(waited) != 1 || waited[0] != operationDelay {
		t.Fatalf("waited = %v, want [%v]", waited, operationDelay)
	}
}

func request(t *testing.T, handler http.Handler, method, url string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, url, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder.Result()
}

func decode(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		t.Fatalf("content type = %q, want application/json", response.Header.Get("Content-Type"))
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// assertStatus fails unless the response has the status and returns it for
// decoding; the caller closes the body.
func assertStatus(t *testing.T, response *http.Response, want int) *http.Response {
	t.Helper()
	if response.StatusCode != want {
		defer response.Body.Close()
		contents, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, want, contents)
	}
	return response
}

func assertError(t *testing.T, response *http.Response, status int, code, message string) {
	t.Helper()
	var body errorResponse
	decode(t, assertStatus(t, response, status), &body)
	if body.Error != code || body.Message != message {
		t.Fatalf("error = %+v, want %q %q", body, code, message)
	}
}
