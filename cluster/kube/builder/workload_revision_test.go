package builder

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"pkg.akt.dev/go/testutil"
)

func TestWorkloadRevisionDoesNotChangePodTemplate(t *testing.T) {
	for _, legacyLabel := range []bool{false, true} {
		name := "without legacy label"
		if legacyLabel {
			name = "with legacy label"
		}
		t.Run(name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			_, workload := testSetup(t, "../../../testdata/deployment/deployment.yaml", 0, lid)
			workload.deployment.SetResourceVersion("original-revision")
			db := NewDeployment(workload)
			sb := BuildStatefulSet(workload)
			deployment, err := db.Create()
			require.NoError(t, err)
			statefulSet, err := sb.Create()
			require.NoError(t, err)
			require.NotContains(t, deployment.Spec.Template.Labels, AkashManifestResourceVersion)
			require.NotContains(t, statefulSet.Spec.Template.Labels, AkashManifestResourceVersion)

			// Older providers included bookkeeping in pod templates. Removing or
			// rewriting that existing label on upgrade would also cause a rollout.
			if legacyLabel {
				deployment.Spec.Template.Labels[AkashManifestResourceVersion] = "original-revision"
				statefulSet.Spec.Template.Labels[AkashManifestResourceVersion] = "original-revision"
			}
			workload.deployment.SetResourceVersion("newer-metadata-revision")
			updatedDeployment, err := db.Update(deployment)
			require.NoError(t, err)
			updatedStatefulSet, err := sb.Update(statefulSet)
			require.NoError(t, err)
			require.Equal(t, deployment.Spec.Template, updatedDeployment.Spec.Template)
			require.Equal(t, statefulSet.Spec.Template, updatedStatefulSet.Spec.Template)
			require.Equal(t, "newer-metadata-revision", updatedDeployment.Labels[AkashManifestResourceVersion])
			require.Equal(t, "newer-metadata-revision", updatedStatefulSet.Labels[AkashManifestResourceVersion])

			workload.group.Services[0].Env = append(workload.group.Services[0].Env, "TENANT_UPDATE=applied")
			updatedDeployment, err = db.Update(updatedDeployment)
			require.NoError(t, err)
			updatedStatefulSet, err = sb.Update(updatedStatefulSet)
			require.NoError(t, err)
			require.NotEqual(t, deployment.Spec.Template, updatedDeployment.Spec.Template)
			require.NotEqual(t, statefulSet.Spec.Template, updatedStatefulSet.Spec.Template)
			want := corev1.EnvVar{Name: "TENANT_UPDATE", Value: "applied"}
			require.Contains(t, updatedDeployment.Spec.Template.Spec.Containers[0].Env, want)
			require.Contains(t, updatedStatefulSet.Spec.Template.Spec.Containers[0].Env, want)
		})
	}
}
