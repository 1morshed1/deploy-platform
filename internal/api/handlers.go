package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/1morshed1/deploy-platform/internal/store"
	"github.com/gin-gonic/gin"
)

// deployRequest is the JSON body of POST /api/v1/deploy. gin's binding tags give
// us free required-field validation.
type deployRequest struct {
	RepoURL string `json:"repo_url" binding:"required"`
	Branch  string `json:"branch"`
	AppName string `json:"app_name" binding:"required"`
}

// deploymentResponse is the JSON view of a deployment returned to clients. We
// map the internal model to a response DTO so internal columns can change
// without breaking the API contract, and so we can compute derived fields like
// the fully-qualified URL.
type deploymentResponse struct {
	ID           string `json:"id"`
	RepoURL      string `json:"repo_url"`
	Branch       string `json:"branch"`
	Language     string `json:"language,omitempty"`
	Status       string `json:"status"`
	URL          string `json:"url,omitempty"`
	ImageURI     string `json:"image_uri,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func (s *Server) toResponse(d *store.Deployment) deploymentResponse {
	r := deploymentResponse{
		ID:           d.ID,
		RepoURL:      d.RepoURL,
		Branch:       d.Branch,
		Language:     d.DetectedLang,
		Status:       string(d.BuildStatus),
		ImageURI:     d.ImageURI,
		ErrorMessage: d.ErrorMessage,
		CreatedAt:    d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    d.UpdatedAt.Format(time.RFC3339),
	}
	if d.Subdomain != "" {
		r.URL = fmt.Sprintf("https://%s.%s", d.Subdomain, s.cfg.PlatformDomain)
	}
	return r
}

// Deploy handles POST /api/v1/deploy. It is the heart of the API: validate,
// detect language, persist, enqueue — then return fast.
func (s *Server) Deploy(c *gin.Context) {
	tenant := tenantFromContext(c)

	var req deployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, "repo_url and app_name are required")
		return
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = "main"
	}

	// Detect the language. This also validates the repo/branch is reachable, so
	// we surface a clear 400 instead of failing deep inside a build minutes later.
	lang, err := s.detector.Detect(c.Request.Context(), req.RepoURL, branch)
	if err != nil {
		s.log.Info("deploy: detection failed", "repo", req.RepoURL, "branch", branch, "err", err)
		abortError(c, http.StatusBadRequest, "could not detect language: "+err.Error())
		return
	}

	// Build the row with platform defaults from the spec.
	d := &store.Deployment{
		TenantID:     tenant.ID,
		RepoURL:      req.RepoURL,
		Branch:       branch,
		DetectedLang: lang,
		Subdomain:    GenerateSubdomain(req.AppName),
		Replicas:     1,
		CPULimit:     "250m",
		MemoryLimit:  "256Mi",
	}
	created, err := s.store.CreateDeployment(c.Request.Context(), d)
	if err != nil {
		s.log.Error("deploy: create row failed", "err", err)
		abortError(c, http.StatusInternalServerError, "could not create deployment")
		return
	}

	// Enqueue the build. If Redis is down we mark the deployment failed so the
	// user isn't left with a row stuck forever in 'queued'.
	_, err = s.queue.EnqueueBuild(queuePayloadFor(created))
	if err != nil {
		s.log.Error("deploy: enqueue failed", "id", created.ID, "err", err)
		_ = s.store.UpdateStatus(c.Request.Context(), created.ID, store.StatusFailed, "failed to enqueue build")
		abortError(c, http.StatusInternalServerError, "could not enqueue build")
		return
	}

	// 202 Accepted: the work is queued, not done. The client polls GET or
	// streams logs to follow progress.
	c.JSON(http.StatusAccepted, s.toResponse(created))
}

// Get handles GET /api/v1/deployments/:id.
func (s *Server) Get(c *gin.Context) {
	tenant := tenantFromContext(c)
	d, err := s.store.GetDeployment(c.Request.Context(), tenant.ID, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		abortError(c, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		s.log.Error("get: store error", "err", err)
		abortError(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, s.toResponse(d))
}

// List handles GET /api/v1/deployments.
func (s *Server) List(c *gin.Context) {
	tenant := tenantFromContext(c)
	ds, err := s.store.ListDeployments(c.Request.Context(), tenant.ID)
	if err != nil {
		s.log.Error("list: store error", "err", err)
		abortError(c, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]deploymentResponse, 0, len(ds))
	for _, d := range ds {
		out = append(out, s.toResponse(d))
	}
	c.JSON(http.StatusOK, gin.H{"deployments": out})
}

// Delete handles DELETE /api/v1/deployments/:id. It verifies ownership, then
// enqueues an asynchronous teardown (cluster + registry cleanup happen in the
// worker, which holds the privileged credentials).
func (s *Server) Delete(c *gin.Context) {
	tenant := tenantFromContext(c)
	d, err := s.store.GetDeployment(c.Request.Context(), tenant.ID, c.Param("id"))
	if errors.Is(err, store.ErrNotFound) {
		abortError(c, http.StatusNotFound, "deployment not found")
		return
	}
	if err != nil {
		s.log.Error("delete: store error", "err", err)
		abortError(c, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := s.queue.EnqueueTeardown(teardownPayloadFor(d)); err != nil {
		s.log.Error("delete: enqueue teardown failed", "id", d.ID, "err", err)
		abortError(c, http.StatusInternalServerError, "could not schedule teardown")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "teardown scheduled", "id": d.ID})
}

// Logs handles GET /api/v1/deployments/:id/logs as Server-Sent Events. The
// client (browser EventSource or `curl -N`) receives each build-log line as it
// is written. We poll the DB with a monotonic id cursor rather than holding a
// long DB connection or a pub/sub subscription — simple, and robust to worker
// restarts (see the learning doc 01 for the trade-off).
func (s *Server) Logs(c *gin.Context) {
	tenant := tenantFromContext(c)
	id := c.Param("id")

	// Ownership check up front so we don't stream another tenant's logs.
	if _, err := s.store.GetDeployment(c.Request.Context(), tenant.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			abortError(c, http.StatusNotFound, "deployment not found")
			return
		}
		abortError(c, http.StatusInternalServerError, "internal error")
		return
	}

	// SSE headers. X-Accel-Buffering:no tells nginx not to buffer the stream.
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		abortError(c, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := c.Request.Context()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var lastID int64
	for {
		logs, err := s.store.ListBuildLogsAfter(ctx, id, lastID)
		if err != nil {
			// Context cancelled (client left) is the normal exit, not an error.
			if ctx.Err() != nil {
				return
			}
			s.log.Error("logs: query failed", "id", id, "err", err)
			writeSSEEvent(c, "error", "log stream error")
			flusher.Flush()
			return
		}
		for _, l := range logs {
			writeSSEData(c, l.ID, l.LogLine)
			lastID = l.ID
		}
		flusher.Flush()

		// If the deployment has reached a terminal state, drain once more (done
		// above) then close the stream so the client's EventSource stops.
		d, err := s.store.GetDeployment(ctx, tenant.ID, id)
		if err == nil && isTerminal(d.BuildStatus) {
			writeSSEEvent(c, "end", string(d.BuildStatus))
			flusher.Flush()
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// isTerminal reports whether a status is an end state (no more logs coming).
func isTerminal(s store.BuildStatus) bool {
	return s == store.StatusLive || s == store.StatusFailed
}

// writeSSEData writes one build-log line as an SSE "message" event. Each physical
// line of the payload is prefixed with "data:" per the SSE spec; the blank line
// terminates the event. The id field lets a reconnecting client resume.
func writeSSEData(c *gin.Context, id int64, line string) {
	fmt.Fprintf(c.Writer, "id: %d\n", id)
	for _, physical := range strings.Split(line, "\n") {
		fmt.Fprintf(c.Writer, "data: %s\n", physical)
	}
	fmt.Fprint(c.Writer, "\n")
}

// writeSSEEvent writes a named SSE event (e.g. "end", "error").
func writeSSEEvent(c *gin.Context, event, data string) {
	fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
}
