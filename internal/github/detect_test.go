package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOwnerRepo(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		wantErr     bool
	}{
		{"https://github.com/gin-gonic/gin", "gin-gonic", "gin", false},
		{"https://github.com/gin-gonic/gin.git", "gin-gonic", "gin", false},
		{"https://github.com/gin-gonic/gin/", "gin-gonic", "gin", false},
		{"https://gitlab.com/foo/bar", "", "", true},   // wrong host
		{"https://github.com/onlyowner", "", "", true}, // missing repo
		{"::::not a url", "", "", true},
	}
	for _, c := range cases {
		owner, repo, err := ownerRepo(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ownerRepo(%q): expected error, got none", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ownerRepo(%q): unexpected error: %v", c.in, err)
			continue
		}
		if owner != c.owner || repo != c.repo {
			t.Errorf("ownerRepo(%q) = %q/%q, want %q/%q", c.in, owner, repo, c.owner, c.repo)
		}
	}
}

// newTestDetector points a Detector at a fake GitHub that returns the given
// directory listing JSON for any request.
func newTestDetector(t *testing.T, listing string) *Detector {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listing))
	}))
	t.Cleanup(srv.Close)
	return &Detector{baseURL: srv.URL, httpClient: srv.Client()}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name     string
		listing  string
		wantLang string
		wantErr  bool
	}{
		{"go", `[{"name":"go.mod","type":"file"},{"name":"main.go","type":"file"}]`, LangGo, false},
		{"node", `[{"name":"package.json","type":"file"}]`, LangNode, false},
		{"python-requirements", `[{"name":"requirements.txt","type":"file"}]`, LangPython, false},
		{"python-pyproject", `[{"name":"pyproject.toml","type":"file"}]`, LangPython, false},
		{"static", `[{"name":"index.html","type":"file"}]`, LangStatic, false},
		// Node takes precedence over static when both markers exist.
		{"node-over-static", `[{"name":"package.json","type":"file"},{"name":"index.html","type":"file"}]`, LangNode, false},
		{"unknown", `[{"name":"README.md","type":"file"}]`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newTestDetector(t, c.listing)
			lang, err := d.Detect(context.Background(), "https://github.com/o/r", "main")
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got lang=%q", lang)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if lang != c.wantLang {
				t.Errorf("Detect = %q, want %q", lang, c.wantLang)
			}
		})
	}
}

func TestDetectNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	d := &Detector{baseURL: srv.URL, httpClient: srv.Client()}
	_, err := d.Detect(context.Background(), "https://github.com/o/r", "main")
	if err != ErrRepoNotAccessible {
		t.Fatalf("expected ErrRepoNotAccessible, got %v", err)
	}
}
