//go:build e2e

package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"pkg.akt.dev/go/cli"
	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	"github.com/akash-network/provider/cluster/kube/builder"
	cutil "github.com/akash-network/provider/cluster/util"
	ptestutil "github.com/akash-network/provider/testutil/provider"
	"github.com/akash-network/provider/tools/fromctx"
)

type E2EProviderRestart struct {
	IntegrationTestSuite

	deploymentFixture    string
	expectedPods         int
	expectedRuntimeClass string
}

// SetupSuite runs the provider as a real OS subprocess so restartProvider kills and
// relaunches an actual process (fresh globals, Viper, caches), not just a cancelled
// context in the same binary.
func (s *E2EProviderRestart) SetupSuite() {
	s.useProviderSubprocess = true
	s.IntegrationTestSuite.SetupSuite()
}

// TestE2EProviderRestartKeepsWorkloads asserts that a real provider process restart
// does not roll, restart, or delete a running workload. It deploys one lease whose
// SDL carries both a StatefulSet-backed service (web, persistent storage) and a
// Deployment-backed service (bew), so a single namespace exercises both controllers.
func (s *E2EProviderRestart) TestE2EProviderRestartKeepsWorkloads() {

	fixture := s.deploymentFixture
	if fixture == "" {
		fixture = "../testdata/deployment/deployment-v2-storage-default.yaml"
	}
	deploymentPath, err := filepath.Abs(fixture)
	s.Require().NoError(err)

	wantPods := s.expectedPods
	if wantPods == 0 {
		wantPods = 2
	}

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(555),
	}

	res, err := clitestutil.ExecDeploymentCreate(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithFrom(s.addrTenant.String()).
			WithDSeq(deploymentID.DSeq).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	bidID := mtypes.MakeBidID(
		mtypes.MakeOrderID(dtypes.MakeGroupID(deploymentID, 1), 1),
		s.addrProvider,
	)

	err = s.waitForBlockchainEvent(&mtypes.EventBidCreated{ID: bidID})
	s.Require().NoError(err)

	_, err = clitestutil.ExecCreateLease(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithBidID(bidID).
			WithFrom(s.addrTenant.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))

	resp, err := clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(resp.Bytes(), leaseRes)
	s.Require().NoError(err)

	lease := newestLease(leaseRes.Leases)
	lid := lease.ID
	s.Require().Equal(s.addrProvider.String(), lid.Provider)

	_, err = ptestutil.ExecSendManifest(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String()).
			WithDSeq(lid.DSeq).
			WithOutputJSON()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))

	namespace := cutil.LeaseIDToNamespace(lid)
	kube := fromctx.MustKubeClientFromCtx(s.ctx)

	s.waitForRunningPods(kube, namespace, wantPods, 2*time.Minute)

	if s.expectedRuntimeClass != "" {
		assertPodsRuntimeClass(s.T(), kube, namespace, s.expectedRuntimeClass)
	}

	observer := NewObserver(namespace)
	s.Require().NoError(observer.Snapshot(context.Background(), kube))

	// The watch must outlive the restarts, so it is tied to a standalone context
	// rather than s.ctx (restartProvider cancels s.ctx). cancelWatch below drives the
	// real window end; the timeout is only a hang guard, sized well above the
	// worst-case three-restart sequence.
	watchCtx, cancelWatch := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelWatch()

	var watchErr error
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchErr = observer.Watch(watchCtx, kube)
	}()

	// The recovery bug this guards against is non-convergent: the first recovery only
	// rewrites the Manifest's resource-version label (bumping the Manifest's own
	// version), and a later recovery copies that into the pod template and rolls the
	// pods. A single restart can therefore pass even against a buggy provider, so we
	// restart several times under one continuous watch: a buggy provider rolls the
	// workload within a few cycles, a healthy one never does.
	for i := 0; i < 3; i++ {
		s.restartProvider()
		waitForProviderStatus(s.T(), s.providerStatusURL, 90*time.Second)
		// A roll surfaces only once the new provider runs its startup reconcile,
		// which is after /status is healthy; settle before the next cycle.
		time.Sleep(15 * time.Second)
	}

	cancelWatch()
	<-watchDone

	// The pod watch is the verdict, so its early death fails loudly; the event watch
	// is attribution only and tolerated. The window must have ended because we
	// cancelled it, not because the hang guard fired mid-loop.
	s.Require().NoError(watchErr, "pod watch failed during the observation window")
	s.Require().ErrorIs(watchCtx.Err(), context.Canceled, "watch ended on the hang guard, not our cancel")

	// Authoritative end-state diff, independent of the watch.
	s.Require().NoError(observer.Compare(context.Background(), kube))

	observer.AssertNoDisruption(s.T())
}

// TestE2EProviderRestartMidUpdate would send an updated manifest and restart the
// provider before it settles, then assert the update converges on the target service
// while the other service is undisturbed. It is not yet implemented: with fresh ports
// on restart the on-chain provider host no longer resolves to the live gateway, so a
// post-restart send-manifest needs additional wiring to reach the new address.
func (s *E2EProviderRestart) TestE2EProviderRestartMidUpdate() {
	s.T().Skip("TODO: restart-mid-update scenario; needs post-restart manifest routing to the fresh gateway address")
}

func (s *E2EProviderRestart) waitForRunningPods(kube kubernetes.Interface, namespace string, want int, timeout time.Duration) {
	s.T().Helper()

	deadline := time.Now().Add(timeout)
	for {
		pods, err := kube.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: builder.AkashManagedLabelName + "=true",
		})
		s.Require().NoError(err)

		running := 0
		for i := range pods.Items {
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				running++
			}
		}
		if running >= want {
			return
		}

		if time.Now().After(deadline) {
			s.T().Fatalf("timed out waiting for %d running pods in %s, saw %d", want, namespace, running)
		}
		time.Sleep(2 * time.Second)
	}
}

func waitForProviderStatus(t *testing.T, statusURL string, timeout time.Duration) {
	t.Helper()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // nolint: gosec
		},
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(statusURL + "/status")
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return
		}
		lastErr = fmt.Errorf("status %d", resp.StatusCode)
		time.Sleep(time.Second)
	}

	t.Fatalf("provider /status not healthy within %s: %v", timeout, lastErr)
}

// TestProviderRestart runs the same-version restart gate.
func TestProviderRestart(t *testing.T) {
	integrationTestOnly(t)
	suite.Run(t, new(E2EProviderRestart))
}

type E2EProviderRestartTEESNP struct {
	E2EProviderRestart
}

func (s *E2EProviderRestartTEESNP) SetupSuite() {
	applyTEEMock(s.T(), "snp")
	s.deploymentFixture = "../testdata/deployment/deployment-v2-tee.yaml"
	s.expectedPods = 1
	s.expectedRuntimeClass = "kata-qemu-snp"
	s.E2EProviderRestart.SetupSuite()
}

func TestProviderRestartTEESNP(t *testing.T) {
	integrationTestOnly(t)
	suite.Run(t, new(E2EProviderRestartTEESNP))
}
