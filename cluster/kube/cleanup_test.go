package kube

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
)

func TestCleanupStaleRBACResources(t *testing.T) {
	ctx := context.Background()
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	// Create labels for a stale service that no longer has permissions
	staleServiceLabels := map[string]string{
		builder.AkashManagedLabelName:         "true",
		builder.AkashManifestServiceLabelName: "old-service",
	}
	builder.AppendLeaseLabels(lid, staleServiceLabels)

	// Create labels for a current service that still has permissions
	currentServiceLabels := map[string]string{
		builder.AkashManagedLabelName:         "true",
		builder.AkashManifestServiceLabelName: "current-service",
	}
	builder.AppendLeaseLabels(lid, currentServiceLabels)

	// Create stale RBAC resources (for old-service which no longer has permissions)
	staleServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-service",
			Namespace: ns,
			Labels:    staleServiceLabels,
		},
	}

	staleRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-service",
			Namespace: ns,
			Labels:    staleServiceLabels,
		},
	}

	staleRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-service",
			Namespace: ns,
			Labels:    staleServiceLabels,
		},
	}

	// Create current RBAC resources (for current-service which still has permissions)
	currentServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "current-service",
			Namespace: ns,
			Labels:    currentServiceLabels,
		},
	}

	currentRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "current-service",
			Namespace: ns,
			Labels:    currentServiceLabels,
		},
	}

	currentRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "current-service",
			Namespace: ns,
			Labels:    currentServiceLabels,
		},
	}

	kc := fake.NewClientset(
		staleServiceAccount, staleRole, staleRoleBinding,
		currentServiceAccount, currentRole, currentRoleBinding,
	)

	// Only "current-service" still has permissions
	servicesWithPermissions := []string{"current-service"}

	// Run cleanup
	err := cleanupStaleRBACResources(ctx, kc, ns, servicesWithPermissions)
	require.NoError(t, err)

	// Verify stale resources are deleted
	serviceAccounts, err := kc.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, serviceAccounts.Items, 1)
	require.Equal(t, "current-service", serviceAccounts.Items[0].Name)

	roles, err := kc.RbacV1().Roles(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, roles.Items, 1)
	require.Equal(t, "current-service", roles.Items[0].Name)

	roleBindings, err := kc.RbacV1().RoleBindings(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, roleBindings.Items, 1)
	require.Equal(t, "current-service", roleBindings.Items[0].Name)
}

func TestCleanupStaleRBACResourcesDoesNotDeleteNonAkashResources(t *testing.T) {
	ctx := context.Background()
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	// Create a non-Akash managed ServiceAccount (no akash.network label)
	nonAkashServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system-service-account",
			Namespace: ns,
			Labels: map[string]string{
				"app": "system",
			},
		},
	}

	// Create labels for a stale service
	staleServiceLabels := map[string]string{
		builder.AkashManagedLabelName:         "true",
		builder.AkashManifestServiceLabelName: "old-service",
	}
	builder.AppendLeaseLabels(lid, staleServiceLabels)

	staleServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-service",
			Namespace: ns,
			Labels:    staleServiceLabels,
		},
	}

	kc := fake.NewClientset(nonAkashServiceAccount, staleServiceAccount)

	// No services currently have permissions
	servicesWithPermissions := []string{}

	// Run cleanup
	err := cleanupStaleRBACResources(ctx, kc, ns, servicesWithPermissions)
	require.NoError(t, err)

	// Verify non-Akash resource is NOT deleted, but stale Akash resource IS deleted
	serviceAccounts, err := kc.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, serviceAccounts.Items, 1)
	require.Equal(t, "system-service-account", serviceAccounts.Items[0].Name)
}

func TestCleanupRBACWhenPermissionsRemoved(t *testing.T) {
	// This tests the specific case where a service STILL EXISTS in the manifest
	// but its permissions have been removed
	ctx := context.Background()
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	// Create labels for "web" service which previously had permissions
	webServiceLabels := map[string]string{
		builder.AkashManagedLabelName:         "true",
		builder.AkashManifestServiceLabelName: "web",
	}
	builder.AppendLeaseLabels(lid, webServiceLabels)

	// RBAC resources created when "web" had permissions
	webServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: ns,
			Labels:    webServiceLabels,
		},
	}

	webRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: ns,
			Labels:    webServiceLabels,
		},
	}

	webRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: ns,
			Labels:    webServiceLabels,
		},
	}

	kc := fake.NewClientset(webServiceAccount, webRole, webRoleBinding)

	// Manifest still has "web" service, but it no longer has permissions
	// So servicesWithPermissions is empty
	group := &mani.Group{
		Services: mani.Services{
			{Name: "web"}, // Service still exists
		},
	}
	servicesWithPermissions := []string{} // But no longer has permissions

	// Run the full cleanup
	err := cleanupStaleResources(ctx, kc, lid, group, servicesWithPermissions)
	require.NoError(t, err)

	// Verify RBAC resources for "web" are deleted even though the service still exists
	serviceAccounts, err := kc.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, serviceAccounts.Items, 0)

	roles, err := kc.RbacV1().Roles(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, roles.Items, 0)

	roleBindings, err := kc.RbacV1().RoleBindings(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, roleBindings.Items, 0)
}

func TestCleanupStaleResourcesIntegration(t *testing.T) {
	ctx := context.Background()
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)

	// Create labels for a removed service
	staleServiceLabels := map[string]string{
		builder.AkashManagedLabelName:         "true",
		builder.AkashManifestServiceLabelName: "removed-service",
	}
	builder.AppendLeaseLabels(lid, staleServiceLabels)

	// Create stale RBAC resources for the removed service
	staleServiceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "removed-service",
			Namespace: ns,
			Labels:    staleServiceLabels,
		},
	}

	staleRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "removed-service",
			Namespace: ns,
			Labels:    staleServiceLabels,
		},
	}

	staleRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "removed-service",
			Namespace: ns,
			Labels:    staleServiceLabels,
		},
	}

	kc := fake.NewClientset(staleServiceAccount, staleRole, staleRoleBinding)

	// Manifest group with only "active-service" (removed-service no longer exists)
	group := &mani.Group{
		Services: mani.Services{
			{Name: "active-service"},
		},
	}

	// active-service has no permissions either
	servicesWithPermissions := []string{}

	// Run the full cleanup
	err := cleanupStaleResources(ctx, kc, lid, group, servicesWithPermissions)
	require.NoError(t, err)

	// Verify all stale RBAC resources are deleted
	serviceAccounts, err := kc.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, serviceAccounts.Items, 0)

	roles, err := kc.RbacV1().Roles(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, roles.Items, 0)

	roleBindings, err := kc.RbacV1().RoleBindings(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, roleBindings.Items, 0)
}
