package kube

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func interconnectSparams(fabric string) crd.ClusterSettings {
	return crd.ClusterSettings{
		SchedulerParams: []*crd.SchedulerParams{
			nil, // non-interconnect service
			{
				Resources: &crd.SchedulerResources{
					Interconnect: &crd.SchedulerResourceInterconnect{
						Enabled:      true,
						Units:        1,
						ResourceName: "rdma/rdma_shared_device_ib",
						Fabric:       fabric,
					},
				},
			},
		},
	}
}

func TestDeploymentNeedsRoCENetworks(t *testing.T) {
	require.True(t, deploymentNeedsRoCENetworks(&builder.ClusterDeployment{
		Sparams: interconnectSparams(builder.InterconnectFabricRoCE),
	}))

	require.False(t, deploymentNeedsRoCENetworks(&builder.ClusterDeployment{
		Sparams: interconnectSparams("infiniband"),
	}), "InfiniBand pins must not trigger NAD attachment")

	require.False(t, deploymentNeedsRoCENetworks(&builder.ClusterDeployment{
		Sparams: crd.ClusterSettings{SchedulerParams: []*crd.SchedulerParams{nil, nil}},
	}), "deployments without interconnect pins must not trigger NAD attachment")
}

func nad(namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("k8s.cni.cncf.io/v1")
	obj.SetKind("NetworkAttachmentDefinition")
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

// nadFakeClient builds a client over a fake dynamic clientset holding the
// given NADs. Objects are created through the fake (not seeded via the
// tracker) because the tracker derives resource names by naive
// pluralization, which mangles multus's dashed
// "network-attachment-definitions".
func nadFakeClient(t *testing.T, objects ...*unstructured.Unstructured) *client {
	t.Helper()

	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{nadGVR: "NetworkAttachmentDefinitionList"},
	)

	for _, obj := range objects {
		_, err := dc.Resource(nadGVR).Namespace(obj.GetNamespace()).Create(context.Background(), obj, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	return &client{
		dc:  dc,
		log: testutil.Logger(t),
	}
}

func TestInterconnectRoCENetworksSortedJoin(t *testing.T) {
	c := nadFakeClient(t,
		nad("akash-rails", "rail1"),
		nad("akash-rails", "rail0"),
		nad("akash-rails", "rail2"),
		nad("elsewhere", "rail9"), // other namespaces are ignored
	)

	networks, err := c.interconnectRoCENetworks(context.Background(), "akash-rails")
	require.NoError(t, err)
	require.Equal(t, "akash-rails/rail0,akash-rails/rail1,akash-rails/rail2", networks)
}

func TestInterconnectRoCENetworksEmptyNamespace(t *testing.T) {
	c := nadFakeClient(t, nad("akash-rails", "rail0"))

	networks, err := c.interconnectRoCENetworks(context.Background(), "")
	require.NoError(t, err)
	require.Empty(t, networks, "empty namespace disables attachment without an API call")
}

func TestInterconnectRoCENetworksNoNADsFailsDeploy(t *testing.T) {
	c := nadFakeClient(t)

	networks, err := c.interconnectRoCENetworks(context.Background(), "akash-rails")
	require.ErrorIs(t, err, kubeclienterrors.ErrNoRoCERailNetworks,
		"a configured rails namespace with no NADs must fail the deploy — the pods could not do RDMA")
	require.ErrorContains(t, err, "akash-rails")
	require.Empty(t, networks)
}
