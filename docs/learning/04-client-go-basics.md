# 04 — Talking to Kubernetes from Go (client-go)

**Code:** `internal/k8s`

The skill this whole project is meant to demonstrate is *programmatically*
managing Kubernetes — not `kubectl apply -f`, but writing Go that creates,
watches, and deletes cluster resources. This doc explains the client library that
makes that possible.

## Two clients, two jobs

`internal/k8s/client.go` builds two different clients. Knowing when to use which
is the core lesson.

### 1. The typed clientset (`kubernetes.Interface`)

This is the client for **built-in resources** — Pods, Jobs, Deployments,
Services, Namespaces, everything in the standard Kubernetes API. It's
strongly-typed and generated from the API schemas, so you write:

```go
cs.BatchV1().Jobs("platform-builds").Create(ctx, job, metav1.CreateOptions{})
```

and the compiler checks that `job` is a `*batchv1.Job`. The worker uses it to
create build Jobs, list build Pods, follow logs, and poll Deployment readiness.

### 2. The dynamic client (`dynamic.Interface`)

Built-in types are known at compile time; **our `TenantApp` CRD is not**. The
dynamic client works with `unstructured.Unstructured` (essentially
`map[string]interface{}`) addressed by a `GroupVersionResource`:

```go
dyn.Resource(TenantAppGVR).Namespace("platform-system").Create(ctx, obj, ...)
```

No generated code required. The worker uses it to create/delete TenantApp objects
(`internal/k8s/tenantapp.go`). This keeps the worker **decoupled** from the
operator's Go types — the worker only knows the *shape* of a TenantApp, not the
struct. (The operator, which reconciles them, does use typed structs — doc 05.)

> **The #1 dynamic-client gotcha:** all numbers in an `unstructured` object must
> be `int64`. Pass a plain `int` for `replicas` and the API server rejects it.
> That's why `ApplyTenantApp` writes `int64(spec.Replicas)`.

## How the client finds the cluster

`RestConfig()` implements the standard resolution order:

1. **In-cluster config** first — when the code runs as a pod, Kubernetes injects a
   ServiceAccount token and CA at a well-known path; `rest.InClusterConfig()`
   reads them. This is how the worker authenticates in production.
2. **Kubeconfig fallback** — when that fails (you're on your laptop),
   it reads `$KUBECONFIG` or `~/.kube/config`. Point that at your kind cluster
   and the same binary works locally.

One binary, both environments, no flags. This is the idiomatic pattern.

## Create-or-update vs. server-side apply

`ApplyTenantApp` does a `Get`, then either `Create` (not found) or `Update`
(patch the spec, keep the `resourceVersion`). We chose this explicit read-modify-
write over Kubernetes **server-side apply** because it's easier to follow when
you're learning: you can see exactly what's read and written. In a larger system
you'd likely switch to server-side apply (`Patch` with `types.ApplyPatchType`) to
avoid the read and get field-ownership conflict detection — a natural next step
behind the same function signature.

## Why RBAC matters (preview)

A pod can only do what its ServiceAccount is allowed to do. The worker's
ServiceAccount needs permission to create Jobs/ConfigMaps in `platform-builds`,
read pods/logs, and create/delete TenantApp objects. Those `Role`/`RoleBinding`
manifests live in `deploy/k8s` (doc 07). If you ever see a `forbidden` error from
these calls, it's an RBAC gap, not a code bug.
