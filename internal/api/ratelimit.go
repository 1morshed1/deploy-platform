package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimiter implements a simple fixed-window per-tenant rate limit backed by
// Redis. The plan requires "5 deploy requests per hour per tenant".
//
// Why Redis and not an in-memory counter? The API is horizontally scaled (many
// replicas). An in-memory counter would let a tenant get 5 deploys *per replica*.
// Redis is the shared source of truth all replicas agree on.
//
// Why a fixed window (not a sliding window or token bucket)? It is the simplest
// correct-enough algorithm: one INCR and one EXPIRE per request. Its known
// weakness — a burst straddling a window boundary can briefly allow up to 2x the
// limit — is acceptable for a deploy endpoint where the limit is coarse. See ADR
// 0005 for the trade-off.
type RateLimiter struct {
	rdb   *redis.Client
	limit int
}

// NewRateLimiter constructs a limiter. It reuses a go-redis client (separate
// from the Asynq client, which manages its own pool).
func NewRateLimiter(redisAddr, redisPassword string, limit int) *RateLimiter {
	return &RateLimiter{
		rdb: redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: redisPassword,
		}),
		limit: limit,
	}
}

// Close releases the Redis connection.
func (r *RateLimiter) Close() error { return r.rdb.Close() }

// Allow increments the current hour's counter for a tenant and reports whether
// the request is within the limit. It returns the number of requests remaining
// so the handler can surface it in a header. On a Redis error it FAILS OPEN
// (allows the request) rather than taking the whole API down when Redis blips —
// the durable DB count in the handler is the backstop.
func (r *RateLimiter) Allow(ctx context.Context, tenantID string) (allowed bool, remaining int, err error) {
	// Bucket key changes each hour, giving us a fresh window automatically.
	bucket := time.Now().UTC().Format("2006010215") // yyyymmddHH
	key := fmt.Sprintf("ratelimit:deploy:%s:%s", tenantID, bucket)

	// INCR returns the new value; the first INCR in a window creates the key.
	count, err := r.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true, r.limit, fmt.Errorf("ratelimit: redis incr: %w", err)
	}
	// Set the TTL only when we just created the key (count == 1). Setting it
	// every time would keep pushing the expiry out and the window would never
	// reset.
	if count == 1 {
		// A little over an hour so the key outlives its window even with clock skew.
		if err := r.rdb.Expire(ctx, key, 65*time.Minute).Err(); err != nil {
			return true, r.limit, fmt.Errorf("ratelimit: redis expire: %w", err)
		}
	}

	remaining = r.limit - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return int(count) <= r.limit, remaining, nil
}

// RateLimitMiddleware enforces the deploy rate limit. It must run AFTER
// AuthMiddleware so the tenant is known.
func (s *Server) RateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant := tenantFromContext(c)
		allowed, remaining, err := s.limiter.Allow(c.Request.Context(), tenant.ID)
		if err != nil {
			// Fail open, but log — a persistent Redis outage should page someone.
			s.log.Warn("ratelimit: redis error, failing open", "err", err)
			c.Next()
			return
		}
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", s.limiter.limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		if !allowed {
			abortError(c, http.StatusTooManyRequests,
				fmt.Sprintf("deploy rate limit of %d/hour exceeded; try again later", s.limiter.limit))
			return
		}
		c.Next()
	}
}
