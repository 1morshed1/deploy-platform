package builder

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"time"

	platformv1 "github.com/1morshed1/deploy-platform/apis/platform/v1"
	"github.com/1morshed1/deploy-platform/internal/config"
	"github.com/1morshed1/deploy-platform/internal/dockerfiles"
	pk8s "github.com/1morshed1/deploy-platform/internal/k8s"
	"github.com/1morshed1/deploy-platform/internal/queue"
	"github.com/1morshed1/deploy-platform/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Builder orchestrates one deployment's build + deploy + teardown. It is the
// only part of the worker that talks to the cluster.
type Builder struct {
	cs  kubernetes.Interface
	dyn dynamic.Interface
	st  *store.Store
	cfg *config.Config
	log *slog.Logger
}

// New constructs a Builder.
func New(cs kubernetes.Interface, dyn dynamic.Interface, st *store.Store, cfg *config.Config, log *slog.Logger) *Builder {
	return &Builder{cs: cs, dyn: dyn, st: st, cfg: cfg, log: log}
}

// appendLog persists a build-log line (for SSE replay) and mirrors it to the
// process log. Log persistence failures are non-fatal: a build should not fail
// just because we couldn't store a log line.
func (b *Builder) appendLog(ctx context.Context, deploymentID, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if err := b.st.AppendBuildLog(ctx, deploymentID, line); err != nil {
		b.log.Warn("append build log failed", "id", deploymentID, "err", err)
	}
}

// Build runs the Kaniko Job for a deployment and returns the pushed image URI.
// On any failure it marks the deployment failed and returns the error.
func (b *Builder) Build(ctx context.Context, p queue.BuildPayload) (string, error) {
	if err := b.st.UpdateStatus(ctx, p.DeploymentID, store.StatusBuilding, ""); err != nil {
		return "", err
	}
	b.appendLog(ctx, p.DeploymentID, "==> Build started for %s@%s (%s)", p.RepoURL, p.Branch, p.Language)

	dockerfile, err := dockerfiles.Render(p.Language)
	if err != nil {
		return "", b.fail(ctx, p.DeploymentID, err)
	}

	destination := fmt.Sprintf("%s:%s", b.cfg.ECRRegistry, p.DeploymentID)
	params := KanikoParams{
		DeploymentID:   p.DeploymentID,
		RepoURL:        p.RepoURL,
		Branch:         p.Branch,
		Destination:    destination,
		CacheRepo:      b.cfg.ECRRegistry + "/cache",
		Namespace:      b.cfg.BuildNamespace,
		DockerfileText: dockerfile,
		RegistrySecret: "ecr-credentials",
	}

	if err := b.applyConfigMap(ctx, DockerfileConfigMap(params)); err != nil {
		return "", b.fail(ctx, p.DeploymentID, err)
	}
	job, err := KanikoJob(params)
	if err != nil {
		return "", b.fail(ctx, p.DeploymentID, err)
	}
	if err := b.recreateJob(ctx, job); err != nil {
		return "", b.fail(ctx, p.DeploymentID, err)
	}
	b.appendLog(ctx, p.DeploymentID, "==> Kaniko build job %s created in %s", job.Name, job.Namespace)

	// Stream the build pod's logs into the DB so the user sees them live via SSE.
	b.streamBuildLogs(ctx, p.DeploymentID, params.Namespace)

	// Wait for the Job to reach a terminal condition.
	ok, err := b.waitForJob(ctx, params.Namespace, job.Name)
	if err != nil {
		return "", b.fail(ctx, p.DeploymentID, err)
	}
	if !ok {
		return "", b.fail(ctx, p.DeploymentID, fmt.Errorf("build job failed; see build logs"))
	}

	if err := b.st.SetImageURI(ctx, p.DeploymentID, destination); err != nil {
		return "", b.fail(ctx, p.DeploymentID, err)
	}
	if err := b.st.UpdateStatus(ctx, p.DeploymentID, store.StatusBuilt, ""); err != nil {
		return "", err
	}
	b.appendLog(ctx, p.DeploymentID, "==> Image pushed: %s", destination)
	return destination, nil
}

// Deploy hands the built image to the operator by creating/updating a TenantApp,
// then waits (best effort) for the workload to become ready before marking live.
func (b *Builder) Deploy(ctx context.Context, p queue.BuildPayload, imageURI string) error {
	spec := pk8s.TenantAppSpec{
		ImageURI:      imageURI,
		Subdomain:     p.Subdomain,
		Replicas:      p.Replicas,
		CPULimit:      p.CPULimit,
		MemoryLimit:   p.MemoryLimit,
		ContainerPort: dockerfiles.ContainerPort(p.Language),
	}
	if err := pk8s.ApplyTenantApp(ctx, b.dyn, b.cfg.SystemNamespace, p.Subdomain, spec); err != nil {
		return b.fail(ctx, p.DeploymentID, err)
	}
	if err := b.st.UpdateStatus(ctx, p.DeploymentID, store.StatusDeploying, ""); err != nil {
		return err
	}
	b.appendLog(ctx, p.DeploymentID, "==> TenantApp %s created; operator is reconciling", p.Subdomain)

	// Poll the operator-created Deployment for readiness. If it doesn't come up
	// within the window we leave the status at 'deploying' (not failed) — the
	// operator keeps trying and a later reconcile/status check can flip it live.
	ns := platformv1.WorkloadNamespace(p.Subdomain)
	if b.waitForWorkloadReady(ctx, ns, p.Subdomain, 3*time.Minute) {
		if err := b.st.UpdateStatus(ctx, p.DeploymentID, store.StatusLive, ""); err != nil {
			return err
		}
		b.appendLog(ctx, p.DeploymentID, "==> Live at https://%s.%s", p.Subdomain, b.cfg.PlatformDomain)
	} else {
		b.appendLog(ctx, p.DeploymentID, "==> Workload not ready yet; deployment remains in 'deploying'")
	}
	return nil
}

