# 07 — Running the whole thing locally on kind

**Code:** `deploy/kind`, `deploy/k8s`, `Makefile`, `docker-compose.yaml`

This walkthrough takes you from an empty machine to a deployed app, entirely on
your laptop, using **kind** (Kubernetes IN Docker). Two ways to run exist; pick
based on what you're learning.

## Option A — control plane only (fastest inner loop)

Run just the API against local Postgres/Redis. No Kubernetes. Great for working on
the API, store, or queue.

```bash
make dev-deps      # Postgres + Redis in Docker (docker-compose)
make migrate-up    # apply the schema
make run-api       # API on :8080
make seed-tenant   # prints an API key
```

You can now hit every endpoint. Builds will *enqueue* but nothing consumes them
(no worker/cluster) — that's expected in this mode.

## Option B — the full platform on kind

This runs all three binaries plus a real (local) Kubernetes cluster, so you can
watch a repo become a running app.

### 1. Create the cluster

```bash
make kind-up   # kind create cluster --config deploy/kind/kind-config.yaml
```

The kind config maps host ports 80/443 into the cluster so the ingress is
reachable at `localhost`.

### 2. Install the ingress controller

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml
kubectl label namespace ingress-nginx platform.example.com/ingress=true
```

That label is what tenant NetworkPolicies allow traffic from (doc 08).

### 3. Build images and load them into kind

```bash
make kind-load   # docker-build + kind load docker-image (all three binaries)
```

kind runs its own container runtime, so images must be *loaded* into it; it can't
see your host's Docker images otherwise.

### 4. Create secrets, then deploy the platform

```bash
# Provide the schema to the in-cluster Postgres and the app secrets.
kubectl -n platform-system create configmap db-init --from-file=migrations/0001_init.up.sql
cp deploy/secrets.example.yaml /tmp/secrets.yaml   # edit if needed
kubectl apply -f /tmp/secrets.yaml

make deploy-platform   # installs the CRD, namespaces, RBAC, datastores, api/worker/operator
```

### 5. Deploy an app

```bash
kubectl -n platform-system port-forward svc/platform-api 8080:80 &
# create a tenant (run the seed against the in-cluster DB, or via a one-off pod)
curl -XPOST localhost:8080/api/v1/deploy \
  -H "Authorization: Bearer $API_KEY" \
  -d '{"repo_url":"https://github.com/OWNER/REPO","branch":"main","app_name":"demo"}'
```

Watch it flow:

```bash
kubectl -n platform-builds get jobs -w        # the Kaniko build Job
kubectl get tenantapps -A                      # the custom resource the worker created
kubectl get ns | grep tenant-                  # the namespace the operator created
kubectl -n tenant-demo-XXXX get deploy,svc,ingress,networkpolicy,resourcequota
```

## Big caveats to know

- **NetworkPolicy is not enforced by kind's default CNI (kindnet).** The policies
  are created but ignored. To actually test isolation, recreate the cluster with
  Calico or Cilium. For app-level testing, kindnet is fine. (This is called out in
  `deploy/kind/kind-config.yaml` too.)
- **Kaniko needs to push to a registry.** Locally you either point `ECR_REGISTRY`
  at a real ECR repo (with `ecr-credentials`), or run a local registry and adjust
  the secret. Without a reachable registry the build's push step fails — that's a
  configuration gap, not a bug.
- **DNS.** `*.apps.example.com` won't resolve on your laptop. Either add specific
  hosts entries (`127.0.0.1 demo-xxxx.apps.example.com`) or curl with
  `--resolve`/`-H "Host: ..."`.

## The dev-loop philosophy

Option A exists because a full cluster round-trip is slow to iterate on. Most code
changes (API, store, detection, queue, even the pure builder/operator constructors
which are unit-tested) never need a cluster. Reach for Option B when you're
specifically exercising the build→deploy→reconcile path. See
[ADR 0010](../decisions/0010-local-dev-kind-compose.md).
