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
	"strings"
	"syscall"
	"time"

	api "github.com/superdurable-apps/dex-newsletter/internal/api"
	appRuntime "github.com/superdurable-apps/dex-newsletter/internal/runtime"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(os.Getenv("LOG_LEVEL"))
	// The Dex SDK and connectors that log to slog.Default() share the handler.
	slog.SetDefault(logger)
	runtime, err := appRuntime.New(logger)
	if err != nil {
		return err
	}
	defer runtime.Close()
	apiHandler, err := api.NewHandler(api.ApplicationInfo{Name: runtime.ApplicationName(), DexWebURL: runtime.DexWebURL()})
	if err != nil {
		return fmt.Errorf("create OpenAPI handler: %w", err)
	}
	server := &http.Server{Addr: ":" + environment("PORT", "8080"), Handler: applicationHandler(apiHandler), ReadHeaderTimeout: 5 * time.Second}
	workerResult := runtime.StartWorker()
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.ListenAndServe() }()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-workerResult:
		if err != nil {
			return fmt.Errorf("run Dex Worker: %w", err)
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

func applicationHandler(apiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler)
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

// newLogger writes JSON records at LOG_LEVEL (debug, info, warn, or error;
// info by default). LOG_LEVEL=debug shows every Slack event the request
// Trigger ignores and why.
func newLogger(levelName string) *slog.Logger {
	level := slog.LevelInfo
	invalid := strings.TrimSpace(levelName) != "" && level.UnmarshalText([]byte(strings.TrimSpace(levelName))) != nil
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	if invalid {
		logger.Warn("LOG_LEVEL is not debug, info, warn, or error; using info", "log_level", levelName)
	}
	return logger
}
