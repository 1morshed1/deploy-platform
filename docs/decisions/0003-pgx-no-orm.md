# 0003 — Use pgx with hand-written SQL (no ORM)

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

The platform needs to talk to PostgreSQL. The Go ecosystem offers a spectrum:
full ORMs (GORM, ent), query builders (squirrel), code generators (sqlc), and the
raw driver (pgx / database-sql).

## Decision

Use **pgx v5** directly with hand-written SQL, all confined to the `internal/store`
package. No ORM.

## Rationale

- **The schema is tiny** — three tables. An ORM's value grows with schema
  complexity; here it would add a dependency and a layer of indirection for
  almost no payoff.
- **Explicit SQL is easier to reason about.** The
  explicit `INSERT ... RETURNING` and indexed lookups teach more than
  `db.Create(&x)` hides.
- **pgx is the best Postgres driver for Go** — fastest, with first-class support
  for Postgres types, `COPY`, and connection pooling via `pgxpool`.
- **One place to change.** All SQL lives in `internal/store`. The rest of the app
  uses typed models and never sees a query, so a schema change has a single blast
  radius.

## Consequences

- We write our own mapping code (`scanDeployment`, the `deploymentColumns`
  constant). That's a little boilerplate, mitigated by sharing the scan/column
  list in one place.
- No compile-time checking that a query matches the schema (sqlc would give
  that). We accept this for a three-table schema; if it grew, sqlc would be the
  natural next step and would slot in behind the same `store` interface.
- Nullable columns are read with `COALESCE(col,'')` so the Go models have plain
  `string` fields instead of `*string`, keeping call sites clean.
