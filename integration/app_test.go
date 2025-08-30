//go:build e2e

package integration

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/cli"
	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/sdl"

	providerCmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EApp struct {
	IntegrationTestSuite
}

func (s *E2EApp) TestE2EApp() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2.yaml")
	s.Require().NoError(err)

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(103),
	}

	// Create Deployments and assert a query to assert
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

	// Test query deployments ---------------------------------------------
	res, err = clitestutil.ExecQueryDeployments(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	deployResp := &dvbeta.QueryDeploymentsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), deployResp)
	s.Require().NoError(err)
	s.Require().Len(deployResp.Deployments, 1, "Deployment Create Failed")
	deployments := deployResp.Deployments
	s.Require().Equal(s.addrTenant.String(), deployments[0].Deployment.ID.Owner)

	// test query deployment
	createdDep := deployments[0]
	res, err = clitestutil.ExecQueryDeployment(s.ctx, cctx, cli.TestFlags().WithOutputJSON().WithDeploymentID(createdDep.Deployment.ID)...)
	s.Require().NoError(err)

	deploymentResp := dvbeta.QueryDeploymentResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), &deploymentResp)
	s.Require().NoError(err)
	s.Require().Equal(createdDep, deploymentResp)
	s.Require().NotEmpty(deploymentResp.Deployment.Hash)

	// test query deployments with filters -----------------------------------
	res, err = clitestutil.ExecQueryDeployments(
		s.ctx,
		cctx,
		cli.TestFlags().WithOutputJSON().
			WithOwner(s.addrTenant.String()).
			WithDSeq(createdDep.Deployment.ID.DSeq)...,
	)
	s.Require().NoError(err, "Error when fetching deployments with owner filter")

	deployResp = &dvbeta.QueryDeploymentsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), deployResp)
	s.Require().NoError(err)
	s.Require().Len(deployResp.Deployments, 1)

	// Assert orders created by provider
	// test query orders
	res, err = clitestutil.ExecQueryOrders(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	result := &mvbeta.QueryOrdersResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), result)
	s.Require().NoError(err)
	s.Require().Len(result.Orders, 1)
	orders := result.Orders
	s.Require().Equal(s.addrTenant.String(), orders[0].ID.Owner)

	// Wait for then EndBlock to handle bidding and creating lease
	s.Require().NoError(s.waitForBlocksCommitted(15))

	// Assert provider made bid and created lease; test query leases
	// Assert provider made bid and created lease; test query leases
	res, err = clitestutil.ExecQueryBids(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	bidsRes := &mvbeta.QueryBidsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), bidsRes)
	s.Require().NoError(err)
	s.Require().Len(bidsRes.Bids, 1)

	res, err = clitestutil.ExecCreateLease(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithGasAuto().
			WithOutputJSON().
			WithFrom(s.addrTenant.String()).
			WithBidID(bidsRes.Bids[0].Bid.ID)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(6))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	res, err = clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), leaseRes)
	s.Require().NoError(err)
	s.Require().Len(leaseRes.Leases, 1)

	lease := newestLease(leaseRes.Leases)
	lid := lease.ID
	s.Require().Equal(s.addrProvider.String(), lid.Provider)

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

	appURL := fmt.Sprintf("http://%s:%s/", s.appHost, s.appPort)
	queryApp(s.T(), appURL, 50)

	cmdResult, err := providerCmd.ExecProviderStatus(s.ctx, cctx, lid.Provider)
	assert.NoError(s.T(), err)
	data := make(map[string]interface{})
	err = json.Unmarshal(cmdResult.Bytes(), &data)
	assert.NoError(s.T(), err)
	leaseCount, ok := data["cluster"].(map[string]interface{})["leases"]
	assert.True(s.T(), ok)
	assert.Equal(s.T(), float64(1), leaseCount)

	// Read SDL into memory so each service can be checked
	deploymentSdl, err := sdl.ReadFile(deploymentPath)
	require.NoError(s.T(), err)
	mani, err := deploymentSdl.Manifest()
	require.NoError(s.T(), err)

	cmdResult, err = providerCmd.ExecProviderLeaseStatus(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String()).
			WithDSeq(lid.DSeq).
			WithGSeq(lid.GSeq).
			WithOSeq(lid.OSeq).
			WithProvider(lid.Provider)...,
	)
	assert.NoError(s.T(), err)
	err = json.Unmarshal(cmdResult.Bytes(), &data)
	assert.NoError(s.T(), err)
	for _, group := range mani.GetGroups() {
		for _, service := range group.Services {
			serviceTotalCount, ok := data["services"].(map[string]interface{})[service.Name].(map[string]interface{})["total"]
			assert.True(s.T(), ok)
			assert.Greater(s.T(), serviceTotalCount, float64(0))
		}
	}

	for _, group := range mani.GetGroups() {
		for _, service := range group.Services {
			cmdResult, err = providerCmd.ExecProviderServiceStatus(
				s.ctx,
				cctx,
				cli.TestFlags().
					WithHome(s.validator.ClientCtx.HomeDir).
					WithFrom(s.addrTenant.String()).
					WithDSeq(lid.DSeq).
					WithGSeq(lid.GSeq).
					WithOSeq(lid.OSeq).
					WithProvider(lid.Provider).
					WithFlag("service", service.Name)...,
			)
			assert.NoError(s.T(), err)
			err = json.Unmarshal(cmdResult.Bytes(), &data)
			assert.NoError(s.T(), err)
			serviceTotalCount, ok := data["services"].(map[string]interface{})[service.Name].(map[string]interface{})["total"]
			assert.True(s.T(), ok)
			assert.Greater(s.T(), serviceTotalCount, float64(0))
		}
	}
}
