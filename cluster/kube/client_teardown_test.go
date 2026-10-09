package kube

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	afake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
)

func TestTeardownLeaseRetriesManifestDeletion(t *testing.T) {
	ctx := context.Background()
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)
	c := clientForTest(t, nil, []runtime.Object{&crd.Manifest{
		ObjectMeta: metav1.ObjectMeta{Name: ns, Namespace: testKubeClientNs},
	}}).(*client)
	manifestErr := errors.New("manifest API unavailable")
	calls := 0
	c.ac.(*afake.Clientset).PrependReactor("delete", "manifests", func(ktesting.Action) (bool, runtime.Object, error) {
		calls++
		if calls == 1 {
			return true, nil, manifestErr
		}
		return false, nil, nil
	})
	require.ErrorIs(t, c.TeardownLease(ctx, lid), manifestErr)
	require.NoError(t, c.TeardownLease(ctx, lid))
	_, err := c.ac.AkashV2beta2().Manifests(testKubeClientNs).Get(ctx, ns, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))
	// Both objects are now absent; repeated cleanup must still succeed.
	require.NoError(t, c.TeardownLease(ctx, lid))
}

func TestTeardownLeaseReportsBothDeletionFailures(t *testing.T) {
	lid := testutil.LeaseID(t)
	c := clientForTest(t, []runtime.Object{&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: builder.LidNS(lid)},
	}}, nil).(*client)
	nsErr, manifestErr := errors.New("namespace delete failed"), errors.New("manifest delete failed")
	c.kc.(*fake.Clientset).PrependReactor("delete", "namespaces", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, nsErr
	})
	c.ac.(*afake.Clientset).PrependReactor("delete", "manifests", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, manifestErr
	})
	err := c.TeardownLease(context.Background(), lid)
	require.ErrorIs(t, err, nsErr)
	require.ErrorIs(t, err, manifestErr)
}
