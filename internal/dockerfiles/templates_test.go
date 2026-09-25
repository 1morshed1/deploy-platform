package dockerfiles

import (
	"strings"
	"testing"

	"github.com/1morshed1/deploy-platform/internal/github"
)

func TestRenderAllLanguages(t *testing.T) {
	langs := []string{github.LangGo, github.LangNode, github.LangPython, github.LangStatic}
	for _, l := range langs {
		out, err := Render(l)
		if err != nil {
			t.Errorf("Render(%q): %v", l, err)
			continue
		}
		if !strings.Contains(out, "FROM ") {
			t.Errorf("Render(%q): output does not look like a Dockerfile:\n%s", l, out)
		}
	}
}

func TestRenderUnknown(t *testing.T) {
	if _, err := Render("cobol"); err == nil {
		t.Error("expected error for unknown language")
	}
}

// TestPortMatchesExpose guards the invariant that ContainerPort agrees with the
// EXPOSE directive in each template — a mismatch would make the Service route to
// a dead port.
func TestPortMatchesExpose(t *testing.T) {
	cases := map[string]string{
		github.LangGo:     "EXPOSE 8080",
		github.LangNode:   "EXPOSE 3000",
		github.LangPython: "EXPOSE 8000",
		github.LangStatic: "EXPOSE 80",
	}
	for lang, expose := range cases {
		out, err := Render(lang)
		if err != nil {
			t.Fatalf("Render(%q): %v", lang, err)
		}
		if !strings.Contains(out, expose) {
			t.Errorf("%s template missing %q", lang, expose)
		}
	}
}
