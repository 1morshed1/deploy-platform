// Package dockerfiles provides the per-language Dockerfile templates that the
// platform injects into each build. The user's repo need not contain a
// Dockerfile — the platform generates one based on the detected language and
// hands it to Kaniko (see internal/builder and learning doc 03).
//
// The Dockerfiles are embedded into the binary with go:embed so there are no
// external files to ship or mount — the worker container is fully self-contained.
package dockerfiles

import (
	"embed"
	"fmt"

	"github.com/1morshed1/deploy-platform/internal/github"
)

//go:embed *.Dockerfile
var files embed.FS

// Render returns the Dockerfile text for a detected language. The language
// strings are the same constants produced by internal/github.
func Render(lang string) (string, error) {
	var name string
	switch lang {
	case github.LangGo:
		name = "go.Dockerfile"
	case github.LangNode:
		name = "node.Dockerfile"
	case github.LangPython:
		name = "python.Dockerfile"
	case github.LangStatic:
		name = "static.Dockerfile"
	default:
		return "", fmt.Errorf("dockerfiles: no template for language %q", lang)
	}
	b, err := files.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("dockerfiles: read %s: %w", name, err)
	}
	return string(b), nil
}

// ContainerPort returns the port the built image listens on for a given
// language. This must match the EXPOSE line in the corresponding Dockerfile —
// the operator uses it as the Service targetPort. Keeping it here next to the
// templates means the two never drift.
func ContainerPort(lang string) int32 {
	switch lang {
	case github.LangGo:
		return 8080
	case github.LangNode:
		return 3000
	case github.LangPython:
		return 8000
	case github.LangStatic:
		return 80
	default:
		return 8080
	}
}
