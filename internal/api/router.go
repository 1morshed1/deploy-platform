package api

import (
	"github.com/gin-gonic/gin"
)

// Routes builds the gin engine with all middleware and routes wired up. Keeping
// route registration in one function makes the API surface easy to audit: you
// can read the whole contract in one place.
func (s *Server) Routes() *gin.Engine {
	// gin.New (not gin.Default) so we choose our own middleware explicitly rather
	// than inheriting gin's default logger/recovery formatting.
	r := gin.New()
	r.Use(gin.Recovery()) // turn panics into 500s instead of crashing the server
	r.Use(s.requestLogger())

	// Liveness/readiness probe, unauthenticated.
	r.GET("/healthz", s.health)

	// All /api/v1 routes require a valid API key.
	v1 := r.Group("/api/v1")
	v1.Use(s.AuthMiddleware())
	{
		// Deploy is additionally rate-limited (expensive, abuse-prone).
		v1.POST("/deploy", s.RateLimitMiddleware(), s.Deploy)

		v1.GET("/deployments", s.List)
		v1.GET("/deployments/:id", s.Get)
		v1.GET("/deployments/:id/logs", s.Logs)
		v1.DELETE("/deployments/:id", s.Delete)
	}
	return r
}

// requestLogger is a minimal structured access log. We write our own rather than
// gin's text logger so logs are structured (slog) and ship cleanly to Loki.
func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		s.log.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"client", c.ClientIP(),
		)
	}
}
