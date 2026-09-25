package controller

import (
	"testing"

	platformv1 "github.com/1morshed1/deploy-platform/apis/platform/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func sampleApp() *platformv1.TenantApp {
	return &platformv1.TenantApp{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-1a2b", Namespace: "platform-system"},
		Spec: platformv1.TenantAppSpec{
			ImageURI:      "reg/tenants:demo",
			Subdomain:     "demo-1a2b",
			Replicas:      2,
			CPULimit:      "250m",
			MemoryLimit:   "256Mi",
			ContainerPort: 8080,
		},
	}
}

func TestNamespaceFor(t *testing.T) {
	ns := NamespaceFor(sampleApp())
	if ns.Name != "tenant-demo-1a2b" {
		t.Errorf("namespace name = %q", ns.Name)
	}
	if ns.Labels["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Errorf("expected baseline PSS enforce label")
	}
}

func TestDeploymentFor(t *testing.T) {
	dep, err := DeploymentFor(sampleApp())
	if err != nil {
		t.Fatalf("DeploymentFor: %v", err)
	}
	if dep.Namespace != "tenant-demo-1a2b" {
		t.Errorf("deployment namespace = %q", dep.Namespace)
	}
	if *dep.Spec.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", *dep.Spec.Replicas)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != "reg/tenants:demo" {
		t.Errorf("image = %q", c.Image)
	}
	if c.Ports[0].ContainerPort != 8080 {
		t.Errorf("containerPort = %d", c.Ports[0].ContainerPort)
	}
	// Hardening assertions.
	if *c.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("AllowPrivilegeEscalation must be false")
	}
	if len(c.SecurityContext.Capabilities.Drop) == 0 || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Errorf("expected capabilities drop ALL")
	}
	// requests == limits (Guaranteed QoS).
	if c.Resources.Requests.Cpu().String() != "250m" || c.Resources.Limits.Cpu().String() != "250m" {
		t.Errorf("cpu request/limit mismatch: %v / %v", c.Resources.Requests.Cpu(), c.Resources.Limits.Cpu())
	}
}

func TestDeploymentForInvalidQuantity(t *testing.T) {
	app := sampleApp()
	app.Spec.CPULimit = "not-a-quantity"
	if _, err := DeploymentFor(app); err == nil {
		t.Error("expected error for invalid cpuLimit")
	}
}

func TestServiceFor(t *testing.T) {
	svc := ServiceFor(sampleApp())
	if svc.Spec.Ports[0].Port != 80 {
		t.Errorf("service port = %d, want 80", svc.Spec.Ports[0].Port)
	}
	if svc.Spec.Ports[0].TargetPort.IntVal != 8080 {
		t.Errorf("targetPort = %v, want 8080", svc.Spec.Ports[0].TargetPort)
	}
	if svc.Spec.Selector["app"] != "demo-1a2b" {
		t.Errorf("selector app = %q", svc.Spec.Selector["app"])
	}
}

func TestIngressFor(t *testing.T) {
	ing := IngressFor(sampleApp(), "apps.example.com", "nginx", "letsencrypt")
	rule := ing.Spec.Rules[0]
	if rule.Host != "demo-1a2b.apps.example.com" {
		t.Errorf("host = %q", rule.Host)
	}
	if *ing.Spec.IngressClassName != "nginx" {
		t.Errorf("ingressClassName = %q", *ing.Spec.IngressClassName)
	}
	if len(ing.Spec.TLS) != 1 {
		t.Errorf("expected TLS block when clusterIssuer set")
	}
	if ing.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt" {
		t.Errorf("missing cert-manager annotation")
	}

	// Without a cluster issuer, TLS is disabled.
	ing2 := IngressFor(sampleApp(), "apps.example.com", "nginx", "")
	if len(ing2.Spec.TLS) != 0 {
		t.Errorf("expected no TLS when clusterIssuer empty")
	}
}

func TestNetworkPolicyFor(t *testing.T) {
	np := NetworkPolicyFor(sampleApp())
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected one ingress rule")
	}
	if len(np.Spec.Ingress[0].From) != 2 {
		t.Errorf("expected 2 allowed sources (same-ns + ingress-ns)")
	}
}
