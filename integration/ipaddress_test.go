//go:build e2e

package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	clitestutil "github.com/akash-network/node/testutil/cli"
	deploycli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	providerCmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EIPAddress struct {
	IntegrationTestSuite
}

func (s *E2EIPAddress) TestIPAddressLease() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-ip-endpoint.yaml")
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
		fmt.Sprintf("--%s", flags.FlagSkipConfirmation),
		fmt.Sprintf("--%s=%s", flags.FlagBroadcastMode, flags.BroadcastBlock),
		fmt.Sprintf("--%s=%s", flags.FlagFees, sdk.NewCoins(sdk.NewCoin(s.cfg.BondDenom, sdk.NewInt(20))).String()),
		fmt.Sprintf("--gas=%d", flags.DefaultGasLimit),
		fmt.Sprintf("--dseq=%v", deploymentID.DSeq),
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
		fmt.Sprintf("--%s=true", flags.FlagSkipConfirmation),
		fmt.Sprintf("--%s=%s", flags.FlagBroadcastMode, flags.BroadcastBlock),
		fmt.Sprintf("--%s=%s", flags.FlagFees, sdk.NewCoins(sdk.NewCoin(s.cfg.BondDenom, sdk.NewInt(10))).String()),
		fmt.Sprintf("--gas=%d", flags.DefaultGasLimit),
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
	leaseID := lease.LeaseID
	s.Require().Equal(s.keyProvider.GetAddress().String(), leaseID.Provider)

	// Send Manifest to Provider ----------------------------------------------
	_, err = ptestutil.TestSendManifest(
		cctxJSON,
		leaseID.BidID(),
		deploymentPath,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	s.Require().NoError(err)

	// Wait for lease to show up
	maxWait := time.After(2 * time.Minute)
	for {
		select {
		case <-s.ctx.Done():
			s.T().Fatal("test context closed before lease is stood up by provider")
		case <-maxWait:
			s.T().Fatal("timed out waiting on lease to be stood up by provider")
		default:
		}
		cmdResult, err := providerCmd.ProviderStatusExec(s.validator.ClientCtx, leaseID.Provider)
		require.NoError(s.T(), err)
		data := make(map[string]interface{})
		err = json.Unmarshal(cmdResult.Bytes(), &data)
		require.NoError(s.T(), err)
		leaseCount, ok := data["cluster"].(map[string]interface{})["leases"]
		s.T().Logf("lease count: %v", leaseCount)
		if ok && leaseCount == float64(1) {
			break
		}

		select {
		case <-s.ctx.Done():
			s.T().Fatal("test context closed before lease is stood up by provider")
		case <-time.After(time.Second):
		}
	}

	time.Sleep(30 * time.Second) // TODO - replace with polling

	// Get the lease status and confirm IP is present
	cmdResult, err := providerCmd.ProviderLeaseStatusExec(
		s.validator.ClientCtx,
		fmt.Sprintf("--%s=%v", "dseq", leaseID.DSeq),
		fmt.Sprintf("--%s=%v", "gseq", leaseID.GSeq),
		fmt.Sprintf("--%s=%v", "oseq", leaseID.OSeq),
		fmt.Sprintf("--%s=%v", "provider", leaseID.Provider),
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)

	require.NoError(s.T(), err)
	leaseStatusData := apclient.LeaseStatus{}
	err = json.Unmarshal(cmdResult.Bytes(), &leaseStatusData)
	require.NoError(s.T(), err)
	s.Require().Len(leaseStatusData.IPs, 1)

	webService := leaseStatusData.IPs["web"]
	s.Require().Len(webService, 1)
	leasedIP := webService[0]
	s.Assert().Equal(leasedIP.Port, uint32(80))
	s.Assert().Equal(leasedIP.ExternalPort, uint32(80))
	s.Assert().Equal(strings.ToUpper(leasedIP.Protocol), "TCP")
	ipAddr := leasedIP.IP
	ip := net.ParseIP(ipAddr)
	s.Assert().NotNilf(ip, "after parsing %q got nil", ipAddr)
}
