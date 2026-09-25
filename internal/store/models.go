package store

import "time"

// BuildStatus is the state machine a deployment moves through. Using a typed
// string (rather than a bare string) means the compiler helps us catch typos
// and lets us attach the valid-transitions logic below.
type BuildStatus string

const (
	StatusQueued    BuildStatus = "queued"    // accepted, waiting for a worker
	StatusBuilding  BuildStatus = "building"  // Kaniko job is running
	StatusBuilt     BuildStatus = "built"     // image pushed to the registry
	StatusDeploying BuildStatus = "deploying" // TenantApp created, operator working
	StatusLive      BuildStatus = "live"      // ingress serving traffic
	StatusFailed    BuildStatus = "failed"    // terminal error; see error_message
)

// Tenant is a customer of the platform.
type Tenant struct {
	ID        string
	Email     string
	APIKey    string // stored as a SHA-256 hex digest, never the raw key
	CreatedAt time.Time
}

// Deployment is one build+run attempt for one repo/branch owned by one tenant.
type Deployment struct {
	ID           string
	TenantID     string
	RepoURL      string
	Branch       string
	DetectedLang string
	BuildStatus  BuildStatus
	ImageURI     string
	Subdomain    string
	Replicas     int
	CPULimit     string
	MemoryLimit  string
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// BuildLog is a single line of build output, kept for replay after the build
// pod is gone. ID is a monotonic cursor used by the SSE stream.
type BuildLog struct {
	ID           int64
	DeploymentID string
	LogLine      string
	Timestamp    time.Time
}
