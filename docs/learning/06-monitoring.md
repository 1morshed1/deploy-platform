# 06 — Per-tenant monitoring with Prometheus + Grafana

**Code:** `deploy/monitoring/`

Once a tenant's app is live, they want to see how it's doing: CPU, memory,
restarts, request rate. This is Phase 4 of the plan.

## The stack: kube-prometheus-stack

We install everything with one Helm chart, **kube-prometheus-stack**
(`deploy/monitoring/kube-prometheus-stack.values.yaml`). It bundles:

- **Prometheus** (managed by the Prometheus Operator) — scrapes and stores metrics.
- **Grafana** — dashboards.
- **kube-state-metrics** — exposes Kubernetes object state (pod restarts, replica
  counts) as metrics.
- **node-exporter** + **cAdvisor** (via the kubelet) — expose per-container CPU and
  memory usage.

The key insight: **you get per-pod CPU/memory/restart metrics for every tenant pod
for free.** cAdvisor and kube-state-metrics report on *all* pods automatically —
the tenant's app doesn't need to be instrumented at all to get a resource
dashboard. Instrumentation (a `/metrics` endpoint) is only needed for
*application-level* metrics like HTTP request rate.

## Service discovery: how Prometheus finds tenant pods

Prometheus doesn't use a static list of targets; it *discovers* them. Two pieces:

1. In the Helm values we set `podMonitorSelectorNilUsesHelmValues: false` and empty
   namespace selectors so Prometheus watches **PodMonitors in every namespace** —
   essential because each tenant has its own namespace.
2. `deploy/monitoring/tenant-podmonitor.yaml` is a **PodMonitor** that selects every
   pod with our `app.kubernetes.io/managed-by: deploy-platform` label, in any
   namespace, and scrapes a container port literally named `metrics`. A tenant app
   opts into application metrics simply by naming a port `metrics`; if it doesn't,
   it's skipped (but still gets resource metrics from cAdvisor).

## Dashboards as code

`deploy/monitoring/grafana-tenant-dashboard.yaml` is a **ConfigMap** containing
Grafana dashboard JSON, labeled `grafana_dashboard: "1"`. Grafana's sidecar
watches for such ConfigMaps and imports them automatically. No clicking in the UI,
no manual export — the dashboard lives in Git and deploys with everything else.

### One templated dashboard, not one per tenant

The plan suggests provisioning a dashboard per tenant via Grafana's API. We instead
ship **one dashboard with a `namespace` dropdown variable** (regex-filtered to
`tenant-.*`). Selecting a tenant's namespace scopes all four panels — CPU, memory,
restarts, HTTP rate — to that tenant. See [ADR 0009](../decisions/0009-templated-grafana-dashboard.md)
for why this is preferable to N per-tenant dashboards.

The PromQL in each panel is worth reading — it's a good PromQL primer:

- **CPU:** `sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="$namespace"}[5m]))`
  — the per-second rate of the CPU-seconds counter, summed per pod = cores used.
- **Memory:** `sum by (pod) (container_memory_working_set_bytes{namespace="$namespace"})`
  — working set is the memory that can't be reclaimed under pressure (the number the
  OOM killer cares about).
- **Restarts:** `sum by (pod) (kube_pod_container_status_restarts_total{namespace="$namespace"})`
  — a climbing line here means a crash-looping app.
- **HTTP rate:** `sum(rate(http_requests_total{namespace="$namespace"}[5m]))`
  — only meaningful for apps that export `http_requests_total`.

## Where the dashboard URL goes

The `deployments` table has room for a dashboard URL. Because we use one templated
dashboard, the "per-tenant" URL is just the shared dashboard with the namespace
pre-selected via a query param:

```
https://<grafana>/d/tenant-app/tenant-app?var-namespace=tenant-<subdomain>
```

That link can be returned from the API without any per-tenant provisioning call.

## Tracing and logs (the other two pillars)

The plan also calls for Jaeger (build-pipeline tracing) and Loki (log aggregation).
Those follow the same pattern — install via Helm, discover via labels — and are
noted here as the natural next additions. The build already emits structured JSON
logs (slog) and writes build logs to Postgres for the SSE stream, so wiring Loki is
a collection concern, not a code change.
