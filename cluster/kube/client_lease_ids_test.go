package kube

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	mtypes "pkg.akt.dev/go/node/market/v1"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

// Startup reconciliation matches the leases in the cluster against the chain,
// so every lease that has a namespace or a manifest is listed, once.
func TestLeaseIDsListsLeaseNamespacesAndManifests(t *testing.T) {
	both, nsOnly, manifestOnly, specOnly := testutil.LeaseID(t), testutil.LeaseID(t), testutil.LeaseID(t), testutil.LeaseID(t)
	terminating, notManaged := testutil.LeaseID(t), testutil.LeaseID(t)
	leaseNamespace := func(lid mtypes.LeaseID) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   builder.LidNS(lid),
			Labels: builder.AppendLeaseLabels(lid, map[string]string{builder.AkashManagedLabelName: "true"}),
		}}
	}
	terminatingNamespace := leaseNamespace(terminating)
	deleted := metav1.Now()
	terminatingNamespace.DeletionTimestamp = &deleted
	terminatingNamespace.Spec.Finalizers = []corev1.FinalizerName{corev1.FinalizerKubernetes}
	manifest := func(name string, id crd.LeaseID, labels map[string]string) *crd.Manifest {
		return &crd.Manifest{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testKubeClientNs, Labels: labels},
			Spec:       crd.ManifestSpec{LeaseID: id},
		}
	}
	leaseManifest := func(lid mtypes.LeaseID) *crd.Manifest {
		return manifest(builder.LidNS(lid), crd.LeaseIDFromAkash(lid), builder.AppendLeaseLabels(lid, map[string]string{}))
	}

	c := clientForTest(t, []runtime.Object{
		leaseNamespace(both),
		leaseNamespace(nsOnly),
		// Kubernetes is already deleting it, and refuses to delete it again.
		terminatingNamespace,
		// Not lease namespaces: unmanaged, or managed without lease labels.
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: "akash-services", Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		}},
		// Lease labels on a namespace the provider does not manage.
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: "copied", Labels: builder.AppendLeaseLabels(notManaged, map[string]string{}),
		}},
	}, []runtime.Object{
		leaseManifest(both),
		leaseManifest(manifestOnly),
		// A manifest from an older provider may carry its lease ID only in its spec.
		manifest(builder.LidNS(specOnly), crd.LeaseIDFromAkash(specOnly), nil),
		// Its spec names no lease.
		manifest("broken", crd.LeaseID{DSeq: "not-a-number"}, nil),
	}).(*client)

	lids, err := c.LeaseIDs(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []mtypes.LeaseID{both, nsOnly, manifestOnly, specOnly}, lids)
}
