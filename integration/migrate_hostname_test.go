//go:build e2e

package integration

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/assert"
	"pkg.akt.dev/go/cli"
	mtypes "pkg.akt.dev/go/node/market/v1"
	apclient "pkg.akt.dev/go/provider/client"

	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"

	providerCmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EMigrateHostname struct {
	IntegrationTestSuite
}

func (s *E2EMigrateHostname) TestE2EMigrateHostname() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-migrate.yaml")
	s.Require().NoError(err)

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(105),
	}

	// Create Deployments and assert query to assert
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

	err = s.waitForBlockchainEvent(&mtypes.EventBidCreated{ID: bidID})
	s.Require().NoError(err)

	// Assert provider made bid and created lease; test query leases
	// check bid
	_, err = clitestutil.ExecQueryBid(s.ctx, cctx, cli.TestFlags().WithBidID(bidID)...)
	s.Require().NoError(err)

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
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.cctx, res.Bytes())

	lid := mtypes.MakeLeaseID(bidID)

	err = s.waitForBlockchainEvent(&mtypes.EventLeaseCreated{ID: lid})
	s.Require().NoError(err)

	res, err = clitestutil.ExecQueryLease(s.ctx, cctx, cli.TestFlags().WithLeaseID(lid).WithOutputJSON()...)
	s.Require().NoError(err)

	// Send Manifest to Provider ----------------------------------------------
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
	s.Require().NoError(s.waitForBlocksCommitted(20))

	const primaryHostname = "leaveme.com"
	const secondaryHostname = "migrateme.com"

	appURL := fmt.Sprintf("http://%s:%s/", s.appHost, s.appPort)
	queryAppWithHostname(s.T(), appURL, 50, primaryHostname)
	queryAppWithHostname(s.T(), appURL, 50, secondaryHostname)

	cmdResult, err := providerCmd.ExecProviderStatus(s.ctx, cctx, lid.Provider)
	assert.NoError(s.T(), err)
	data := make(map[string]interface{})
	err = json.Unmarshal(cmdResult.Bytes(), &data)
	assert.NoError(s.T(), err)
	leaseCount, ok := data["cluster"].(map[string]interface{})["leases"]
	assert.True(s.T(), ok)
	assert.Equal(s.T(), float64(1), leaseCount)

	// Create another deployment, use the same exact SDL

	secondDeploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(106),
	}

	res, err = clitestutil.ExecDeploymentCreate(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithFrom(s.addrTenant.String()).
			WithDSeq(secondDeploymentID.DSeq).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.validator.ClientCtx, res.Bytes())

	bidID = mtypes.MakeBidID(
		mtypes.MakeOrderID(dtypes.MakeGroupID(secondDeploymentID, 1), 1),
		s.addrProvider,
	)

	err = s.waitForBlockchainEvent(&mtypes.EventBidCreated{ID: bidID})
	s.Require().NoError(err)

	// Assert provider made bid and created lease; test query leases
	// check bid
	_, err = clitestutil.ExecQueryBid(s.ctx, cctx, cli.TestFlags().WithBidID(bidID)...)
	s.Require().NoError(err)

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
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.validator.ClientCtx, res.Bytes())

	slid := mtypes.MakeLeaseID(bidID)

	err = s.waitForBlockchainEvent(&mtypes.EventLeaseCreated{ID: slid})
	s.Require().NoError(err)

	res, err = clitestutil.ExecQueryLease(s.ctx, cctx, cli.TestFlags().WithLeaseID(lid).WithOutputJSON()...)
	s.Require().NoError(err)

	// Send Manifest to Provider ----------------------------------------------
	_, err = ptestutil.ExecSendManifest(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String()).
			WithDSeq(slid.DSeq).
			WithOutputJSON()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(20))

	// migrate hostname
	_, err = ptestutil.ExecMigrateHostname(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(
				secondaryHostname,
			).
			WithProvider(lid.Provider).
			WithDSeq(slid.DSeq).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String())...)
	s.Require().NoError(err)

	time.Sleep(10 * time.Second) // update happens in kube async

	// Get the lease status and confirm hostname is present
	cmdResult, err = providerCmd.ExecProviderLeaseStatus(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithDSeq(slid.DSeq).
			WithGSeq(slid.GSeq).
			WithOSeq(slid.OSeq).
			WithProvider(slid.Provider).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String())...,
	)
	assert.NoError(s.T(), err)
	leaseStatusData := apclient.LeaseStatus{}
	err = json.Unmarshal(cmdResult.Bytes(), &leaseStatusData)
	assert.NoError(s.T(), err)

	hostnameFound := false
	for _, service := range leaseStatusData.Services {
		for _, serviceURI := range service.URIs {
			if serviceURI == secondaryHostname {
				hostnameFound = true
				break
			}
		}
	}
	s.Require().True(hostnameFound, "could not find hostname")

	// close first deployment & lease
	res, err = clitestutil.ExecDeploymentClose(
		s.ctx,
		cctx,
		cli.TestFlags().WithFrom(s.addrTenant.String()).
			WithOwner(deploymentID.Owner).
			WithDSeq(deploymentID.DSeq).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(1))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), s.validator.ClientCtx, res.Bytes())

	time.Sleep(10 * time.Second) // Make sure provider has time to close the lease
	// Query the first lease, make sure it is closed
	cmdResult, err = providerCmd.ExecProviderLeaseStatus(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithDSeq(lid.DSeq).
			WithGSeq(lid.GSeq).
			WithOSeq(lid.OSeq).
			WithProvider(lid.Provider).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String())...,
	)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "remote server returned 404")
	s.Require().NotNil(cmdResult)

	// confirm hostname still reachable, on the hostname it was migrated to
	queryAppWithHostname(s.T(), appURL, 50, secondaryHostname)
}
