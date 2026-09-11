package kube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
	mani "pkg.akt.dev/go/manifest/v2beta3"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
)

// Add valid feature fixtures to testdata/deployment; every YAML runs here.
// Compare complete pod templates, without an allowlist of fields or
// features. Fake clients let this run in normal CI for GPU/TEE/RDMA workloads;
// recovery_integration_test.go separately checks API defaulting and live pods.
func TestDeploymentRecoveryAcrossFeatures(t *testing.T) {
	paths, err := filepath.Glob("../../testdata/deployment/*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			for _, config := range []struct {
				name                string
				runtimeClass        ctypes.RuntimeClass
				gpu                 string
				fabric              string
				tee                 string
				attestationDisabled bool
			}{
				{name: "default"},
				{name: "nvidia", runtimeClass: "nvidia", gpu: "nvidia"},
				{name: "amd", gpu: "amd"},
				{name: "infiniband", runtimeClass: "nvidia", gpu: "nvidia", fabric: "infiniband"},
				{name: "roce", runtimeClass: "nvidia", gpu: "nvidia", fabric: "roce"},
				{name: "snp", runtimeClass: ctypes.RuntimeClassKataQemuSNP, tee: "cpu"},
				{name: "tdx", runtimeClass: ctypes.RuntimeClassKataQemuTDX, tee: "cpu"},
				{name: "snp_gpu", runtimeClass: ctypes.RuntimeClassKataQemuNvidiaGPUSNP, gpu: "nvidia", tee: "cpu-gpu"},
				{name: "tdx_gpu", runtimeClass: ctypes.RuntimeClassKataQemuNvidiaGPUTDX, gpu: "nvidia", tee: "cpu-gpu"},
				{name: "attestation_disabled", runtimeClass: ctypes.RuntimeClassKataQemuSNP, tee: "cpu", attestationDisabled: true},
			} {
				t.Run(config.name, func(t *testing.T) {
					data, err := sdl.ReadFile(path)
					require.NoError(t, err)
					manifest, err := data.Manifest()
					require.NoError(t, err)
					require.NotEmpty(t, manifest.GetGroups())
					for _, group := range manifest.GetGroups() {
						t.Run(group.Name, func(t *testing.T) {
							params := make(crd.ReservationClusterSettings)
							for i := range group.Services {
								service := &group.Services[i]
								var sp *crd.SchedulerParams
								if config.name != "default" {
									sp = &crd.SchedulerParams{
										RuntimeClass: config.runtimeClass, TEEType: config.tee,
										AttestationDisabled: config.attestationDisabled,
									}
								}
								if config.gpu != "" {
									service.Resources.GPU.Units = rtypes.NewResourceValue(1)
									sp.Resources = &crd.SchedulerResources{
										GPU: &crd.SchedulerResourceGPU{Vendor: config.gpu, Model: "test-gpu", MemorySize: "80Gi", Interface: "pcie"},
									}
								}
								if config.fabric != "" {
									sp.Resources.Interconnect = &crd.SchedulerResourceInterconnect{
										Enabled: true, Units: 1, ResourceName: "rdma/rdma_shared_device_ib",
										Fabric: config.fabric, NCCLHCAPrefixes: []string{"mlx5", "bnxt_re"},
									}
								}
								params[service.Resources.ID] = sp
							}
							checkDeploymentRecovery(t, group, params)
						})
					}
				})
			}
		})
	}
}

func checkDeploymentRecovery(t *testing.T, group mani.Group, params crd.ReservationClusterSettings) {
	t.Helper()
	c := nadFakeClient(t, nad("akash-rails", "rail1"), nad("akash-rails", "rail0"))
	c.kc = fake.NewClientset()
	c.ac = akashfake.NewSimpleClientset()
	c.ns = "lease"
	settings := builder.NewDefaultSettings()
	settings.InterconnectRoCENetworksNamespace = "akash-rails"
	ctx := context.WithValue(context.Background(), builder.SettingsKey, settings)
	lid := testutil.LeaseID(t)
	ns := builder.LidNS(lid)
	tenantDeploy := func() {
		require.NoError(t, c.Deploy(ctx, &ctypes.Deployment{Lid: lid, MGroup: &group, CParams: params}))
	}
	tenantDeploy()
	if exportRecoverySnapshot(t, ctx, c, ns, settings) {
		return
	}
	checkRecoveredDeployment(t, ctx, c, &ctypes.Deployment{Lid: lid, MGroup: &group, CParams: params})
}

