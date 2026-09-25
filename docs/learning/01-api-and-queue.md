# 01 — The API and the build queue

**Code:** `internal/config`, `internal/store`, `internal/queue`, `internal/api`, `cmd/api`, `cmd/seed`, `migrations/`

This is the front door of the platform. Everything a user does starts with an
HTTP request here. The guiding principle, repeated from ADR 0002, is: **the API
is fast and does no slow or privileged work.** Read the code with that lens and
every decision makes sense.

## The request that matters: `POST /api/v1/deploy`

Walk through `Server.Deploy` in `internal/api/handlers.go`. It does exactly five
things, in order, and then returns:

1. **Authenticate** (done earlier by `AuthMiddleware`) — who is this tenant?
2. **Validate** the JSON body — is there a repo URL and app name?
3. **Detect** the language — one GitHub API call (`internal/github`). This
   doubles as a reachability check: if we can't list the repo, we reject with a
   clear `400` instead of failing deep inside a build minutes later.
4. **Persist** a `deployments` row in state `queued` (`internal/store`).
5. **Enqueue** a build task on Redis via Asynq (`internal/queue`) and return
   `202 Accepted`.

Notice what it does *not* do: it never clones the repo, never runs Docker, never
talks to Kubernetes. Those take tens of seconds and need privileges; they belong
to the worker. The API's job is to record intent and hand off.

### Why 202 and not 200?

`200 OK` implies "your request is done." It isn't — the build hasn't started.
`202 Accepted` is the honest status code: "I've accepted this; it will happen
asynchronously." The client then follows progress with `GET .../:id` (poll) or
`GET .../:id/logs` (stream).

## The queue is a contract, not just a pipe

Open `internal/queue/tasks.go`. The `BuildPayload` struct **is** the interface
between two programs (api → worker). Because both import this one package, a
change to the payload is a single compile unit: if you add a field the worker
needs, both binaries recompile against the same definition and a mismatch is a
compiler error. That's the whole reason for the monorepo (ADR 0002).

Asynq gives us, for free, the things you'd otherwise hand-roll:

- **Retries with backoff** — a transient build failure is retried (`MaxRetry(2)`
  = 3 attempts), matching the Kaniko Job's `backoffLimit`.
- **Timeouts** — a build that hangs is killed at 20 minutes.
- **Deduplication** — `TaskID(deploymentID)` means enqueuing the same deployment
  twice does not create two builds.
- **A monitoring UI** — `asynqmon` shows queued/active/failed tasks.

## Streaming logs with Server-Sent Events (SSE)

`GET /api/v1/deployments/:id/logs` streams build output live. Read
`Server.Logs`. The mechanism is deliberately boring: **poll the `build_logs`
table on a 1-second ticker using a monotonic `id` cursor**, and write each new
row to the client as an SSE `data:` event.

Why polling and not Redis pub/sub or a persistent DB `LISTEN/NOTIFY`?

- The logs are already being written to Postgres for durable replay (you can
  read a finished build's logs hours later). Given they're in the DB anyway,
  reading new rows is nearly free and needs no second delivery system.
- It is robust to worker restarts: the cursor is just "max id I've sent." If the
  worker crashes and a new one resumes, the reader keeps working.
- SSE (not WebSockets) because the stream is one-directional (server→client) and
  SSE is plain HTTP: it works through the nginx ingress, auto-reconnects in the
  browser's `EventSource`, and needs no protocol upgrade.

The one subtlety: SSE frames are line-oriented. A log line containing newlines
must be split so each physical line gets its own `data:` prefix — see
`writeSSEData`. The stream closes itself when the deployment reaches a terminal
state (`live`/`failed`) by emitting a named `end` event.

## Configuration: 12-factor from day one

`internal/config` reads everything from environment variables with
local-dev-friendly defaults. In Kubernetes, config arrives as env vars projected
from ConfigMaps and Secrets, so this is the natural fit. A fresh checkout runs
with zero configuration; production overrides via the environment. See the
`config.Config` doc comment for each knob.

## Try it

```bash
make dev-deps        # Postgres + Redis in Docker
make migrate-up      # apply migrations/0001_init.up.sql
make run-api         # start the API on :8080
make seed-tenant     # prints an API key — export it as API_KEY

curl -N localhost:8080/api/v1/deployments -H "Authorization: Bearer $API_KEY"
```

## Related decisions

- [ADR 0003](../decisions/0003-pgx-no-orm.md) — why pgx and hand-written SQL.
- [ADR 0004](../decisions/0004-api-key-auth-and-tenant-scoping.md) — how auth and
  tenant isolation work.
- [ADR 0005](../decisions/0005-fixed-window-rate-limit.md) — the rate-limit
  algorithm and its known weakness.
