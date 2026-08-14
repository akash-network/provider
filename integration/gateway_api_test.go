//go:build e2e

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

var snippetsFilterGVR = schema.GroupVersionResource{
	Group:    "gateway.nginx.org",
	Version:  "v1alpha1",
	Resource: "snippetsfilters",
}

var gatewayGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gateways",
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

	// Get bid for this specific deployment
	res, err = clitestutil.ExecQueryBids(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	bidsRes := &mvbeta.QueryBidsResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), bidsRes)
	s.Require().NoError(err)
	s.Require().NotEmpty(bidsRes.Bids, "expected at least one bid")

	// Find bid for this deployment's DSeq
	var targetBid *mvbeta.QueryBidResponse
	for i := range bidsRes.Bids {
		if bidsRes.Bids[i].Bid.ID.DSeq == deploymentID.DSeq {
			targetBid = &bidsRes.Bids[i]
			break
		}
	}
	s.Require().NotNil(targetBid, "expected bid for deployment DSeq %d", deploymentID.DSeq)

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

	// Get lease for this specific deployment
	res, err = clitestutil.ExecQueryLeases(s.ctx, cctx, cli.TestFlags().WithOutputJSON()...)
	s.Require().NoError(err)
	leaseRes := &mvbeta.QueryLeasesResponse{}
	err = s.validator.ClientCtx.Codec.UnmarshalJSON(res.Bytes(), leaseRes)
	s.Require().NoError(err)

	// Find lease for this deployment's DSeq
	var lease *mvbeta.QueryLeaseResponse
	for i := range leaseRes.Leases {
		if leaseRes.Leases[i].Lease.ID.DSeq == deploymentID.DSeq {
			lease = &leaseRes.Leases[i]
			break
		}
	}
	s.Require().NotNil(lease, "expected lease for deployment DSeq %d", deploymentID.DSeq)
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

	s.T().Run("HTTPRoute references SnippetsFilter with http_options", func(t *testing.T) {
		s.verifyHTTPRouteSnippet(ns, "gateway-test.localhost")
	})

	s.T().Run("SnippetsFilter accepted by NGF", func(t *testing.T) {
		s.waitConditionTrue(snippetsFilterGVR, ns, "gateway-test.localhost", "Accepted")
	})

	s.T().Run("HTTPRoute accepted and refs resolved", func(t *testing.T) {
		s.waitConditionTrue(httpRouteGVR, ns, "gateway-test.localhost", "Accepted")
		s.waitConditionTrue(httpRouteGVR, ns, "gateway-test.localhost", "ResolvedRefs")
	})

	s.T().Run("Gateway programmed", func(t *testing.T) {
		s.waitConditionTrue(gatewayGVR, "akash-gateway", "akash-gateway", "Programmed")
	})

	s.T().Run("body-size limit enforced end-to-end", func(t *testing.T) {
		s.verifyBodySizeLimit("gateway-test.localhost", 2097152)
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

// verifyHTTPRouteDeleted checks that an HTTPRoute no longer exists.
// Only a NotFound error indicates successful deletion; other errors (auth, network, etc.)
// are treated as failures to avoid false positives.
func (s *E2EGatewayAPI) verifyHTTPRouteDeleted(namespace, routeName string) {
	if s.dc == nil {
		s.T().Skip("dynamic client not available")
		return
	}

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	var lastErr error
	for i := 0; i < 10; i++ {
		_, err := s.dc.Resource(httpRouteGVR).Namespace(namespace).Get(ctx, routeName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Successfully confirmed deletion
				return
			}
			// Non-NotFound error - keep polling but record the error
			lastErr = err
		}
		time.Sleep(time.Second)
	}

	// If we got here, either the route still exists or we got non-NotFound errors
	if lastErr != nil {
		s.T().Errorf("HTTPRoute %s deletion check failed with error: %v", routeName, lastErr)
	} else {
		s.T().Errorf("HTTPRoute %s still exists in namespace %s after timeout", routeName, namespace)
	}
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

// verifyHTTPRouteSnippet checks that the HTTPRoute references a per-route
// SnippetsFilter through an ExtensionRef filter, and that the SnippetsFilter
// carries the SDL http_options as nginx directives. NGF applies http_options via
// SnippetsFilters, not nginx.org/* annotations. Directives from
// deployment-v2-gateway-api.yaml http_options (timeouts in milliseconds):
//   - max_body_size: 2097152 -> client_max_body_size 2097152;
//   - read_timeout: 60000 -> proxy_read_timeout 60000ms;
//   - send_timeout: 60000 -> proxy_send_timeout 60000ms;
//   - next_tries: 3 -> proxy_next_upstream_tries 3;
//   - next_timeout: 30000 -> proxy_next_upstream_timeout 30000ms;
func (s *E2EGatewayAPI) verifyHTTPRouteSnippet(namespace, routeName string) {
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
	rules, ok := spec["rules"].([]interface{})
	require.True(s.T(), ok, "HTTPRoute should have rules")
	require.NotEmpty(s.T(), rules)
	rule := rules[0].(map[string]interface{})
	filters, ok := rule["filters"].([]interface{})
	require.True(s.T(), ok, "rule should have an ExtensionRef filter")
	require.NotEmpty(s.T(), filters)

	filter := filters[0].(map[string]interface{})
	assert.Equal(s.T(), "ExtensionRef", filter["type"], "filter should be an ExtensionRef")
	extRef, ok := filter["extensionRef"].(map[string]interface{})
	require.True(s.T(), ok, "filter should have extensionRef")
	assert.Equal(s.T(), "gateway.nginx.org", extRef["group"])
	assert.Equal(s.T(), "SnippetsFilter", extRef["kind"])
	assert.Equal(s.T(), routeName, extRef["name"], "ExtensionRef should reference the route's SnippetsFilter")

	sf, err := s.dc.Resource(snippetsFilterGVR).Namespace(namespace).Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(s.T(), err, "SnippetsFilter %s should exist", routeName)

	sfSpec, ok := sf.Object["spec"].(map[string]interface{})
	require.True(s.T(), ok, "SnippetsFilter should have spec")
	snippets, ok := sfSpec["snippets"].([]interface{})
	require.True(s.T(), ok, "SnippetsFilter should have snippets")
	require.NotEmpty(s.T(), snippets)
	snippet := snippets[0].(map[string]interface{})
	assert.Equal(s.T(), "http.server.location", snippet["context"])

	value, ok := snippet["value"].(string)
	require.True(s.T(), ok, "snippet should have a value")
	s.T().Logf("SnippetsFilter value:\n%s", value)

	for _, want := range []string{
		"client_max_body_size 2097152;",
		"proxy_read_timeout 60000ms;",
		"proxy_send_timeout 60000ms;",
		"proxy_next_upstream_tries 3;",
		"proxy_next_upstream_timeout 30000ms;",
	} {
		assert.Contains(s.T(), value, want, "snippet should contain %q", want)
	}
}

// waitConditionTrue polls the resource until a status condition of the given type
// reports status True, wherever it appears: Gateway status.conditions, HTTPRoute
// status.parents[].conditions, or SnippetsFilter status.controllers[].conditions.
func (s *E2EGatewayAPI) waitConditionTrue(gvr schema.GroupVersionResource, namespace, name, condType string) {
	if s.dc == nil {
		s.T().Skip("dynamic client not available")
		return
	}
	require.Eventually(s.T(), func() bool {
		obj, err := s.dc.Resource(gvr).Namespace(namespace).Get(s.ctx, name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		return statusConditionTrue(obj, condType)
	}, 90*time.Second, 2*time.Second, "%s %q should report %s=True", gvr.Resource, name, condType)
}

// statusConditionTrue reports whether any condition of condType with status "True"
// appears anywhere under the object's status.
func statusConditionTrue(obj *unstructured.Unstructured, condType string) bool {
	status, ok := obj.Object["status"]
	if !ok {
		return false
	}
	return anyConditionTrue(status, condType)
}

func anyConditionTrue(v interface{}, condType string) bool {
	switch t := v.(type) {
	case map[string]interface{}:
		if conds, ok := t["conditions"].([]interface{}); ok {
			for _, c := range conds {
				cm, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				if ct, _ := cm["type"].(string); ct == condType {
					if cs, _ := cm["status"].(string); cs == "True" {
						return true
					}
				}
			}
		}
		for _, val := range t {
			if anyConditionTrue(val, condType) {
				return true
			}
		}
	case []interface{}:
		for _, val := range t {
			if anyConditionTrue(val, condType) {
				return true
			}
		}
	}
	return false
}

// verifyBodySizeLimit drives the deployed route through the gateway data plane and
// asserts client_max_body_size is enforced: a request body over the limit is
// rejected with 413 while the route otherwise serves 200. This exercises a real
// http_option end to end, not just the rendered directive.
func (s *E2EGatewayAPI) verifyBodySizeLimit(hostname string, maxBodySize int) {
	host, port := appEnv(s.T())
	appURL := fmt.Sprintf("http://%s:%s/", host, port)
	client := &http.Client{Timeout: 5 * time.Second}

	// Wait for the data plane to program the route and serve it.
	require.Eventually(s.T(), func() bool {
		req, err := http.NewRequest(http.MethodGet, appURL, nil)
		if err != nil {
			return false
		}
		req.Host = hostname
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 120*time.Second, 2*time.Second, "route %s should serve 200 through the gateway", hostname)

	// A body over max_body_size must be rejected by nginx before it reaches the backend.
	oversized := bytes.Repeat([]byte("a"), maxBodySize+1024)
	req, err := http.NewRequest(http.MethodPost, appURL, bytes.NewReader(oversized))
	require.NoError(s.T(), err)
	req.Host = hostname
	resp, err := client.Do(req)
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	assert.Equal(s.T(), http.StatusRequestEntityTooLarge, resp.StatusCode,
		"a body over max_body_size (%d) must be rejected with 413", maxBodySize)
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
