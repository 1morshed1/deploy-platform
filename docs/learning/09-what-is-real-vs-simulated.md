# 09 — What is real vs. what needs a cloud account

**The honest map.** This project is a *complete, compiling, unit-tested codebase*,
but different parts run in different places. This doc draws the line explicitly so
you always know what you're looking at and never think something is "done running"
when it actually needs infrastructure you haven't provisioned.

## Runs right now, no cloud, no cluster

These are exercised by `make test` and `make build` on any machine with Go:

- **All three binaries compile** (`go build ./cmd/...`).
- **Language detection** — unit-tested against a fake GitHub server (`internal/github`).
- **Subdomain generation, API-key hashing** — pure logic, unit-tested (`internal/api`).
- **Dockerfile templating + port mapping** — unit-tested (`internal/dockerfiles`).
- **Kaniko Job / ConfigMap construction** — pure builders, unit-tested (`internal/builder`).
- **Operator resource builders** (Deployment/Service/Ingress/NetworkPolicy/Quota)
  — pure functions, unit-tested (`internal/controller`).

If it's a pure function, it has a test, and that test runs anywhere.

## Runs with local Docker (no cloud)

- **The API end-to-end** against Postgres + Redis via `docker-compose` (doc 07,
  Option A): real HTTP, real auth, real rate limiting, real queue enqueue, real
  SSE log streaming from the DB.

## Runs on a local Kubernetes cluster (kind, no cloud)

- **The full build→deploy→reconcile loop** (doc 07, Option B): the worker creates
  real Kaniko Jobs, the operator reconciles real TenantApps into real
  Deployments/Services/Ingresses. Caveats: NetworkPolicy isn't enforced by kind's
  default CNI, and Kaniko needs a reachable registry.

## Needs a cloud account (provided as code, not executed here)

These are real, reviewable infrastructure-as-code, but applying them costs money
and requires AWS credentials, so they are **not** run by CI:

- **EKS, VPC, ECR, Route53, NLB ingress** — `infra/terraform`. `terraform plan`
  shows exactly what would be created; `apply` provisions it. Costs are noted in
  `infra/terraform/main.tf` and the plan.
- **AWS ECR image deletion on teardown** — the `Builder.Teardown` registry-delete
  step is a **logged no-op stub**. Wiring it means calling the ECR
  `BatchDeleteImage` API with the AWS SDK; the DB row and cluster resources *are*
  really cleaned up today, only the registry blob deletion is stubbed.
- **cert-manager TLS** — the operator emits the cert-manager annotation and TLS
  block when `CLUSTER_ISSUER` is set, but issuing real Let's Encrypt certs needs
  cert-manager installed and a public, DNS-resolvable domain.
- **Prometheus/Grafana** — manifests in `deploy/monitoring` are applied via Helm on
  a real cluster; they're standard and correct but not spun up by CI.

## Deliberately left as documented extension points

- **Trivy scan gate** — template + integration recipe in doc 08; not wired into
  the Go pipeline by default.
- **Jaeger tracing / Loki logs** — the other two observability pillars; the code
  already emits structured logs and per-build logs, so these are collection
  concerns (doc 06).
- **Server-side apply, sqlc, Redis-cached auth** — noted in the relevant ADRs as
  natural next steps behind the existing interfaces.

## Why build it this way?

The valuable, transferable parts — how a PaaS is *architected*, how you program
Kubernetes with client-go and controller-runtime, how tenant isolation is layered
— are all fully realized and testable. The parts that are stubbed or IaC-only are
the ones whose "realness" is just "you pay AWS and run `apply`," which teaches
little beyond spending money. This split maximizes what you can *learn and run*
for free while keeping the cloud path honest and one `terraform apply` away.
