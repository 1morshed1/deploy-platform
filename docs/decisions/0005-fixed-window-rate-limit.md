# 0005 — Fixed-window rate limiting in Redis

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The plan requires limiting deploy requests to **5 per hour per tenant**. Deploys
are expensive (they spin up build pods) and abuse-prone, so the limit protects
the cluster. The API runs as multiple replicas, so the counter must be shared.

## Decision

Implement a **fixed-window counter in Redis**: one key per `(tenant, hour)`,
incremented on each deploy, with a TTL just over an hour. Over the limit → `429`.
On a Redis error, **fail open** (allow the request) and log.

See `internal/api/ratelimit.go`.

## Rationale

### Why Redis?

The API is horizontally scaled. An in-memory counter would let a tenant get "5
per replica," defeating the limit. Redis is the single source of truth every
replica agrees on, and we already run Redis for the queue.

### Why fixed-window (not sliding-window or token-bucket)?

Fixed-window is the simplest algorithm that is correct enough: **one `INCR` plus
one `EXPIRE`** per request. The bucket key embeds the current hour
(`ratelimit:deploy:{tenant}:{yyyymmddHH}`), so a new window appears automatically
each hour with no cleanup job.

Its well-known weakness: a burst straddling the boundary between two windows can
allow up to *2×* the limit in a short span (5 at 10:59, 5 at 11:00). For a coarse
deploy limit this is acceptable — the goal is to stop runaway automation, not to
enforce a precise SLA. A sliding-window log or token bucket would fix the edge
case at the cost of more Redis operations and code; not worth it here.

### Why fail open on Redis errors?

If Redis blips, we would rather briefly allow deploys than take the entire deploy
endpoint down. The durable backstop is the database: `store.CountDeploymentsSince`
exists so a future hardening step can enforce the limit from Postgres when Redis
is unavailable. Availability of the core function beats strict enforcement of a
soft limit.

### Setting the TTL only on the first increment

The `EXPIRE` is applied only when `INCR` returns `1` (the key was just created).
If we reset the TTL on every request, the window would keep sliding forward and
never reset — the limit would become permanent after the first request. This is a
classic fixed-window bug; the `count == 1` guard avoids it.

## Consequences

- Boundary bursts up to 2× are possible and accepted.
- The limit is enforced only for `POST /deploy` (the middleware is attached to
  that route only), so reads and log streams are never rate-limited.
- `X-RateLimit-Limit` / `X-RateLimit-Remaining` headers let clients self-regulate.
