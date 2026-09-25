// Package github detects a repository's language by inspecting the files at its
// root via the GitHub REST API. This is how the platform decides which
// Dockerfile template to generate for the build.
//
// We deliberately do NOT clone the repo to detect its language — that would be
// slow and would run on the API request path. A single API call listing the
// root directory is enough to find the marker files (go.mod, package.json, ...).
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Supported languages. These strings are stored in deployments.detected_lang
// and drive Dockerfile selection in internal/dockerfiles.
const (
	LangGo     = "go"
	LangNode   = "node"
	LangPython = "python"
	LangStatic = "static"
)

// ErrUnknownLanguage means no recognized marker file was found at the repo root.
var ErrUnknownLanguage = errors.New("github: unable to detect project language")

// ErrRepoNotAccessible means the repo/branch could not be listed (private,
// missing, or a bad branch name).
var ErrRepoNotAccessible = errors.New("github: repository or branch not accessible")

// Detector performs language detection. baseURL and httpClient are fields (not
// package globals) so tests can point it at an httptest.Server. token is an
// optional PAT that raises the GitHub rate limit from 60 to 5000 req/hour.
type Detector struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewDetector builds a Detector against the real GitHub API. Pass "" for token
// to make unauthenticated (rate-limited) requests.
func NewDetector(token string) *Detector {
	return &Detector{
		baseURL:    "https://api.github.com",
		token:      token,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ownerRepo extracts "owner" and "repo" from a GitHub URL. It accepts the common
// forms: https://github.com/owner/repo, .../owner/repo.git, and a trailing
// slash. Returning a clear error here avoids a confusing 404 from the API later.
func ownerRepo(repoURL string) (owner, repo string, err error) {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return "", "", fmt.Errorf("github: invalid repo url %q: %w", repoURL, err)
	}
	if !strings.EqualFold(u.Host, "github.com") {
		return "", "", fmt.Errorf("github: only github.com repos are supported, got host %q", u.Host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("github: cannot parse owner/repo from %q", repoURL)
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	return owner, repo, nil
}

// contentEntry is one item in the GitHub "list directory contents" response.
type contentEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" or "dir"
}

// Detect lists the repo root for the given branch and maps marker files to a
// language. The precedence order (go > node > python > static) is deliberate:
// a repo may contain both an index.html and a package.json (a Node app that
// serves HTML), in which case it should be treated as a Node build, not static.
func (d *Detector) Detect(ctx context.Context, repoURL, branch string) (string, error) {
	names, err := d.listRoot(ctx, repoURL, branch)
	if err != nil {
		return "", err
	}
	has := func(name string) bool {
		for _, n := range names {
			if strings.EqualFold(n, name) {
				return true
			}
		}
		return false
	}

	switch {
	case has("go.mod"):
		return LangGo, nil
	case has("package.json"):
		return LangNode, nil
	case has("requirements.txt"), has("Pipfile"), has("pyproject.toml"):
		return LangPython, nil
	case has("index.html"):
		return LangStatic, nil
	default:
		return "", ErrUnknownLanguage
	}
}

// listRoot returns the file/dir names at the repo root for a branch.
func (d *Detector) listRoot(ctx context.Context, repoURL, branch string) ([]string, error) {
	owner, repo, err := ownerRepo(repoURL)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/?ref=%s",
		d.baseURL, url.PathEscape(owner), url.PathEscape(repo), url.QueryEscape(branch))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	// The GitHub API requires an Accept header and a User-Agent.
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "deploy-platform")
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through and parse
	case http.StatusNotFound:
		return nil, ErrRepoNotAccessible
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, fmt.Errorf("github: rate limited or forbidden (status %d); set GITHUB_TOKEN", resp.StatusCode)
	default:
		return nil, fmt.Errorf("github: unexpected status %d listing repo root", resp.StatusCode)
	}

	var entries []contentEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("github: decode contents: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names, nil
}
