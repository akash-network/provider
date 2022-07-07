package integration

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/stretchr/testify/assert"

	clitestutil "github.com/ovrclk/akash/testutil/cli"
	deploycli "github.com/ovrclk/akash/x/deployment/client/cli"
	dtypes "github.com/ovrclk/akash/x/deployment/types/v1beta2"
	mcli "github.com/ovrclk/akash/x/market/client/cli"
	mtypes "github.com/ovrclk/akash/x/market/types/v1beta2"

	providerCmd "github.com/ovrclk/provider-services/cmd/provider-services/cmd"
	ptestutil "github.com/ovrclk/provider-services/testutil/provider"
)

type E2EEscrowMonitor struct {
	IntegrationTestSuite
}

func (s *E2EEscrowMonitor) TestE2EEscrowMonitor() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-escrow.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.keyTenant.GetAddress().String(),
		DSeq:  uint64(1000),
	}

	// Create Deployments
	res, err := deploycli.TxCreateDeploymentExec(
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		deploymentPath,
		cliGlobalFlags(deploymentDeposit,
			fmt.Sprintf("--dseq=%v", deploymentID.DSeq))...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(7))
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	bidID := mtypes.MakeBidID(
		mtypes.MakeOrderID(dtypes.MakeGroupID(deploymentID, 1), 1),
		s.keyProvider.GetAddress(),
	)

	// check bid
	_, err = mcli.QueryBidExec(s.validator.ClientCtx, bidID)
	s.Require().NoError(err)

	// create lease
	_, err = mcli.TxCreateLeaseExec(
		s.validator.ClientCtx,
		bidID,
		s.keyTenant.GetAddress(),
		cliGlobalFlags()...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	lid := bidID.LeaseID()

	// Send Manifest to Provider ----------------------------------------------
	_, err = ptestutil.TestSendManifest(
		s.validator.ClientCtx.WithOutputFormat("json"),
		lid.BidID(),
		deploymentPath,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))

	// Hit the endpoint to set a key in redis, foo = bar
	appURL := fmt.Sprintf("http://%s:%s/SET/foo/bar", s.appHost, s.appPort)

	const testHost = "webdistest.localhost"
	const attempts = 120
	httpResp := queryAppWithRetries(s.T(), appURL, testHost, attempts)
	bodyData, err := ioutil.ReadAll(httpResp.Body)
	s.Require().NoError(err)
	s.Require().Equal(`{"SET":[true,"OK"]}`, string(bodyData))

	s.Require().NoError(s.waitForBlocksCommitted(5))

	// Get the lease status
	_, err = providerCmd.ProviderLeaseStatusExec(
		s.validator.ClientCtx,
		fmt.Sprintf("--%s=%v", "dseq", lid.DSeq),
		fmt.Sprintf("--%s=%v", "gseq", lid.GSeq),
		fmt.Sprintf("--%s=%v", "oseq", lid.OSeq),
		fmt.Sprintf("--%s=%v", "provider", lid.Provider),
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	assert.NoError(s.T(), err)
	// data := ctypes.LeaseStatus{}
	// err = json.Unmarshal(cmdResult.Bytes(), &data)
}
