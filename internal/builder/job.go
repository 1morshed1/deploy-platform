// Package builder turns a build request into a running Kaniko Job in Kubernetes,
// streams its logs, waits for completion, and reports the resulting image URI.
//
// This file holds the PURE constructors that build the Kubernetes objects (a
// ConfigMap holding the generated Dockerfile, and the Kaniko Job). They take
// plain inputs and return API objects with no side effects, so they are fully
// unit-testable without a cluster. The orchestration that actually talks to the
// cluster lives in builder.go.
package builder

import (
	"fmt"
	"net/url"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// Mount paths inside the Kaniko pod.
const (
	dockerfileMountPath = "/config"         // where the generated Dockerfile ConfigMap is mounted
	dockerConfigPath    = "/kaniko/.docker" // where the registry credentials Secret is mounted
	kanikoImage         = "gcr.io/kaniko-project/executor:v1.23.2"
)

// KanikoParams is everything needed to render the build Job and its ConfigMap.
type KanikoParams struct {
	DeploymentID   string // used to name the Job/ConfigMap and as the image tag
	RepoURL        string // https://github.com/owner/repo
	Branch         string
	Destination    string // full image ref, e.g. registry/tenants:<tag>
	CacheRepo      string // registry path used for layer cache
	Namespace      string // build namespace (e.g. platform-builds)
	DockerfileText string // the generated Dockerfile contents
	RegistrySecret string // name of the Secret holding registry creds (docker config json)
}

// jobName / configMapName derive stable, DNS-safe names from the deployment id.
func jobName(deploymentID string) string       { return truncate63("build-" + deploymentID) }
func configMapName(deploymentID string) string { return truncate63("dockerfile-" + deploymentID) }

// truncate63 keeps Kubernetes object names within the 63-char DNS-1123 limit.
func truncate63(s string) string {
	if len(s) > 63 {
		return strings.TrimRight(s[:63], "-")
	}
	return s
}

// GitContext converts an https GitHub URL into the git:// context string Kaniko
// understands, pinned to a branch. Kaniko clones this as the build context.
//
//	https://github.com/o/r(.git) + main  ->  git://github.com/o/r.git#refs/heads/main
func GitContext(repoURL, branch string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return "", fmt.Errorf("builder: invalid repo url: %w", err)
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return "", fmt.Errorf("builder: cannot derive git context from %q", repoURL)
	}
	return fmt.Sprintf("git://%s/%s.git#refs/heads/%s", u.Host, path, branch), nil
}

// DockerfileConfigMap holds the platform-generated Dockerfile. We inject the
// Dockerfile via a ConfigMap (rather than committing one to the user's repo)
// because the whole point of a PaaS is that the user doesn't have to write one.
func DockerfileConfigMap(p KanikoParams) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(p.DeploymentID),
			Namespace: p.Namespace,
			Labels:    buildLabels(p.DeploymentID),
		},
		Data: map[string]string{"Dockerfile": p.DockerfileText},
	}
}

// buildLabels tag build objects so they can be found and cleaned up by selector.
func buildLabels(deploymentID string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "deploy-platform",
		"platform.example.com/build":   deploymentID,
	}
}

// KanikoJob builds the Job that runs Kaniko. See the spec and learning doc 03 for
// the meaning of each argument.
func KanikoJob(p KanikoParams) (*batchv1.Job, error) {
	gitContext, err := GitContext(p.RepoURL, p.Branch)
	if err != nil {
		return nil, err
	}

	args := []string{
		// Use the Dockerfile we mounted from the ConfigMap, not one in the repo.
		"--dockerfile=" + dockerfileMountPath + "/Dockerfile",
		// Clone the user's repo as the build context.
		"--context=" + gitContext,
		// Push the result here.
		"--destination=" + p.Destination,
		// Reuse cached layers across builds to speed up rebuilds.
		"--cache=true",
	}
	if p.CacheRepo != "" {
		args = append(args, "--cache-repo="+p.CacheRepo)
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName(p.DeploymentID),
			Namespace: p.Namespace,
			Labels:    buildLabels(p.DeploymentID),
		},
		Spec: batchv1.JobSpec{
			// Kubernetes deletes the finished Job (and its pod) after 5 minutes,
			// so completed builds don't accumulate.
			TTLSecondsAfterFinished: ptr.To[int32](300),
			// Retry the build pod at most twice (3 attempts) before marking failed.
			BackoffLimit: ptr.To[int32](2),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: buildLabels(p.DeploymentID)},
				Spec: corev1.PodSpec{
					// A build pod runs once; never restart it in place.
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "kaniko",
						Image: kanikoImage,
						Args:  args,
						VolumeMounts: []corev1.VolumeMount{
							{Name: "dockerfile", MountPath: dockerfileMountPath},
							{Name: "docker-config", MountPath: dockerConfigPath},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "dockerfile",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName(p.DeploymentID),
									},
								},
							},
						},
						{
							Name: "docker-config",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: p.RegistrySecret,
									// Kaniko expects the file to be named config.json.
									Items: []corev1.KeyToPath{
										{Key: ".dockerconfigjson", Path: "config.json"},
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}
