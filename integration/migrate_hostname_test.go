//go:build e2e

package integration

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/stretchr/testify/assert"

	"github.com/cosmos/cosmos-sdk/client/flags"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	clitestutil "github.com/akash-network/node/testutil/cli"
	deploycli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

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

	cctxJSON := s.validator.ClientCtx.WithOutputFormat("json")

	deploymentID := dtypes.DeploymentID{
		Owner: s.keyTenant.GetAddress().String(),
		DSeq:  uint64(105),
	}

	// Create Deployments and assert query to assert
	res, err := deploycli.TxCreateDeploymentExec(
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		deploymentPath,
		cliGlobalFlags(fmt.Sprintf("--dseq=%v", deploymentID.DSeq))...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	// Wait for then EndBlock to handle bidding and creating lease
	s.Require().NoError(s.waitForBlocksCommitted(15))

	// Assert provider made bid and created lease; test query leases
	res, err = mcli.QueryBidsExec(cctxJSON)
	s.Require().NoError(err)
	bidsRes := &mtypes.QueryBidsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), bidsRes)
	s.Require().NoError(err)
	selectedIdx := -1
	for i, bidEntry := range bidsRes.Bids {
		bid := bidEntry.GetBid()
		if bid.GetBidID().DeploymentID().Equals(deploymentID) {
			selectedIdx = i
			break
		}
	}
	s.Require().NotEqual(selectedIdx, -1)
	bid := bidsRes.Bids[selectedIdx].GetBid()

	res, err = mcli.TxCreateLeaseExec(
		cctxJSON,
		bid.BidID,
		s.keyTenant.GetAddress(),
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(6))
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	res, err = mcli.QueryLeasesExec(cctxJSON)
	s.Require().NoError(err)

	leaseRes := &mtypes.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), leaseRes)
	s.Require().NoError(err)
	selectedIdx = -1
	for idx, leaseEntry := range leaseRes.Leases {
		lease := leaseEntry.GetLease()
		if lease.GetLeaseID().DeploymentID().Equals(deploymentID) {
			selectedIdx = idx
			break
		}
	}
	s.Require().NotEqual(selectedIdx, -1)

	lease := leaseRes.Leases[selectedIdx].GetLease()
	lid := lease.LeaseID
	s.Require().Equal(s.keyProvider.GetAddress().String(), lid.Provider)

	// Send Manifest to Provider ----------------------------------------------
	_, err = ptestutil.TestSendManifest(
		cctxJSON,
		lid.BidID(),
		deploymentPath,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(20))

	const primaryHostname = "leaveme.com"
	const secondaryHostname = "migrateme.com"

	appURL := fmt.Sprintf("http://%s:%s/", s.appHost, s.appPort)
	queryAppWithHostname(s.T(), appURL, 50, primaryHostname)
	queryAppWithHostname(s.T(), appURL, 50, secondaryHostname)

	cmdResult, err := providerCmd.ProviderStatusExec(s.validator.ClientCtx, lid.Provider)
	assert.NoError(s.T(), err)
	data := make(map[string]interface{})
	err = json.Unmarshal(cmdResult.Bytes(), &data)
	assert.NoError(s.T(), err)
	leaseCount, ok := data["cluster"].(map[string]interface{})["leases"]
	assert.True(s.T(), ok)
	assert.Equal(s.T(), float64(1), leaseCount)

	// Create another deployment, use the same exact SDL

	secondDeploymentID := dtypes.DeploymentID{
		Owner: s.keyTenant.GetAddress().String(),
		DSeq:  uint64(106),
	}

	res, err = deploycli.TxCreateDeploymentExec(
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		deploymentPath,
		cliGlobalFlags(fmt.Sprintf("--dseq=%v", secondDeploymentID.DSeq))...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.network.WaitForNextBlock())
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	// Wait for then EndBlock to handle bidding and creating lease
	s.Require().NoError(s.waitForBlocksCommitted(15))

	// Assert provider made bid and created lease; test query leases
	res, err = mcli.QueryBidsExec(cctxJSON)
	s.Require().NoError(err)
	bidsRes = &mtypes.QueryBidsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), bidsRes)
	s.Require().NoError(err)

	selectedIdx = -1
	for i, bidEntry := range bidsRes.Bids {
		bid := bidEntry.GetBid()
		if bid.GetBidID().DeploymentID().Equals(secondDeploymentID) {
			selectedIdx = i
			break
		}
	}
	s.Require().NotEqual(selectedIdx, -1)
	bid = bidsRes.Bids[selectedIdx].GetBid()

	res, err = mcli.TxCreateLeaseExec(
		cctxJSON,
		bid.BidID,
		s.keyTenant.GetAddress(),
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(6))
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	res, err = mcli.QueryLeasesExec(cctxJSON)
	s.Require().NoError(err)

	leaseRes = &mtypes.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), leaseRes)
	s.Require().NoError(err)
	selectedIdx = -1
	for idx, leaseEntry := range leaseRes.Leases {
		lease := leaseEntry.GetLease()
		if lease.GetLeaseID().DeploymentID().Equals(secondDeploymentID) {
			selectedIdx = idx
			break
		}
	}
	s.Require().NotEqual(selectedIdx, -1)

	secondLease := leaseRes.Leases[selectedIdx].GetLease()
	secondLID := secondLease.LeaseID
	s.Require().Equal(s.keyProvider.GetAddress().String(), lid.Provider)

	// Send Manifest to Provider ----------------------------------------------
	_, err = ptestutil.TestSendManifest(
		cctxJSON,
		secondLID.BidID(),
		deploymentPath,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(20))

	// migrate hostname
	_, err = ptestutil.TestMigrateHostname(cctxJSON, lid, secondDeploymentID.DSeq, secondaryHostname,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir))
	s.Require().NoError(err)

	time.Sleep(10 * time.Second) // update happens in kube async

	// Get the lease status and confirm hostname is present
	cmdResult, err = providerCmd.ProviderLeaseStatusExec(
		s.validator.ClientCtx,
		fmt.Sprintf("--%s=%v", "dseq", secondLID.DSeq),
		fmt.Sprintf("--%s=%v", "gseq", secondLID.GSeq),
		fmt.Sprintf("--%s=%v", "oseq", secondLID.OSeq),
		fmt.Sprintf("--%s=%v", "provider", secondLID.Provider),
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
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
	res, err = deploycli.TxCloseDeploymentExec(
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		cliGlobalFlags(fmt.Sprintf("--owner=%s", deploymentID.GetOwner()),
			fmt.Sprintf("--dseq=%v", deploymentID.GetDSeq()))...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(1))
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	time.Sleep(10 * time.Second) // Make sure provider has time to close the lease
	// Query the first lease, make sure it is closed
	cmdResult, err = providerCmd.ProviderLeaseStatusExec(
		s.validator.ClientCtx,
		fmt.Sprintf("--%s=%v", "dseq", lid.DSeq),
		fmt.Sprintf("--%s=%v", "gseq", lid.GSeq),
		fmt.Sprintf("--%s=%v", "oseq", lid.OSeq),
		fmt.Sprintf("--%s=%v", "provider", lid.Provider),
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "remote server returned 404")
	s.Require().NotNil(cmdResult)

	// confirm hostname still reachable, on the hostname it was migrated to
	queryAppWithHostname(s.T(), appURL, 50, secondaryHostname)
}
