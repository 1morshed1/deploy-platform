// Command api is the platform's HTTP API server. It accepts deploy requests,
// authenticates tenants, rate-limits, persists deployments, enqueues builds, and
// streams build logs. It does NO slow or privileged work itself.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/1morshed1/deploy-platform/internal/api"
	"github.com/1morshed1/deploy-platform/internal/config"
	"github.com/1morshed1/deploy-platform/internal/github"
	"github.com/1morshed1/deploy-platform/internal/queue"
	"github.com/1morshed1/deploy-platform/internal/store"
)

func main() {
	// Structured JSON logging to stdout: the 12-factor way. Kubernetes collects
	// stdout and ships it to Loki; JSON makes it queryable.
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// A single background context cancelled on SIGINT/SIGTERM drives graceful
	// shutdown of every component.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Persistence.
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	// Producer side of the queue + the rate limiter (both talk to Redis).
	q := queue.NewClient(cfg.RedisAddr, cfg.RedisPassword)
	defer q.Close()

	limiter := api.NewRateLimiter(cfg.RedisAddr, cfg.RedisPassword, cfg.RateLimitPerHour)
	defer limiter.Close()

	detector := github.NewDetector(cfg.GitHubToken)

	srv := api.NewServer(cfg, st, detector, q, limiter, log)
	httpServer := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: srv.Routes(),
	}

	// Run the HTTP server in a goroutine so main can wait for a shutdown signal.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Wait for either a fatal server error or a shutdown signal.
	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	// Graceful shutdown: stop accepting new connections, let in-flight requests
	// finish within the timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}
