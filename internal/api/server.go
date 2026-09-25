// Package api implements the platform's HTTP API using Gin. It is the only
// synchronous, user-facing component: it validates input, persists intent, and
// hands slow work to the queue. It never builds images or touches Kubernetes
// itself (see ADR 0002).
package api

import (
	"log/slog"
	"net/http"

	"github.com/1morshed1/deploy-platform/internal/config"
	"github.com/1morshed1/deploy-platform/internal/github"
	"github.com/1morshed1/deploy-platform/internal/queue"
	"github.com/1morshed1/deploy-platform/internal/store"
	"github.com/gin-gonic/gin"
)

// Server bundles the dependencies every handler needs. Dependencies are injected
// (constructor arguments) rather than reached through globals, which keeps
// handlers testable — a test can construct a Server with fakes.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	detector *github.Detector
	queue    *queue.Client
	limiter  *RateLimiter
	log      *slog.Logger
}

// NewServer wires the dependencies together.
func NewServer(cfg *config.Config, st *store.Store, det *github.Detector, q *queue.Client, rl *RateLimiter, log *slog.Logger) *Server {
	return &Server{cfg: cfg, store: st, detector: det, queue: q, limiter: rl, log: log}
}

// abortError writes a consistent JSON error body and stops the handler chain.
// Centralizing the error shape means every error the API returns looks the same
// to clients: {"error": "..."}.
func abortError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// health is an unauthenticated liveness probe used by Kubernetes.
func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
