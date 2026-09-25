// Package config centralizes all runtime configuration for the platform's
// binaries (api, worker, operator).
//
// Design decision: we read configuration exclusively from environment
// variables. In a Kubernetes world, config comes from ConfigMaps and Secrets
// that are surfaced to the container as env vars, so reading env vars keeps the
// code cloud-native and 12-factor (https://12factor.net/config). There is no
// config file to mount, parse, or keep in sync.
//
// Every field has a sensible default aimed at *local development* (kind +
// docker-compose), so a fresh checkout runs with zero configuration. Production
// overrides everything via the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved configuration for a process. It is built once at
// startup by Load and then passed explicitly to the components that need it
// (constructor injection) rather than being read from globals. Explicit
// dependencies make the code easier to test.
type Config struct {
	// HTTPAddr is the address the API server's HTTP listener binds to.
	HTTPAddr string

	// DatabaseURL is a standard PostgreSQL connection string (DSN).
	// Example: postgres://user:pass@localhost:5432/deploy_platform?sslmode=disable
	DatabaseURL string

	// RedisAddr is host:port for Redis, used both by Asynq (the task queue)
	// and by the API's rate limiter.
	RedisAddr string

	// RedisPassword is optional; empty means no auth (typical for local dev).
	RedisPassword string

	// GitHubToken is an optional PAT. The language-detection code calls the
	// GitHub REST API; unauthenticated requests are limited to 60/hour, while
	// a token raises that to 5000/hour. Detection still works without it for
	// public repos at low volume.
	GitHubToken string

	// PlatformDomain is the wildcard base domain. A deployed app called
	// "myapp" becomes "myapp.<PlatformDomain>". DNS for *.<PlatformDomain>
	// must point at the ingress controller's load balancer.
	PlatformDomain string

	// ECRRegistry is the base URI of the container registry that built images
	// are pushed to, e.g. 123456789012.dkr.ecr.us-east-1.amazonaws.com/tenants
	ECRRegistry string

	// BuildNamespace is the Kubernetes namespace where Kaniko build Jobs run.
	BuildNamespace string

	// SystemNamespace is where the platform's own TenantApp custom resources
	// live (one per deployment). The operator watches this namespace. Tenant
	// workloads run in their OWN per-tenant namespaces, created by the operator.
	SystemNamespace string

	// RateLimitPerHour is the number of deploy requests each tenant may make
	// per rolling hour. The plan specifies 5.
	RateLimitPerHour int

	// MaxConcurrentBuilds caps how many builds Asynq processes at once. The
	// plan caps the dev environment at 3 concurrent deployments.
	MaxConcurrentBuilds int

	// ShutdownTimeout bounds how long we wait for in-flight HTTP requests to
	// drain during graceful shutdown.
	ShutdownTimeout time.Duration
}

// Load reads configuration from the environment, applying defaults. It returns
// an error only when a value is present but malformed (e.g. a non-numeric rate
// limit); missing values fall back to defaults rather than erroring, so the
// happy path for local dev is "just run it".
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:            getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:         getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/deploy_platform?sslmode=disable"),
		RedisAddr:           getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:       getEnv("REDIS_PASSWORD", ""),
		GitHubToken:         getEnv("GITHUB_TOKEN", ""),
		PlatformDomain:      getEnv("PLATFORM_DOMAIN", "apps.localhost"),
		ECRRegistry:         getEnv("ECR_REGISTRY", "registry.local/tenants"),
		BuildNamespace:      getEnv("BUILD_NAMESPACE", "platform-builds"),
		SystemNamespace:     getEnv("SYSTEM_NAMESPACE", "platform-system"),
		RateLimitPerHour:    5,
		MaxConcurrentBuilds: 3,
		ShutdownTimeout:     15 * time.Second,
	}

	var err error
	if cfg.RateLimitPerHour, err = getEnvInt("RATE_LIMIT_PER_HOUR", 5); err != nil {
		return nil, err
	}
	if cfg.MaxConcurrentBuilds, err = getEnvInt("MAX_CONCURRENT_BUILDS", 3); err != nil {
		return nil, err
	}

	return cfg, nil
}

// getEnv returns the value of key, or def when the variable is unset or empty.
func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// getEnvInt parses an integer env var, returning def when unset. A malformed
// value is an error because silently ignoring it would hide a misconfiguration.
func getEnvInt(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer, got %q: %w", key, raw, err)
	}
	return n, nil
}
