# 0004 — API-key auth and tenant scoping

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

This is a multi-tenant platform: many customers share one control plane and one
cluster. Two things must be true: (1) requests must be authenticated, and (2) one
tenant must never see or touch another tenant's data. We had to choose an auth
mechanism and an isolation strategy for the data layer.

## Decision

**Authentication:** static per-tenant **API keys**, presented as
`Authorization: Bearer dp_...`. We store only the **SHA-256 hash** of each key,
never the raw value.

**Data isolation:** every tenant-facing query is **scoped by `tenant_id` in the
`WHERE` clause**. Ownership is enforced in the database query, not in application
`if` checks after the fetch.

## Rationale

### Why API keys, not OAuth/JWT?

The clients here are scripts and CI systems ("deploy my repo"), not interactive
users in a browser. API keys are the idiomatic credential for machine-to-machine
APIs (this is what Stripe, GitHub PATs, and most PaaS deploy hooks use). OAuth
would add an authorization-server dependency and a redirect flow that a `curl`
client doesn't want. JWTs would add key rotation and revocation complexity we
don't need at this scale.

### Why hash the key, and why SHA-256 (not bcrypt)?

Storing raw keys means a database leak hands attackers working credentials.
Storing a hash means a leak exposes only useless digests. We use **SHA-256, not
bcrypt/argon2**, because API keys are **high-entropy random tokens** (24 random
bytes). Password hashes are deliberately slow to resist brute-forcing
*low-entropy* human passwords; there is nothing to brute-force in a 192-bit
random token, so a fast hash is correct — and it lets us index the hash column
and look tenants up in one query on the auth hot path.

### Why scope in the query, not after the fetch?

`GetDeployment(tenantID, id)` puts `AND tenant_id = $2` in the SQL. If we instead
fetched by `id` and then compared `row.TenantID == tenantID` in Go, a forgotten
check anywhere would be a cross-tenant data leak. Pushing the constraint into the
query makes the *default* safe: a query that forgets the scope simply returns no
rows for another tenant's id. This is defense by construction.

## Consequences

- Keys can't be rotated without issuing a new one (acceptable; the `seed` tool
  and a future `POST /tenants` mint them). Revocation is "delete the tenant row."
- The auth middleware does one indexed DB lookup per request. At high volume this
  would be cached in Redis; for now the index makes it cheap.
- A constant-time compare (`subtle.ConstantTimeCompare`) guards the final check
  even though the indexed lookup already matched — documenting intent and
  future-proofing against a non-indexed comparison path.
