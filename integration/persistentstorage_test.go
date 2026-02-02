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
	"pkg.akt.dev/go/cli"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"

	ptestutil "github.com/akash-network/provider/testutil/provider"
)

type E2EPersistentStorageDefault struct {
	IntegrationTestSuite
}

type E2EPersistentStorageBeta2 struct {
	IntegrationTestSuite
}

type E2EPersistentStorageDeploymentUpdate struct {
	IntegrationTestSuite
}

func (s *E2EPersistentStorageDefault) TestDefaultStorageClass() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-storage-default.yaml")
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

	// give kube to reschedule pod
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

func (s *E2EPersistentStorageBeta2) TestDedicatedStorageClass() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-storage-beta2.yaml")
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
	s.Require().NoError(s.waitForBlocksCommitted(7))
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

	// Hit the endpoint to set a key in redis, foo = bar
	appURL := fmt.Sprintf("http://%s:%s/GET/value", s.appHost, s.appPort)

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

	// give kube to reschedule pod
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

func (s *E2EPersistentStorageDeploymentUpdate) TestPersistentStorageDeploymentUpdate() {
	// create a deployment
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-storage-updateA.yaml")
	s.Require().NoError(err)

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(102),
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
	s.Require().NoError(s.waitForBlocksCommitted(3))
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

	// Assert provider made bid and created lease; test query leases ---------
	resp, err := clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)

	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = cctx.Codec.UnmarshalJSON(resp.Bytes(), leaseRes)
	s.Require().NoError(err)

	s.Require().Len(leaseRes.Leases, 1)

	lease := newestLease(leaseRes.Leases)
	lid := lease.ID
	s.Require().Equal(s.addrProvider.String(), lid.Provider)

	// Send Manifest to Provider
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

	appURL := fmt.Sprintf("http://%s:%s/", s.appHost, s.appPort)
	queryAppWithHostname(s.T(), appURL, 50, "testupdatea.localhost")

	deploymentPath, err = filepath.Abs("../testdata/deployment/deployment-v2-storage-updateB.yaml")
	s.Require().NoError(err)

	res, err = clitestutil.ExecDeploymentUpdate(
		s.ctx,
		cctx,
		cli.TestFlags().
			With(deploymentPath).
			WithFrom(s.addrTenant.String()).
			WithDSeq(lease.GetID().DSeq).
			WithOwner(lease.GetID().Owner).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(2))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Send Updated Manifest to Provider
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
	queryAppWithHostname(s.T(), appURL, 50, "testupdatea.localhost")
	queryAppWithHostname(s.T(), appURL, 50, "testupdateb.localhost")
}
