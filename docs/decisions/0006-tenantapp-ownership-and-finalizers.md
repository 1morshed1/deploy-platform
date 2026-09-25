# 0006 — TenantApp placement, ownership, and finalizer-based cleanup

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

A successful build must become a running app. We use a custom resource,
`TenantApp`, as the hand-off: the worker creates one, and the operator reconciles
it into a Deployment, Service, Ingress, NetworkPolicy, and ResourceQuota. Three
questions had to be answered:

1. **Where do TenantApp objects live?**
2. **Where do the tenant's workloads run?**
3. **How is everything cleaned up on delete?**

Kubernetes garbage collection via `ownerReferences` is the usual answer to (3),
but owner references have a hard rule: **an owner and its owned object must be in
the same namespace** (a namespaced object cannot own something in another
namespace, and cannot own a cluster-scoped object like a Namespace).

## Decision

1. **TenantApp objects live in a single shared `platform-system` namespace**, one
   per deployment, named after the deployment's subdomain.
2. **Each deployment's workload runs in its own dedicated namespace**,
   `tenant-<subdomain>`, created by the operator. Per-tenant namespaces are what
   make NetworkPolicy isolation and ResourceQuota limits possible (doc 08).
3. **Cleanup uses a finalizer, not owner references.** The operator adds the
   finalizer `platform.example.com/finalizer` to every TenantApp. On delete, the
   operator deletes the entire `tenant-<subdomain>` namespace (which cascades to
   every child resource), then removes the finalizer so the TenantApp can vanish.

## Why not owner references?

The workload namespace is cluster-scoped and the child resources live in a
*different* namespace from the TenantApp, so owner references simply can't span
that gap. A finalizer is the correct tool: it guarantees the operator runs its
teardown logic *before* the TenantApp is deleted, and deleting one Namespace is a
clean, atomic cascade for everything inside it.

## Why a namespace per tenant (not a shared namespace with labels)?

Namespaces are Kubernetes' primary isolation boundary. A namespace per tenant
gives us, for free:

- **NetworkPolicy isolation** — deny cross-namespace traffic so tenants can't
  reach each other's pods.
- **ResourceQuota** — cap total CPU/memory per tenant at the namespace level.
- **Blast-radius containment** — deleting a tenant is deleting a namespace.

A shared namespace with per-app labels would force every policy and quota to be
expressed as label selectors, which is fiddlier and easier to get wrong.

## Consequences

- The worker and operator must agree on names. `apis/platform/v1/names.go`
  centralizes `WorkloadNamespace(subdomain)`, the labels, and the finalizer
  string so they can't drift.
- Deletion is *asynchronous*: `DELETE` returns `202`, the worker deletes the
  TenantApp, and the operator's finalizer does the real teardown. A brief window
  exists where the CR is `Terminating`. This is normal Kubernetes behavior.
- A stuck finalizer (operator down) will block TenantApp deletion until the
  operator recovers — the correct, safe behavior (better than orphaning
  resources), but something to know when debugging a CR "stuck deleting."
