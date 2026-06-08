package builder

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	mtypes "pkg.akt.dev/go/node/market/v1"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const fakeHostname = "ahostname.dev"

func testSetup(t *testing.T, sdlFile string, serviceIdx int, lid mtypes.LeaseID) (*crd.Manifest, *Workload) {
	t.Helper()

	log := testutil.Logger(t)
	settings := Settings{
		ClusterPublicHostname: fakeHostname,
	}

	sdlData, err := sdl.ReadFile(sdlFile)
	require.NoError(t, err)

	mani, err := sdlData.Manifest()
	require.NoError(t, err)

	sparams := make([]*crd.SchedulerParams, len(mani.GetGroups()[0].Services))

	cmani, err := crd.NewManifest("lease", lid, &mani.GetGroups()[0], crd.ClusterSettings{SchedulerParams: sparams})
	require.NoError(t, err)

	group, sparams, err := cmani.Spec.Group.FromCRD()
	require.NoError(t, err)

	cdep := &ClusterDeployment{
		Lid:     lid,
		Group:   &group,
		Sparams: crd.ClusterSettings{SchedulerParams: sparams},
	}

	workload, err := NewWorkloadBuilder(log, settings, cdep, cmani, serviceIdx)
	require.NoError(t, err)

	return cmani, workload
}

func TestDeploySetsEnvironmentVariables(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)

	deploymentBuilder := NewDeployment(workload)

	require.NotNil(t, deploymentBuilder)

	dbuilder := deploymentBuilder.(*deployment)

	container := dbuilder.container()
	require.NotNil(t, container)

	env := make(map[string]string)
	for _, entry := range container.Env {
		env[entry.Name] = entry.Value
	}

	value, ok := env[envVarAkashClusterPublicHostname]
	require.True(t, ok)
	require.Equal(t, fakeHostname, value)

	value, ok = env[envVarAkashDeploymentSequence]
	require.True(t, ok)
	require.Equal(t, fmt.Sprintf("%d", lid.GetDSeq()), value)

	value, ok = env[envVarAkashGroupSequence]
	require.True(t, ok)
	require.Equal(t, fmt.Sprintf("%d", lid.GetGSeq()), value)

	value, ok = env[envVarAkashOrderSequence]
	require.True(t, ok)
	require.Equal(t, fmt.Sprintf("%d", lid.GetOSeq()), value)

	value, ok = env[envVarAkashOwner]
	require.True(t, ok)
	require.Equal(t, lid.Owner, value)

	value, ok = env[envVarAkashProvider]
	require.True(t, ok)
	require.Equal(t, lid.Provider, value)
}

func TestDeploymentPermissions(t *testing.T) {
	tests := []struct {
		name          string
		sdlFile       string
		serviceIdx    int
		expectedToken bool
	}{
		{
			name:          "defaults to false when no permissions specified",
			sdlFile:       "../../../testdata/deployment/deployment.yaml",
			serviceIdx:    0,
			expectedToken: false,
		},
		{
			name:          "enables automount when permissions are defined",
			sdlFile:       "../../../testdata/sdl/permissions.yaml",
			serviceIdx:    0,
			expectedToken: true,
		},
		{
			name:          "disables automount when no permissions defined",
			sdlFile:       "../../../testdata/sdl/permissions.yaml",
			serviceIdx:    1,
			expectedToken: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			_, workload := testSetup(t, tt.sdlFile, tt.serviceIdx, lid)

			deploymentBuilder := NewDeployment(workload)
			deployment, err := deploymentBuilder.Create()
			require.NoError(t, err)
			require.NotNil(t, deployment)

			require.NotNil(t, deployment.Spec.Template.Spec.AutomountServiceAccountToken)
			require.Equal(t, tt.expectedToken, *deployment.Spec.Template.Spec.AutomountServiceAccountToken)
		})
	}
}

