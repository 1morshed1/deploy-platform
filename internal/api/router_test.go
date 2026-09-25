package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newTestServer builds a Server suitable for testing routes that don't touch the
// database (health, and auth rejection which happens before any store call). The
// data dependencies are left nil deliberately — any test that exercised them would
// panic, which is the signal that it needs real fakes.
func newTestServer() *Server {
	gin.SetMode(gin.TestMode)
	return &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestHealthz(t *testing.T) {
	r := newTestServer().Routes()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", w.Code)
	}
}

func TestProtectedRoutesRejectMissingAuth(t *testing.T) {
	r := newTestServer().Routes()
	// Auth is checked before any handler/store access, so these return 401 with a
	// nil store — proving the middleware short-circuits correctly.
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/deployments"},
		{http.MethodGet, "/api/v1/deployments/abc"},
		{http.MethodGet, "/api/v1/deployments/abc/logs"},
		{http.MethodDelete, "/api/v1/deployments/abc"},
		{http.MethodPost, "/api/v1/deploy"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, c.path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without auth = %d, want 401", c.method, c.path, w.Code)
		}
	}
}

func TestMalformedAuthHeaderRejected(t *testing.T) {
	r := newTestServer().Routes()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/deployments", nil)
	req.Header.Set("Authorization", "Basic not-a-bearer-token")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("malformed auth = %d, want 401", w.Code)
	}
}
