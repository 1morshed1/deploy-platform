package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/1morshed1/deploy-platform/internal/store"
	"github.com/gin-gonic/gin"
)

// contextKeyTenant is the gin.Context key under which the authenticated tenant
// is stashed by the auth middleware for handlers to read.
const contextKeyTenant = "tenant"

// apiKeyPrefix makes keys recognizable in logs/config and lets us reject
// obviously-wrong tokens early.
const apiKeyPrefix = "dp_"

// GenerateAPIKey returns a new random API key (the raw value shown to the user
// once) and its SHA-256 hash (what we store). We store only the hash so a
// database leak does not expose usable credentials — see ADR 0004.
func GenerateAPIKey() (raw, hash string, err error) {
	buf := make([]byte, 24) // 24 bytes -> 48 hex chars, plenty of entropy
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = apiKeyPrefix + hex.EncodeToString(buf)
	return raw, HashAPIKey(raw), nil
}

// HashAPIKey computes the storage form of an API key. SHA-256 is appropriate
// here (not bcrypt) because API keys are high-entropy random tokens, not
// low-entropy human passwords — there is nothing to brute-force, so a fast hash
// is fine and lets us look tenants up by an indexed hash column.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// AuthMiddleware authenticates each request by its "Authorization: Bearer <key>"
// header. On success it stores the tenant in the context; on failure it aborts
// with 401. Handlers downstream can assume a tenant is present.
func (s *Server) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := bearerToken(c.Request.Header.Get("Authorization"))
		if err != nil {
			abortError(c, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		tenant, err := s.store.GetTenantByAPIKeyHash(c.Request.Context(), HashAPIKey(raw))
		if err != nil {
			// Same generic message whether the key is unknown or a DB error, so
			// we don't leak which keys exist. Real errors are still logged.
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Error("auth: store lookup failed", "err", err)
			}
			abortError(c, http.StatusUnauthorized, "invalid API key")
			return
		}
		// Constant-time compare of the stored hash against the recomputed hash.
		// Lookup already matched, but this guards against any future non-indexed
		// comparison path and documents the intent.
		if subtle.ConstantTimeCompare([]byte(tenant.APIKey), []byte(HashAPIKey(raw))) != 1 {
			abortError(c, http.StatusUnauthorized, "invalid API key")
			return
		}
		c.Set(contextKeyTenant, tenant)
		c.Next()
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(header string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", errors.New("not a bearer token")
	}
	tok := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if tok == "" {
		return "", errors.New("empty bearer token")
	}
	return tok, nil
}

// tenantFromContext retrieves the tenant the auth middleware stored. It panics
// if called on a route that isn't behind AuthMiddleware — that would be a
// programming error, not a runtime condition.
func tenantFromContext(c *gin.Context) *store.Tenant {
	v, ok := c.Get(contextKeyTenant)
	if !ok {
		panic("tenantFromContext called without AuthMiddleware")
	}
	return v.(*store.Tenant)
}
