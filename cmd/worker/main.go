// Command worker is the asynchronous build/teardown processor. It consumes tasks
// from Redis (via Asynq), runs Kaniko build Jobs in Kubernetes, hands the built
// image to the operator by creating a TenantApp, and tears everything down on
// delete. It is where all the slow, privileged work happens (see ADR 0002).
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/1morshed1/deploy-platform/internal/builder"
	"github.com/1morshed1/deploy-platform/internal/config"
	pk8s "github.com/1morshed1/deploy-platform/internal/k8s"
	"github.com/1morshed1/deploy-platform/internal/queue"
	"github.com/1morshed1/deploy-platform/internal/store"
	"github.com/hibiken/asynq"
)

func main() {
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
	ctx := context.Background()

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	// The worker needs cluster access: a typed clientset for Jobs/Pods and a
	// dynamic client for the TenantApp CR. In local dev these use your kubeconfig
	// (point it at a kind cluster); in production, the pod's ServiceAccount.
	cs, err := pk8s.NewClientset()
	if err != nil {
		return err
	}
	dyn, err := pk8s.NewDynamic()
	if err != nil {
		return err
	}

	b := builder.New(cs, dyn, st, cfg, log)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr, Password: cfg.RedisPassword},
		asynq.Config{
			// Cap concurrent builds so a burst can't overwhelm the cluster (the
			// plan limits the dev environment to 3 concurrent deployments).
			Concurrency: cfg.MaxConcurrentBuilds,
			Queues:      map[string]int{queue.QueueBuilds: 1},
			Logger:      asynqLogger{log},
		},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeBuildImage, handleBuild(b, log))
	mux.HandleFunc(queue.TypeTeardownApp, handleTeardown(b, log))

	log.Info("worker starting", "concurrency", cfg.MaxConcurrentBuilds)
	// Run blocks until SIGINT/SIGTERM, then drains in-flight tasks gracefully.
	return srv.Run(mux)
}

// handleBuild processes one build task: build the image, then create the
// TenantApp so the operator deploys it.
func handleBuild(b *builder.Builder, log *slog.Logger) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		p, err := queue.ParseBuildPayload(t)
		if err != nil {
			// A malformed payload will never succeed on retry, so skip retries.
			return errors.Join(err, asynq.SkipRetry)
		}
		log.Info("build task received", "deployment", p.DeploymentID)
		imageURI, err := b.Build(ctx, p)
		if err != nil {
			return err // Asynq retries per the task's MaxRetry
		}
		return b.Deploy(ctx, p, imageURI)
	}
}

// handleTeardown processes one teardown task.
func handleTeardown(b *builder.Builder, log *slog.Logger) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		p, err := queue.ParseTeardownPayload(t)
		if err != nil {
			return errors.Join(err, asynq.SkipRetry)
		}
		log.Info("teardown task received", "deployment", p.DeploymentID)
		return b.Teardown(ctx, p)
	}
}

// asynqLogger adapts slog to Asynq's logger interface so all logs are structured
// and land in one place.
type asynqLogger struct{ l *slog.Logger }

func (a asynqLogger) Debug(args ...any) { a.l.Debug("asynq", "msg", args) }
func (a asynqLogger) Info(args ...any)  { a.l.Info("asynq", "msg", args) }
func (a asynqLogger) Warn(args ...any)  { a.l.Warn("asynq", "msg", args) }
func (a asynqLogger) Error(args ...any) { a.l.Error("asynq", "msg", args) }
func (a asynqLogger) Fatal(args ...any) { a.l.Error("asynq-fatal", "msg", args) }
