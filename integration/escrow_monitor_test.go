//go:build e2e

package integration

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/stretchr/testify/assert"
	"pkg.akt.dev/go/cli"

	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"

	providerCmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EEscrowMonitor struct {
	IntegrationTestSuite
}

func (s *E2EEscrowMonitor) TestE2EEscrowMonitor() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-escrow.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(1000),
	}

	// Create Deployments
	res, err := clitestutil.ExecDeploymentCreate(
		s.ctx,
		s.validator.ClientCtx,
		cli.TestFlags().
			With(deploymentPath).
			WithFrom(s.addrTenant.String()).
			WithDSeq(deploymentID.DSeq).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.validator.ClientCtx, res.Bytes())

	bidID := mtypes.MakeBidID(
		mtypes.MakeOrderID(dtypes.MakeGroupID(deploymentID, 1), 1),
		s.addrProvider,
	)

	err = s.waitForBlockchainEvent(&mtypes.EventBidCreated{ID: bidID})
	s.Require().NoError(err)

	// check bid
	_, err = clitestutil.ExecQueryBid(s.ctx, s.validator.ClientCtx, cli.TestFlags().WithBidID(bidID)...)
	s.Require().NoError(err)

	// create lease
	_, err = clitestutil.ExecCreateLease(
		s.ctx,
		s.validator.ClientCtx,
		cli.TestFlags().
			WithBidID(bidID).
			WithFrom(s.addrTenant.String()).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.validator.ClientCtx, res.Bytes())

	lid := bidID.LeaseID()

	// Send Manifest to Provider ----------------------------------------------
	_, err = ptestutil.ExecSendManifest(
		s.ctx,
		s.validator.ClientCtx,
		cli.TestFlags().
			With(deploymentPath).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String()).
			WithDSeq(lid.DSeq).
			WithOutputJSON()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))

	// Hit the endpoint to set a key in redis, foo = bar
	appURL := fmt.Sprintf("http://%s:%s/SET/foo/bar", s.appHost, s.appPort)

	const testHost = "webdistest.localhost"
	const attempts = 120
	httpResp := queryAppWithRetries(s.T(), appURL, testHost, attempts)
	bodyData, err := io.ReadAll(httpResp.Body)
	s.Require().NoError(err)
	s.Require().Equal(`{"SET":[true,"OK"]}`, string(bodyData))

	s.Require().NoError(s.waitForBlocksCommitted(5))

	// Get the lease status
	_, err = providerCmd.ExecProviderLeaseStatus(
		s.ctx,
		s.validator.ClientCtx,
		cli.TestFlags().
			WithBidID(lid.BidID()).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String())...,
	)
	assert.NoError(s.T(), err)
	// data := ctypes.LeaseStatus{}
	// err = json.Unmarshal(cmdResult.Bytes(), &data)
}
