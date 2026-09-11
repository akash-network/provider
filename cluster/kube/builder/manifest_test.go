package builder

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/testutil"
)

func TestManifestUpdatePreservesMetadata(t *testing.T) {
	for _, version := range []string{"", "previous-revision"} {
		t.Run("version="+version, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)
			b := BuildManifest(testutil.Logger(t), workload.settings, "lease", workload.deployment)
			stored, err := b.Create()
			require.NoError(t, err)
			stored.ResourceVersion = "current-revision"
			stored.Labels[AkashManifestResourceVersion] = version
			stored.Labels["operator.example.com/managed"] = "true"
			stored.Annotations = map[string]string{"operator.example.com/note": "keep"}

			recovered, err := stored.Deployment()
			require.NoError(t, err)
			deployment, err := ClusterDeploymentFromDeployment(recovered)
			require.NoError(t, err)
			b = BuildManifest(testutil.Logger(t), workload.settings, "lease", deployment)

			updated, err := b.Update(stored)
			require.NoError(t, err)
			require.Equal(t, stored.ObjectMeta, updated.ObjectMeta,
				"recovery must not rewrite metadata and advance the API-server resource version")

			deployment.ManifestGroup().Services[0].Image = "example.com/updated:v2"
			updated, err = b.Update(stored)
			require.NoError(t, err)
			require.Equal(t, "example.com/updated:v2", updated.Spec.Group.Services[0].Image)
			require.Equal(t, stored.ObjectMeta, updated.ObjectMeta,
				"tenant updates change the spec, preserving stored metadata")
		})
	}
}

func TestManifestDoesNotLabelItsOwnResourceVersion(t *testing.T) {
	lid := testutil.LeaseID(t)
	_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)
	workload.deployment.SetResourceVersion("current-revision")
	b := BuildManifest(testutil.Logger(t), workload.settings, "lease", workload.deployment)
	manifest, err := b.Create()
	require.NoError(t, err)
	require.NotContains(t, manifest.Labels, AkashManifestResourceVersion)
}
