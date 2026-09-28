package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/api"
	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
)

// fakeSubscriptions records Subscribe and Unsubscribe calls and returns
// scripted results.
type fakeSubscriptions struct {
	calls            []string
	email            string
	err              error
	unsubscribeCalls []string
	unsubscribeErr   error
}

func (fake *fakeSubscriptions) Subscribe(_ context.Context, email string) (string, error) {
	fake.calls = append(fake.calls, email)
	return fake.email, fake.err
}

func (fake *fakeSubscriptions) Unsubscribe(_ context.Context, token string) error {
	fake.unsubscribeCalls = append(fake.unsubscribeCalls, token)
	return fake.unsubscribeErr
}

func newTestServer(t *testing.T, info api.ApplicationInfo) *httptest.Server {
	t.Helper()
	return newSubscriptionTestServer(t, info, &fakeSubscriptions{})
}

func newSubscriptionTestServer(t *testing.T, info api.ApplicationInfo, subscriptions api.NewsletterSubscriptions) *httptest.Server {
	t.Helper()
	handler, err := api.NewHandler(info, subscriptions)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestGetApplicationInfoThroughGeneratedClient(t *testing.T) {
	server := newTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog", DexWebURL: "http://127.0.0.1:8802"})
	client, err := generated.NewClient(server.URL, generated.WithClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	info, err := client.GetApplicationInfo(context.Background())
	if err != nil {
		t.Fatalf("GetApplicationInfo: %v", err)
	}
	if info.Name != "Dex Tech Blog" {
		t.Errorf("name = %q, want %q", info.Name, "Dex Tech Blog")
	}
	if url, ok := info.DexWebUrl.Get(); !ok || url != "http://127.0.0.1:8802" {
		t.Errorf("dexWebUrl = %q (set=%v), want %q", url, ok, "http://127.0.0.1:8802")
	}
}

func TestGetApplicationInfoOmitsUnconfiguredDexWebURL(t *testing.T) {
	server := newTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"})
	response, err := server.Client().Get(server.URL + "/api/application-info")
	if err != nil {
		t.Fatalf("GET application info: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["name"] != "Dex Tech Blog" {
		t.Errorf("name = %v, want %q", body["name"], "Dex Tech Blog")
	}
	if _, present := body["dexWebUrl"]; present {
		t.Errorf("dexWebUrl must be omitted when unconfigured: %v", body)
	}
}

func TestNoProcessManagementRoutes(t *testing.T) {
	server := newTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"})
	cases := []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodPost, "/api/flows", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/api/flows/x", http.StatusNotFound, "not_found"},
		{http.MethodPost, "/api/flows/x/approvals", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/api/health", http.StatusNotFound, "not_found"},
		{http.MethodPost, "/api/application-info", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/api/newsletter/subscriptions", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/api/newsletter/subscriptions/someone@example.com", http.StatusNotFound, "not_found"},
		{http.MethodDelete, "/api/newsletter/subscriptions", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/api/newsletter/unsubscriptions", http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request, err := http.NewRequest(tc.method, server.URL+tc.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
			if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
				t.Errorf("content type = %q, want application/json", contentType)
			}
			var body struct{ Error, Message string }
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error != tc.code || body.Message == "" {
				t.Errorf("error body = %+v, want code %q with a message", body, tc.code)
			}
			if tc.status == http.StatusMethodNotAllowed && response.Header.Get("Allow") == "" {
				t.Error("405 responses must carry an Allow header")
			}
		})
	}
}

func TestNewHandlerRequiresSubscriptions(t *testing.T) {
	if _, err := api.NewHandler(api.ApplicationInfo{Name: "Dex Tech Blog"}, nil); err == nil {
		t.Fatal("NewHandler accepted nil subscriptions")
	}
}

func TestSubscribeToNewsletterThroughGeneratedClient(t *testing.T) {
	subscriptions := &fakeSubscriptions{email: "reader@example.com"}
	server := newSubscriptionTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"}, subscriptions)
	client, err := generated.NewClient(server.URL, generated.WithClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := client.SubscribeToNewsletter(context.Background(), &generated.NewsletterSubscriptionRequest{Email: " Reader@Example.com "})
	if err != nil {
		t.Fatalf("SubscribeToNewsletter: %v", err)
	}
	subscription, ok := response.(*generated.NewsletterSubscription)
	if !ok || subscription.Email != "reader@example.com" {
		t.Fatalf("response = %#v, want the canonical subscription", response)
	}
	if len(subscriptions.calls) != 1 || subscriptions.calls[0] != " Reader@Example.com " {
		t.Fatalf("Subscribe calls = %q, want the submitted address once", subscriptions.calls)
	}
}

func TestSubscribeToNewsletterMapsOutcomesToStatuses(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid address", api.ErrInvalidEmailAddress, http.StatusBadRequest, "invalid_email"},
		{"wrapped invalid address", errors.Join(errors.New("rpc"), api.ErrInvalidEmailAddress), http.StatusBadRequest, "invalid_email"},
		{"list full", api.ErrSubscriberListFull, http.StatusConflict, "subscriber_list_full"},
		{"Dex unavailable", errors.New("connection refused"), http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newSubscriptionTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"}, &fakeSubscriptions{err: tc.err})
			response := postSubscription(t, server, `{"email":"reader@example.com"}`)
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
			var body struct{ Error, Message string }
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error != tc.code || body.Message == "" {
				t.Errorf("error body = %+v, want code %q with a message", body, tc.code)
			}
			if strings.Contains(body.Message, "refused") {
				t.Errorf("error message leaks the internal cause: %q", body.Message)
			}
		})
	}
}