func TestSidecarResourceSubtraction(t *testing.T) {
	tests := []struct {
		name               string
		runtimeClass       string
		attestationDisabled bool
		cpuMillis          uint64 // SDL cpu in millicores
		memBytes           uint64 // SDL memory in bytes
		expectSubtraction  bool
	}{
		{
			name:              "CC with attestation — 1Gi memory, 4000m CPU",
			runtimeClass:      RuntimeClassKataQemuSNP,
			cpuMillis:         4000,
			memBytes:          1024 * 1024 * 1024, // 1Gi
			expectSubtraction: true,
		},
		{
			name:              "CC with attestation — 500m CPU, 128Mi memory",
			runtimeClass:      RuntimeClassKataQemuSNP,
			cpuMillis:         500,
			memBytes:          128 * 1024 * 1024, // 128Mi
			expectSubtraction: true,
		},
		{
			name:              "CC with attestation — GPU runtime class",
			runtimeClass:      RuntimeClassKataQemuNvidiaGPUSNP,
			cpuMillis:         2000,
			memBytes:          512 * 1024 * 1024, // 512Mi
			expectSubtraction: true,
		},
		{
			name:              "CC with attestation disabled — no subtraction",
			runtimeClass:      RuntimeClassKataQemuSNP,
			attestationDisabled: true,
			cpuMillis:         500,
			memBytes:          128 * 1024 * 1024,
			expectSubtraction: false,
		},
		{
			name:              "non-CC workload — no subtraction",
			runtimeClass:      "nvidia",
			cpuMillis:         500,
			memBytes:          128 * 1024 * 1024,
			expectSubtraction: false,
		},
		{
			name:              "no runtime class — no subtraction",
			runtimeClass:      "",
			cpuMillis:         500,
			memBytes:          128 * 1024 * 1024,
			expectSubtraction: false,
		},
		{
			name:              "CC with small resources — floors at minimum",
			runtimeClass:      RuntimeClassKataQemuSNP,
			cpuMillis:         110, // barely above sidecar limit (100m)
			memBytes:          68 * 1024 * 1024, // barely above sidecar limit (64Mi)
			expectSubtraction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)

			log := testutil.Logger(t)
			settings := Settings{
				CPUCommitLevel:    1, // request = limit (simplifies assertions)
				MemoryCommitLevel: 1,
			}

			sdlData, err := sdl.ReadFile("../../../testdata/deployment/deployment.yaml")
			require.NoError(t, err)

			mani, err := sdlData.Manifest()
			require.NoError(t, err)

			sp := &crd.SchedulerParams{
				RuntimeClass:        tt.runtimeClass,
				AttestationDisabled: tt.attestationDisabled,
			}
			sparams := []*crd.SchedulerParams{sp}

			cmani, err := crd.NewManifest("lease", lid, &mani.GetGroups()[0], crd.ClusterSettings{SchedulerParams: sparams})
			require.NoError(t, err)

			// Override CPU and memory in the CRD before building the workload
			cmani.Spec.Group.Services[0].Resources.CPU.Units = uint32(tt.cpuMillis) //nolint:gosec
			cmani.Spec.Group.Services[0].Resources.Memory.Size = fmt.Sprintf("%d", tt.memBytes)

			group, retSparams, err := cmani.Spec.Group.FromCRD()
			require.NoError(t, err)

			cdep := &ClusterDeployment{
				Lid:     lid,
				Group:   &group,
				Sparams: crd.ClusterSettings{SchedulerParams: retSparams},
			}
			// Override scheduler params with our CC config
			cdep.Sparams.SchedulerParams[0] = sp

			workload, err := NewWorkloadBuilder(log, settings, cdep, cmani, 0)
			require.NoError(t, err)

			container := workload.container()

			cpuLimit := container.Resources.Limits.Cpu().MilliValue()
			cpuRequest := container.Resources.Requests.Cpu().MilliValue()
			memLimit := container.Resources.Limits.Memory().Value()
			memRequest := container.Resources.Requests.Memory().Value()

			if tt.expectSubtraction {
				sidecarMemLimit := SidecarMemoryLimitBytes
				if IsGPURuntimeClass(tt.runtimeClass) {
					sidecarMemLimit = SidecarGPUMemoryLimitBytes
				}

				expectedCPULimit := int64(tt.cpuMillis) - SidecarCPULimitMillicores
				if expectedCPULimit < MinPrimaryCPUMillicores {
					expectedCPULimit = MinPrimaryCPUMillicores
				}
				expectedMemLimit := int64(tt.memBytes) - sidecarMemLimit
				if expectedMemLimit < MinPrimaryMemoryBytes {
					expectedMemLimit = MinPrimaryMemoryBytes
				}

				require.Equal(t, expectedCPULimit, cpuLimit,
					"CPU limit: want %dm, got %dm", expectedCPULimit, cpuLimit)
				require.Equal(t, expectedMemLimit, memLimit,
					"Memory limit: want %d, got %d", expectedMemLimit, memLimit)

				// Pod total LIMIT should equal user's original limit
				// (only when resources are above sidecar footprint)
				// Pod total equals user limit only when resources are well above sidecar + minimum
				if int64(tt.cpuMillis)-SidecarCPULimitMillicores >= MinPrimaryCPUMillicores {
					require.Equal(t, int64(tt.cpuMillis), cpuLimit+SidecarCPULimitMillicores,
						"Pod CPU limit total should equal user limit")
				}
				if int64(tt.memBytes)-sidecarMemLimit >= MinPrimaryMemoryBytes {
					require.Equal(t, int64(tt.memBytes), memLimit+sidecarMemLimit,
						"Pod memory limit total should equal user limit")
				}

				// K8s constraint: limit >= request
				require.GreaterOrEqual(t, cpuLimit, cpuRequest,
					"CPU limit must be >= request")
				require.GreaterOrEqual(t, memLimit, memRequest,
					"Memory limit must be >= request")
			} else {
				// No subtraction — primary container gets full user resources
				require.Equal(t, int64(tt.cpuMillis), cpuLimit,
					"CPU limit should be unmodified")
				require.Equal(t, int64(tt.memBytes), memLimit,
					"Memory limit should be unmodified")
			}

			// Always: limit >= request (K8s invariant)
			require.GreaterOrEqual(t, cpuLimit, cpuRequest, "K8s: CPU limit >= request")
			require.GreaterOrEqual(t, memLimit, memRequest, "K8s: memory limit >= request")
		})
	}
}
