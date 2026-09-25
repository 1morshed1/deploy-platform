// Package controller implements the TenantApp reconcile loop using
// controller-runtime. This is the declarative core of the platform: it does not
// "deploy" once, it continuously drives cluster reality toward the desired state
// recorded on each TenantApp. If someone deletes the Deployment by hand, the next
// reconcile recreates it. That self-healing behavior is the whole point of the
// operator pattern (see learning doc 05).
package controller

import (
	"context"
	"fmt"
	"time"

	platformv1 "github.com/1morshed1/deploy-platform/apis/platform/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// TenantAppReconciler reconciles TenantApp objects into a running workload.
type TenantAppReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Domain        string // platform base domain, e.g. apps.example.com
	IngressClass  string // e.g. "nginx"
	ClusterIssuer string // cert-manager ClusterIssuer name, or "" to disable TLS
}

// +kubebuilder:rbac:groups=platform.example.com,resources=tenantapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.example.com,resources=tenantapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.example.com,resources=tenantapps/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces;services;resourcequotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses;networkpolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile is called by controller-runtime for every add/update/delete of a
// TenantApp (and on a periodic resync). It must be idempotent: running it twice
// with the same input must produce the same result.
func (r *TenantAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	var app platformv1.TenantApp
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		// NotFound means the object was deleted and fully finalized; nothing to do.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If the object is being deleted, run finalizer cleanup and stop.
	if !app.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &app)
	}

	// Ensure our finalizer is present so we get a chance to clean up on delete.
	if !controllerutil.ContainsFinalizer(&app, platformv1.Finalizer) {
		controllerutil.AddFinalizer(&app, platformv1.Finalizer)
		if err := r.Update(ctx, &app); err != nil {
			return ctrl.Result{}, err
		}
		// The Update re-triggers reconcile; return and let the next pass continue.
		return ctrl.Result{}, nil
	}

	// Drive the desired resources into existence.
	if err := r.reconcileResources(ctx, &app); err != nil {
		l.Error(err, "reconcile resources failed")
		_ = r.setStatus(ctx, &app, platformv1.PhaseFailed, err.Error(), "")
		return ctrl.Result{}, err
	}

	// Report readiness based on the Deployment's status.
	ready, err := r.deploymentReady(ctx, &app)
	if err != nil {
		return ctrl.Result{}, err
	}
	if ready {
		url := fmt.Sprintf("https://%s.%s", app.Spec.Subdomain, r.Domain)
		if err := r.setStatus(ctx, &app, platformv1.PhaseReady, "workload is serving", url); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Not ready yet — record Provisioning and requeue to check again soon.
	if err := r.setStatus(ctx, &app, platformv1.PhaseProvisioning, "waiting for pods to become ready", ""); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// reconcileResources ensures every child object exists and matches desired state.
// Order matters: the namespace must exist before objects placed inside it.
func (r *TenantAppReconciler) reconcileResources(ctx context.Context, app *platformv1.TenantApp) error {
	if err := r.ensure(ctx, NamespaceFor(app)); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	if err := r.ensure(ctx, ResourceQuotaFor(app)); err != nil {
		return fmt.Errorf("resourcequota: %w", err)
	}
	if err := r.ensure(ctx, NetworkPolicyFor(app)); err != nil {
		return fmt.Errorf("networkpolicy: %w", err)
	}
	dep, err := DeploymentFor(app)
	if err != nil {
		return err
	}
	if err := r.ensure(ctx, dep); err != nil {
		return fmt.Errorf("deployment: %w", err)
	}
	if err := r.ensure(ctx, ServiceFor(app)); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := r.ensure(ctx, IngressFor(app, r.Domain, r.IngressClass, r.ClusterIssuer)); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	return nil
}

// ensure creates the object if absent, or updates it to the desired state if
// present. It is the generic create-or-update used for all child resources.
//
// Child resources live in a DIFFERENT namespace from the TenantApp, so we cannot
// set an ownerReference (owner refs are same-namespace only) — cleanup happens via
// the finalizer deleting the whole namespace (ADR 0006). That's why we manage
// create/update by hand rather than relying on garbage collection.
func (r *TenantAppReconciler) ensure(ctx context.Context, desired client.Object) error {
	existing, ok := desired.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("controller: object is not a client.Object")
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Carry the existing resourceVersion so the Update is a valid
	// optimistic-concurrency write.
	desired.SetResourceVersion(existing.GetResourceVersion())

	// A Service's spec.clusterIP is immutable and assigned by the API server; we
	// must preserve it or the Update is rejected.
	if svc, isSvc := desired.(*corev1.Service); isSvc {
		if cur, curOK := existing.(*corev1.Service); curOK {
			svc.Spec.ClusterIP = cur.Spec.ClusterIP
		}
	}
	return r.Update(ctx, desired)
}

// deploymentReady reports whether the workload Deployment has at least one ready
// replica.
func (r *TenantAppReconciler) deploymentReady(ctx context.Context, app *platformv1.TenantApp) (bool, error) {
	var dep appsv1.Deployment
	key := client.ObjectKey{
		Namespace: platformv1.WorkloadNamespace(app.Spec.Subdomain),
		Name:      app.Spec.Subdomain,
	}
	if err := r.Get(ctx, key, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return dep.Status.ReadyReplicas >= 1, nil
}

// reconcileDelete deletes the tenant's workload namespace (cascading to every
// child resource) and then removes the finalizer so the TenantApp can be deleted.
func (r *TenantAppReconciler) reconcileDelete(ctx context.Context, app *platformv1.TenantApp) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(app, platformv1.Finalizer) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platformv1.WorkloadNamespace(app.Spec.Subdomain)}}
		if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(app, platformv1.Finalizer)
		if err := r.Update(ctx, app); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// setStatus writes the observed status subresource. We only write when something
// changed to avoid a hot loop of status updates each triggering another reconcile.
func (r *TenantAppReconciler) setStatus(ctx context.Context, app *platformv1.TenantApp, phase, msg, url string) error {
	if app.Status.Phase == phase && app.Status.Message == msg && app.Status.URL == url &&
		app.Status.ObservedGeneration == app.Generation {
		return nil
	}
	app.Status.Phase = phase
	app.Status.Message = msg
	app.Status.URL = url
	app.Status.ObservedGeneration = app.Generation
	return r.Status().Update(ctx, app)
}

// SetupWithManager registers the reconciler with the manager, watching TenantApp
// objects. We don't Own() the child resources because they're cross-namespace;
// the RequeueAfter in Reconcile handles periodic readiness re-checks.
func (r *TenantAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1.TenantApp{}).
		Complete(r)
}
