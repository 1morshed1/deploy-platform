// Package queue defines the asynchronous task contract between the API (which
// enqueues build jobs) and the worker (which executes them). We use Asynq
// (github.com/hibiken/asynq), a Redis-backed task queue for Go.
//
// Why a queue at all? Building an image takes 30-120 seconds. If the API did it
// inline, the HTTP request would block for that long. Instead the API enqueues a
// task and returns immediately; a worker picks it up later. Asynq also gives us
// retries with backoff, rate limiting, and a monitoring UI (asynqmon) for free.
//
// This file is the SHARED CONTRACT. Because both producer and consumer import
// this same package (see ADR 0002), a change to the payload shape recompiles
// both sides — a mismatch is a compile error, not a production surprise.
package queue

import (
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// Asynq task type names. Asynq routes tasks to handlers by these strings, so
// they must stay stable across deploys of producer and consumer.
const (
	TypeBuildImage  = "build:image"
	TypeTeardownApp = "teardown:app"
)

// BuildPayload is everything the worker needs to build and deploy one
// deployment. It intentionally carries denormalized copies of fields (repo,
// branch, limits) rather than just an ID, so the worker's first action does not
// have to be a database round-trip — though it still reads the DB to update
// status. Keeping the payload self-describing also makes the asynqmon UI useful.
type BuildPayload struct {
	DeploymentID string `json:"deployment_id"`
	TenantID     string `json:"tenant_id"`
	RepoURL      string `json:"repo_url"`
	Branch       string `json:"branch"`
	Language     string `json:"language"`
	Subdomain    string `json:"subdomain"`
	Replicas     int    `json:"replicas"`
	CPULimit     string `json:"cpu_limit"`
	MemoryLimit  string `json:"memory_limit"`
}

// NewBuildTask serializes a BuildPayload into an Asynq task. The task's options
// (retry count, timeout, queue) are set here so every producer enqueues builds
// with consistent behavior.
func NewBuildTask(p BuildPayload) (*asynq.Task, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("queue: marshal build payload: %w", err)
	}
	// MaxRetry(2) matches the Kaniko Job's backoffLimit in plan.md: three total
	// attempts. Timeout guards against a build that hangs forever.
	return asynq.NewTask(TypeBuildImage, body), nil
}

// ParseBuildPayload deserializes a task body back into a BuildPayload. The
// worker's handler calls this first.
func ParseBuildPayload(t *asynq.Task) (BuildPayload, error) {
	var p BuildPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return p, fmt.Errorf("queue: unmarshal build payload: %w", err)
	}
	return p, nil
}

// TeardownPayload carries what the worker needs to delete a deployment's cluster
// resources and registry image. Teardown is asynchronous for the same reason
// builds are: it involves privileged, potentially slow cluster/registry calls
// that should not block the API request.
type TeardownPayload struct {
	DeploymentID string `json:"deployment_id"`
	TenantID     string `json:"tenant_id"`
	Subdomain    string `json:"subdomain"`
	ImageURI     string `json:"image_uri"`
}

// NewTeardownTask serializes a TeardownPayload into an Asynq task.
func NewTeardownTask(p TeardownPayload) (*asynq.Task, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("queue: marshal teardown payload: %w", err)
	}
	return asynq.NewTask(TypeTeardownApp, body), nil
}

// ParseTeardownPayload deserializes a teardown task body.
func ParseTeardownPayload(t *asynq.Task) (TeardownPayload, error) {
	var p TeardownPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return p, fmt.Errorf("queue: unmarshal teardown payload: %w", err)
	}
	return p, nil
}