func TestSubscribeToNewsletterRejectsRequestsOutsideTheContract(t *testing.T) {
	for name, body := range map[string]string{
		"missing email":    `{}`,
		"empty email":      `{"email":""}`,
		"extra field":      `{"email":"reader@example.com","name":"Reader"}`,
		"overlong email":   `{"email":"` + strings.Repeat("a", 250) + `@example.com"}`,
		"not JSON":         `email=reader@example.com`,
		"non-string email": `{"email":42}`,
	} {
		t.Run(name, func(t *testing.T) {
			subscriptions := &fakeSubscriptions{email: "reader@example.com"}
			server := newSubscriptionTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"}, subscriptions)
			response := postSubscription(t, server, body)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
			if len(subscriptions.calls) != 0 {
				t.Fatalf("Subscribe was called for a request outside the contract: %q", subscriptions.calls)
			}
		})
	}
}

func postSubscription(t *testing.T, server *httptest.Server, body string) *http.Response {
	t.Helper()
	response, err := server.Client().Post(server.URL+"/api/newsletter/subscriptions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST subscription: %v", err)
	}
	return response
}

const testToken = "Ab0-_Ab0-_Ab0-_Ab0-_Ab"

func TestUnsubscribeFromNewsletterThroughGeneratedClient(t *testing.T) {
	subscriptions := &fakeSubscriptions{}
	server := newSubscriptionTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"}, subscriptions)
	client, err := generated.NewClient(server.URL, generated.WithClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := client.UnsubscribeFromNewsletter(context.Background(), &generated.NewsletterUnsubscriptionRequest{Token: testToken})
	if err != nil {
		t.Fatalf("UnsubscribeFromNewsletter: %v", err)
	}
	if unsubscribed, ok := response.(*generated.NewsletterUnsubscription); !ok || unsubscribed.Status != generated.NewsletterUnsubscriptionStatusUnsubscribed {
		t.Fatalf("response = %#v, want status unsubscribed", response)
	}
	if len(subscriptions.unsubscribeCalls) != 1 || subscriptions.unsubscribeCalls[0] != testToken {
		t.Fatalf("Unsubscribe calls = %q, want the token once", subscriptions.unsubscribeCalls)
	}
}

func TestUnsubscribeFromNewsletterReportsAnUnavailableListWithoutTheCause(t *testing.T) {
	server := newSubscriptionTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"}, &fakeSubscriptions{unsubscribeErr: errors.New("connection refused")})
	response := postJSON(t, server, "/api/newsletter/unsubscriptions", `{"token":"`+testToken+`"}`)
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
	var body struct{ Error, Message string }
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != "unavailable" || body.Message == "" || strings.Contains(body.Message, "refused") {
		t.Fatalf("error body = %+v", body)
	}
}

func TestUnsubscribeFromNewsletterRejectsMalformedTokens(t *testing.T) {
	for name, body := range map[string]string{
		"missing token": `{}`,
		"short token":   `{"token":"abc"}`,
		"long token":    `{"token":"` + testToken + `x"}`,
		"bad character": `{"token":"` + testToken[:21] + `="}`,
		"extra field":   `{"token":"` + testToken + `","email":"reader@example.com"}`,
		"not JSON":      `token=` + testToken,
	} {
		t.Run(name, func(t *testing.T) {
			subscriptions := &fakeSubscriptions{}
			server := newSubscriptionTestServer(t, api.ApplicationInfo{Name: "Dex Tech Blog"}, subscriptions)
			response := postJSON(t, server, "/api/newsletter/unsubscriptions", body)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
			if len(subscriptions.unsubscribeCalls) != 0 {
				t.Fatalf("Unsubscribe was called for a request outside the contract: %q", subscriptions.unsubscribeCalls)
			}
		})
	}
}

func postJSON(t *testing.T, server *httptest.Server, path string, body string) *http.Response {
	t.Helper()
	response, err := server.Client().Post(server.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return response
}
