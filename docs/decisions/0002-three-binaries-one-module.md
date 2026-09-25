# 0002 — Three binaries in one Go module

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The platform has three clearly different runtime jobs:

1. Serve a fast HTTP API.
2. Run slow container builds asynchronously.
3. Continuously reconcile Kubernetes resources.

We had to decide how to package these: one process that does everything, three
separate repositories/modules, or three binaries inside a single module.

## Decision

**One Go module, three `main` packages** under `cmd/api`, `cmd/worker`, and
`cmd/operator`. They share the same `internal/` packages (config, store, queue,
k8s helpers, builder).

## Why not one process?

The three jobs have opposite operational profiles:

- The API must stay responsive; a 90-second image build inside the request
  handler would exhaust the HTTP server's connection pool and time out clients.
- The worker should scale on build throughput; the API should scale on request
  rate. Merged, you can only scale on one axis.
- The operator must hold a *singleton-ish* leadership lease so two copies don't
  fight over the same resources; the API is happily horizontally scaled. Merging
  them forces the API to inherit the operator's leader-election constraints.

Separation lets each be deployed, scaled, restarted, and reasoned about
independently. This mirrors how real platforms are built.

## Why not three repositories?

Three repos would mean three module versions, three CI pipelines, and painful
sharing of the `store`, `queue`, and `k8s` code that all three legitimately
need. The queue task definitions in `internal/queue` are literally the *contract*
between the API (producer) and the worker (consumer) — keeping them in one module
means a change to that contract is a single atomic commit that recompiles both
sides, so a mismatch is caught by the compiler rather than in production.

A monorepo with a shared `internal/` gives us the isolation benefits of separate
processes without the coordination cost of separate repos.

## Consequences

- All three binaries build from `go build ./cmd/...` and share dependencies, so
  their versions never drift.
- The `internal/` boundary (Go forbids importing another module's `internal/`)
  keeps these shared packages private to the platform.
- Downside: the three binaries share one dependency graph, so a heavy dependency
  pulled in by the operator (controller-runtime) is also compiled into the api
  and worker images even though they don't use it. We accept the larger build;
  the images are still small because Go statically links only what each `main`
  actually references. (Verify with `go build` — unused packages are dropped.)
