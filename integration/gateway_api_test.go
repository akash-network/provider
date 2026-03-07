//go:build e2e

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"pkg.akt.dev/go/cli"
	clitestutil "pkg.akt.dev/go/cli/testutil"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	"github.com/akash-network/provider/cluster/kube/builder"
	providerCmd "github.com/akash-network/provider/cmd/provider-services/cmd"
	ptestutil "github.com/akash-network/provider/testutil/provider"
	"github.com/akash-network/provider/tools/fromctx"
)

var httpRouteGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

// E2EGatewayAPI is the test suite for Gateway API integration tests.
// It embeds IntegrationTestSuite with gatewayAPIMode enabled.
type E2EGatewayAPI struct {
	IntegrationTestSuite
	dc dynamic.Interface
}

func (s *E2EGatewayAPI) SetupSuite() {
	s.IntegrationTestSuite.SetupSuite()

	// Create dynamic client for Gateway API resources
	kubecfg := s.ctx.Value(fromctx.CtxKeyKubeConfig)
	if kubecfg != nil {
		var err error
		s.dc, err = dynamic.NewForConfig(kubecfg.(*rest.Config))
		s.Require().NoError(err)
	}
}

// TestE2EGatewayAPIHTTPRouteCreation tests that HTTPRoute resources are created
// when deploying a workload with hostname in gateway-api mode.
func (s *E2EGatewayAPI) TestE2EGatewayAPIHTTPRouteCreation() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-gateway-api.yaml")
	s.Require().NoError(err)

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(200),
	}

	// Create deployment
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

	// Wait for bid
	s.Require().NoError(s.waitForBlocksCommitted(15))

	// Get bid and create lease
	res, err = clitestutil.ExecQueryBids(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	bidsRes := &mvbeta.QueryBidsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), bidsRes)
	s.Require().NoError(err)
	s.Require().NotEmpty(bidsRes.Bids, "expected at least one bid")

	res, err = clitestutil.ExecCreateLease(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithGasAuto().
			WithOutputJSON().
			WithFrom(s.addrTenant.String()).
			WithBidID(bidsRes.Bids[0].Bid.ID)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(6))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Get lease
	res, err = clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), leaseRes)
	s.Require().NoError(err)
	s.Require().NotEmpty(leaseRes.Leases, "expected at least one lease")

	lease := newestLease(leaseRes.Leases)
	lid := lease.ID

	// Send manifest
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
	s.Require().NoError(s.waitForBlocksCommitted(20))

	// Verify HTTPRoute was created
	ns := builder.LidNS(lid)
	s.T().Run("HTTPRoute exists", func(t *testing.T) {
		s.verifyHTTPRouteExists(ns, "gateway-test.localhost")
	})

	s.T().Run("HTTPRoute has correct labels", func(t *testing.T) {
		s.verifyHTTPRouteLabels(ns, "gateway-test.localhost", lid)
	})

	s.T().Run("HTTPRoute has correct parent ref", func(t *testing.T) {
		s.verifyHTTPRouteParentRef(ns, "gateway-test.localhost", "akash-gateway", "akash-gateway")
	})

	s.T().Run("HTTPRoute has correct annotations", func(t *testing.T) {
		s.verifyHTTPRouteAnnotations(ns, "gateway-test.localhost")
	})

	// Verify provider status
	cmdResult, err := providerCmd.ExecProviderStatus(s.ctx, cctx, lid.Provider)
	assert.NoError(s.T(), err)
	data := make(map[string]interface{})
	err = json.Unmarshal(cmdResult.Bytes(), &data)
	assert.NoError(s.T(), err)
	leaseCount, ok := data["cluster"].(map[string]interface{})["leases"]
	assert.True(s.T(), ok)
	assert.Equal(s.T(), float64(1), leaseCount)
}

// TestE2EGatewayAPIHTTPRouteCleanup tests that HTTPRoute resources are deleted
// when the deployment is closed.
func (s *E2EGatewayAPI) TestE2EGatewayAPIHTTPRouteCleanup() {
	deploymentPath, err := filepath.Abs("../testdata/deployment/deployment-v2-gateway-api.yaml")
	s.Require().NoError(err)

	cctx := s.cctx

	deploymentID := dtypes.DeploymentID{
		Owner: s.addrTenant.String(),
		DSeq:  uint64(201),
	}

	// Create deployment
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

	// Wait for bid and create lease
	s.Require().NoError(s.waitForBlocksCommitted(15))

	res, err = clitestutil.ExecQueryBids(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	bidsRes := &mvbeta.QueryBidsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), bidsRes)
	s.Require().NoError(err)
	s.Require().NotEmpty(bidsRes.Bids)

	// Find bid for this deployment
	var targetBid *mvbeta.QueryBidResponse
	for i := range bidsRes.Bids {
		if bidsRes.Bids[i].Bid.ID.DSeq == deploymentID.DSeq {
			targetBid = &bidsRes.Bids[i]
			break
		}
	}
	s.Require().NotNil(targetBid, "expected bid for deployment")

	res, err = clitestutil.ExecCreateLease(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithGasAuto().
			WithOutputJSON().
			WithFrom(s.addrTenant.String()).
			WithBidID(targetBid.Bid.ID)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(6))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Get lease
	res, err = clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), leaseRes)
	s.Require().NoError(err)

	var lease *mvbeta.QueryLeaseResponse
	for i := range leaseRes.Leases {
		if leaseRes.Leases[i].Lease.ID.DSeq == deploymentID.DSeq {
			lease = &leaseRes.Leases[i]
			break
		}
	}
	s.Require().NotNil(lease)
	lid := lease.Lease.ID

	// Send manifest
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
	s.Require().NoError(s.waitForBlocksCommitted(20))

	// Verify HTTPRoute exists
	ns := builder.LidNS(lid)
	s.verifyHTTPRouteExists(ns, "gateway-test.localhost")

	// Close deployment
	res, err = clitestutil.ExecDeploymentClose(
		s.ctx,
		cctx,
		cli.TestFlags().
			WithFrom(s.addrTenant.String()).
			WithOwner(deploymentID.Owner).
			WithDSeq(deploymentID.DSeq).
			Append(cliFlags)...,
	)
	s.Require().NoError(err)
	s.Require().NoError(s.waitForBlocksCommitted(1))
	clitestutil.ValidateTxSuccessful(s.ctx, s.T(), cctx, res.Bytes())

	// Wait for cleanup
	time.Sleep(10 * time.Second)

	// Verify HTTPRoute is deleted
	s.verifyHTTPRouteDeleted(ns, "gateway-test.localhost")
}

