# Architecture Decision Records (ADRs)

Each file captures one decision: the context that forced it, what we chose, the
alternatives we rejected, and the consequences we accepted. They are append-only
(see [0001](./0001-record-architecture-decisions.md)). Read them to understand
*why* the system is shaped the way it is.

| ADR | Decision |
|---|---|
| [0001](./0001-record-architecture-decisions.md) | Record architecture decisions (use ADRs) |
| [0002](./0002-three-binaries-one-module.md) | Three binaries in one Go module |
| [0003](./0003-pgx-no-orm.md) | Use pgx with hand-written SQL (no ORM) |
| [0004](./0004-api-key-auth-and-tenant-scoping.md) | API-key auth and tenant scoping |
| [0005](./0005-fixed-window-rate-limit.md) | Fixed-window rate limiting in Redis |
| [0006](./0006-tenantapp-ownership-and-finalizers.md) | TenantApp placement, ownership, and finalizer cleanup |
| [0007](./0007-kaniko-over-dind.md) | Kaniko over Docker-in-Docker for builds |
| [0008](./0008-pod-security-baseline.md) | Enforce "baseline" Pod Security on tenant workloads |
| [0009](./0009-templated-grafana-dashboard.md) | One templated Grafana dashboard, not one per tenant |
| [0010](./0010-local-dev-kind-compose.md) | Local development with kind + docker-compose |
