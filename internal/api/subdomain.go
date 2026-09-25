package api

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

// dnsLabel matches characters that are NOT allowed in a DNS label. A valid label
// is lowercase alphanumerics and hyphens, not starting/ending with a hyphen, max
// 63 chars (RFC 1123). Subdomains become part of a hostname and a Kubernetes
// Ingress host, so they must be valid DNS labels.
var notDNSLabelChar = regexp.MustCompile(`[^a-z0-9-]+`)

// GenerateSubdomain turns a user-supplied app name into a unique, DNS-safe
// subdomain label. It lowercases, replaces invalid characters with hyphens,
// trims stray hyphens, and appends a short random suffix so two apps named
// "api" don't collide (the DB column is UNIQUE, but we avoid the collision
// up front rather than retrying on a constraint violation).
//
// Example: "My Cool App!" -> "my-cool-app-3f9a"
func GenerateSubdomain(appName string) string {
	base := strings.ToLower(strings.TrimSpace(appName))
	base = notDNSLabelChar.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "app"
	}
	// Leave room for a "-XXXX" suffix within the 63-char label limit.
	const maxBase = 63 - 5
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
	}
	return base + "-" + randomHex(2) // 2 bytes -> 4 hex chars
}

// randomHex returns n random bytes as a hex string. Uses crypto/rand because the
// suffix is a (weak) uniqueness token; math/rand would be predictable.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is catastrophic and effectively never happens;
		// a fixed fallback keeps the function total.
		return "0000"
	}
	return hex.EncodeToString(b)
}
