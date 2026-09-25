-- 0001_init.down.sql
-- Reverses 0001_init.up.sql. Drop in reverse dependency order (children first).
DROP TABLE IF EXISTS build_logs;
DROP TABLE IF EXISTS deployments;
DROP TABLE IF EXISTS tenants;
