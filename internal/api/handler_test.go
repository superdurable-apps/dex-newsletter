package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/api"
	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
)

func newTestServer(t *testing.T, info api.ApplicationInfo) *httptest.Server {
	t.Helper()
	handler, err := api.NewHandler(info)
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
			if tc.status == http.StatusMethodNotAllowed && response.Header.Get("Allow") != "GET" {
				t.Errorf("Allow = %q, want GET", response.Header.Get("Allow"))
			}
		})
	}
}
