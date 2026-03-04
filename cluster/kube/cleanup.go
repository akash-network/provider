package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
)

func cleanupStaleResources(ctx context.Context, kc kubernetes.Interface, lid mtypes.LeaseID, group *mani.Group, servicesWithPermissions []string) error {
	ns := builder.LidNS(lid)

	// build label selector for objects not in current manifest group
	svcnames := make([]string, 0, len(group.Services))
	for _, svc := range group.Services {
		svcnames = append(svcnames, svc.Name)
	}

	req1, err := labels.NewRequirement(builder.AkashManifestServiceLabelName, selection.NotIn, svcnames)
	if err != nil {
		return err
	}
	req2, err := labels.NewRequirement(builder.AkashServiceTarget, selection.Equals, []string{"true"})
	if err != nil {
		return err
	}

	req3, err := labels.NewRequirement(builder.AkashManagedLabelName, selection.NotIn, []string{builder.AkashMetalLB})
	if err != nil {
		return err
	}

	selector := labels.NewSelector().Add(*req1).Add(*req2).Add(*req3).String()

	// delete stale deployments
	if err := kc.AppsV1().Deployments(ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: selector,
	}); err != nil {
		return err
	}

	// delete stale deployments
	if err := kc.AppsV1().StatefulSets(ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: selector,
	}); err != nil {
		return err
	}

	// delete stale services (no DeleteCollection)
	services, err := kc.CoreV1().Services(ns).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return err
	}
	for _, svc := range services.Items {
		if err := kc.CoreV1().Services(ns).Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}

	// cleanup stale RBAC resources for services that no longer have permissions
	if err := cleanupStaleRBACResources(ctx, kc, ns, servicesWithPermissions); err != nil {
		return err
	}

	return nil
}

// cleanupStaleRBACResources removes ServiceAccounts, Roles, and RoleBindings
// that belong to services that no longer have permissions in the current manifest.
// This handles two cases:
// 1. A service is removed from the manifest entirely
// 2. A service still exists but its permissions are removed
func cleanupStaleRBACResources(ctx context.Context, kc kubernetes.Interface, ns string, servicesWithPermissions []string) error {
	selector := labels.NewSelector()

	if len(servicesWithPermissions) > 0 {
		req, err := labels.NewRequirement(builder.AkashManifestServiceLabelName, selection.NotIn, servicesWithPermissions)
		if err != nil {
			return err
		}
		selector = selector.Add(*req)
	}

	reqManaged, err := labels.NewRequirement(builder.AkashManagedLabelName, selection.Equals, []string{"true"})
	if err != nil {
		return err
	}
	selector = selector.Add(*reqManaged)

	rbacSelector := selector.String()

	// delete stale service accounts
	serviceAccounts, err := kc.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{
		LabelSelector: rbacSelector,
	})
	if err != nil {
		return err
	}
	for _, sa := range serviceAccounts.Items {
		if err := kc.CoreV1().ServiceAccounts(ns).Delete(ctx, sa.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}

	// delete stale roles
	roles, err := kc.RbacV1().Roles(ns).List(ctx, metav1.ListOptions{
		LabelSelector: rbacSelector,
	})
	if err != nil {
		return err
	}
	for _, role := range roles.Items {
		if err := kc.RbacV1().Roles(ns).Delete(ctx, role.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}

	// delete stale role bindings
	roleBindings, err := kc.RbacV1().RoleBindings(ns).List(ctx, metav1.ListOptions{
		LabelSelector: rbacSelector,
	})
	if err != nil {
		return err
	}
	for _, rb := range roleBindings.Items {
		if err := kc.RbacV1().RoleBindings(ns).Delete(ctx, rb.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}

	return nil
}
