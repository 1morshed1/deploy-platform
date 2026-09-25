# 0008 — Enforce "baseline" (not "restricted") Pod Security on tenant workloads

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Kubernetes Pod Security Standards (PSS) come in three profiles, applied per
namespace via labels:

- **privileged** — no restrictions.
- **baseline** — blocks the known privilege-escalation vectors (host namespaces,
  privileged containers, most `hostPath`, etc.) but still allows containers to run
  as root.
- **restricted** — everything in baseline *plus* requires non-root users,
  `seccomp: RuntimeDefault`, dropping all capabilities, and read-only root fs.

The plan calls for enforcing `restricted` on tenant namespaces. But tenant images
are arbitrary user code: many popular base images (the `nginx` official image, some
Node images) **run as root by default**. Under enforced `restricted`, those pods
are rejected at admission and the deployment simply fails — a bad experience for a
PaaS whose promise is "push any repo, it just runs."

## Decision

On each tenant namespace the operator sets:

```
pod-security.kubernetes.io/enforce: baseline
pod-security.kubernetes.io/warn:    restricted
pod-security.kubernetes.io/audit:   restricted
```

That is: **enforce baseline** (so common images run), while **warning and
auditing against restricted** (so violations are visible in `kubectl` warnings and
the audit log). Independently, the operator applies a **hardened container
SecurityContext** to every workload it creates: `allowPrivilegeEscalation: false`,
drop all capabilities, and `seccompProfile: RuntimeDefault` at the pod level.

## Rationale

This is a pragmatic middle ground:

- **Baseline enforcement** stops the dangerous stuff (privileged pods, host
  access) — the things that actually enable container escape and cross-tenant
  compromise — while still admitting the everyday "runs as root inside its own
  container" image.
- **The hardened SecurityContext** we inject gives most of restricted's
  *container-level* benefits (no privilege escalation, no extra capabilities)
  regardless of the namespace policy.
- **warn/audit = restricted** keeps the stronger bar visible: an operator can see
  which tenants would fail restricted and nudge them, or a future "strict mode"
  can flip enforce to restricted per tenant.

The build namespace (`platform-builds`) is separately exempt from restricted
because Kaniko needs to run as root (ADR 0007); it is isolated and ephemeral.

## Consequences

- A malicious tenant image *can* run as root **inside its own container and
  namespace**. That is contained by: the hardened SecurityContext (no escalation,
  no capabilities), the per-tenant Namespace + NetworkPolicy (no cross-tenant
  traffic), and the ResourceQuota (no resource exhaustion). Root-in-container is a
  much smaller risk than root-on-node, which baseline still forbids.
- If a deployment target requires true `restricted` (e.g. a compliance regime),
  flip the namespace label to `enforce: restricted` — but expect root-based images
  to fail, and document that requirement to tenants.
