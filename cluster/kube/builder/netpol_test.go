package builder

import (
	"testing"

	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func netPolDeployment(t *testing.T) *ClusterDeployment {
	t.Helper()

	mani := &crd.Manifest{Spec: crd.ManifestSpec{Group: crd.ManifestGroup{
		Name:     "testgroup",
		Services: []crd.ManifestService{{Name: "web"}},
	}}}
	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	return &ClusterDeployment{
		Lid:     testutil.LeaseID(t),
		Group:   &group,
		Sparams: crd.ClusterSettings{SchedulerParams: sparams},
	}
}

// TestNetPolAllowsGatewayNamespace asserts tenant ingress is allowed from the
// Gateway's namespace, where the gateway data plane runs, and no longer from an
// ingress-nginx controller.
func TestNetPolAllowsGatewayNamespace(t *testing.T) {
	settings := NewDefaultSettings()
	settings.NetworkPoliciesEnabled = true
	settings.GatewayNamespace = "akash-gateway"

	pols, err := BuildNetPol(settings, netPolDeployment(t)).Create()
	require.NoError(t, err)
	require.NotEmpty(t, pols)

	var gatewayAllowed bool
	for _, rule := range pols[0].Spec.Ingress {
		for _, peer := range rule.From {
			if peer.NamespaceSelector == nil {
				continue
			}
			labels := peer.NamespaceSelector.MatchLabels
			require.NotEqual(t, "ingress-nginx", labels["app.kubernetes.io/name"], "no ingress-nginx peer")
			if labels["kubernetes.io/metadata.name"] == "akash-gateway" {
				gatewayAllowed = true
			}
		}
	}
	require.True(t, gatewayAllowed, "gateway namespace must be allowed")
}
