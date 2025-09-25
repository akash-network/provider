//go:build e2e

package integration

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	clitestutil "github.com/akash-network/node/testutil/cli"
	"github.com/akash-network/node/testutil/network"
	deploycli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"
	cutil "github.com/akash-network/provider/cluster/util"
	ptestutil "github.com/akash-network/provider/testutil/provider"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type E2EDeploymentCreate struct {
	IntegrationTestSuite
}

func (s *E2EDeploymentCreate) TestE2EDeploymentCreateProviderShutdown() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.keyTenant.GetAddress().String(),
		DSeq:  uint64(102),
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

	// Send Manifest to Provider
	_, err = ptestutil.TestSendManifest(
		s.validator.ClientCtx.WithOutputFormat("json"),
		lid.BidID(),
		deploymentPath,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)

	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))

	appURL := fmt.Sprintf("http://%s:%s/", s.appHost, s.appPort)
	queryAppWithHostname(s.T(), appURL, 50, "test.localhost")

	// Get initial pod information
	kubeClient := fromctx.MustKubeClientFromCtx(s.ctx)
	namespace := cutil.LeaseIDToNamespace(lid)
	observer := NewObserver(namespace)

	err = observer.Observe(kubeClient)
	s.Require().NoError(err)

	s.T().Logf("canceling provider context to force a restart")
	s.ctxCancel()

	ctx, cancel := context.WithCancel(context.Background())
	s.ctxCancel = cancel

	group, ctx := errgroup.WithContext(ctx)
	ctx = context.WithValue(ctx, fromctx.CtxKeyErrGroup, group)

	s.ctx = ctx
	s.group = group

	// Restart the provider by stopping and starting it again
	cliHome := strings.Replace(s.validator.ClientCtx.HomeDir, "simd", "simcli", 1)

	numPorts := 3
	if s.ipMarketplace {
		numPorts++
	}

	ports, err := network.GetFreePorts(numPorts)
	s.Require().NoError(err)

	// address for provider to listen on
	provHost := fmt.Sprintf("localhost:%d", ports[0])
	provURL := url.URL{
		Host:   provHost,
		Scheme: "https",
	}

	hostnameOperatorPort := ports[2]
	hostnameOperatorHost := fmt.Sprintf("localhost:%d", hostnameOperatorPort)

	var ipOperatorHost string
	var ipOperatorPort int
	if s.ipMarketplace {
		ipOperatorPort = ports[3]
		ipOperatorHost = fmt.Sprintf("localhost:%d", ipOperatorPort)
	}

	extraArgs := []string{
		fmt.Sprintf("--%s=%s", flags.FlagFees, sdk.NewCoins(sdk.NewCoin(s.cfg.BondDenom, sdk.NewInt(20))).String()),
		"--deployment-runtime-class=none", // do not use gvisor in test
		fmt.Sprintf("--hostname-operator-endpoint=%s", hostnameOperatorHost),
	}

	if s.ipMarketplace {
		extraArgs = append(extraArgs, fmt.Sprintf("--ip-operator-endpoint=%s", ipOperatorHost))
		extraArgs = append(extraArgs, "--ip-operator")
	}

	s.group.Go(func() error {
		_, err := ptestutil.RunLocalProvider(ctx,
			s.validator.ClientCtx,
			s.validator.ClientCtx.ChainID,
			s.validator.RPCAddress,
			cliHome,
			s.keyProvider.GetName(),
			provURL.Host,
			extraArgs...,
		)

		if err != nil {
			s.T().Logf("provider exit %v", err)
		}

		return err
	})

	dialer := net.Dialer{
		Timeout: time.Second * 3,
	}

	// Wait for the provider gateway to be up and running
	s.T().Log("waiting for provider gateway")
	waitForTCPSocket(s.ctx, dialer, provHost, s.T())

	// --- Start hostname operator
	s.group.Go(func() error {
		s.T().Logf("starting hostname operator for test on %s", hostnameOperatorHost)
		_, err := ptestutil.RunLocalHostnameOperator(s.ctx, s.validator.ClientCtx, hostnameOperatorPort)
		s.Assert().NoError(err)
		return err
	})

	s.T().Log("waiting for hostname operator")
	waitForTCPSocket(s.ctx, dialer, hostnameOperatorHost, s.T())

	if s.ipMarketplace {
		s.group.Go(func() error {
			s.T().Logf("starting ip operator for test on %v", ipOperatorHost)
			_, err := ptestutil.RunLocalIPOperator(s.ctx, s.validator.ClientCtx, ipOperatorPort, s.keyProvider.GetAddress())
			s.Assert().NoError(err)
			return err
		})

		s.T().Log("waiting for IP operator")
		waitForTCPSocket(s.ctx, dialer, ipOperatorHost, s.T())
	}

	s.Require().NoError(s.network.WaitForNextBlock())

	err = observer.VerifyNoChangeOccurred(kubeClient)
	s.Require().NoError(err)
}
