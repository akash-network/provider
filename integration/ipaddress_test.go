//go:build e2e

package integration

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"pkg.akt.dev/go/cli"
	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	apclient "pkg.akt.dev/go/provider/client"

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
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	leaseID := mtypes.MakeLeaseID(bidID)

	err = s.waitForBlockchainEvent(&mtypes.EventLeaseCreated{ID: leaseID})
	s.Require().NoError(err)

	res, err = clitestutil.ExecQueryLease(s.ctx, cctx, cli.TestFlags().WithLeaseID(leaseID).WithOutputJSON()...)
	s.Require().NoError(err)

	// Send Manifest to Provider ----------------------------------------------
	_, err = ptestutil.ExecSendManifest(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String()).
			WithDSeq(leaseID.DSeq).
			WithOutputJSON()...,
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
		cmdResult, err := providerCmd.ExecProviderStatus(s.ctx, cctx, cli.TestFlags().With(leaseID.Provider)...)
		require.NoError(s.T(), err)
		data := make(map[string]interface{})
		err = json.Unmarshal(cmdResult.Bytes(), &data)
		require.NoError(s.T(), err)
		leaseCount, ok := data["cluster"].(map[string]interface{})["leases"]
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
	cmdResult, err := providerCmd.ExecProviderLeaseStatus(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithDSeq(leaseID.DSeq).
			WithGSeq(leaseID.GSeq).
			WithOSeq(leaseID.OSeq).
			WithProvider(leaseID.Provider).
			WithHome(s.validator.ClientCtx.HomeDir).
			WithFrom(s.addrTenant.String())...,
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
