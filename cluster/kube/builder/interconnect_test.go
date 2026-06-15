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
	return &crd.SchedulerParams{
		Resources: &crd.SchedulerResources{
			Interconnect: &crd.SchedulerResourceInterconnect{
				Enabled:       true,
				Units:         1,
				ResourceName:  "rdma/rdma_shared_device_ib",
				Fabric:        "infiniband",
				NCCLHCAPrefix: "mlx5",
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