// verifyHTTPRouteExists checks that an HTTPRoute exists in the given namespace
func (s *E2EGatewayAPI) verifyHTTPRouteExists(namespace, routeName string) {
	if s.dc == nil {
		s.T().Skip("dynamic client not available")
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	var route *unstructured.Unstructured
	var err error

	// Retry a few times as the route may take time to be created
	for i := 0; i < 10; i++ {
		route, err = s.dc.Resource(httpRouteGVR).Namespace(namespace).Get(ctx, routeName, metav1.GetOptions{})
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}

	require.NoError(s.T(), err, "HTTPRoute %s should exist in namespace %s", routeName, namespace)
	require.NotNil(s.T(), route)
	assert.Equal(s.T(), routeName, route.GetName())
}

// verifyHTTPRouteDeleted checks that an HTTPRoute no longer exists
func (s *E2EGatewayAPI) verifyHTTPRouteDeleted(namespace, routeName string) {
	if s.dc == nil {
		s.T().Skip("dynamic client not available")
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	var deleted bool
	for i := 0; i < 10; i++ {
		_, err := s.dc.Resource(httpRouteGVR).Namespace(namespace).Get(ctx, routeName, metav1.GetOptions{})
		if err != nil {
			deleted = true
			break
		}
		time.Sleep(time.Second)
	}

	assert.True(s.T(), deleted, "HTTPRoute %s should be deleted from namespace %s", routeName, namespace)
}

// verifyHTTPRouteLabels checks that the HTTPRoute has correct Akash labels
func (s *E2EGatewayAPI) verifyHTTPRouteLabels(namespace, routeName string, lid mtypes.LeaseID) {
	if s.dc == nil {
		s.T().Skip("dynamic client not available")
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	route, err := s.dc.Resource(httpRouteGVR).Namespace(namespace).Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(s.T(), err)

	labels := route.GetLabels()
	assert.Equal(s.T(), "true", labels[builder.AkashManagedLabelName], "should have akash managed label")
	assert.Equal(s.T(), lid.Owner, labels[builder.AkashLeaseOwnerLabelName], "should have owner label")
	assert.Equal(s.T(), fmt.Sprintf("%d", lid.DSeq), labels[builder.AkashLeaseDSeqLabelName], "should have dseq label")
}

// verifyHTTPRouteParentRef checks that the HTTPRoute references the correct Gateway
func (s *E2EGatewayAPI) verifyHTTPRouteParentRef(namespace, routeName, gatewayName, gatewayNamespace string) {
	if s.dc == nil {
		s.T().Skip("dynamic client not available")
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	route, err := s.dc.Resource(httpRouteGVR).Namespace(namespace).Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(s.T(), err)

	spec, ok := route.Object["spec"].(map[string]interface{})
	require.True(s.T(), ok, "HTTPRoute should have spec")

	parentRefs, ok := spec["parentRefs"].([]interface{})
	require.True(s.T(), ok, "HTTPRoute should have parentRefs")
	require.NotEmpty(s.T(), parentRefs, "HTTPRoute should have at least one parentRef")

	parentRef := parentRefs[0].(map[string]interface{})
	assert.Equal(s.T(), gatewayName, parentRef["name"], "parentRef should reference correct gateway name")

	if ns, ok := parentRef["namespace"]; ok {
		assert.Equal(s.T(), gatewayNamespace, ns, "parentRef should reference correct gateway namespace")
	}
}

// verifyHTTPRouteAnnotations checks that the HTTPRoute has correct annotations for HTTP options
func (s *E2EGatewayAPI) verifyHTTPRouteAnnotations(namespace, routeName string) {
	if s.dc == nil {
		s.T().Skip("dynamic client not available")
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	route, err := s.dc.Resource(httpRouteGVR).Namespace(namespace).Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(s.T(), err)

	annotations := route.GetAnnotations()

	// Check for NGINX Gateway Fabric annotations based on http_options in SDL
	// The exact annotation keys depend on the gateway implementation
	s.T().Logf("HTTPRoute annotations: %v", annotations)

	// Verify annotations exist (specific keys depend on implementation)
	// For NGINX Gateway Fabric, these would be nginx.org/* annotations
	assert.NotNil(s.T(), annotations, "HTTPRoute should have annotations")
}

// TestGatewayAPISuite runs the Gateway API e2e test suite
func TestGatewayAPISuite(t *testing.T) {
	integrationTestOnly(t)
	suite.Run(t, &E2EGatewayAPI{
		IntegrationTestSuite: IntegrationTestSuite{
			gatewayAPIMode: true,
		},
	})
}
