//go:build e2e

package integration

import (
	"context"
	"path/filepath"
	"time"

	sdktest "github.com/cosmos/cosmos-sdk/testutil"
	"github.com/gyuho/linux-inspect/df"
	"pkg.akt.dev/go/cli"
	mtypes "pkg.akt.dev/go/node/market/v1"

	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"

	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EStorageClassRam struct {
	IntegrationTestSuite
}

func (s *E2EStorageClassRam) TestRAM() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-storage-ram.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(100),
	}

	cctx := s.cctx

	// Create Deployments
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
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	bidID := mtypes.MakeBidID(
		mtypes.MakeOrderID(dtypes.MakeGroupID(deploymentID, 1), 1),
		s.addrProvider,
	)

	err = s.waitForBlockchainEvent(&mtypes.EventBidCreated{ID: bidID})
	s.Require().NoError(err)

	_, err = clitestutil.ExecQueryBid(s.ctx, cctx, cli.TestFlags().WithBidID(bidID)...)
	s.Require().NoError(err)

	_, err = clitestutil.ExecCreateLease(
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

	lid := bidID.LeaseID()

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
	s.Require().NoError(s.waitForBlocksCommitted(2))

	extraArgs := cli.TestFlags().
		WithLeaseID(lid).
		WithHome(s.validator.ClientCtx.HomeDir).
		WithFrom(s.addrTenant.String())

	logged := make(map[string]struct{})

	cmd := `df --all --sync --block-size=1024 --output=source,target,fstype,file,itotal,iavail,iused,ipcent,size,avail,used,pcent`
	var out sdktest.BufferWriter
	leaseShellCtx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()

	// Loop until we get a shell or the context times out
	for {
		select {
		case <-leaseShellCtx.Done():
			// s.T().Fatalf("context is done while trying to run lease-shell: %v", leaseShellCtx.Err())
			return
		default:
		}
		out, err = ptestutil.ExecLeaseShell(
			leaseShellCtx,
			cctx,
			extraArgs.
				WithFlag("replica-index", 0).
				WithFlag("tty", false).
				WithFlag("stdin", false).
				With("web", cmd)...,
		)
		if err != nil {
			_, hasBeenLogged := logged[err.Error()]
			if !hasBeenLogged {
				// Don't spam an error message in a test, that is very annoying
				s.T().Logf("encountered %v, waiting before next attempt", err)
				logged[err.Error()] = struct{}{}
			}
			time.Sleep(2000 * time.Millisecond)
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
	s.Require().Equal(int64(65536), found.TotalBlocks)
}
