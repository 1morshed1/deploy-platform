package controller

import (
	"fmt"

	platformv1 "github.com/1morshed1/deploy-platform/apis/platform/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// This file contains PURE builders: given a TenantApp, each returns the desired
// Kubernetes object. They perform no cluster I/O, so resources_test.go can assert
// their output exactly. The reconciler (controller.go) applies them.

// labelsFor is the common label set. "app" is the selector label wiring
// Service -> Deployment -> Pods together.
func labelsFor(app *platformv1.TenantApp) map[string]string {
	return map[string]string{
		platformv1.LabelManagedBy: platformv1.ManagedByValue,
		platformv1.LabelTenantApp: app.Name,
		"app":                     app.Spec.Subdomain,
	}
}

// NamespaceFor builds the per-tenant workload namespace. The Pod Security
// Standards labels enforce the "baseline" profile (which admits typical images
// that may run as root, like nginx) while surfacing violations of the stricter
// "restricted" profile as warnings — see ADR 0008 for why we don't hard-enforce
// restricted on tenant workloads.
func NamespaceFor(app *platformv1.TenantApp) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: platformv1.WorkloadNamespace(app.Spec.Subdomain),
			Labels: map[string]string{
				platformv1.LabelManagedBy: platformv1.ManagedByValue,
				platformv1.LabelTenantApp: app.Name,
				// Let the ingress controller's NetworkPolicy target this namespace.
				"platform.example.com/tenant": app.Spec.Subdomain,
				// Pod Security admission labels.
				"pod-security.kubernetes.io/enforce": "baseline",
				"pod-security.kubernetes.io/warn":    "restricted",
				"pod-security.kubernetes.io/audit":   "restricted",
			},
		},
	}
}

// DeploymentFor builds the app Deployment with hardened container security and
// resource limits taken from the TenantApp spec.
func DeploymentFor(app *platformv1.TenantApp) (*appsv1.Deployment, error) {
	cpu, err := resource.ParseQuantity(app.Spec.CPULimit)
	if err != nil {
		return nil, fmt.Errorf("controller: invalid cpuLimit %q: %w", app.Spec.CPULimit, err)
	}
	mem, err := resource.ParseQuantity(app.Spec.MemoryLimit)
	if err != nil {
		return nil, fmt.Errorf("controller: invalid memoryLimit %q: %w", app.Spec.MemoryLimit, err)
	}
	labels := labelsFor(app)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Spec.Subdomain,
			Namespace: platformv1.WorkloadNamespace(app.Spec.Subdomain),
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(app.Spec.Replicas),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": app.Spec.Subdomain}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// Pod-level hardening.
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  "app",
						Image: app.Spec.ImageURI,
						Ports: []corev1.ContainerPort{{ContainerPort: app.Spec.ContainerPort}},
						// requests == limits gives the pod a Guaranteed QoS class,
						// so it is the last to be evicted under node pressure.
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    cpu,
								corev1.ResourceMemory: mem,
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    cpu,
								corev1.ResourceMemory: mem,
							},
						},
						// Container-level hardening: no privilege escalation, drop
						// all Linux capabilities.
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}, nil
}

// ServiceFor builds a ClusterIP Service that exposes the Deployment on port 80,
// forwarding to the container's actual port. The Ingress routes to this Service.
func ServiceFor(app *platformv1.TenantApp) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Spec.Subdomain,
			Namespace: platformv1.WorkloadNamespace(app.Spec.Subdomain),
			Labels:    labelsFor(app),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": app.Spec.Subdomain},
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt32(app.Spec.ContainerPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

// IngressFor builds the Ingress that maps <subdomain>.<domain> to the Service.
// The cert-manager annotation requests an automatic Let's Encrypt certificate.
func IngressFor(app *platformv1.TenantApp, domain, ingressClass, clusterIssuer string) *networkingv1.Ingress {
	host := fmt.Sprintf("%s.%s", app.Spec.Subdomain, domain)
	pathType := networkingv1.PathTypePrefix

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Spec.Subdomain,
			Namespace: platformv1.WorkloadNamespace(app.Spec.Subdomain),
			Labels:    labelsFor(app),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(ingressClass),
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: app.Spec.Subdomain,
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if clusterIssuer != "" {
		ing.Annotations = map[string]string{"cert-manager.io/cluster-issuer": clusterIssuer}
		ing.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      []string{host},
			SecretName: app.Spec.Subdomain + "-tls",
		}}
	}
	return ing
}

// NetworkPolicyFor isolates the tenant namespace. It allows ingress traffic only
// from within the same namespace and from the ingress-controller namespace, which
// blocks pod-to-pod traffic between different tenants (their namespaces can't
// reach each other). Egress is left unrestricted so apps can reach the internet;
// cross-tenant access is already denied by every tenant's ingress rules.
func NetworkPolicyFor(app *platformv1.TenantApp) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tenant-isolation",
			Namespace: platformv1.WorkloadNamespace(app.Spec.Subdomain),
			Labels:    labelsFor(app),
		},
		Spec: networkingv1.NetworkPolicySpec{
			// Empty podSelector = applies to all pods in the namespace.
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{
					// Same namespace (pods in this tenant).
					{PodSelector: &metav1.LabelSelector{}},
					// The ingress controller's namespace (labeled at install time).
					{NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"platform.example.com/ingress": "true"},
					}},
				},
			}},
		},
	}
}

// ResourceQuotaFor caps the tenant namespace's total resource usage so one tenant
// cannot starve the cluster. Values are deliberately generous relative to the
// default per-pod limits but bounded (plan: ~500m CPU / 512Mi baseline).
func ResourceQuotaFor(app *platformv1.TenantApp) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tenant-quota",
			Namespace: platformv1.WorkloadNamespace(app.Spec.Subdomain),
			Labels:    labelsFor(app),
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU:    resource.MustParse("1"),
				corev1.ResourceLimitsCPU:      resource.MustParse("2"),
				corev1.ResourceRequestsMemory: resource.MustParse("1Gi"),
				corev1.ResourceLimitsMemory:   resource.MustParse("2Gi"),
				corev1.ResourcePods:           resource.MustParse("10"),
			},
		},
	}
}
