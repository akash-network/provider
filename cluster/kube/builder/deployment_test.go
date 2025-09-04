package builder

import (
	"fmt"
	"testing"

	"github.com/akash-network/node/sdl"
	"github.com/akash-network/node/testutil"
	"github.com/stretchr/testify/require"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func TestDeploySetsEnvironmentVariables(t *testing.T) {
	log := testutil.Logger(t)
	const fakeHostname = "ahostname.dev"
	settings := Settings{
		ClusterPublicHostname: fakeHostname,
	}
	lid := testutil.LeaseID(t)
	sdl, err := sdl.ReadFile("../../../testdata/deployment/deployment.yaml")
	require.NoError(t, err)

	mani, err := sdl.Manifest()
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

	workload, err := NewWorkloadBuilder(log, settings, cdep, cmani, 0)
	require.NoError(t, err)

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

func TestDeploymentAutomountServiceAccountToken(t *testing.T) {
	log := testutil.Logger(t)
	const fakeHostname = "ahostname.dev"
	settings := Settings{
		ClusterPublicHostname: fakeHostname,
	}
	lid := testutil.LeaseID(t)

	testCases := []struct {
		name           string
		sdlFile        string
		expectedResult bool
	}{
		{
			name:           "should enable automount for log-collector image",
			sdlFile:        "../../../testdata/deployment/deployment-log-collector.yaml",
			expectedResult: true,
		},
		{
			name:           "should disable automount for regular image",
			sdlFile:        "../../../testdata/deployment/deployment.yaml",
			expectedResult: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sdl, err := sdl.ReadFile(testCase.sdlFile)
			require.NoError(t, err)

			manifest, err := sdl.Manifest()
			require.NoError(t, err)

			schedulerParams := make([]*crd.SchedulerParams, len(manifest.GetGroups()[0].Services))

			clusterManifest, err := crd.NewManifest("lease", lid, &manifest.GetGroups()[0], crd.ClusterSettings{SchedulerParams: schedulerParams})
			require.NoError(t, err)

			group, schedulerParams, err := clusterManifest.Spec.Group.FromCRD()
			require.NoError(t, err)

			clusterDeployment := &ClusterDeployment{
				Lid:     lid,
				Group:   &group,
				Sparams: crd.ClusterSettings{SchedulerParams: schedulerParams},
			}

			workload, err := NewWorkloadBuilder(log, settings, clusterDeployment, clusterManifest, 0)
			require.NoError(t, err)

			deploymentBuilder := NewDeployment(workload)

			require.NotNil(t, deploymentBuilder)

			deploymentInstance := deploymentBuilder.(*deployment)

			deployment, err := deploymentInstance.Create()
			require.NoError(t, err)
			require.NotNil(t, deployment)

			automountValue := deployment.Spec.Template.Spec.AutomountServiceAccountToken
			require.NotNil(t, automountValue)
			require.Equal(t, testCase.expectedResult, *automountValue)
		})
	}
}
