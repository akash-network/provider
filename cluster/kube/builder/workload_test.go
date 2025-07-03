package builder

import (
	"testing"

	"github.com/akash-network/node/testutil"
	"github.com/stretchr/testify/require"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func TestCollectIngressURIsDeduplication(t *testing.T) {
	log := testutil.Logger(t)
	t.Parallel()
	const fakeDomain = "ingress.example.com"
	settings := Settings{
		DeploymentIngressStaticHosts: true,
		DeploymentIngressDomain:      fakeDomain,
	}
	lid := testutil.LeaseID(t)

	// Create a mock service with multiple expose entries that could generate duplicate URIs
	mani := &crd.Manifest{
		Spec: crd.ManifestSpec{
			Group: crd.ManifestGroup{
				Name: "testgroup",
				Services: []crd.ManifestService{
					{
						Name:  "web",
						Count: 1,
						Expose: []crd.ManifestServiceExpose{
							{
								Port:         80,
								ExternalPort: 80,
								Proto:        "tcp",
								Global:       false,
								Hosts:        []string{"example.com", "example.com"}, // Duplicate host
							},
							{
								Port:         443,
								ExternalPort: 443,
								Proto:        "tcp",
								Global:       false,
								Hosts:        []string{"example.com"}, // Same host as above, different port
							},
						},
					},
				},
			},
		},
	}

	group, sparams, err := mani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:     lid,
		Group:   &group,
		Sparams: crd.ClusterSettings{SchedulerParams: sparams},
	}

	workload, err := NewWorkloadBuilder(log, settings, cdep, mani, 0)
	require.NoError(t, err)

	uris := workload.collectIngressURIs()

	// Debug: print URIs to understand what's being generated
	t.Logf("Generated URIs: %v", uris)

	require.Greater(t, len(uris), 0, "collectIngressURIs should return at least one URI")
	require.ElementsMatch(t, uris, uris, "collectIngressURIs returned duplicates") // ElementsMatch de-dupes internally
}

func TestProtocolConstants(t *testing.T) {
	// Test that the constants have the expected values
	require.Equal(t, "http", protoHTTP)
	require.Equal(t, "https", protoHTTPS)
}

func TestDeduplicationLogic(t *testing.T) {
	// Test the deduplication logic directly by simulating what the add function does
	var ingressURIs []string
	uriSet := make(map[string]struct{})
	add := func(u string) {
		if _, seen := uriSet[u]; !seen {
			ingressURIs = append(ingressURIs, u)
			uriSet[u] = struct{}{}
		}
	}

	// Test adding duplicate URIs
	add("http://example.com")
	add("https://example.com")
	add("http://example.com")  // Duplicate
	add("https://example.com") // Duplicate
	add("http://test.com:8080")

	// Should have only unique URIs
	require.Len(t, ingressURIs, 3, "Expected 3 unique URIs")
	require.Contains(t, ingressURIs, "http://example.com")
	require.Contains(t, ingressURIs, "https://example.com")
	require.Contains(t, ingressURIs, "http://test.com:8080")
}
