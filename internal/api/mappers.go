package api

import (
	"github.com/1morshed1/deploy-platform/internal/queue"
	"github.com/1morshed1/deploy-platform/internal/store"
)

// queuePayloadFor projects a persisted Deployment into the build task payload.
// Keeping this mapping in the api package (rather than making queue import store)
// preserves the queue package's independence from the database model.
func queuePayloadFor(d *store.Deployment) queue.BuildPayload {
	return queue.BuildPayload{
		DeploymentID: d.ID,
		TenantID:     d.TenantID,
		RepoURL:      d.RepoURL,
		Branch:       d.Branch,
		Language:     d.DetectedLang,
		Subdomain:    d.Subdomain,
		Replicas:     d.Replicas,
		CPULimit:     d.CPULimit,
		MemoryLimit:  d.MemoryLimit,
	}
}

// teardownPayloadFor projects a Deployment into the teardown task payload.
func teardownPayloadFor(d *store.Deployment) queue.TeardownPayload {
	return queue.TeardownPayload{
		DeploymentID: d.ID,
		TenantID:     d.TenantID,
		Subdomain:    d.Subdomain,
		ImageURI:     d.ImageURI,
	}
}
