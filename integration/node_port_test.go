//go:build e2e

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
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

type E2EAppNodePort struct {
	IntegrationTestSuite
}

func (s *E2EAppNodePort) TestE2EAppNodePort() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-nodeport.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.keyTenant.GetAddress().String(),
		DSeq:  uint64(101),
	}

	// Create Deployments
	res, err := deploycli.TxCreateDeploymentExec(
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		deploymentPath,
		cliGlobalFlags(fmt.Sprintf("--dseq=%v", deploymentID.DSeq))...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(3))
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

	// Assert provider made bid and created lease; test query leases ---------
	resp, err := mcli.QueryLeasesExec(s.validator.ClientCtx.WithOutputFormat("json"))
	s.Require().NoError(err)

	leaseRes := &mtypes.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(resp.Bytes(), leaseRes)
	s.Require().NoError(err)
	s.Require().Len(leaseRes.Leases, 1)

	lease := newestLease(leaseRes.Leases)
	lid := lease.LeaseID
	s.Require().Equal(s.keyProvider.GetAddress().String(), lid.Provider)

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

	// Get the lease status
	cmdResult, err := providerCmd.ProviderLeaseStatusExec(
		s.validator.ClientCtx,
		fmt.Sprintf("--%s=%v", "dseq", lid.DSeq),
		fmt.Sprintf("--%s=%v", "gseq", lid.GSeq),
		fmt.Sprintf("--%s=%v", "oseq", lid.OSeq),
		fmt.Sprintf("--%s=%v", "provider", lid.Provider),
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	assert.NoError(s.T(), err)
	data := apclient.LeaseStatus{}
	err = json.Unmarshal(cmdResult.Bytes(), &data)
	assert.NoError(s.T(), err)

	forwardedPort := uint16(0)
portLoop:
	for _, entry := range data.ForwardedPorts {
		for _, port := range entry {
			forwardedPort = port.ExternalPort
			break portLoop
		}
	}
	s.Require().NotEqual(uint16(0), forwardedPort)

	const maxAttempts = 60
	var recvData []byte
	var connErr error
	var conn net.Conn

	kubernetesIP := getKubernetesIP()
	if len(kubernetesIP) != 0 {
		for attempts := 0; attempts != maxAttempts; attempts++ {
			// Connect with a timeout so the test doesn't get stuck here
			conn, connErr = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", kubernetesIP, forwardedPort), 2*time.Second)
			// If an error, just wait and try again
			if connErr != nil {
				time.Sleep(time.Duration(500) * time.Millisecond)
				continue
			}
			break
		}

		// check that a connection was created without any error
		s.Require().NoError(connErr)
		// Read everything with a timeout
		err = conn.SetReadDeadline(time.Now().Add(time.Duration(10) * time.Second))
		s.Require().NoError(err)
		recvData, err = io.ReadAll(conn)
		s.Require().NoError(err)
		s.Require().NoError(conn.Close())

		s.Require().Regexp("^.*hello world(?s:.)*$", string(recvData))
	}
}
