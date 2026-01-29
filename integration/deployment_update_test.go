//go:build e2e

package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/cli"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	sdktest "github.com/cosmos/cosmos-sdk/testutil"

	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"

	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EDeploymentUpdate struct {
	IntegrationTestSuite
}

func (s *E2EDeploymentUpdate) TestE2EDeploymentUpdate() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-updateA.yaml")
	s.Require().NoError(err)

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(102),
	}

	// Create Deployments
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

	// check bid
	_, err = clitestutil.ExecQueryBid(s.ctx, cctx, cli.TestFlags().WithBidID(bidID)...)
	s.Require().NoError(err)

	// create lease
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
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Assert provider made bid and created lease; test query leases ---------
	resp, err := clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(resp.Bytes(), leaseRes)
	s.Require().NoError(err)

	s.Require().Len(leaseRes.Leases, 1)

	lease := newestLease(leaseRes.Leases)
	lid := lease.ID
	did := lid.DeploymentID()
	s.Require().Equal(s.addrProvider.String(), lid.Provider)

	// Send Manifest to Provider
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

	appURL := fmt.Sprintf("http://%s:%s/", s.appHost, s.appPort)
	queryAppWithHostname(s.T(), appURL, 50, "testupdatea.localhost")

	deploymentPath, err = filepath.Abs("../testdata/deployment/deployment-v2-updateB.yaml")
	s.Require().NoError(err)

	res, err = clitestutil.ExecDeploymentUpdate(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithFrom(s.addrTenant.String()).
			WithDSeq(did.DSeq).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Send Updated Manifest to Provider
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
	queryAppWithHostname(s.T(), appURL, 50, "testupdateb.localhost")
}

func (s *E2EDeploymentUpdate) TestE2ELeaseShell() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2.yaml")
	s.Require().NoError(err)

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(104),
	}

	// Create Deployments
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

	// check bid
	_, err = clitestutil.ExecQueryBid(s.ctx, cctx, cli.TestFlags().WithBidID(bidID)...)
	s.Require().NoError(err)

	// create lease
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
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Assert provider made bid and created lease; test query leases ---------
	resp, err := clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = cctx.Codec.UnmarshalJSON(resp.Bytes(), leaseRes)
	s.Require().NoError(err)

	lease := newestLease(leaseRes.Leases)
	lID := lease.ID

	s.Require().Equal(s.addrProvider.String(), lID.Provider)

	// Send Manifest to Provider
	_, err = ptestutil.ExecSendManifest(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithHome(cctx.HomeDir).
			WithFrom(s.addrTenant.String()).
			WithDSeq(lID.DSeq).
			WithOutputJSON()...,
	)

	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))

	extraArgs := cli.TestFlags().
		WithDSeq(lID.DSeq).
		WithProvider(lID.Provider).
		WithHome(cctx.HomeDir).
		WithFrom(s.addrTenant.String())

	var out sdktest.BufferWriter

	leaseShellCtx, cancel := context.WithTimeout(s.ctx, time.Minute)
	defer cancel()

	logged := make(map[string]struct{})
	// Loop until we get a shell or the context times out
	for {
		select {
		case <-leaseShellCtx.Done():
			s.T().Fatalf("context is done while trying to run lease-shell: %v", leaseShellCtx.Err())
			return
		default:
		}
		out, err = ptestutil.ExecLeaseShell(
			leaseShellCtx,
			cctx,
			extraArgs.
				WithFlag("replica-index", 0).
				WithFlag("tty", false).
				WithFlag("stdin", false).
				With("web", "/bin/echo", "foo")...,
		)
		if err != nil {
			_, hasBeenLogged := logged[err.Error()]
			if !hasBeenLogged {
				// Don't spam an error message in a test, that is very annoying
				s.T().Logf("encountered %v, waiting before next attempt", err)
				logged[err.Error()] = struct{}{}
			}
			time.Sleep(100 * time.Millisecond)
			continue // Try again until the context times out
		}
		require.NotNil(s.T(), out)
		break
	}

	// Test failure cases now
	_, err = ptestutil.ExecLeaseShell(
		leaseShellCtx,
		cctx,
		extraArgs.
			WithFlag("replica-index", 0).
			WithFlag("tty", false).
			WithFlag("stdin", false).
			With("web", "/bin/baz", "foo")...,
	)
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*command could not be executed because it does not exist.*", err.Error())

	_, err = ptestutil.ExecLeaseShell(
		leaseShellCtx,
		cctx,
		extraArgs.
			WithFlag("replica-index", 0).
			WithFlag("tty", false).
			WithFlag("stdin", false).
			With("web", "baz", "foo")...,
	)
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*command could not be executed because it does not exist.*", err.Error())

	_, err = ptestutil.ExecLeaseShell(
		leaseShellCtx,
		cctx,
		extraArgs.
			WithFlag("replica-index", 0).
			WithFlag("tty", false).
			WithFlag("stdin", false).
			With("web", "baz", "foo")...,
	)
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*command could not be executed because it does not exist.*", err.Error())

	_, err = ptestutil.ExecLeaseShell(
		leaseShellCtx,
		cctx,
		extraArgs.
			WithFlag("replica-index", 99).
			WithFlag("tty", false).
			WithFlag("stdin", false).
			With("web", "/bin/echo", "foo")...,
	)
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*pod index out of range.*", err.Error())

	_, err = ptestutil.ExecLeaseShell(
		leaseShellCtx,
		cctx,
		extraArgs.
			WithFlag("replica-index", 99).
			WithFlag("tty", false).
			WithFlag("stdin", false).
			With("web", "/bin/echo", "foo")...,
	)
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*pod index out of range.*", err.Error())

	_, err = ptestutil.ExecLeaseShell(
		leaseShellCtx,
		cctx,
		extraArgs.
			WithFlag("replica-index", 0).
			WithFlag("tty", false).
			WithFlag("stdin", false).
			With("web", "/bin/cat", "/foo")...)
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*remote process exited with code 1.*", err.Error())

	_, err = ptestutil.ExecLeaseShell(
		leaseShellCtx,
		cctx,
		extraArgs.
			WithFlag("replica-index", 99).
			WithFlag("tty", false).
			WithFlag("stdin", false).
			With("notaservice", "/bin/echo", "/foo")...,
	)
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*no service for that lease.*", err.Error())
}
