// Package store is the persistence layer. It is the ONLY package that speaks
// SQL; everything else works with the typed models in models.go. Concentrating
// SQL here means a schema change touches one package, and the rest of the code
// is decoupled from the database.
//
// We use pgx (github.com/jackc/pgx/v5) directly rather than an ORM. For a system
// this small, hand-written SQL is clearer than ORM magic, and pgx is the
// fastest, most feature-complete Postgres driver for Go. See ADR 0003.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup matches no row. Callers (e.g. the API
// layer) map this to a 404 without needing to know about pgx.ErrNoRows.
var ErrNotFound = errors.New("store: not found")

// Store wraps a pgx connection pool. A pool is safe for concurrent use, so a
// single Store is shared across all HTTP handlers / workers.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool to the given DSN and verifies connectivity with a
// ping. Failing fast at startup is better than discovering a bad DSN on the
// first request.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases all pooled connections. Call it on shutdown.
func (s *Store) Close() { s.pool.Close() }

// ---- Tenants -------------------------------------------------------------

// CreateTenant inserts a tenant. apiKeyHash must already be hashed by the
// caller; the store never sees or stores a raw API key.
func (s *Store) CreateTenant(ctx context.Context, email, apiKeyHash string) (*Tenant, error) {
	const q = `
		INSERT INTO tenants (email, api_key)
		VALUES ($1, $2)
		RETURNING id, email, api_key, created_at`
	var t Tenant
	err := s.pool.QueryRow(ctx, q, email, apiKeyHash).
		Scan(&t.ID, &t.Email, &t.APIKey, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("store: create tenant: %w", err)
	}
	return &t, nil
}

// GetTenantByAPIKeyHash looks a tenant up by the hash of their API key. This is
// the authentication hot path, called on every authenticated request.
func (s *Store) GetTenantByAPIKeyHash(ctx context.Context, apiKeyHash string) (*Tenant, error) {
	const q = `SELECT id, email, api_key, created_at FROM tenants WHERE api_key = $1`
	var t Tenant
	err := s.pool.QueryRow(ctx, q, apiKeyHash).
		Scan(&t.ID, &t.Email, &t.APIKey, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get tenant: %w", err)
	}
	return &t, nil
}

// ---- Deployments ---------------------------------------------------------

// CreateDeployment inserts a new deployment row in the 'queued' state and
// returns it (with server-generated id/timestamps).
func (s *Store) CreateDeployment(ctx context.Context, d *Deployment) (*Deployment, error) {
	const q = `
		INSERT INTO deployments
			(tenant_id, repo_url, branch, detected_lang, build_status,
			 subdomain, replicas, cpu_limit, memory_limit)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at`
	err := s.pool.QueryRow(ctx, q,
		d.TenantID, d.RepoURL, d.Branch, d.DetectedLang, string(StatusQueued),
		d.Subdomain, d.Replicas, d.CPULimit, d.MemoryLimit,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("store: create deployment: %w", err)
	}
	d.BuildStatus = StatusQueued
	return d, nil
}

