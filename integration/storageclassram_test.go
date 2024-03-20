//go:build e2e

package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cosmos/cosmos-sdk/client/flags"
	sdktest "github.com/cosmos/cosmos-sdk/testutil"
	"github.com/gyuho/linux-inspect/df"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	clitestutil "github.com/akash-network/node/testutil/cli"
	deploycli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EStorageClassRam struct {
	IntegrationTestSuite
}

type dfOutput struct {
	Mount      string `json:"mount"`
	Spacetotal string `json:"spacetotal"`
}

type dfResult []dfOutput

func (s *E2EStorageClassRam) TestRAM() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-storage-ram.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.keyTenant.GetAddress().String(),
		DSeq:  uint64(100),
	}

	// Create Deployments
	res, err := deploycli.TxCreateDeploymentExec(
		s.validator.ClientCtx,
		s.keyTenant.GetAddress(),
		deploymentPath,
		cliGlobalFlags(fmt.Sprintf("--dseq=%v", deploymentID.DSeq))...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(7))
	clitestutil.ValidateTxSuccessful(s.T(), s.validator.ClientCtx, res.Bytes())

	bidID := mtypes.MakeBidID(
		mtypes.MakeOrderID(dtypes.MakeGroupID(deploymentID, 1), 1),
		s.keyProvider.GetAddress(),
	)

	_, err = mcli.QueryBidExec(s.validator.ClientCtx, bidID)
	s.Require().NoError(err)

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

	var out sdktest.BufferWriter
	leaseShellCtx, cancel := context.WithTimeout(s.ctx, time.Minute)
	defer cancel()

	extraArgs := []string{
		fmt.Sprintf("--%s=%s", flags.FlagFrom, s.keyTenant.GetAddress().String()),
		fmt.Sprintf("--%s=%s", flags.FlagHome, s.validator.ClientCtx.HomeDir),
	}

	logged := make(map[string]struct{})

	cmd := `df --all --sync --block-size=1024 --output=source,target,fstype,file,itotal,iavail,iused,ipcent,size,avail,used,pcent`

	// Loop until we get a shell or the context times out
	for {
		select {
		case <-leaseShellCtx.Done():
			s.T().Fatalf("context is done while trying to run lease-shell: %v", leaseShellCtx.Err())
			return
		default:
		}
		out, err = ptestutil.TestLeaseShell(leaseShellCtx, s.validator.ClientCtx.WithOutputFormat("json"), extraArgs, lid, 0, false, false, "web", cmd)
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
		s.Require().NotNil(s.T(), out)
		break
	}

	dfRes, err := df.Parse(out.String())
	s.Require().NoError(err)

	var found *df.Row

	for i := range dfRes {
		if dfRes[i].MountedOn == "/dev/shm" {
			found = &dfRes[i]
			break
		}
	}

	s.Require().NotNil(found)
	s.Require().Equal(int64(131072), found.TotalBlocks)
}
