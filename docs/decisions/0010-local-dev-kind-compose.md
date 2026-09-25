# 0010 — Local development with kind + docker-compose

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Developers need to run and test the platform without an AWS bill. The system has
two very different kinds of dependency: stateful backing services (Postgres,
Redis) and a Kubernetes cluster. Requiring a cloud cluster for every code change
would make the inner loop slow and expensive.

## Decision

Provide **two local modes**:

1. **docker-compose** for Postgres + Redis, so the API/store/queue can be run and
   tested with no Kubernetes at all (`make dev-deps`, `make run-api`).
2. **kind** for a full local Kubernetes cluster, so the build→deploy→reconcile path
   can be exercised (`make kind-up`, `make deploy-platform`).

The plan itself recommends kind for local development and a single small EKS node
for cloud testing; this ADR records how we operationalize that.

## Rationale

- **Fast inner loop.** Most changes (API, detection, store, queue, and even the
  pure builder/operator constructors, which are unit-tested) never need a cluster.
  docker-compose starts in seconds; a cluster round-trip is minutes. Separating the
  two means you only pay the cluster cost when you're actually testing cluster
  behavior.
- **Same binaries, both modes.** `k8s.RestConfig()` uses in-cluster config in a pod
  and kubeconfig locally, so the identical binary runs on kind and on EKS — no
  build flags, no code branches.
- **kind is a real cluster.** It runs actual kube-apiserver, scheduler, and kubelet
  in Docker, so CRDs, controllers, RBAC, and admission all behave as in production.
  What you debug locally is genuinely what runs in the cloud.

## Consequences

- **Two known kind limitations** must be taught, not hidden (both are called out in
  the config and docs): the default CNI doesn't enforce NetworkPolicy, and Kaniko
  needs a reachable registry. These are properties of the local environment, not
  bugs.
- The dev-grade in-cluster Postgres/Redis (`deploy/k8s/15-datastores.yaml`) use
  `emptyDir` and lose data on restart. That's intentional for local use; production
  points `DATABASE_URL`/`REDIS_ADDR` at managed services and deletes that file.
