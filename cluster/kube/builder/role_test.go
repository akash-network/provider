package builder

import (
	"testing"

	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func TestRolePolicyRules(t *testing.T) {
	log := testutil.Logger(t)
	const fakeHostname = "ahostname.dev"
	settings := Settings{
		ClusterPublicHostname: fakeHostname,
	}
	lid := testutil.LeaseID(t)

	t.Run("generates logs permissions correctly", func(t *testing.T) {
		sdl, err := sdl.ReadFile("../../../testdata/sdl/permissions.yaml")
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

		// Test service with permissions.read: [logs] (index 0 = web)
		workload, err := NewWorkloadBuilder(log, settings, cdep, cmani, 0)
		require.NoError(t, err)

		roleBuilder := BuildRole(workload)
		role, err := roleBuilder.Create()
		require.NoError(t, err)
		require.NotNil(t, role)

		// Should have 3 rules for logs: pods, pods/log, deployments
		require.Len(t, role.Rules, 3)

		// Check pods rule
		require.Contains(t, role.Rules[0].Resources, "pods")
		require.Contains(t, role.Rules[0].Verbs, "get")
		require.Contains(t, role.Rules[0].Verbs, "list")
		require.Contains(t, role.Rules[0].Verbs, "watch")

		// Check pods/log rule
		require.Contains(t, role.Rules[1].Resources, "pods/log")
		require.Contains(t, role.Rules[1].Verbs, "get")
		require.Contains(t, role.Rules[1].Verbs, "list")

		// Check deployments rule
		require.Contains(t, role.Rules[2].Resources, "deployments")
		require.Equal(t, []string{"apps"}, role.Rules[2].APIGroups)
		require.Contains(t, role.Rules[2].Verbs, "get")
		require.Contains(t, role.Rules[2].Verbs, "list")
		require.Contains(t, role.Rules[2].Verbs, "watch")
	})

	t.Run("generates logs and events permissions correctly", func(t *testing.T) {
		sdl, err := sdl.ReadFile("../../../testdata/sdl/permissions-events.yaml")
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

		// Test service with permissions.read: [logs, events] (index 0 = log-collector)
		workload, err := NewWorkloadBuilder(log, settings, cdep, cmani, 0)
		require.NoError(t, err)

		roleBuilder := BuildRole(workload)
		role, err := roleBuilder.Create()
		require.NoError(t, err)
		require.NotNil(t, role)

		// Should have 4 rules: pods, pods/log, deployments (from logs), events (from events)
		require.Len(t, role.Rules, 4)

		// Check events rule (should be last)
		require.Contains(t, role.Rules[3].Resources, "events")
		require.Contains(t, role.Rules[3].Verbs, "get")
		require.Contains(t, role.Rules[3].Verbs, "list")
		require.Contains(t, role.Rules[3].Verbs, "watch")
	})

	t.Run("generates empty rules when no permissions", func(t *testing.T) {
		sdl, err := sdl.ReadFile("../../../testdata/sdl/permissions.yaml")
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

		// Test service without permissions (index 1 = web2)
		workload, err := NewWorkloadBuilder(log, settings, cdep, cmani, 1)
		require.NoError(t, err)

		roleBuilder := BuildRole(workload)
		role, err := roleBuilder.Create()
		require.NoError(t, err)
		require.NotNil(t, role)

		// Should have no rules when no permissions are defined
		require.Len(t, role.Rules, 0)
	})
}