// scanDeployment is the shared column-scan for the queries below. Keeping it in
// one place means the column list only has to be correct once. COALESCE turns
// nullable columns into empty strings so the model has no pointer fields.
func scanDeployment(row pgx.Row) (*Deployment, error) {
	var d Deployment
	var status string
	err := row.Scan(
		&d.ID, &d.TenantID, &d.RepoURL, &d.Branch,
		&d.DetectedLang, &status, &d.ImageURI, &d.Subdomain,
		&d.Replicas, &d.CPULimit, &d.MemoryLimit, &d.ErrorMessage,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	d.BuildStatus = BuildStatus(status)
	return &d, nil
}

const deploymentColumns = `
	id, tenant_id, repo_url, branch,
	COALESCE(detected_lang,'') , build_status, COALESCE(image_uri,''),
	COALESCE(subdomain,''), replicas, cpu_limit, memory_limit,
	COALESCE(error_message,''), created_at, updated_at`

// GetDeployment fetches one deployment scoped to a tenant. Scoping by tenant_id
// in the WHERE clause is a security control: a tenant can never read another
// tenant's deployment even if they guess its UUID (see ADR 0004).
func (s *Store) GetDeployment(ctx context.Context, tenantID, id string) (*Deployment, error) {
	q := `SELECT ` + deploymentColumns + ` FROM deployments WHERE id = $1 AND tenant_id = $2`
	d, err := scanDeployment(s.pool.QueryRow(ctx, q, id, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get deployment: %w", err)
	}
	return d, nil
}

// GetDeploymentByID fetches a deployment without a tenant scope. This is for
// internal use by the worker, which is trusted and operates across tenants.
func (s *Store) GetDeploymentByID(ctx context.Context, id string) (*Deployment, error) {
	q := `SELECT ` + deploymentColumns + ` FROM deployments WHERE id = $1`
	d, err := scanDeployment(s.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get deployment by id: %w", err)
	}
	return d, nil
}

// ListDeployments returns a tenant's deployments, newest first.
func (s *Store) ListDeployments(ctx context.Context, tenantID string) ([]*Deployment, error) {
	q := `SELECT ` + deploymentColumns + ` FROM deployments WHERE tenant_id = $1 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: list deployments: %w", err)
	}
	defer rows.Close()

	var out []*Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan deployment: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateStatus moves a deployment to a new status and stamps updated_at. When
// status is StatusFailed the caller passes the error message; otherwise "".
func (s *Store) UpdateStatus(ctx context.Context, id string, status BuildStatus, errMsg string) error {
	const q = `
		UPDATE deployments
		SET build_status = $2,
		    error_message = NULLIF($3,''),
		    updated_at = NOW()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, string(status), errMsg)
	if err != nil {
		return fmt.Errorf("store: update status: %w", err)
	}
	return nil
}

// SetImageURI records the registry URI once the build succeeds.
func (s *Store) SetImageURI(ctx context.Context, id, imageURI string) error {
	const q = `UPDATE deployments SET image_uri = $2, updated_at = NOW() WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, imageURI)
	if err != nil {
		return fmt.Errorf("store: set image uri: %w", err)
	}
	return nil
}

// DeleteDeployment removes a deployment and (via ON DELETE CASCADE) its build
// logs. Used by the worker at the end of a teardown task. Returns ErrNotFound if
// the row is already gone, which the caller can treat as success (idempotent).
func (s *Store) DeleteDeployment(ctx context.Context, id string) error {
	const q = `DELETE FROM deployments WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: delete deployment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountDeploymentsSince counts a tenant's deployments created after 'since'.
// The rate limiter uses this as a durable backstop to the Redis counter.
func (s *Store) CountDeploymentsSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	const q = `SELECT COUNT(*) FROM deployments WHERE tenant_id = $1 AND created_at >= $2`
	var n int
	if err := s.pool.QueryRow(ctx, q, tenantID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count deployments: %w", err)
	}
	return n, nil
}

// ---- Build logs ----------------------------------------------------------

// AppendBuildLog stores one line of build output.
func (s *Store) AppendBuildLog(ctx context.Context, deploymentID, line string) error {
	const q = `INSERT INTO build_logs (deployment_id, log_line) VALUES ($1,$2)`
	_, err := s.pool.Exec(ctx, q, deploymentID, line)
	if err != nil {
		return fmt.Errorf("store: append build log: %w", err)
	}
	return nil
}

// ListBuildLogsAfter returns build-log lines for a deployment whose id is
// greater than 'afterID', in order. The SSE handler calls this in a loop with
// the last id it delivered, so it only ever fetches new lines (a cursor).
func (s *Store) ListBuildLogsAfter(ctx context.Context, deploymentID string, afterID int64) ([]*BuildLog, error) {
	const q = `
		SELECT id, deployment_id, log_line, timestamp
		FROM build_logs
		WHERE deployment_id = $1 AND id > $2
		ORDER BY id ASC`
	rows, err := s.pool.Query(ctx, q, deploymentID, afterID)
	if err != nil {
		return nil, fmt.Errorf("store: list build logs: %w", err)
	}
	defer rows.Close()

	var out []*BuildLog
	for rows.Next() {
		var l BuildLog
		if err := rows.Scan(&l.ID, &l.DeploymentID, &l.LogLine, &l.Timestamp); err != nil {
			return nil, fmt.Errorf("store: scan build log: %w", err)
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}
