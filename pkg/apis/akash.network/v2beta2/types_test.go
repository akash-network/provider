package v2beta2

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	atestutil "pkg.akt.dev/go/testutil"

	mtestutil "github.com/akash-network/provider/testutil/manifest/v2beta2"
)

// ProviderHost objects stored in clusters, and the CRD schema, encode dseq as a
// JSON number; the spec must keep reading and writing that format.
func Test_ProviderHostSpec_wire_format(t *testing.T) {
	const stored = `{"owner":"akash1owner","provider":"akash1provider","hostname":"example.com","dseq":42,"gseq":1,"oseq":2,"service_name":"web","external_port":80}`

	var spec ProviderHostSpec
	require.NoError(t, json.Unmarshal([]byte(stored), &spec))

	out, err := json.Marshal(spec)
	require.NoError(t, err)
	require.JSONEq(t, stored, string(out))
}

func Test_ProviderHostSpec_LeaseID(t *testing.T) {
	lid := atestutil.LeaseID(t)
	spec := ProviderHostSpec{Owner: lid.Owner, Provider: lid.Provider, Dseq: lid.DSeq, Gseq: lid.GSeq, Oseq: lid.OSeq}

	got, err := spec.LeaseID()
	require.NoError(t, err)
	require.Equal(t, lid, got)

	invalidOwner := spec
	invalidOwner.Owner = "invalid"
	_, err = invalidOwner.LeaseID()
	require.Error(t, err)

	invalidProvider := spec
	invalidProvider.Provider = "invalid"
	_, err = invalidProvider.LeaseID()
	require.Error(t, err)
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
