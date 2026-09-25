// Command operator runs the TenantApp controller. It watches TenantApp custom
// resources and reconciles each into a Namespace + Deployment + Service + Ingress
// + NetworkPolicy + ResourceQuota. It is built on controller-runtime, the same
// framework Kubernetes' own controllers and most operators use.
package main

import (
	"log/slog"
	"os"

	platformv1 "github.com/1morshed1/deploy-platform/apis/platform/v1"
	"github.com/1morshed1/deploy-platform/internal/config"
	"github.com/1morshed1/deploy-platform/internal/controller"
	pk8s "github.com/1morshed1/deploy-platform/internal/k8s"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// controller-runtime uses its own logger (logr). Wire in zap's dev logger so
	// the manager's logs are readable; app logs elsewhere use slog.
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// The scheme teaches the manager about both built-in types and our TenantApp.
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := platformv1.AddToScheme(scheme); err != nil {
		return err
	}

	restCfg, err := pk8s.RestConfig()
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: ":8081"},
		// Leader election ensures only ONE operator replica reconciles at a time,
		// even if we run multiple for availability. The lease lives in the system
		// namespace. Without this, two replicas would fight over the same objects.
		LeaderElection:          true,
		LeaderElectionID:        "tenantapp-operator.platform.example.com",
		LeaderElectionNamespace: cfg.SystemNamespace,
		HealthProbeBindAddress:  ":8082",
	})
	if err != nil {
		return err
	}

	if err := (&controller.TenantAppReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Domain:        cfg.PlatformDomain,
		IngressClass:  cfg.IngressClass,
		ClusterIssuer: cfg.ClusterIssuer,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}

	// Start blocks until the process receives SIGINT/SIGTERM.
	return mgr.Start(ctrl.SetupSignalHandler())
}
