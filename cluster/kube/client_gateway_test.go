package kube

import (
	"testing"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/akash-network/provider/cluster/kube/gateway"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	mtypes "pkg.akt.dev/go/node/market/v1"
)

func TestNginxGatewayHTTPRouteSpec(t *testing.T) {
	impl := gateway.NewNginxGateway(log.NewNopLogger())

	directive := chostname.ConnectToDeploymentDirective{
		Hostname:    "test.example.com",
		LeaseID:     mtypes.LeaseID{},
		ServiceName: "test-service",
		ServicePort: 8080,
		ReadTimeout: 60000,
		SendTimeout: 30000,
	}

	spec := impl.BuildHTTPRouteSpec(
		"test-gateway",
		"test-namespace",
		"test.example.com",
		"test-service",
		8080,
		directive,
	)

	require.Len(t, spec.ParentRefs, 1)
	require.Equal(t, gatewayv1.ObjectName("test-gateway"), spec.ParentRefs[0].Name)
	require.Equal(t, gatewayv1.Namespace("test-namespace"), *spec.ParentRefs[0].Namespace)

	require.Len(t, spec.Hostnames, 1)
	require.Equal(t, gatewayv1.Hostname("test.example.com"), spec.Hostnames[0])

	require.Len(t, spec.Rules, 1)
	rule := spec.Rules[0]

	// NGF does not support timeouts in the HTTPRoute spec. http_options are applied
	// via a per-route SnippetsFilter referenced by an ExtensionRef filter instead.
	require.Nil(t, rule.Timeouts)
	require.Len(t, rule.Filters, 1)
	require.Equal(t, gatewayv1.HTTPRouteFilterExtensionRef, rule.Filters[0].Type)
	require.NotNil(t, rule.Filters[0].ExtensionRef)
	require.Equal(t, gatewayv1.Kind("SnippetsFilter"), rule.Filters[0].ExtensionRef.Kind)
	require.Equal(t, gatewayv1.ObjectName("test.example.com"), rule.Filters[0].ExtensionRef.Name)

	require.Len(t, rule.Matches, 1)
	match := rule.Matches[0]
	require.NotNil(t, match.Path)
	require.Equal(t, gatewayv1.PathMatchPathPrefix, *match.Path.Type)
	require.Equal(t, "/", *match.Path.Value)

	require.Len(t, rule.BackendRefs, 1)
	require.Equal(t, gatewayv1.ObjectName("test-service"), rule.BackendRefs[0].Name)
	require.Equal(t, gatewayv1.PortNumber(8080), *rule.BackendRefs[0].Port)
}
