package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationHandlerRoutesAPIPrefixToAPIHandler(t *testing.T) {
	handler := applicationHandler(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("X-Test-Route", "api")
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, path := range []string{"/api/application-info", "/api/flows"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusTeapot || response.Header().Get("X-Test-Route") != "api" {
			t.Errorf("%s was not routed to the API handler: status %d", path, response.Code)
		}
	}
}

func TestHTTPServerBoundsSlowClients(t *testing.T) {
	server := newHTTPServer(":0", http.NotFoundHandler())
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatalf("server timeouts = header %v, read %v, idle %v; want all positive", server.ReadHeaderTimeout, server.ReadTimeout, server.IdleTimeout)
	}
}

func TestApplicationHandlerHasNoMockControls(t *testing.T) {
	handler := applicationHandler(http.NotFoundHandler())
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/__mock__/control", strings.NewReader(`{"action":"reset"}`)))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s /__mock__/control status = %d, want %d", method, response.Code, http.StatusNotFound)
		}
	}
}

func TestApplicationHandlerBoundsAPIRequestBodies(t *testing.T) {
	var readErr error
	handler := applicationHandler(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, readErr = io.ReadAll(request.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	body := strings.NewReader(`{"email":"` + strings.Repeat("a", maximumAPIRequestBytes) + `"}`)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/newsletter/subscriptions", body))
	if readErr == nil {
		t.Fatal("an API request body over the limit was read in full")
	}
}

func TestStaticHandlerServesAssetsAndFallsBackToIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "<html>shell</html>")
	writeFile(t, filepath.Join(root, "assets", "app.js"), "console.log('app')")
	handler := staticHandler(root)

	cases := []struct{ path, want string }{
		{"/", "<html>shell</html>"},
		{"/assets/app.js", "console.log('app')"},
		{"/some/client/route", "<html>shell</html>"},
		{"/assets", "<html>shell</html>"},
	}
	for _, tc := range cases {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", tc.path, response.Code, http.StatusOK)
			continue
		}
		if body, _ := io.ReadAll(response.Body); strings.TrimSpace(string(body)) != tc.want {
			t.Errorf("GET %s body = %q, want %q", tc.path, body, tc.want)
		}
	}
}

func TestStaticHandlerDoesNotServeFilesOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "dist")
	writeFile(t, filepath.Join(root, "index.html"), "<html>shell</html>")
	writeFile(t, filepath.Join(parent, "secret.txt"), "outside the web root")
	response := httptest.NewRecorder()
	staticHandler(root).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/../secret.txt", nil))
	if body, _ := io.ReadAll(response.Body); strings.Contains(string(body), "outside the web root") {
		t.Fatalf("static handler served a file outside its root: status %d", response.Code)
	}
}

func TestStaticHandlerRejectsNonReadMethods(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "<html>shell</html>")
	handler := staticHandler(root)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/", nil))
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s / status = %d, want %d", method, response.Code, http.StatusMethodNotAllowed)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/", nil))
	if response.Code != http.StatusOK {
		t.Errorf("HEAD / status = %d, want %d", response.Code, http.StatusOK)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
