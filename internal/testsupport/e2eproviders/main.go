// Command e2eproviders serves the fake Slack, GitHub, Gemini, and Gmail APIs for the browser
// end-to-end tests and writes a Dex Web connection store that points every connection at them.
// It serves until SIGINT or SIGTERM. It is test support only and never linked into the application.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/superdurable-apps/dex-newsletter/internal/testsupport/fakeproviders"
)

func main() {
	address := flag.String("address", "127.0.0.1:0", "listen address; port 0 picks a free loopback port")
	connectionsDirectory := flag.String("connections-dir", "", "directory that receives connections.json (required)")
	urlFile := flag.String("url-file", "", "file that receives the fake's base URL once it serves (required)")
	flag.Parse()
	if err := run(*address, *connectionsDirectory, *urlFile); err != nil {
		fmt.Fprintln(os.Stderr, "e2eproviders:", err)
		os.Exit(1)
	}
}

func run(address, connectionsDirectory, urlFile string) error {
	if connectionsDirectory == "" || urlFile == "" {
		return errors.New("-connections-dir and -url-file are required")
	}
	fake, err := fakeproviders.Start(address)
	if err != nil {
		return err
	}
	defer fake.Close()
	if err := os.MkdirAll(connectionsDirectory, 0o700); err != nil {
		return err
	}
	if _, err := fakeproviders.WriteConnectionStore(connectionsDirectory, fake.URL); err != nil {
		return fmt.Errorf("write the connection store: %w", err)
	}
	// The URL file appears last and whole, so a waiting script sees a serving fake and a complete store.
	if err := writeFileAtomically(urlFile, []byte(fake.URL+"\n")); err != nil {
		return err
	}
	fmt.Println(fake.URL)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}

func writeFileAtomically(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".e2eproviders-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(contents); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
