package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"temporalpayments/internal/stubservice"
)

func main() {
	logger := log.New(os.Stdout, "stub-service ", log.LstdFlags|log.Lmicroseconds)
	statePath := envOrDefault("STATE_PATH", "/data/state.json")
	captureMode := envOrDefault("CAPTURE_MODE", "success")
	captureDelay, err := time.ParseDuration(envOrDefault("CAPTURE_DELAY", "35s"))
	if err != nil {
		logger.Fatalf("invalid CAPTURE_DELAY: %v", err)
	}

	store, err := stubservice.NewStore(statePath)
	if err != nil {
		logger.Fatalf("create persistent store: %v", err)
	}
	handler := stubservice.NewServer(store, captureMode, captureDelay, logger).Handler()
	server := &http.Server{
		Addr:              ":8081",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Printf("listening on %s capture_mode=%s", server.Addr, captureMode)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("serve: %v", err)
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Printf("shutdown: %v", err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
