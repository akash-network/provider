package hostname

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/pager"

	"cosmossdk.io/log"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
)

// ErrMigrationIncomplete means the migration did not reach a state in which the
// Gateway can safely change class.
var ErrMigrationIncomplete = errors.New("gateway migration incomplete")

// MigrateConfig configures MigrateRoutes.
type MigrateConfig struct {
	Logger      log.Logger
	Akash       akashclientset.Interface
	Dynamic     dynamic.Interface
	ManifestNS  string
	GatewayName string
	// GatewayNamespace is the Gateway's namespace.
	GatewayNamespace string
	// Target is the implementation to render every route for: "envoy" or "nginx".
	Target string
	// DryRun reports the planned changes without writing anything.
	DryRun bool
	// Concurrency bounds how many routes are reconciled at once.
	Concurrency int
}

// MigrateReport is the inventory MigrateRoutes ends with.
type MigrateReport struct {
	Target string
	DryRun bool
	// Migrated lists the hostnames reconciled to the target shape.
	Migrated []string
	// Failed maps hostnames to their reconcile error.
	Failed map[string]string
	// NoManifest lists hostnames whose ProviderHost has no usable manifest; their
	// routes are left as they are.
	NoManifest []string
	// Incompatible lists routes the target implementation cannot serve: for envoy,
	// routes that still reference an NGF SnippetsFilter (Envoy serves 500 for them);
	// for nginx, routes that still have a BackendTrafficPolicy.
	Incompatible []string
}

// Ready reports whether the Gateway may change class: nothing failed and every
// route is in a shape the target implementation can serve.
func (r MigrateReport) Ready() bool {
	return len(r.Failed) == 0 && len(r.Incompatible) == 0
}

