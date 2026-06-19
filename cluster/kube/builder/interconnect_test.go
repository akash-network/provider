package builder

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

// stampinterconnect mutates the workload's per-service state to simulate the
// post-reservation pin: the SchedulerParams that tryAdjustInterconnect stamps and
// the InterconnectGroup label that the SDL parser preserves on
// `mani.Service.InterconnectGroup`. We poke directly instead of going through SDL
// fixtures so we can drive every (with/without group, with/without env
// override) permutation without proliferating yaml files.
//
// NewWorkloadBuilder takes a fresh FromCRD() of the manifest group, so
// `b.group` and `b.deployment.ManifestGroup()` are independent copies.
// container()/affinity() read b.group; labels() reads
// b.deployment.ManifestGroup(). Mutate both so the test exercises the
// real call paths.
func stampinterconnect(b *Workload, group string, sparams *crd.SchedulerParams) {
	b.sparams[b.serviceIdx] = sparams
	b.group.Services[b.serviceIdx].InterconnectGroup = group
	b.deployment.ManifestGroup().Services[b.serviceIdx].InterconnectGroup = group
}

func interconnectParams() *crd.SchedulerParams {
	return interconnectParamsWith("infiniband", []string{"mlx5"})
}

// interconnectParamsWith constructs scheduler params for a given fabric +
// HCA prefix list, so individual tests (e.g. RoCE GID injection) can
// override the defaults without forking the whole helper.
func interconnectParamsWith(fabric string, prefixes []string) *crd.SchedulerParams {
	resourceName := "rdma/rdma_shared_device_ib"
	if fabric == "roce" {
		resourceName = "rdma/rdma_shared_device_eth"
	}
	return &crd.SchedulerParams{
		Resources: &crd.SchedulerResources{
			Interconnect: &crd.SchedulerResourceInterconnect{
				Enabled:         true,
				Units:           1,
				ResourceName:    resourceName,
				Fabric:          fabric,
				NCCLHCAPrefixes: prefixes,
			},
		},
	}
}

func TestWorkloadInjectsinterconnectResourceAndNCCLEnv(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	stampinterconnect(workload, "", interconnectParams())

	container := workload.container()

	// Extended resource: request == limit (kubelet rejects mismatched
	// device-plugin resources).
	req := container.Resources.Requests[corev1.ResourceName("rdma/rdma_shared_device_ib")]
	lim := container.Resources.Limits[corev1.ResourceName("rdma/rdma_shared_device_ib")]
	require.Equal(t, int64(1), req.Value())
	require.Equal(t, int64(1), lim.Value())

	env := envMap(container.Env)
	require.Equal(t, "0", env[envVarNCCLIBDisable])
	require.Equal(t, "mlx5", env[envVarNCCLIBHCA])
}

func TestWorkloadRespectsSDLNCCLOverride(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	// Tenant pinned specific HCAs via SDL env; provider must not
	// clobber it with the default prefix.
	workload.group.Services[workload.serviceIdx].Env = []string{
		"NCCL_IB_HCA=mlx5_0,mlx5_1",
	}
	stampinterconnect(workload, "", interconnectParams())

	container := workload.container()
	env := envMap(container.Env)
	require.Equal(t, "mlx5_0,mlx5_1", env[envVarNCCLIBHCA])
	require.Equal(t, "0", env[envVarNCCLIBDisable])
}

// AKT-492: mixed-vendor hosts publish every distinct HCA family. The
// workload builder joins them with commas — NCCL accepts that natively
// for NCCL_IB_HCA.
func TestWorkloadJoinsMultipleHCAPrefixes(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	stampinterconnect(workload, "", interconnectParamsWith("infiniband", []string{"mlx5", "bnxt_re"}))

	container := workload.container()
	env := envMap(container.Env)
	require.Equal(t, "mlx5,bnxt_re", env[envVarNCCLIBHCA])
}

// AKT-494: NCCL on RoCE needs NCCL_IB_GID_INDEX=3 to pick the RoCEv2 +
// VLAN GID. The builder auto-injects it for RoCE-pinned reservations.
func TestWorkloadInjectsGIDIndexOnRoCE(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	stampinterconnect(workload, "", interconnectParamsWith("roce", []string{"mlx5"}))

	container := workload.container()
	env := envMap(container.Env)
	require.Equal(t, "3", env[envVarNCCLIBGIDIndex])
}

// AKT-494: IB fabrics do NOT get NCCL_IB_GID_INDEX injected — NCCL's
// default GID selection is correct on IB, and overriding it can pick the
// wrong index on multi-port hosts.
func TestWorkloadOmitsGIDIndexOnInfiniBand(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	stampinterconnect(workload, "", interconnectParamsWith("infiniband", []string{"mlx5"}))

	container := workload.container()
	env := envMap(container.Env)
	_, hasGID := env[envVarNCCLIBGIDIndex]
	require.False(t, hasGID, "InfiniBand reservations must not carry an explicit NCCL_IB_GID_INDEX")
}

// AKT-494: a tenant who already set NCCL_IB_GID_INDEX in SDL env wins —
// addIfNotPresent honors the override on RoCE just like every other
// auto-injected NCCL knob.
func TestWorkloadRespectsSDLGIDIndexOverrideOnRoCE(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	workload.group.Services[workload.serviceIdx].Env = []string{
		"NCCL_IB_GID_INDEX=5",
	}
	stampinterconnect(workload, "", interconnectParamsWith("roce", []string{"mlx5"}))

	container := workload.container()
	env := envMap(container.Env)
	require.Equal(t, "5", env[envVarNCCLIBGIDIndex])
}

func TestWorkloadNointerconnectNoEnvNoResource(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	container := workload.container()
	env := envMap(container.Env)

	_, hasDisable := env[envVarNCCLIBDisable]
	_, hasHCA := env[envVarNCCLIBHCA]
	require.False(t, hasDisable, "no NCCL_IB_DISABLE without interconnect pin")
	require.False(t, hasHCA, "no NCCL_IB_HCA without interconnect pin")
	_, hasInterconnect := container.Resources.Limits[corev1.ResourceName("rdma/rdma_shared_device_ib")]
	require.False(t, hasInterconnect, "no interconnect resource without interconnect pin")
}

func TestWorkloadInterconnectGroupAddsLabelAndAntiAffinity(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	stampinterconnect(workload, "pair0", interconnectParams())

	labels := workload.labels()
	require.Equal(t, "pair0", labels[AkashInterconnectGroupLabelName])

	aff := workload.affinity()
	require.NotNil(t, aff.PodAntiAffinity)
	require.Len(t, aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 1)
	term := aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]
	require.Equal(t, "kubernetes.io/hostname", term.TopologyKey)
	require.Equal(t, "pair0", term.LabelSelector.MatchLabels[AkashInterconnectGroupLabelName])
}

func TestWorkloadNoInterconnectGroupNoAntiAffinity(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	// Has interconnect pin but no group label — single-node interconnect workload.
	stampinterconnect(workload, "", interconnectParams())

	labels := workload.labels()
	_, hasLabel := labels[AkashInterconnectGroupLabelName]
	require.False(t, hasLabel)

	aff := workload.affinity()
	require.Nil(t, aff.PodAntiAffinity)
}

func envMap(env []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		out[e.Name] = e.Value
	}
	return out
}
