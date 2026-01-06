package kube

import (
	"testing"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/assert"
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

	// Verify parent refs
	require.Len(t, spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("test-gateway"), spec.ParentRefs[0].Name)
	assert.Equal(t, gatewayv1.Namespace("test-namespace"), *spec.ParentRefs[0].Namespace)

	// Verify hostnames
	require.Len(t, spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("test.example.com"), spec.Hostnames[0])

	// Verify rules
	require.Len(t, spec.Rules, 1)
	rule := spec.Rules[0]

	// Verify that timeouts are NOT in the spec (they're in annotations instead)
	assert.Nil(t, rule.Timeouts, "Timeouts should not be in HTTPRoute spec (not supported by NGINX Gateway Fabric)")

	// Verify path matching
	require.Len(t, rule.Matches, 1)
	match := rule.Matches[0]
	require.NotNil(t, match.Path)
	assert.Equal(t, gatewayv1.PathMatchPathPrefix, *match.Path.Type)
	assert.Equal(t, "/", *match.Path.Value)

	// Verify backend refs
	require.Len(t, rule.BackendRefs, 1)
	backendRef := rule.BackendRefs[0]
	assert.Equal(t, gatewayv1.ObjectName("test-service"), backendRef.Name)
	assert.Equal(t, gatewayv1.PortNumber(8080), *backendRef.Port)
}

func TestNginxGatewayAnnotations(t *testing.T) {
	impl := gateway.NewNginxGateway(log.NewNopLogger())

	directive := chostname.ConnectToDeploymentDirective{
		Hostname:    "test.example.com",
		LeaseID:     mtypes.LeaseID{},
		ServiceName: "test-service",
		ServicePort: 8080,
		ReadTimeout: 60000,
		SendTimeout: 60000,
		NextTimeout: 30000,
		MaxBodySize: 1048576,
		NextTries:   3,
		NextCases:   []string{"error", "timeout"},
	}

	annotations := impl.BuildAnnotations(directive)

	// Verify NGINX-specific annotations
	assert.Equal(t, "1048576", annotations["nginx.org/client-max-body-size"])
	assert.Equal(t, "60", annotations["nginx.org/proxy-read-timeout"])
	assert.Equal(t, "60", annotations["nginx.org/proxy-send-timeout"])
	assert.Equal(t, "30s", annotations["nginx.org/proxy-next-upstream-timeout"])
	assert.Equal(t, "3", annotations["nginx.org/proxy-next-upstream-tries"])
	assert.Equal(t, "error timeout", annotations["nginx.org/proxy-next-upstream"])
}

func TestNginxGatewayAnnotationsWithHTTPCodes(t *testing.T) {
	impl := gateway.NewNginxGateway(log.NewNopLogger())

	directive := chostname.ConnectToDeploymentDirective{
		Hostname:    "test.example.com",
		LeaseID:     mtypes.LeaseID{},
		ServiceName: "test-service",
		ServicePort: 8080,
		ReadTimeout: 30000,
		SendTimeout: 30000,
		NextTimeout: 0,
		MaxBodySize: 2097152,
		NextTries:   5,
		NextCases:   []string{"error", "502", "503", "504"},
	}

	annotations := impl.BuildAnnotations(directive)

	assert.Equal(t, "2097152", annotations["nginx.org/client-max-body-size"])
	assert.Equal(t, "5", annotations["nginx.org/proxy-next-upstream-tries"])
	assert.Equal(t, "error http_502 http_503 http_504", annotations["nginx.org/proxy-next-upstream"])

	_, hasNextTimeout := annotations["nginx.org/proxy-next-upstream-timeout"]
	assert.False(t, hasNextTimeout, "Should not set next-upstream-timeout when NextTimeout is 0")
}

func TestNginxGatewayAnnotationsMinimal(t *testing.T) {
	impl := gateway.NewNginxGateway(log.NewNopLogger())

	directive := chostname.ConnectToDeploymentDirective{
		Hostname:    "test.example.com",
		LeaseID:     mtypes.LeaseID{},
		ServiceName: "test-service",
		ServicePort: 8080,
		ReadTimeout: 10000,
		SendTimeout: 10000,
		NextTimeout: 0,
		MaxBodySize: 1024,
		NextTries:   1,
		NextCases:   []string{},
	}

	annotations := impl.BuildAnnotations(directive)

	assert.Equal(t, "1024", annotations["nginx.org/client-max-body-size"])
	assert.Equal(t, "1", annotations["nginx.org/proxy-next-upstream-tries"])

	_, hasNextUpstream := annotations["nginx.org/proxy-next-upstream"]
	assert.False(t, hasNextUpstream, "Should not set proxy-next-upstream when NextCases is empty")
}

func TestNginxGatewayValidateOptions(t *testing.T) {
	impl := gateway.NewNginxGateway(log.NewNopLogger())

	directive := chostname.ConnectToDeploymentDirective{
		Hostname:    "test.example.com",
		ServiceName: "test-service",
		ServicePort: 8080,
		ReadTimeout: 60000,
		SendTimeout: 30000,
		MaxBodySize: 1048576,
	}

	warnings := impl.ValidateOptions(directive)

	// NGINX Gateway Fabric supports all current directive options
	assert.Empty(t, warnings, "NGINX implementation should not have any warnings for supported options")
}
