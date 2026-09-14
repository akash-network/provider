//go:build e2e

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

type E2EProviderSubprocessSmoke struct {
	IntegrationTestSuite
}

func (s *E2EProviderSubprocessSmoke) SetupSuite() {
	s.useProviderSubprocess = true
	s.IntegrationTestSuite.SetupSuite()
}

func (s *E2EProviderSubprocessSmoke) TestProviderRunsAsSubprocess() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2.yaml")
	s.Require().NoError(err)

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(601),
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

	_, err = clitestutil.ExecQueryBid(s.ctx, cctx, cli.TestFlags().WithBidID(bidID)...)
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

	deadline := time.Now().Add(2 * time.Minute)
	for {
		pods, err := kube.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: builder.AkashManagedLabelName + "=true",
		})
		s.Require().NoError(err)

		for i := range pods.Items {
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				return
			}
		}

		if time.Now().After(deadline) {
			s.T().Fatalf("subprocess provider did not bring up a running pod in %s", namespace)
		}
		time.Sleep(2 * time.Second)
	}
}

func TestProviderSubprocessSmoke(t *testing.T) {
	integrationTestOnly(t)
	suite.Run(t, new(E2EProviderSubprocessSmoke))
}

type E2EProviderUpgrade struct {
	IntegrationTestSuite

	deploymentFixture    string
	expectedPods         int
	expectedRuntimeClass string
}

func upgradeBaseRef() string {
	if r := os.Getenv("PROVIDER_UPGRADE_BASE_REF"); r != "" {
		return r
	}
	return "HEAD"
}

func (s *E2EProviderUpgrade) SetupSuite() {
	s.useProviderSubprocess = true
	s.providerBinaryPath = buildProviderBinaryAtRef(s.T(), upgradeBaseRef())
	s.IntegrationTestSuite.SetupSuite()
}

// TestWorkloadSurvivesProviderUpgrade deploys a workload with one provider version and
// recovers it with another, asserting the pods are not disrupted by the upgrade. The
// version to deploy with is set by PROVIDER_UPGRADE_BASE_REF; when it is unset both
// versions are the current tree, which still exercises the deploy/swap machinery.
func (s *E2EProviderUpgrade) TestWorkloadSurvivesProviderUpgrade() {
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
		DSeq:  uint64(611),
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

	deadline := time.Now().Add(2 * time.Minute)
	for {
		pods, perr := kube.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: builder.AkashManagedLabelName + "=true",
		})
		s.Require().NoError(perr)

		running := 0
		for i := range pods.Items {
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				running++
			}
		}
		if running >= wantPods {
			break
		}
		if time.Now().After(deadline) {
			s.T().Fatalf("base provider did not bring up running pods in %s, saw %d", namespace, running)
		}
		time.Sleep(2 * time.Second)
	}

	if s.expectedRuntimeClass != "" {
		assertPodsRuntimeClass(s.T(), kube, namespace, s.expectedRuntimeClass)
	}

	observer := NewObserver(namespace)
	s.Require().NoError(observer.Snapshot(context.Background(), kube))

	// The watch must outlive the restart, so it uses a standalone context rather than
	// s.ctx (restartProvider cancels s.ctx). cancelWatch drives the real window end;
	// the timeout is only a hang guard.
	watchCtx, cancelWatch := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelWatch()

	var watchErr error
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchErr = observer.Watch(watchCtx, kube)
	}()

	// The recovery bug this guards against is non-convergent: the candidate's first
	// recovery of a base-written manifest may only rewrite the manifest's resource-
	// version label, and a later recovery copies it into the pod template and rolls the
	// pods. A single swap can therefore pass even against a buggy candidate, so we swap
	// to the candidate and then restart it repeatedly under one continuous watch: a
	// buggy candidate rolls the workload within a few recoveries, a healthy one never
	// does.
	s.providerBinaryPath = buildProviderBinary(s.T())
	for i := 0; i < 3; i++ {
		s.restartProvider()
		waitForProviderStatus(s.T(), s.providerStatusURL, 90*time.Second)
		// A roll surfaces only once the provider runs its startup reconcile, which is
		// after /status is healthy; settle before the next recovery.
		time.Sleep(15 * time.Second)
	}

	cancelWatch()
	<-watchDone

	// Only the pod watch (the verdict) fails loudly; the event watch is tolerated.
	s.Require().NoError(watchErr, "pod watch failed during the observation window")
	s.Require().ErrorIs(watchCtx.Err(), context.Canceled)
	s.Require().NoError(observer.Compare(context.Background(), kube))

	observer.AssertNoDisruption(s.T())
}

// TestProviderUpgrade runs the cross-version upgrade gate. In CI the base ref is the
// PR's base branch, so it deploys with the base version and recovers with the
// candidate; locally it defaults to HEAD, a same-code mechanism check.
func TestProviderUpgrade(t *testing.T) {
	integrationTestOnly(t)
	suite.Run(t, new(E2EProviderUpgrade))
}

type E2EProviderUpgradeTEESNP struct {
	E2EProviderUpgrade
}

func (s *E2EProviderUpgradeTEESNP) SetupSuite() {
	applyTEEMock(s.T(), "snp")
	s.deploymentFixture = "../testdata/deployment/deployment-v2-tee.yaml"
	s.expectedPods = 1
	s.expectedRuntimeClass = "kata-qemu-snp"
	s.E2EProviderUpgrade.SetupSuite()
}

func TestProviderUpgradeTEESNP(t *testing.T) {
	integrationTestOnly(t)
	suite.Run(t, new(E2EProviderUpgradeTEESNP))
}
