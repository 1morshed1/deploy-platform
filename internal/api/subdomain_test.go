package api

import (
	"regexp"
	"strings"
	"testing"
)

// rfc1123Label is the shape a valid DNS label must have. We assert every
// generated subdomain matches it, because an invalid label would make the
// Ingress host illegal and the deployment unreachable.
var rfc1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestGenerateSubdomain(t *testing.T) {
	inputs := []string{
		"My Cool App!",
		"UPPER",
		"hasspaces and $ymbols",
		"----leading-and-trailing----",
		"",                       // empty should still produce a valid label
		strings.Repeat("x", 200), // over-long must be truncated to <= 63
	}
	for _, in := range inputs {
		got := GenerateSubdomain(in)
		if len(got) > 63 {
			t.Errorf("GenerateSubdomain(%q) = %q, length %d exceeds 63", in, got, len(got))
		}
		if !rfc1123Label.MatchString(got) {
			t.Errorf("GenerateSubdomain(%q) = %q, not a valid DNS label", in, got)
		}
	}
}

func TestGenerateSubdomainUniqueSuffix(t *testing.T) {
	// Same input should (almost always) yield different subdomains thanks to the
	// random suffix. We assert the base prefix is shared but the whole differs.
	a := GenerateSubdomain("api")
	b := GenerateSubdomain("api")
	if !strings.HasPrefix(a, "api-") || !strings.HasPrefix(b, "api-") {
		t.Fatalf("expected 'api-' prefix, got %q and %q", a, b)
	}
	if a == b {
		t.Errorf("expected different suffixes, both were %q", a)
	}
}
