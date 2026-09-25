# 0007 — Kaniko over Docker-in-Docker for builds

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The platform builds container images from untrusted user repositories, inside a
Kubernetes cluster shared by many tenants. We need a way to run `docker build`-
equivalent work in a pod.

## Options considered

1. **Docker-in-Docker (DinD)** — run a Docker daemon inside the build pod.
2. **Mount the host Docker socket** — let the build pod use the node's daemon.
3. **Kaniko** — a daemonless image builder that runs in userspace.
4. **Buildah / img** — other daemonless builders.

## Decision

Use **Kaniko** (`gcr.io/kaniko-project/executor`).

## Rationale

- **Security.** DinD requires a **privileged** container, and mounting the host
  socket gives the build pod control over the node's Docker daemon — effectively
  root on the node. In a multi-tenant platform running *untrusted* build inputs,
  that is an unacceptable escape risk. Kaniko needs no daemon and no privileged
  mode; it executes the Dockerfile in userspace and snapshots the filesystem
  itself.
- **Native registry push.** Kaniko pushes directly to a registry (ECR) with a
  mounted docker-config Secret — no intermediate daemon to load into.
- **Layer caching.** `--cache=true --cache-repo=...` caches layers in the registry
  so rebuilds of unchanged dependency layers are fast, which matters when builds
  are the platform's slowest step.
- **First-class Kubernetes fit.** Kaniko is designed to run as a one-shot pod,
  which maps perfectly onto a `batchv1.Job` with `restartPolicy: Never`.

Buildah/img are reasonable alternatives but historically needed extra kernel
capabilities or fuse; Kaniko has the simplest "just works in a plain pod" story
and the widest adoption for CI image builds.

## Consequences

- Kaniko's executor typically runs as root *inside the pod*. That conflicts with
  the `restricted` Pod Security Standard, so the **build namespace
  (`platform-builds`) is intentionally exempt** from `restricted` PSS, while every
  **tenant workload namespace enforces `restricted`** (doc 08). We accept a
  relaxed policy in the isolated, ephemeral build namespace to keep tenant
  workloads locked down.
- Kaniko has occasional edge cases with unusual Dockerfiles (certain `RUN` mount
  features). Our generated Dockerfiles are simple and known-good, so this doesn't
  bite us — but a user-supplied Dockerfile path (a future feature) would need
  testing against Kaniko's supported instruction set.
