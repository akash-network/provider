//go:build e2e

package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/cosmos/cosmos-sdk/client/flags"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	clitestutil "github.com/akash-network/node/testutil/cli"
	deploycli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2ECustomCurrency struct {
	IntegrationTestSuite
}

func (s *E2ECustomCurrency) TestDefaultStorageClass() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-custom-currency.yaml")
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
		cliGlobalFlags(deploymentAxlUSDCDeposit, fmt.Sprintf("--dseq=%v", deploymentID.DSeq))...,
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

	// Hit the endpoint to set a key in redis, foo = bar
	appURL := fmt.Sprintf("http://webdistest.localhost:%s/GET/value", s.appPort)

	const testHost = "webdistest.localhost"
	const attempts = 120
	httpResp := queryAppWithRetries(s.T(), appURL, testHost, attempts)
	s.Require().Equal(http.StatusOK, httpResp.StatusCode)

	bodyData, err := io.ReadAll(httpResp.Body)
	s.Require().NoError(err)
	s.Require().Equal(`default`, string(bodyData))

	testData := uuid.New()

	// Hit the endpoint to read a key in redis, foo
	appURL = fmt.Sprintf("http://%s:%s/SET/value", s.appHost, s.appPort)
	httpResp = queryAppWithRetries(s.T(), appURL, testHost, attempts, queryWithBody([]byte(testData.String())))
	s.Require().Equal(http.StatusOK, httpResp.StatusCode)

	appURL = fmt.Sprintf("http://%s:%s/GET/value", s.appHost, s.appPort)
	httpResp = queryAppWithRetries(s.T(), appURL, testHost, attempts)
	s.Require().Equal(http.StatusOK, httpResp.StatusCode)

	bodyData, err = io.ReadAll(httpResp.Body)
	s.Require().NoError(err)
	s.Require().Equal(testData.String(), string(bodyData))

	// send signal for pod to die
	appURL = fmt.Sprintf("http://%s:%s/kill", s.appHost, s.appPort)
	httpResp = queryAppWithRetries(s.T(), appURL, testHost, attempts)
	s.Require().Equal(http.StatusOK, httpResp.StatusCode)

	// give kube time to reschedule pod
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		cancel()
		return
	}
	cancel()

	appURL = fmt.Sprintf("http://%s:%s/GET/value", s.appHost, s.appPort)
	httpResp = queryAppWithRetries(s.T(), appURL, testHost, attempts)
	s.Require().Equal(http.StatusOK, httpResp.StatusCode)
	bodyData, err = io.ReadAll(httpResp.Body)
	s.Require().NoError(err)
	s.Require().Equal(testData.String(), string(bodyData))
}
