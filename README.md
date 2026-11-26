# deploy-platform — a mini Vercel/Heroku (Platform-as-a-Service)

[![CI](https://github.com/1morshed1/deploy-platform/actions/workflows/ci.yml/badge.svg)](https://github.com/1morshed1/deploy-platform/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Submit a GitHub repo URL → the platform detects the language, builds a container
image, deploys it to Kubernetes, gives it a subdomain, and monitors it.

This is a **production-shaped** Platform-as-a-Service. Every non-obvious decision is
written down in [`docs/decisions/`](./docs/decisions) (ADRs) and every subsystem
has a walkthrough in [`docs/learning/`](./docs/learning). To see how a PaaS works
end to end, start with
[`docs/learning/00-how-to-read-this-repo.md`](./docs/learning/00-how-to-read-this-repo.md).

---

## What it does (the request lifecycle)

```
POST /api/v1/deploy {repo_url, branch, app_name}
        │
        ▼
  [API server]  validate repo → detect language → insert deployment row → enqueue build task
        │                                                                        │
        │                                                                  (Redis + Asynq)
        │                                                                        ▼
        │                                                              [Worker]  create Kaniko Job
        │                                                                        │  build image, push to ECR
        │                                                                        ▼
        │                                                              create/patch a TenantApp custom resource
        │                                                                        │
        ▼                                                                        ▼
  GET /api/v1/deployments/:id  ◀───── status updates ──────  [Operator]  reconcile TenantApp into:
  GET /api/v1/deployments/:id/logs (SSE)                                  Namespace + Deployment + Service +
                                                                          Ingress + NetworkPolicy + ResourceQuota
                                                                                 │
                                                                                 ▼
                                                                    myapp.apps.example.com is live
```

Three independent binaries, one Go module:

| Binary | Path | Responsibility |
|---|---|---|
| **api** | `cmd/api` | HTTP API (Gin): accept deploys, auth, rate-limit, stream logs, enqueue builds. |
| **worker** | `cmd/worker` | Asynq consumer: run Kaniko build Jobs, then create the `TenantApp` custom resource. |
| **operator** | `cmd/operator` | Kubernetes controller: reconcile `TenantApp` CRs into real cluster resources. |

## Repository layout

```
cmd/            entrypoints for the three binaries
internal/
  config/       env-var configuration shared by all binaries
  api/          Gin router, handlers, middleware (auth, rate limit)
  store/        PostgreSQL persistence (tenants, deployments, build_logs)
  github/       language detection via the GitHub REST API
  queue/        Asynq task definitions + client (the contract between api and worker)
  builder/      Dockerfile templates + Kaniko Job generation
  k8s/          client-go / controller-runtime client helpers
  dockerfiles/  embedded Dockerfile templates per language
apis/platform/v1  TenantApp CRD Go types (the API the operator serves)
internal/controller  the TenantApp reconcile loop
deploy/
  crd/          the TenantApp CustomResourceDefinition
  k8s/          RBAC, Deployments, Services for the platform itself
  kind/         local cluster config
  monitoring/   Prometheus/Grafana provisioning + dashboard template
infra/terraform  EKS, ECR, Route53, ingress controller
build/          Dockerfiles for the three platform binaries
migrations/     PostgreSQL schema migrations
docs/           learning walkthroughs + architecture decision records
```

## Quickstart (local development)

Prerequisites: Docker, Go 1.24+, [kind](https://kind.sigs.k8s.io/),
kubectl, and `golang-migrate` (or run the SQL by hand).

```bash
# 1. Start Postgres + Redis for the control plane
make dev-deps

# 2. Apply the database schema
make migrate-up

# 3. Run the API locally (talks to the Postgres/Redis above)
make run-api

# 4. In another terminal, create a tenant + API key and deploy something
make seed-tenant
curl -XPOST localhost:8080/api/v1/deploy \
  -H "Authorization: Bearer $API_KEY" \
  -d '{"repo_url":"https://github.com/gin-gonic/examples","branch":"master","app_name":"demo"}'
```

The full cluster path (kind + operator + Kaniko) is documented in
[`docs/learning/07-local-cluster-walkthrough.md`](./docs/learning/07-local-cluster-walkthrough.md).

## Status of this implementation

This is a **complete, compiling, unit-tested codebase**. The parts that require
a live cloud account (a real EKS cluster, ECR, Route53 DNS) are provided as
Terraform and Kubernetes manifests that you apply against your own account; they
are documented but not executed by CI. See
[`docs/learning/09-what-is-real-vs-simulated.md`](./docs/learning/09-what-is-real-vs-simulated.md)
for an honest breakdown of what runs where.

## Build & test

```bash
make build   # compiles all three binaries into ./bin
make test    # go test ./...
make lint    # go vet + gofmt check
```

## License

[MIT](LICENSE)
