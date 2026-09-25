package v1

// Naming conventions shared by the worker and the operator. Defining them once
// here guarantees both components compute the same names — otherwise the worker
// might poll a namespace the operator never created.

// WorkloadNamespace is the per-tenant namespace the operator creates to run one
// deployment's workload. Each deployment gets its own namespace so tenants are
// isolated by NetworkPolicy and bounded by ResourceQuota (see ADR 0006 / doc 08).
func WorkloadNamespace(subdomain string) string {
	return "tenant-" + subdomain
}

// Labels applied to every resource the operator manages, so they can be listed,
// selected, and garbage-collected together.
const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelTenantApp = "platform.example.com/tenantapp"
	ManagedByValue = "deploy-platform"
)

// Finalizer is added to every TenantApp so the operator gets a chance to delete
// the tenant's namespace (and everything in it) before the CR disappears.
const Finalizer = "platform.example.com/finalizer"
