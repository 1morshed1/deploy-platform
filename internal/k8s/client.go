// Package k8s provides thin helpers for talking to the Kubernetes API from the
// worker: a clientset (typed access to built-in resources like Jobs and Pods)
// and a dynamic client (schema-less access to our TenantApp custom resource).
//
// Why two clients? The typed clientset (client-go) knows the built-in APIs at
// compile time and is the right tool for Jobs/Pods. Our TenantApp CRD is not
// known to client-go, so we use the dynamic client, which works with
// unstructured JSON and any GroupVersionResource — no generated code needed.
// This keeps the worker decoupled from the operator's Go types (see ADR 0006).
package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RestConfig resolves how to reach the cluster. In production the worker runs as
// a pod and uses its ServiceAccount via in-cluster config. For local dev it falls
// back to KUBECONFIG (or ~/.kube/config), which is how you'd point it at a kind
// cluster. Trying in-cluster first, then kubeconfig, means the same binary works
// in both places with no flags.
func RestConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("k8s: cannot locate kubeconfig: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("k8s: build config from %s: %w", kubeconfig, err)
	}
	return cfg, nil
}

// NewClientset returns a typed clientset for built-in resources (Jobs, Pods, ...).
func NewClientset() (kubernetes.Interface, error) {
	cfg, err := RestConfig()
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: new clientset: %w", err)
	}
	return cs, nil
}

// NewDynamic returns a dynamic client for custom resources (TenantApp).
func NewDynamic() (dynamic.Interface, error) {
	cfg, err := RestConfig()
	if err != nil {
		return nil, err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: new dynamic client: %w", err)
	}
	return dc, nil
}
