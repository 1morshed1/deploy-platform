# 0009 — One templated Grafana dashboard instead of one per tenant

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The spec suggests using Grafana's provisioning API to create a dashboard for each
tenant when their app deploys, and storing the dashboard URL per deployment.

## Decision

Ship **one dashboard** (`deploy/monitoring/grafana-tenant-dashboard.yaml`) with a
`namespace` template variable, delivered as code via a ConfigMap the Grafana
sidecar auto-imports. A "per-tenant" link is the shared dashboard with the tenant's
namespace pre-selected:

```
/d/tenant-app/tenant-app?var-namespace=tenant-<subdomain>
```

## Rationale

- **No moving parts at deploy time.** Per-tenant provisioning means the worker must
  call Grafana's HTTP API on every deploy, handle its auth, retries, and failures,
  and delete the dashboard on teardown. A templated dashboard needs none of that —
  the deploy path stays free of a Grafana dependency.
- **Identical panels for every tenant.** The dashboards would be byte-for-byte the
  same except for a namespace filter. A template variable expresses that difference
  in one place instead of duplicating JSON N times.
- **Dashboards as code.** The single dashboard lives in Git and deploys with the
  rest of the manifests; changes are reviewed and versioned. Per-tenant dashboards
  created via API are runtime state that drifts from source control.
- **Scales.** 10,000 tenants is still one dashboard and one dropdown, not 10,000
  Grafana objects to create, update, and garbage-collect.

## Consequences

- The URL is a query-param link, not a unique dashboard per tenant. Functionally
  identical for the user; they land on their app's metrics.
- If a tenant needed a *bespoke* dashboard (custom panels for their own metrics),
  this model wouldn't cover it — you'd add per-tenant dashboards for those cases.
  That's a rare need and can be layered on later without changing the default.
- Access control is coarser: anyone who can open the dashboard can switch the
  namespace dropdown. Real multi-tenant Grafana would use per-org Grafana or data
  source permissions; noted as a future hardening step.
