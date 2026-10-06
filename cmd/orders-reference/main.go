// Command orders-reference runs only the trusted disposable Orders fixture.
package main

import (
	"context"
	"errors"
	"fmt"
	ref "github.com/yashok111/mocker/internal/ordersreference"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Populated by the manifest builder. An unmanifested binary refuses startup.
var buildHash, sourceTreeHash string

const serviceVersion = "1.0.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "orders-reference:", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := ref.ConfigFromEnv()
	if err != nil {
		return err
	}
	s, err := ref.Open(c, ref.Build{Variant: variant, ServiceVersion: serviceVersion, BuildHash: buildHash, SourceTreeHash: sourceTreeHash})
	if err != nil {
		return err
	}
	defer s.Close()
	server := &http.Server{Addr: c.Addr, Handler: s, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
