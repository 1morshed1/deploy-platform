# 08 — Tenant isolation and security

**Code:** `internal/controller/resources.go`, `internal/api/ratelimit.go`,
`internal/api/auth.go`, `deploy/security/trivy-scan-job.template.yaml`

This is Phase 5 — the set of controls that separate a demo from something you'd let
strangers push code to. Multi-tenancy means *hostile* neighbors must be assumed:
one tenant must not be able to read another's data, eat the whole cluster, or
escape their container. Security here is **defense in depth** — many independent
layers, so one failure isn't game over.

## Layer 1 — Authentication and data isolation (API)

- Every `/api/v1` request needs a valid API key (`AuthMiddleware`). Keys are stored
  only as SHA-256 hashes.
- Every tenant-facing query is **scoped by `tenant_id` in SQL**, so a tenant can't
  read another's deployment even by guessing its UUID.

Details in [ADR 0004](../decisions/0004-api-key-auth-and-tenant-scoping.md).

## Layer 2 — Rate limiting (API)

Deploys are capped at 5/hour/tenant via a Redis fixed-window counter
(`RateLimitMiddleware`). This stops runaway automation from spawning unbounded
build pods. Details in [ADR 0005](../decisions/0005-fixed-window-rate-limit.md).

## Layer 3 — Namespace isolation (operator)

Each deployment runs in its **own namespace** `tenant-<subdomain>`. A namespace is
Kubernetes' primary tenancy boundary and is what makes the next two layers
expressible. Deleting a tenant is deleting a namespace. See
[ADR 0006](../decisions/0006-tenantapp-ownership-and-finalizers.md).

## Layer 4 — Network isolation (NetworkPolicy)

`NetworkPolicyFor` attaches a policy to every tenant namespace that allows ingress
**only** from:

1. the same namespace (the tenant's own pods talking to each other), and
2. the ingress-controller namespace (so public traffic can reach the app).

Everything else — including pods in *other tenants'* namespaces — is denied. That's
what stops tenant A from reaching tenant B's pods directly.

> **Setup requirement:** for rule (2) to work, the ingress controller's namespace
> must carry the label `platform.example.com/ingress=true`, and your CNI must
> enforce NetworkPolicy (Calico, Cilium; kind's default kindnet does **not** — see
> doc 07). Without an enforcing CNI, NetworkPolicies are silently ignored — a
> classic "it looked secure but wasn't" trap.

## Layer 5 — Resource quotas (operator)

`ResourceQuotaFor` caps each tenant namespace's total CPU, memory, and pod count.
One tenant can't starve the cluster or the other tenants. Combined with per-pod
limits (requests == limits ⇒ Guaranteed QoS), resource usage is bounded top and
bottom.

## Layer 6 — Pod Security (operator)

Each tenant namespace is labeled with Pod Security Standards (`enforce: baseline`,
`warn`/`audit: restricted`), and every workload gets a hardened container
SecurityContext: no privilege escalation, all capabilities dropped, seccomp
`RuntimeDefault`. We enforce *baseline* rather than *restricted* so ordinary
root-in-container images still run, while blocking the escape vectors (privileged
pods, host namespaces) that actually threaten the node. The full trade-off is
[ADR 0008](../decisions/0008-pod-security-baseline.md).

## Layer 7 — Image scanning (Trivy)

`deploy/security/trivy-scan-job.template.yaml` scans a built image for CRITICAL CVEs and
exits non-zero if any are found. Wired in, it becomes a **gate**: build → scan → (only
if clean) deploy. The `--exit-code=1` contract is what lets the worker treat a
failed scan as a failed build.

> **Honest status:** the scan template exists and is documented, but is *not* wired
> into the Go pipeline by default. Enabling it is described in the template's
> comments and is a natural exercise. See doc 09 for the full real-vs-simulated map.

## The mental model

```
Attacker's reach                     Stopped by
────────────────                     ──────────
guess another tenant's API           per-tenant API keys (hashed)
read another tenant's deployments     tenant_id-scoped SQL
spam builds to exhaust cluster        Redis rate limit + ResourceQuota
reach another tenant's pods           per-namespace NetworkPolicy
consume all cluster CPU/RAM           ResourceQuota + per-pod limits
escape the container to the node      baseline PSS + hardened SecurityContext
deploy a known-vulnerable image       Trivy scan gate (opt-in)
```

No single layer is trusted to be perfect; each assumes the one before it might
fail. That's the whole idea of defense in depth.
