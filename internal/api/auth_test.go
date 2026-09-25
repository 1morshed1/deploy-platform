package api

import (
	"strings"
	"testing"
)

func TestGenerateAPIKeyAndHash(t *testing.T) {
	raw, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if !strings.HasPrefix(raw, apiKeyPrefix) {
		t.Errorf("raw key %q missing prefix %q", raw, apiKeyPrefix)
	}
	// SHA-256 hex is always 64 characters.
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	// Hashing must be deterministic: the auth path recomputes it on each request.
	if HashAPIKey(raw) != hash {
		t.Errorf("HashAPIKey not deterministic")
	}
	// Different keys must not collide.
	raw2, hash2, _ := GenerateAPIKey()
	if raw == raw2 || hash == hash2 {
		t.Errorf("two generated keys collided")
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header  string
		want    string
		wantErr bool
	}{
		{"Bearer dp_abc123", "dp_abc123", false},
		{"Bearer   spaced  ", "spaced", false},
		{"Basic dp_abc123", "", true}, // wrong scheme
		{"dp_abc123", "", true},       // no scheme
		{"Bearer ", "", true},         // empty token
		{"", "", true},
	}
	for _, c := range cases {
		got, err := bearerToken(c.header)
		if c.wantErr {
			if err == nil {
				t.Errorf("bearerToken(%q): expected error", c.header)
			}
			continue
		}
		if err != nil {
			t.Errorf("bearerToken(%q): unexpected error %v", c.header, err)
		}
		if got != c.want {
			t.Errorf("bearerToken(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}
