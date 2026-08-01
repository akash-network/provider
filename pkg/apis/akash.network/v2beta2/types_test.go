package v2beta2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	atestutil "pkg.akt.dev/go/testutil"

	mtestutil "github.com/akash-network/provider/testutil/manifest/v2beta2"
)

func TestManifestStorageKeyRefRoundTrip(t *testing.T) {
	const keyRef = "sealed.header.payload.signature"
	original := mani.Service{
		Name: "proof",
		Params: &mani.ServiceParams{Storage: []mani.StorageParams{{
			Name:     "data",
			Mount:    "/proof",
			ReadOnly: false,
			KeyRef:   keyRef,
		}}},
	}

	stored, err := manifestServiceFromProvider(original, nil)
	require.NoError(t, err)
	require.Equal(t, keyRef, stored.Params.Storage[0].KeyRef)

	recovered, err := stored.fromCRD()
	require.NoError(t, err)
	require.Equal(t, keyRef, recovered.Params.Storage[0].KeyRef)
}

func Test_Manifest_encoding(t *testing.T) {
	for _, spec := range mtestutil.Generators {
		// ensure decode(encode(obj)) == obj

		lid := atestutil.LeaseID(t)
		mgroup := spec.Generator.Group(t)
		sparams := make([]*SchedulerParams, len(mgroup.Services))

		kmani, err := NewManifest("foo", lid, &mgroup, ClusterSettings{SchedulerParams: sparams})
		require.NoError(t, err, spec.Name)

		deployment, err := kmani.Deployment()
		require.NoError(t, err, spec.Name)

		assert.Equal(t, lid, deployment.LeaseID(), spec.Name)
		assert.Equal(t, &mgroup, deployment.ManifestGroup(), spec.Name)
	}
}

// Recovered deployments must carry ReservationClusterSettings (keyed by resource ID),
// matching what inventory.Adjust() produces for live reservations. Returning
// ClusterSettings instead leaves updateManifest false in the kube builder, which
// silently drops deployment updates for leases recovered after a provider restart.
func Test_Manifest_Deployment_recovers_reservation_cluster_params(t *testing.T) {
	for _, spec := range mtestutil.Generators {
		lid := atestutil.LeaseID(t)
		mgroup := spec.Generator.Group(t)
		sparams := make([]*SchedulerParams, len(mgroup.Services))

		kmani, err := NewManifest("foo", lid, &mgroup, ClusterSettings{SchedulerParams: sparams})
		require.NoError(t, err, spec.Name)

		deployment, err := kmani.Deployment()
		require.NoError(t, err, spec.Name)

		cparams, ok := deployment.ClusterParams().(ReservationClusterSettings)
		require.True(t, ok, "%s: expected ReservationClusterSettings, got %T", spec.Name, deployment.ClusterParams())

		for i := range mgroup.Services {
			_, exists := cparams[mgroup.Services[i].Resources.ID]
			assert.True(t, exists, "%s: missing cluster params for resource %d", spec.Name, mgroup.Services[i].Resources.ID)
		}
	}
}
