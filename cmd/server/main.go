package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/api"
	"github.com/superdurable-apps/dex-newsletter/internal/application"
	"github.com/superdurable-apps/dex-newsletter/internal/config"

	llmrouter "github.com/superdurable/dex-connectors-library/connectors/superdurable/llm"

	"github.com/superdurable/dex-connectors-library/sdkgo/localconfig"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	if configuration.Newsletter.IsLoopback() {
		logger.Warn("newsletter.publicBaseUrl is a loopback address, so unsubscribe links only work on this machine", "url", configuration.Newsletter.PublicBaseURL)
	}
	store, err := localconfig.LoadFromEnvironment()
	if err != nil {
		return err
	}
	app, err := application.New(application.Options{
		Logger: logger, Config: configuration, Store: store,
		FlowServiceAddress: environment("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
		WorkerBindAddress:  environment("DEX_WORKER_BIND_ADDRESS", "127.0.0.1:8811"),
		WorkerTarget:       environment("DEX_WORKER_TARGET", "127.0.0.1:8811"),
		BlobCacheDirectory: environment("DEX_BLOB_CACHE_DIR", filepath.Join(os.TempDir(), "dex-blog-newsletter-blobs")),
		// SLACK_TRIGGER=off serves only the reader pages, for E2E tests and local work without Slack.
		WithoutSlackTrigger: os.Getenv("SLACK_TRIGGER") == "off",
		LLMOptions:          testModelOptions(),
	})
	if err != nil {
		return err
	}
	defer app.Close()
	apiHandler, err := api.NewHandler(app.Subscribers, app.Drafts, api.ApplicationInfo{Name: configuration.Blog.PublicationName, DexWebURL: configuration.DexWebURL}, logger)
	if err != nil {
		return fmt.Errorf("create OpenAPI handler: %w", err)
	}
	server := &http.Server{Addr: environment("BIND_ADDRESS", "127.0.0.1") + ":" + environment("PORT", "8080"), Handler: applicationHandler(apiHandler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	appResult := make(chan error, 1)
	go func() { appResult <- app.Run(ctx) }()
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.ListenAndServe() }()
	logger.Info("application started", "address", server.Addr)
	select {
	case <-ctx.Done():
	case err := <-appResult:
		if err != nil {
			return fmt.Errorf("run Dex application: %w", err)
		}
	case err := <-serverResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("run HTTP server: %w", err)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

// testModelOptions sends Gemini calls to BLOG_NEWSLETTER_TEST_GEMINI_BASE_URL for E2E tests; the
// connector accepts only a loopback URL there, so this cannot redirect a real key elsewhere.
func testModelOptions() []llmrouter.Option {
	if baseURL := os.Getenv("BLOG_NEWSLETTER_TEST_GEMINI_BASE_URL"); baseURL != "" {
		return []llmrouter.Option{llmrouter.WithProviderBaseURLForTest(llmrouter.ProviderGemini, baseURL)}
	}
	return nil
}

// maxAPIRequestBytes bounds every API body; a full draft is well under 1 MiB.
const maxAPIRequestBytes = 1 << 20

func applicationHandler(apiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(w, request.Body, maxAPIRequestBytes)
		apiHandler.ServeHTTP(w, request)
	}))
	mux.Handle("/", staticHandler("web/dist"))
	return mux
}

func staticHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		asset := filepath.Join(root, filepath.Clean(request.URL.Path))
		if request.URL.Path != "/" {
			if info, err := os.Stat(asset); err == nil && !info.IsDir() {
				files.ServeHTTP(w, request)
				return
			}
		}
		http.ServeFile(w, request, filepath.Join(root, "index.html"))
	})
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