// Teardown deletes the TenantApp (the operator's finalizer removes the workload
// namespace), best-effort deletes the registry image, and removes the DB rows.
func (b *Builder) Teardown(ctx context.Context, p queue.TeardownPayload) error {
	if p.Subdomain != "" {
		if err := pk8s.DeleteTenantApp(ctx, b.dyn, b.cfg.SystemNamespace, p.Subdomain); err != nil {
			return err
		}
	}
	// Registry image deletion is provider-specific (ECR API). It is stubbed with
	// a clear log line; see learning doc 09 for what is real vs. simulated.
	if p.ImageURI != "" {
		b.log.Info("teardown: registry image deletion is a no-op stub", "image", p.ImageURI)
	}
	if err := b.st.DeleteDeployment(ctx, p.DeploymentID); err != nil && err != store.ErrNotFound {
		return err
	}
	return nil
}

// fail marks a deployment failed with the error message and returns the error so
// callers can `return b.fail(...)`.
func (b *Builder) fail(ctx context.Context, deploymentID string, cause error) error {
	b.appendLog(ctx, deploymentID, "!! Build failed: %v", cause)
	if err := b.st.UpdateStatus(ctx, deploymentID, store.StatusFailed, cause.Error()); err != nil {
		b.log.Error("failed to mark deployment failed", "id", deploymentID, "err", err)
	}
	return cause
}

// ---- cluster helpers -----------------------------------------------------

// applyConfigMap creates the Dockerfile ConfigMap, replacing any stale one.
func (b *Builder) applyConfigMap(ctx context.Context, cm *corev1.ConfigMap) error {
	cms := b.cs.CoreV1().ConfigMaps(cm.Namespace)
	_ = cms.Delete(ctx, cm.Name, metav1.DeleteOptions{}) // best effort
	if _, err := cms.Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("builder: create configmap: %w", err)
	}
	return nil
}

// recreateJob deletes any previous Job for this deployment (Jobs are immutable
// once created) and creates a fresh one.
func (b *Builder) recreateJob(ctx context.Context, job *batchv1.Job) error {
	jobs := b.cs.BatchV1().Jobs(job.Namespace)
	policy := metav1.DeletePropagationBackground
	_ = jobs.Delete(ctx, job.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
	if _, err := jobs.Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("builder: create job: %w", err)
	}
	return nil
}

// streamBuildLogs waits for the build pod to appear, then follows its logs,
// appending each line to the store. It returns when the container's stream ends
// (i.e. the build finished) or the context is cancelled.
func (b *Builder) streamBuildLogs(ctx context.Context, deploymentID, namespace string) {
	pod := b.waitForBuildPod(ctx, deploymentID, namespace, time.Minute)
	if pod == "" {
		b.appendLog(ctx, deploymentID, "!! build pod did not start in time")
		return
	}
	req := b.cs.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{Follow: true})
	stream, err := req.Stream(ctx)
	if err != nil {
		b.log.Warn("stream logs failed", "pod", pod, "err", err)
		return
	}
	defer stream.Close()

	sc := bufio.NewScanner(stream)
	for sc.Scan() {
		b.appendLog(ctx, deploymentID, "%s", sc.Text())
	}
}

// waitForBuildPod polls until a pod with this deployment's build label exists.
func (b *Builder) waitForBuildPod(ctx context.Context, deploymentID, namespace string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pods, err := b.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "platform.example.com/build=" + deploymentID,
		})
		if err == nil && len(pods.Items) > 0 {
			return pods.Items[0].Name
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return ""
		}
	}
	return ""
}

// waitForJob polls a Job until it reports Complete or Failed. Returns (true,nil)
// on success, (false,nil) on failure, or an error if polling itself failed.
func (b *Builder) waitForJob(ctx context.Context, namespace, name string) (bool, error) {
	for {
		job, err := b.cs.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// TTL may have collected it right after success; treat as done.
				return true, nil
			}
			return false, fmt.Errorf("builder: get job: %w", err)
		}
		for _, c := range job.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				return true, nil
			}
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				return false, nil
			}
		}
		if !sleepCtx(ctx, 3*time.Second) {
			return false, ctx.Err()
		}
	}
}

// waitForWorkloadReady polls the operator-created Deployment until it has at
// least one ready replica, or the timeout elapses.
func (b *Builder) waitForWorkloadReady(ctx context.Context, namespace, name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dep, err := b.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && dep.Status.ReadyReplicas >= 1 {
			return true
		}
		if !sleepCtx(ctx, 5*time.Second) {
			return false
		}
	}
	return false
}

// sleepCtx sleeps for d unless the context is cancelled first. Returns false if
// the context was cancelled (caller should stop).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
