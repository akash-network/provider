package kube

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/logging/logtest"
)

func TestDeployFailureLogsError(t *testing.T) {
	rec := logtest.New()
	ac := akashfake.NewSimpleClientset()
	ac.PrependReactor("get", "manifests", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("api unavailable")
	})
	c := &client{kc: fake.NewClientset(), ac: ac, ns: "lease", log: rec}
	ctx := context.WithValue(context.Background(), builder.SettingsKey, builder.NewDefaultSettings())

	data, err := sdl.ReadFile("../../testdata/deployment/deployment-v2-storage-default.yaml")
	require.NoError(t, err)
	manifest, err := data.Manifest()
	require.NoError(t, err)
	group := manifest.GetGroups()[0]

	lid := testutil.LeaseID(t)
	err = c.Deploy(ctx, &ctypes.Deployment{
		Lid:     lid,
		MGroup:  &group,
		CParams: crd.ClusterSettings{SchedulerParams: make([]*crd.SchedulerParams, len(group.Services))},
	})
	require.ErrorContains(t, err, "api unavailable")

	var failure *logtest.Entry
	for _, e := range rec.Entries() {
		if e.Msg == "unable to deploy" {
			failure = &e
		}
	}
	require.NotNil(t, failure, "the deploy failure must be logged as \"unable to deploy\"")
	require.Equal(t, "error", failure.Level)
	require.Equal(t, lid, failure.KV["lid"])
	failErr, ok := failure.KV["err"].(error)
	require.True(t, ok, "the failure entry must carry the error")
	require.ErrorContains(t, failErr, "api unavailable")
}

func TestTeardownLeaseLogsLeaseID(t *testing.T) {
	rec := logtest.New()
	c := clientForTest(t, nil, nil).(*client)
	c.log = rec

	lid := testutil.LeaseID(t)
	require.NoError(t, c.TeardownLease(context.Background(), lid))

	entries := rec.Entries()
	require.NotEmpty(t, entries)

	var sawError bool
	for _, e := range entries {
		require.Equal(t, lid, e.KV["lid"], "entry %q must carry the lease ID", e.Msg)
		sawError = sawError || e.Msg == "teardown lease: unable to delete namespace"
	}
	require.True(t, sawError, "expected the namespace deletion failure to be logged")
}