// MigrateRoutes reconciles every tenant route to the target implementation's
// shape: the route spec, the complete desired extension set, and deletion of every
// other extension of the route. It is the explicit counterpart of the hostname
// operator's startup resync, run inside a cutover window with the operator stopped.
//
// Moving to envoy runs before the Gateway changes class, when Envoy is not yet a
// parent of the routes and so never reports status: extensions are applied without
// waiting. Moving to nginx (rollback) runs after the class switch, with NGF's normal
// ordering: each filter is accepted before its route references it.
//
// It is idempotent and resumable: a rerun repairs anything missing, stale or
// wrongly owned. A failed route is reported and does not stop the rest.
func MigrateRoutes(ctx context.Context, cfg MigrateConfig) (MigrateReport, error) {
	report := MigrateReport{Target: cfg.Target, DryRun: cfg.DryRun, Failed: map[string]string{}}

	provider, err := gateway.GetProvider(cfg.Target, cfg.Logger)
	if err != nil {
		return report, err
	}

	op := &hostnameOperator{
		ctx: ctx,
		ns:  cfg.ManifestNS,
		log: cfg.Logger,
		ac:  cfg.Akash,
		dc:  cfg.Dynamic,
	}

	hosts, err := listProviderHosts(ctx, cfg.Akash, cfg.ManifestNS)
	if err != nil {
		return report, err
	}

	routeCfg := gateway.HTTPRouteConfig{
		GatewayName:        cfg.GatewayName,
		GatewayNamespace:   cfg.GatewayNamespace,
		Provider:           provider,
		SkipAcceptanceWait: provider.RouteFirst(),
	}

	concurrency := cfg.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	var mu sync.Mutex
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(concurrency)

	for _, ph := range hosts {
		group.Go(func() error {
			hostname := ph.Spec.Hostname

			lid, err := ph.Spec.LeaseID()
			if err != nil {
				mu.Lock()
				report.Failed[hostname] = fmt.Sprintf("invalid lease id: %v", err)
				mu.Unlock()
				return nil
			}

			ev := hostnameResourceEvent{
				lid:          lid,
				eventType:    ctypes.ProviderResourceAdd,
				hostname:     hostname,
				serviceName:  ph.Spec.ServiceName,
				externalPort: ph.Spec.ExternalPort,
			}

			expose, err := op.locateServiceFromManifest(gctx, lid, ev.serviceName, ev.externalPort)
			if err != nil {
				mu.Lock()
				defer mu.Unlock()
				if errorIsKubernetesResourceNotFound(err) {
					report.NoManifest = append(report.NoManifest, hostname)
				} else {
					report.Failed[hostname] = err.Error()
				}
				return nil
			}

			directive := buildDirective(ev, expose)

			if cfg.DryRun {
				exts := provider.BuildRouteExtensions(builder.LidNS(lid), hostname, directive)
				names := make([]string, 0, len(exts))
				for _, ext := range exts {
					names = append(names, ext.GetKind()+"/"+ext.GetName())
				}
				cfg.Logger.Info("would reconcile route",
					"hostname", hostname,
					"ns", builder.LidNS(lid),
					"implementation", provider.Name(),
					"extensions", strings.Join(names, ","))
				mu.Lock()
				report.Migrated = append(report.Migrated, hostname)
				mu.Unlock()
				return nil
			}

			err = gateway.CreateOrUpdateHTTPRoute(gctx, cfg.Dynamic, routeCfg, directive, gateway.NoopHTTPRouteObserver{})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				report.Failed[hostname] = err.Error()
				return nil
			}
			report.Migrated = append(report.Migrated, hostname)
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return report, err
	}

	if !cfg.DryRun {
		incompatible, err := incompatibleRoutes(ctx, cfg.Dynamic, cfg.Target)
		if err != nil {
			return report, err
		}
		report.Incompatible = incompatible
	}

	sort.Strings(report.Migrated)
	sort.Strings(report.NoManifest)

	return report, nil
}

func listProviderHosts(ctx context.Context, ac akashclientset.Interface, ns string) ([]crd.ProviderHost, error) {
	p := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return ac.AkashV2beta2().ProviderHosts(ns).List(ctx, opts)
	})

	hosts := make([]crd.ProviderHost, 0, 128)
	err := p.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		hosts = append(hosts, *(obj.(*crd.ProviderHost)))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list provider hosts: %w", err)
	}

	return hosts, nil
}

// incompatibleRoutes lists tenant routes, as namespace/name, that the target
// implementation cannot serve as they stand.
func incompatibleRoutes(ctx context.Context, dc dynamic.Interface, target string) ([]string, error) {
	selector := fmt.Sprintf("%s=true", builder.AkashManagedLabelName)

	routes, err := dc.Resource(gateway.HTTPRouteGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}

	var result []string
	switch target {
	case "envoy":
		for _, route := range routes.Items {
			if referencesSnippetsFilter(route) {
				result = append(result, route.GetNamespace()+"/"+route.GetName())
			}
		}
	case "nginx":
		policies, err := dc.Resource(gateway.BackendTrafficPolicyGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil && !absentResourceErr(err) {
			return nil, fmt.Errorf("failed to list backend traffic policies: %w", err)
		}
		if policies != nil {
			for _, p := range policies.Items {
				result = append(result, p.GetNamespace()+"/"+p.GetAnnotations()[gateway.RouteNameAnnotation])
			}
		}
	}

	sort.Strings(result)
	return result, nil
}

func referencesSnippetsFilter(route unstructured.Unstructured) bool {
	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	for _, r := range rules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		filters, _, _ := unstructured.NestedSlice(rm, "filters")
		for _, f := range filters {
			fm, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			if kind, _, _ := unstructured.NestedString(fm, "extensionRef", "kind"); kind == "SnippetsFilter" {
				return true
			}
		}
	}
	return false
}

// absentResourceErr reports whether err means the object or its CRD does not exist.
func absentResourceErr(err error) bool {
	return kerrors.IsNotFound(err) || meta.IsNoMatchError(err)
}
