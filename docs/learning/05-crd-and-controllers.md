# 05 — CRDs and controllers: the operator pattern

**Code:** `apis/platform/v1`, `internal/controller`, `cmd/operator`, `deploy/crd`

This is the advanced heart of the project and the rarest skill it demonstrates:
**extending Kubernetes with your own resource type and writing a controller that
manages it.** If you learn one thing from this repo, learn this.

## The big idea: desired state + a reconcile loop

Kubernetes doesn't "run scripts." You declare *desired state* (a Deployment
should have 3 replicas) and a **controller** continuously works to make reality
match. If a pod dies, the controller notices the gap and creates a new one. This
loop — observe, diff, act, repeat — is called **reconciliation**, and it's why
Kubernetes is self-healing.

We add our own resource type, `TenantApp`, and our own controller that reconciles
it into six standard resources. The payoff: the worker just creates *one*
declarative object ("run this image at this subdomain") and the operator handles
all the messy details, forever — including recreating anything a human deletes by
hand.

## Step 1: Define the resource (the CRD)

A **CustomResourceDefinition** (`deploy/crd/tenantapp-crd.yaml`) teaches the API
server a new type. After `kubectl apply -f` it, `kubectl get tenantapps` works and
the API server validates every TenantApp against the OpenAPI schema in the CRD.

The matching Go types are in `apis/platform/v1/tenantapp_types.go`. Study the
shape:

- **`Spec`** = desired state, written by the *user* (here, the worker). Immutable
  from the controller's perspective — the controller reads it, never writes it.
- **`Status`** = observed state, written by the *controller*. The `/status`
  subresource (declared in the CRD) lets the controller update status without
  touching spec, and vice versa. This spec/status split is a fundamental
  Kubernetes API convention.

### DeepCopy and runtime.Object

Every object stored via the Kubernetes API must implement `runtime.Object`, which
requires a `DeepCopyObject()` method. controller-runtime relies on deep copies so
a cached object is never mutated by accident. These methods are normally generated
by `controller-gen` into `zz_generated.deepcopy.go`; we **hand-wrote them** (they're
trivial for value-only structs) to keep the build free of a code-generation step.
Read them at the bottom of `tenantapp_types.go` — they're a good way to understand
what the generator would otherwise produce.

## Step 2: Write the controller (the reconciler)

`internal/controller/tenantapp_controller.go` is built on **controller-runtime**,
the library Kubernetes' own controllers use. The whole controller is one method:

```go
func (r *TenantAppReconciler) Reconcile(ctx, req) (ctrl.Result, error)
```

controller-runtime calls it for every create/update/delete of a TenantApp (and on
a periodic resync). The golden rule: **Reconcile must be idempotent.** Running it
twice with the same input must be safe, because it *will* run many times. That's
why every child object is created via `ensure` (create-if-absent, update-if-present)
rather than a bare `Create` that would fail the second time.

Trace what one reconcile does for a live TenantApp:

1. **Fetch** the TenantApp. Gone? Nothing to do (it was fully deleted).
2. **Deleting?** (`DeletionTimestamp` set) → run finalizer cleanup (step 5).
3. **Ensure the finalizer** is present so we get to clean up later.
4. **Reconcile resources** — ensure the Namespace, ResourceQuota, NetworkPolicy,
   Deployment, Service, and Ingress all exist and match desired state, *in that
   order* (namespace first — the rest live inside it).
5. **Report status** — read the Deployment's readiness and write `Ready` (with the
   URL) or `Provisioning` (and requeue in 15s to check again).

### The finalizer: cleanup you can rely on

Normally Kubernetes garbage-collects child objects via `ownerReferences`. We
*can't* use them here: our children live in a different namespace from the
TenantApp, and owner refs are same-namespace only (ADR 0006). So we use a
**finalizer**. Adding `platform.example.com/finalizer` to the TenantApp tells
Kubernetes: "don't actually delete this object until I remove the finalizer." On
delete, `reconcileDelete` deletes the whole `tenant-<subdomain>` namespace — which
cascades to every child — then removes the finalizer so the TenantApp can vanish.
This guarantees cleanup even though GC can't help us.

## Step 3: Run it (the manager)

`cmd/operator/main.go` sets up a controller-runtime **Manager**, which provides
the shared cache, clients, and leader election. Two things worth understanding:

- **The scheme** maps Go types ↔ GroupVersionKinds. We register both the built-in
  types (`clientgoscheme`) and our `TenantApp` (`platformv1.AddToScheme`) so the
  manager can serialize either.
- **Leader election** (`LeaderElection: true`) ensures that even if you run 3
  operator replicas for availability, only *one* is reconciling at a time. Two
  active reconcilers would fight over the same objects. The others stand by,
  ready to take the lease if the leader dies.

## Why this is better than generating YAML

The naive alternative is: the API generates a Deployment/Service/Ingress YAML and
runs `kubectl apply`. That's a *one-shot* action — if the Deployment is later
deleted or drifts, nothing fixes it. The operator is a *continuous* controller:
drift is corrected on the next reconcile, deletes are cleaned up via the
finalizer, and the desired state lives in one auditable object (`kubectl get
tenantapp demo -o yaml`). It's the difference between a script and a system.

## Related decisions

- [ADR 0006](../decisions/0006-tenantapp-ownership-and-finalizers.md) — placement,
  ownership, and the finalizer model.
- [ADR 0008](../decisions/0008-pod-security-baseline.md) — Pod Security Standards
  choice for tenant workloads.
