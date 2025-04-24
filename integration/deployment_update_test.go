//go:build e2e

package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	cutil "github.com/akash-network/provider/cluster/util"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client/flags"
	sdktest "github.com/cosmos/cosmos-sdk/testutil"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	clitestutil "github.com/akash-network/node/testutil/cli"
	deploycli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	ptestutil "github.com/akash-network/provider/testutil/provider"

	"github.com/akash-network/provider/tools/fromctx"
)

type E2EDeploymentUpdate struct {
	IntegrationTestSuite
}

func (s *E2EDeploymentUpdate) TestE2EDeploymentUpdate() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-updateA.yaml")
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
	did := lease.GetLeaseID().DeploymentID()
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
	queryAppWithHostname(s.T(), appURL, 50, "testupdatea.localhost")

	deploymentPath, err = filepath.Abs("../testdata/deployment/deployment-v2-updateB.yaml")
	s.Require().NoError(err)

	res, err = deploycli.TxUpdateDeploymentExec(s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		deploymentPath,
		cliGlobalFlags(fmt.Sprintf("--owner=%s", lease.GetLeaseID().Owner),
			fmt.Sprintf("--dseq=%v", did.GetDSeq()))...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	// Get initial pod information
	kubeClient := fromctx.MustKubeClientFromCtx(s.ctx)
	namespace := cutil.LeaseIDToNamespace(lid)
	observer := NewObserver(namespace)

	err = observer.Observe(kubeClient)
	s.Require().NoError(err)

	// Send Updated Manifest to Provider
	_, err = ptestutil.TestSendManifest(
		s.validator.ClientCtx.WithOutputFormat("json"),
		lid.BidID(),
		deploymentPath,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	queryAppWithHostname(s.T(), appURL, 50, "testupdateb.localhost")

	err = observer.VerifyNewPodCreate(kubeClient)
	s.Require().NoError(err)
}

func (s *E2EDeploymentUpdate) TestE2ELeaseShell() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.keyTenant.GetAddress().String(),
		DSeq:  uint64(104),
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

	lease := newestLease(leaseRes.Leases)
	lID := lease.LeaseID

	s.Require().Equal(s.keyProvider.GetAddress().String(), lID.Provider)

	// Send Manifest to Provider
	_, err = ptestutil.TestSendManifest(
		s.validator.ClientCtx.WithOutputFormat("json"),
		lID.BidID(),
		deploymentPath,
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	)

	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))

	// Get initial pod information
	kubeClient := fromctx.MustKubeClientFromCtx(s.ctx)
	namespace := cutil.LeaseIDToNamespace(lID)
	observer := NewObserver(namespace)

	err = observer.Observe(kubeClient)
	s.Require().NoError(err)

	extraArgs := []string{
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	}

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
		out, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
			lID, 0, false, false, "web", "/bin/echo", "foo")
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
	_, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
		lID, 0, false, false, "web", "/bin/baz", "foo")
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*command could not be executed because it does not exist.*", err.Error())

	_, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
		lID, 0, false, false, "web", "baz", "foo")
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*command could not be executed because it does not exist.*", err.Error())

	_, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
		lID, 0, false, false, "web", "baz", "foo")
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*command could not be executed because it does not exist.*", err.Error())

	_, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
		lID, 99, false, false, "web", "/bin/echo", "foo")
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*pod index out of range.*", err.Error())

	_, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
		lID, 99, false, false, "web", "/bin/echo", "foo")
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*pod index out of range.*", err.Error())

	_, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
		lID, 0, false, false, "web", "/bin/cat", "/foo")
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*remote process exited with code 1.*", err.Error())

	_, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs,
		lID, 99, false, false, "notaservice", "/bin/echo", "/foo")
	require.Error(s.T(), err)
	require.Regexp(s.T(), ".*no service for that lease.*", err.Error())

	err = observer.VerifyNoChangeOccurred(kubeClient)
	s.Require().NoError(err)
}
