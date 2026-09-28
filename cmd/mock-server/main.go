// Command mock-server serves the application OpenAPI contract from the
// in-memory mockserver package for make mock and make test-mock-e2e. It never
// starts Dex or a Worker.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/mockserver"
)

func main() {
	if err := run(); err != nil {
		slog.Error("mock server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	handler, err := mockserver.New(mockserver.NewStore())
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              environment("MOCK_API_ADDRESS", "127.0.0.1:18081"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.ListenAndServe() }()
	slog.Info("mock API listening", "address", server.Addr)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-serverResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("run mock HTTP server: %w", err)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
