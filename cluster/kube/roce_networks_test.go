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
	"k8s.io/client-go/kubernetes/fake"

	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
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

// Exercise Deploy itself: independently testing NAD lookup and podAnnotations
// misses a broken connection between those helpers and the submitted workloads.
// Real API-server version/recovery behavior is covered by the integration test.
func TestDeployConnectsRoCENetworksToWorkloads(t *testing.T) {
	for _, tt := range []struct {
		name      string
		fabric    string
		rails     bool
		wantError bool
		want      string
	}{
		{name: "roce", fabric: "roce", rails: true, want: "akash-rails/rail0,akash-rails/rail1"},
		{name: "infiniband", fabric: "infiniband", rails: true},
		{name: "missing rails", fabric: "roce", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var objects []*unstructured.Unstructured
			if tt.rails {
				objects = []*unstructured.Unstructured{nad("akash-rails", "rail1"), nad("akash-rails", "rail0")}
			}
			c := nadFakeClient(t, objects...)
			c.kc = fake.NewClientset()
			c.ac = akashfake.NewSimpleClientset()
			c.ns = "lease"
			settings := builder.NewDefaultSettings()
			settings.InterconnectRoCENetworksNamespace = "akash-rails"
			ctx := context.WithValue(context.Background(), builder.SettingsKey, settings)

			data, err := sdl.ReadFile("../../testdata/deployment/deployment-v2-storage-default.yaml")
			require.NoError(t, err)
			manifest, err := data.Manifest()
			require.NoError(t, err)
			group := manifest.GetGroups()[0]
			params := make(crd.ReservationClusterSettings)
			for i := range group.Services {
				service := &group.Services[i]
				service.Expose = nil
				service.Resources.Endpoints = nil
				service.Resources.GPU.Units = rtypes.NewResourceValue(1)
				params[service.Resources.ID] = &crd.SchedulerParams{
					RuntimeClass: "nvidia",
					Resources: &crd.SchedulerResources{
						GPU: &crd.SchedulerResourceGPU{Vendor: "nvidia", Model: "a100"},
						Interconnect: &crd.SchedulerResourceInterconnect{
							Enabled: true, Units: 1, ResourceName: "rdma/rdma_shared_device_ib",
							Fabric: tt.fabric, NCCLHCAPrefixes: []string{"mlx5"},
						},
					},
				}
			}
			lid := testutil.LeaseID(t)
			err = c.Deploy(ctx, &ctypes.Deployment{Lid: lid, MGroup: &group, CParams: params})
			if tt.wantError {
				require.ErrorIs(t, err, kubeclienterrors.ErrNoRoCERailNetworks)
				deployments, listErr := c.kc.AppsV1().Deployments(builder.LidNS(lid)).List(ctx, metav1.ListOptions{})
				require.NoError(t, listErr)
				require.Empty(t, deployments.Items)
				statefulSets, listErr := c.kc.AppsV1().StatefulSets(builder.LidNS(lid)).List(ctx, metav1.ListOptions{})
				require.NoError(t, listErr)
				require.Empty(t, statefulSets.Items)
				return
			}
			require.NoError(t, err)
			for recovery := 0; recovery <= 2; recovery++ {
				if recovery > 0 {
					c = &client{kc: c.kc, ac: c.ac, dc: c.dc, ns: c.ns, log: testutil.Logger(t)}
					deployments, err := c.Deployments(ctx)
					require.NoError(t, err)
					require.Len(t, deployments, 1)
					require.NoError(t, c.Deploy(ctx, deployments[0]))
				}
				deployment, err := c.kc.AppsV1().Deployments(builder.LidNS(lid)).Get(ctx, "bew", metav1.GetOptions{})
				require.NoError(t, err)
				statefulSet, err := c.kc.AppsV1().StatefulSets(builder.LidNS(lid)).Get(ctx, "web", metav1.GetOptions{})
				require.NoError(t, err)
				for _, annotations := range []map[string]string{deployment.Spec.Template.Annotations, statefulSet.Spec.Template.Annotations} {
					require.Equal(t, tt.want, annotations["k8s.v1.cni.cncf.io/networks"], "recovery %d", recovery)
				}
			}
		})
	}
}
