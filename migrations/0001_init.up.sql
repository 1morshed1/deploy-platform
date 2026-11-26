-- 0001_init.up.sql
-- Initial schema for the deploy platform control plane.
--
-- This mirrors the schema in the original spec. A few design notes are inline so the
-- reasoning behind each column choice is visible next to the column itself.

-- gen_random_uuid() lives in the pgcrypto extension on older Postgres and is
-- built-in from Postgres 13+. We enable pgcrypto defensively so the DEFAULTs
-- below work on any reasonably recent Postgres.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- A tenant is a customer of the platform. They authenticate with an API key.
CREATE TABLE tenants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       VARCHAR(255) UNIQUE NOT NULL,
    -- api_key is stored hashed (SHA-256 hex = 64 chars). We never store the
    -- raw key; see internal/store and ADR 0004 for why.
    api_key     VARCHAR(64) UNIQUE NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A deployment is one attempt to build+run one repo/branch for one tenant.
CREATE TABLE deployments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    repo_url        VARCHAR(512) NOT NULL,
    branch          VARCHAR(128) NOT NULL DEFAULT 'main',
    -- detected_lang is one of: go, node, python, static (see internal/github).
    detected_lang   VARCHAR(32),
    -- build_status is a small state machine:
    --   queued -> building -> built -> deploying -> live
    --   any state -> failed
    build_status    VARCHAR(20) NOT NULL DEFAULT 'queued',
    image_uri       VARCHAR(512),
    subdomain       VARCHAR(128) UNIQUE,
    replicas        INT NOT NULL DEFAULT 1,
    cpu_limit       VARCHAR(10) NOT NULL DEFAULT '250m',
    memory_limit    VARCHAR(10) NOT NULL DEFAULT '256Mi',
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- We frequently list a tenant's deployments newest-first; index for it.
CREATE INDEX idx_deployments_tenant_created ON deployments (tenant_id, created_at DESC);

-- build_logs stores every line of build output so it can be replayed after the
-- build pod is gone. Live streaming reads new rows as they are appended.
CREATE TABLE build_logs (
    id              BIGSERIAL PRIMARY KEY,
    deployment_id   UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    log_line        TEXT NOT NULL,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- SSE streaming polls "rows for this deployment with id > last seen id".
-- A composite index on (deployment_id, id) makes that a cheap range scan.
CREATE INDEX idx_build_logs_deployment_id ON build_logs (deployment_id, id);
