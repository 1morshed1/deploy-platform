package builder

import (
	"strings"
	"testing"
)

func TestGitContext(t *testing.T) {
	cases := []struct {
		repo, branch, want string
		wantErr            bool
	}{
		{"https://github.com/o/r", "main", "git://github.com/o/r.git#refs/heads/main", false},
		{"https://github.com/o/r.git", "dev", "git://github.com/o/r.git#refs/heads/dev", false},
		{"https://github.com/o/r/", "main", "git://github.com/o/r.git#refs/heads/main", false},
		{"https://github.com/", "main", "", true},
	}
	for _, c := range cases {
		got, err := GitContext(c.repo, c.branch)
		if c.wantErr {
			if err == nil {
				t.Errorf("GitContext(%q): expected error", c.repo)
			}
			continue
		}
		if err != nil {
			t.Errorf("GitContext(%q): %v", c.repo, err)
		}
		if got != c.want {
			t.Errorf("GitContext(%q,%q) = %q, want %q", c.repo, c.branch, got, c.want)
		}
	}
}

func TestKanikoJobArgs(t *testing.T) {
	p := KanikoParams{
		DeploymentID:   "abc-123",
		RepoURL:        "https://github.com/o/r",
		Branch:         "main",
		Destination:    "reg/tenants:abc-123",
		CacheRepo:      "reg/tenants/cache",
		Namespace:      "platform-builds",
		DockerfileText: "FROM scratch",
		RegistrySecret: "ecr-credentials",
	}
	job, err := KanikoJob(p)
	if err != nil {
		t.Fatalf("KanikoJob: %v", err)
	}
	if job.Name != "build-abc-123" {
		t.Errorf("job name = %q", job.Name)
	}
	if job.Namespace != "platform-builds" {
		t.Errorf("job namespace = %q", job.Namespace)
	}
	c := job.Spec.Template.Spec.Containers[0]
	joined := strings.Join(c.Args, " ")
	for _, want := range []string{
		"--dockerfile=/config/Dockerfile",
		"--context=git://github.com/o/r.git#refs/heads/main",
		"--destination=reg/tenants:abc-123",
		"--cache=true",
		"--cache-repo=reg/tenants/cache",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("kaniko args missing %q; got: %s", want, joined)
		}
	}
	// The Job must not restart the build pod in place.
	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Errorf("restart policy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
}

func TestDockerfileConfigMap(t *testing.T) {
	p := KanikoParams{DeploymentID: "abc-123", Namespace: "platform-builds", DockerfileText: "FROM scratch"}
	cm := DockerfileConfigMap(p)
	if cm.Name != "dockerfile-abc-123" {
		t.Errorf("configmap name = %q", cm.Name)
	}
	if cm.Data["Dockerfile"] != "FROM scratch" {
		t.Errorf("configmap Dockerfile data = %q", cm.Data["Dockerfile"])
	}
}

func TestTruncate63(t *testing.T) {
	long := strings.Repeat("a", 100)
	if got := truncate63(long); len(got) > 63 {
		t.Errorf("truncate63 returned length %d", len(got))
	}
	if got := truncate63("short"); got != "short" {
		t.Errorf("truncate63(short) = %q", got)
	}
}
