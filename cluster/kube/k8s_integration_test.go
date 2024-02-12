//go:build k8s_integration

package kube

import (
	"bufio"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/flowcontrol"

	atestutil "github.com/akash-network/node/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	mtestutil "github.com/akash-network/provider/testutil/manifest/v2beta2"
	"github.com/akash-network/provider/tools/fromctx"
)

func TestNewClientNSNotFound(t *testing.T) {
	// create lease
	lid := atestutil.LeaseID(t)
	ns := builder.LidNS(lid)

	settings := builder.Settings{
		DeploymentServiceType:          corev1.ServiceTypeClusterIP,
		DeploymentIngressStaticHosts:   false,
		DeploymentIngressDomain:        "bar.com",
		DeploymentIngressExposeLBHosts: false,
	}

	kcfg, err := clientcommon.OpenKubeConfig(providerflags.KubeConfigDefaultPath, atestutil.Logger(t))
	require.NoError(t, err)
	require.NotNil(t, kcfg)

	kc, err := kubernetes.NewForConfig(kcfg)
	require.NoError(t, err)
	require.NotNil(t, kc)

	ac, err := akashclientset.NewForConfig(kcfg)
	require.NoError(t, err)
	require.NotNil(t, ac)

	ctx := context.WithValue(context.Background(), builder.SettingsKey, settings)
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeConfig, kcfg)
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, akashclientset.Interface(ac))

	cl, err := NewClient(ctx, atestutil.Logger(t), ns)
	require.True(t, kubeErrors.IsNotFound(err))
	require.Nil(t, cl)
}

func TestNewClient(t *testing.T) {
	// create lease
	lid := atestutil.LeaseID(t)
	group := mtestutil.AppManifestGenerator.Group(t)

	ns := builder.LidNS(lid)

	settings := builder.Settings{
		DeploymentServiceType:          corev1.ServiceTypeClusterIP,
		DeploymentIngressStaticHosts:   false,
		DeploymentIngressDomain:        "bar.com",
		DeploymentIngressExposeLBHosts: false,
	}

	kcfg, err := clientcommon.OpenKubeConfig(providerflags.KubeConfigDefaultPath, atestutil.Logger(t))
	require.NoError(t, err)
	require.NotNil(t, kcfg)

	kc, err := kubernetes.NewForConfig(kcfg)
	require.NoError(t, err)
	require.NotNil(t, kc)

	ac, err := akashclientset.NewForConfig(kcfg)
	require.NoError(t, err)
	require.NotNil(t, ac)

	ctx := context.WithValue(context.Background(), builder.SettingsKey, settings)
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeConfig, kcfg)
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, akashclientset.Interface(ac))

	kcfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	_, err = kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	cl, err := NewClient(ctx, atestutil.Logger(t), ns)

	require.NoError(t, err)

	cc, ok := cl.(*client)
	require.True(t, ok)
	require.NotNil(t, cc)

	// ensure no deployments
	deployments, err := cl.Deployments(ctx)
	assert.NoError(t, err)
	require.Empty(t, deployments)

	reservationCparams := make(crd.ReservationClusterSettings)

	for _, svc := range group.Services {
		reservationCparams[svc.Resources.ID] = nil
	}

	cdep := &ctypes.Deployment{
		Lid:     lid,
		MGroup:  &group,
		CParams: reservationCparams,
	}

	// deploy lease
	err = cl.Deploy(ctx, cdep)
	assert.NoError(t, err)

	// query deployments, ensure lease present
	deployments, err = cl.Deployments(ctx)
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	deployment := deployments[0]

	assert.Equal(t, lid, deployment.LeaseID())

	svcname := group.Services[0].Name

	// There is some sort of race here, work around it
	time.Sleep(time.Second * 10)

	lstat, err := cl.LeaseStatus(ctx, lid)
	assert.NoError(t, err)
	assert.Len(t, lstat, 1)
	assert.Equal(t, svcname, lstat[svcname].Name)

	sstat, err := cl.ServiceStatus(ctx, lid, svcname)
	require.NoError(t, err)

	const (
		maxtries = 30
		delay    = time.Second
	)

	tries := 0
	for ; err == nil && tries < maxtries; tries++ {
		t.Log(sstat)
		if uint32(sstat.AvailableReplicas) == group.Services[0].Count {
			break
		}
		time.Sleep(delay)
		sstat, err = cl.ServiceStatus(ctx, lid, svcname)
	}

	assert.NoError(t, err)
	assert.NotEqual(t, maxtries, tries)

	logs, err := cl.LeaseLogs(ctx, lid, svcname, true, nil)
	require.NoError(t, err)
	require.Equal(t, int(sstat.AvailableReplicas), len(logs))

	log := make(chan string, 1)

	go func(scan *bufio.Scanner) {
		for scan.Scan() {
			log <- scan.Text()
			break
		}
	}(logs[0].Scanner)

	select {
	case line := <-log:
		assert.NotEmpty(t, line)
	case <-time.After(10 * time.Second):
		assert.Fail(t, "timed out waiting for logs")
	}

	for _, lg := range logs {
		assert.NoError(t, lg.Stream.Close(), lg.Name)
	}

	npi := cc.kc.NetworkingV1().NetworkPolicies(ns)
	npList, err := npi.List(ctx, metav1.ListOptions{})
	assert.NoError(t, err)
	assert.Equal(t, len(npList.Items), 0)

	// teardown lease
	err = cl.TeardownLease(ctx, lid)
	assert.NoError(t, err)
}