func checkRecoveredDeployment(t *testing.T, ctx context.Context, c *client, deployment ctypes.IDeployment) {
	t.Helper()
	lid := deployment.LeaseID()
	ns := builder.LidNS(lid)
	group := *deployment.ManifestGroup()
	tenantDeploy := func() {
		require.NoError(t, c.Deploy(ctx, &ctypes.Deployment{Lid: lid, MGroup: &group, CParams: deployment.ClusterParams()}))
	}

	type workloadState struct {
		Template corev1.PodTemplateSpec
		Replicas *int32
	}
	type workloadSpecs struct {
		Deployments  map[string]workloadState
		StatefulSets map[string]workloadState
		Manifest     crd.ManifestSpec
	}
	readSpecs := func() workloadSpecs {
		deployments, err := c.kc.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		require.NoError(t, err)
		statefulSets, err := c.kc.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
		require.NoError(t, err)
		require.Equal(t, len(group.Services), len(deployments.Items)+len(statefulSets.Items))
		m, err := c.ac.AkashV2beta2().Manifests(c.ns).Get(ctx, ns, metav1.GetOptions{})
		require.NoError(t, err)
		state := workloadSpecs{
			Deployments: make(map[string]workloadState), StatefulSets: make(map[string]workloadState), Manifest: m.Spec,
		}
		// Controller bookkeeping such as revisionHistoryLimit does not roll
		// pods. Compare every template field, workload kind and replica count.
		for _, d := range deployments.Items {
			state.Deployments[d.Name] = workloadState{Template: d.Spec.Template, Replicas: d.Spec.Replicas}
		}
		for _, s := range statefulSets.Items {
			state.StatefulSets[s.Name] = workloadState{Template: s.Spec.Template, Replicas: s.Spec.Replicas}
		}
		// Match the API representation. Fake clients retain internal quantity
		// string caches, which may change without changing serialized resources.
		// Round-trip every field instead of masking selected template fields.
		wire, err := json.Marshal(state)
		require.NoError(t, err)
		state = workloadSpecs{}
		require.NoError(t, json.Unmarshal(wire, &state))
		return state
	}
	checkRecovery := func() {
		before := readSpecs()
		for recovery := 1; recovery <= 3; recovery++ {
			c = &client{kc: c.kc, ac: c.ac, dc: c.dc, ns: c.ns, log: testutil.Logger(t)}
			recovered, err := c.Deployments(ctx)
			require.NoError(t, err)
			require.Len(t, recovered, 1)
			require.NoError(t, c.Deploy(ctx, recovered[0]))
			require.Equal(t, before, readSpecs(), "recovery %d changed an unchanged workload", recovery)
		}
	}
	checkRecovery()
	before := readSpecs()
	tenantDeploy()
	require.Equal(t, before, readSpecs(), "resending a manifest must preserve every workload spec")

	// A real update must reach the intended workload, leaving other services
	// alone. Freezing all updates would make a no-restart-only test pass.
	service := &group.Services[0]
	service.Env = append(service.Env, "RECOVERY_UPDATE=applied")
	tenantDeploy()
	after := readSpecs()
	want := corev1.EnvVar{Name: "RECOVERY_UPDATE", Value: "applied"}
	for name, d := range after.Deployments {
		if name == service.Name {
			require.NotEqual(t, before.Deployments[name].Template, d.Template)
			require.Contains(t, d.Template.Spec.Containers[0].Env, want)
		} else {
			require.Equal(t, before.Deployments[name], d, "updating %s changed unrelated service %s", service.Name, name)
		}
	}
	for name, s := range after.StatefulSets {
		if name == service.Name {
			require.NotEqual(t, before.StatefulSets[name].Template, s.Template)
			require.Contains(t, s.Template.Spec.Containers[0].Env, want)
		} else {
			require.Equal(t, before.StatefulSets[name], s, "updating %s changed unrelated service %s", service.Name, name)
		}
	}
	checkRecovery()
}

// These snapshots are transient CI artifacts produced by the BASE revision.
// The candidate must recover them without changing any pod template, so an
// unconditional new default cannot hide by affecting both Create and Update.
type recoverySnapshot struct {
	Name         string
	Settings     builder.Settings
	Defaults     builder.Settings
	Namespace    corev1.Namespace
	Manifest     crd.Manifest
	Deployments  []appsv1.Deployment
	StatefulSets []appsv1.StatefulSet
	Networks     []unstructured.Unstructured
}

func exportRecoverySnapshot(t *testing.T, ctx context.Context, c *client, ns string, settings builder.Settings) bool {
	t.Helper()
	dir := os.Getenv("PROVIDER_RECOVERY_EXPORT_DIR")
	if dir == "" {
		return false
	}
	namespace, err := c.kc.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	require.NoError(t, err)
	manifest, err := c.ac.AkashV2beta2().Manifests(c.ns).Get(ctx, ns, metav1.GetOptions{})
	require.NoError(t, err)
	deployments, err := c.kc.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	statefulSets, err := c.kc.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	networks, err := c.dc.Resource(nadGVR).Namespace(settings.InterconnectRoCENetworksNamespace).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	data, err := json.Marshal(recoverySnapshot{
		Name: t.Name(), Settings: settings, Defaults: builder.NewDefaultSettings(), Namespace: *namespace, Manifest: *manifest,
		Deployments: deployments.Items, StatefulSets: statefulSets.Items, Networks: networks.Items,
	})
	require.NoError(t, err)
	file, err := os.CreateTemp(dir, "recovery-*.json")
	require.NoError(t, err)
	_, err = file.Write(data)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	return true
}
