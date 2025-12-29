package kube

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	mtypes "pkg.akt.dev/go/node/market/v1"
)

func TestHTTPRouteRules(t *testing.T) {
	serviceName := "test-service"
	servicePort := int32(8080)

	rules := httpRouteRules(serviceName, servicePort)

	require.Len(t, rules, 1)
	rule := rules[0]

	require.Len(t, rule.Matches, 1)
	match := rule.Matches[0]
	require.NotNil(t, match.Path)
	assert.Equal(t, gatewayv1.PathMatchPathPrefix, *match.Path.Type)
	assert.Equal(t, "/", *match.Path.Value)

	require.Len(t, rule.BackendRefs, 1)
	backendRef := rule.BackendRefs[0]
	assert.Equal(t, gatewayv1.ObjectName(serviceName), backendRef.Name)
	assert.Equal(t, gatewayv1.PortNumber(servicePort), *backendRef.Port)
}

func TestGatewayAPIAnnotations(t *testing.T) {
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

	annotations := gatewayAPIAnnotations(directive)

	assert.Equal(t, "1048576", annotations["nginx.org/client-max-body-size"])
	assert.Equal(t, "60s", annotations["nginx.org/proxy-read-timeout"])
	assert.Equal(t, "60s", annotations["nginx.org/proxy-send-timeout"])
	assert.Equal(t, "30s", annotations["nginx.org/proxy-next-upstream-timeout"])
	assert.Equal(t, "3", annotations["nginx.org/proxy-next-upstream-tries"])
	assert.Equal(t, "error timeout", annotations["nginx.org/proxy-next-upstream"])
}

func TestGatewayAPIAnnotationsWithHTTPCodes(t *testing.T) {
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

	annotations := gatewayAPIAnnotations(directive)

	assert.Equal(t, "2097152", annotations["nginx.org/client-max-body-size"])
	assert.Equal(t, "30s", annotations["nginx.org/proxy-read-timeout"])
	assert.Equal(t, "30s", annotations["nginx.org/proxy-send-timeout"])
	assert.Equal(t, "5", annotations["nginx.org/proxy-next-upstream-tries"])
	assert.Equal(t, "error http_502 http_503 http_504", annotations["nginx.org/proxy-next-upstream"])

	_, hasNextTimeout := annotations["nginx.org/proxy-next-upstream-timeout"]
	assert.False(t, hasNextTimeout, "Should not set next-upstream-timeout when NextTimeout is 0")
}

func TestGatewayAPIAnnotationsMinimal(t *testing.T) {
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

	annotations := gatewayAPIAnnotations(directive)

	assert.Equal(t, "1024", annotations["nginx.org/client-max-body-size"])
	assert.Equal(t, "10s", annotations["nginx.org/proxy-read-timeout"])
	assert.Equal(t, "10s", annotations["nginx.org/proxy-send-timeout"])
	assert.Equal(t, "1", annotations["nginx.org/proxy-next-upstream-tries"])

	_, hasNextUpstream := annotations["nginx.org/proxy-next-upstream"]
	assert.False(t, hasNextUpstream, "Should not set proxy-next-upstream when NextCases is empty")
}

func TestStrPtr(t *testing.T) {
	s := "test-string"
	ptr := strPtr(s)
	require.NotNil(t, ptr)
	assert.Equal(t, s, *ptr)
}
