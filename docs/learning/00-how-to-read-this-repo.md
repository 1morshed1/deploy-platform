# 00 — How to read this repository (a learning path)

This repo is built to be *read*, not just run. This document is the map.

## The mental model

A Platform-as-a-Service (PaaS) like Vercel or Heroku is, at its core, a machine
that turns **"here is my source code"** into **"here is a URL where it runs"**.
Everything else — dashboards, custom domains, scaling knobs — hangs off that
core loop. We implement the core loop and enough of the surrounding system to be
realistic.

There are three long-lived processes. Keeping them separate is the single most
important architectural idea in the whole project, so internalize it early:

1. **The API** is *synchronous and fast*. A user's HTTP request must return in
   milliseconds. It therefore does no slow work itself — it validates, writes a
   row to the database, drops a message on a queue, and returns.

2. **The worker** is *asynchronous and slow*. Building a container image takes
   30–120 seconds. That work happens here, off the request path, where it can
   retry, be observed, and not block anyone.

3. **The operator** is *declarative and eventually-consistent*. It does not
   "deploy" in a one-shot sense. It continuously compares the desired state (a
   `TenantApp` custom resource) with the real state of the cluster and nudges
   reality toward the desire. This is the Kubernetes "reconciliation" pattern,
   and it is the reason Kubernetes is self-healing.

If you understand *why* the work is split this way, you understand the project.
The reasons are written up in
[`docs/decisions/0002-three-binaries-one-module.md`](../decisions/0002-three-binaries-one-module.md).

## Suggested reading order

Read the code and its companion learning doc together, phase by phase. Each
learning doc explains the concepts; the code shows them.

| Order | Learning doc | Code to read alongside |
|---|---|---|
| 1 | [`01-api-and-queue.md`](./01-api-and-queue.md) | `internal/config`, `internal/store`, `internal/api`, `internal/queue`, `cmd/api` |
| 2 | [`02-language-detection.md`](./02-language-detection.md) | `internal/github` |
| 3 | [`03-builder-kaniko.md`](./03-builder-kaniko.md) | `internal/dockerfiles`, `internal/builder`, `cmd/worker` |
| 4 | [`04-client-go-basics.md`](./04-client-go-basics.md) | `internal/k8s` |
| 5 | [`05-crd-and-controllers.md`](./05-crd-and-controllers.md) | `apis/platform/v1`, `internal/controller`, `cmd/operator`, `deploy/crd` |
| 6 | [`06-monitoring.md`](./06-monitoring.md) | `deploy/monitoring` |
| 7 | [`07-local-cluster-walkthrough.md`](./07-local-cluster-walkthrough.md) | `deploy/kind`, `deploy/k8s`, `Makefile` |
| 8 | [`08-security-and-isolation.md`](./08-security-and-isolation.md) | `internal/controller` (NetworkPolicy/ResourceQuota), `deploy/k8s` |
| 9 | [`09-what-is-real-vs-simulated.md`](./09-what-is-real-vs-simulated.md) | `infra/terraform` |

## How the docs are structured

- **`docs/learning/`** — narrative explanations aimed at someone learning the
  concepts. They answer "what is this and why does it exist?".
- **`docs/decisions/`** — Architecture Decision Records (ADRs). Each one captures
  a single decision, the alternatives considered, and the trade-off we accepted.
  They answer "why is it built *this* way and not another?". ADRs are numbered
  and immutable: if we change our minds later, we add a new ADR that supersedes
  the old one rather than editing history.

## A note on honesty

Some of this system genuinely runs in a container right now (the Go code
compiles and is unit-tested). Some of it (a real EKS cluster, ECR, DNS) needs a
cloud account and is provided as infrastructure-as-code you apply yourself.
[`09-what-is-real-vs-simulated.md`](./09-what-is-real-vs-simulated.md) draws that
line explicitly so you always know what you're looking at.
