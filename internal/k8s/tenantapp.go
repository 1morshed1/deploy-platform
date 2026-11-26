package k8s

import (
	"context"
	"fmt"

	platformv1 "github.com/1morshed1/deploy-platform/apis/platform/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// TenantAppSpec is the desired state the worker records on a TenantApp custom
// resource after a successful build. The operator reads this and reconciles the
// real cluster resources. It mirrors the CRD schema in deploy/crd.
type TenantAppSpec struct {
	ImageURI      string
	Subdomain     string
	Replicas      int
	CPULimit      string
	MemoryLimit   string
	ContainerPort int32
}

// ApplyTenantApp creates or updates a TenantApp object in the given namespace.
// The worker calls this once a build succeeds; creating the CR is the hand-off
// from the (imperative) build pipeline to the (declarative) operator.
//
// We implement a get-then-create-or-update rather than server-side apply to keep
// the dependency surface small and the behavior obvious.
func ApplyTenantApp(ctx context.Context, dyn dynamic.Interface, namespace, name string, spec TenantAppSpec) error {
	rc := dyn.Resource(platformv1.TenantAppGVR).Namespace(namespace)

	// unstructured numbers must be int64; the API server rejects other numeric
	// Go types. This is the main gotcha of the dynamic client.
	desiredSpec := map[string]interface{}{
		"imageUri":      spec.ImageURI,
		"subdomain":     spec.Subdomain,
		"replicas":      int64(spec.Replicas),
		"cpuLimit":      spec.CPULimit,
		"memoryLimit":   spec.MemoryLimit,
		"containerPort": int64(spec.ContainerPort),
	}

	existing, err := rc.Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		obj := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": platformv1.GroupVersion.String(),
			"kind":       platformv1.TenantAppGVK.Kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": desiredSpec,
		}}
		if _, err := rc.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("k8s: create tenantapp %s/%s: %w", namespace, name, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("k8s: get tenantapp %s/%s: %w", namespace, name, err)
	default:
		// Update only the spec, preserving metadata/resourceVersion so the update
		// is a proper optimistic-concurrency write.
		if err := unstructured.SetNestedMap(existing.Object, desiredSpec, "spec"); err != nil {
			return fmt.Errorf("k8s: set tenantapp spec: %w", err)
		}
		if _, err := rc.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("k8s: update tenantapp %s/%s: %w", namespace, name, err)
		}
		return nil
	}
}

// DeleteTenantApp deletes a TenantApp. The operator's finalizer then tears down
// the tenant's workload namespace and everything in it. A missing object is
// treated as success so teardown is idempotent (safe to retry).
func DeleteTenantApp(ctx context.Context, dyn dynamic.Interface, namespace, name string) error {
	rc := dyn.Resource(platformv1.TenantAppGVR).Namespace(namespace)
	err := rc.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("k8s: delete tenantapp %s/%s: %w", namespace, name, err)
	}
	return nil
}
