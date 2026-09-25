// Package v1 contains the API types for the platform's Custom Resource,
// TenantApp (group platform.example.com, version v1).
//
// This file defines only the group/version identifiers. They live here — the
// single source of truth — so that BOTH consumers agree on them:
//
//   - the worker, which creates/deletes TenantApp objects via the dynamic client
//     using the GroupVersionResource below (it never imports the Go structs), and
//   - the operator, which reconciles TenantApp using the typed structs and
//     SchemeBuilder added in tenantapp_types.go.
//
// Keeping the strings in one place means the loosely-coupled worker and the
// tightly-typed operator can never disagree about what a TenantApp is.
package v1

import "k8s.io/apimachinery/pkg/runtime/schema"

const (
	// GroupName is the API group for all platform custom resources.
	GroupName = "platform.example.com"
	// Version is the API version we serve.
	Version = "v1"
)

// GroupVersion is the group+version pair used throughout.
var GroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}

// TenantAppGVR is the GroupVersionResource for TenantApp. The dynamic client
// addresses resources by their *plural, lowercase* resource name ("tenantapps"),
// which is what the CRD's spec.names.plural declares.
var TenantAppGVR = schema.GroupVersionResource{
	Group:    GroupName,
	Version:  Version,
	Resource: "tenantapps",
}

// TenantAppGVK is the GroupVersionKind for TenantApp. The Kind ("TenantApp") is
// what appears in a manifest's `kind:` field.
var TenantAppGVK = GroupVersion.WithKind("TenantApp")
