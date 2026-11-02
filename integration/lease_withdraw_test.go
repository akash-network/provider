//go:build e2e

package integration

import (
	"fmt"
	"path/filepath"
	"time"

	"pkg.akt.dev/go/cli"
	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	etypes "pkg.akt.dev/go/node/escrow/types/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	ptestutil "github.com/akash-network/provider/testutil/provider"
)

// E2ELeaseWithdraw runs the provider with a short withdrawal period, so it
// withdraws from the leases it serves while the test runs.
type E2ELeaseWithdraw struct {
	IntegrationTestSuite
}

// The provider withdraws its earnings from a lease's escrow payment once per
// withdrawal period.
func (s *E2ELeaseWithdraw) TestE2ELeaseWithdraw() {
	s.Require().Positive(s.withdrawalPeriod, "the suite runs the provider without a withdrawal period")

	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2.yaml")
	s.Require().NoError(err)

	cctx := s.cctx
	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(110),
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
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	bidID := mtypes.MakeBidID(
		mtypes.MakeOrderID(dtypes.MakeGroupID(deploymentID, 1), 1),
		s.addrProvider,
	)
	s.Require().NoError(s.waitForBlockchainEvent(&mtypes.EventBidCreated{ID: bidID}))

	res, err = clitestutil.ExecCreateLease(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithBidID(bidID).
			WithFrom(s.addrTenant.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	lid := mtypes.MakeLeaseID(bidID)
	s.Require().NoError(s.waitForBlockchainEvent(&mtypes.EventLeaseCreated{ID: lid}))

	// The provider monitors the lease's funds once it receives the manifest.
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
	queryApp(s.T(), fmt.Sprintf("http://%s:%s/", s.appHost, s.appPort), 50)

	// The first withdrawal is due one withdrawal period after the monitoring starts.
	timeout := 3 * s.withdrawalPeriod
	deadline := time.Now().Add(timeout)
	for {
		res, err = clitestutil.ExecQueryLease(s.ctx, cctx, cli.TestFlags().WithLeaseID(lid).WithOutputJSON()...)
		s.Require().NoError(err)

		lease := mvbeta.QueryLeaseResponse{}
		s.Require().NoError(s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), &lease))
		if lease.EscrowPayment.State.Withdrawn.IsPositive() {
			// Closing a lease settles its payment too, so only a live lease shows a
			// withdrawal by the provider.
			s.Require().Equal(mtypes.LeaseActive, lease.Lease.State)
			s.Require().Equal(etypes.StateOpen, lease.EscrowPayment.State.State)
			return
		}

		s.Require().True(time.Now().Before(deadline), "the provider withdrew nothing from lease %s within %s", lid, timeout)
		s.Require().NoError(s.waitForBlocksCommitted(2))
	}
}
