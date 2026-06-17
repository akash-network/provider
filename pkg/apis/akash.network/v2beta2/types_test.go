package v2beta2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	atestutil "pkg.akt.dev/go/testutil"

	mtestutil "github.com/akash-network/provider/testutil/manifest/v2beta2"
)

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

func TestResourcesFromAkashCopiesGPUAttributes(t *testing.T) {
	attrs := attrtypes.Attributes{
		attrtypes.NewStringAttribute("vendor/nvidia/model/a100", "true"),
	}

	resources := rtypes.Resources{
		GPU: &rtypes.GPU{
			Units:      rtypes.NewResourceValue(1),
			Attributes: attrs,
		},
	}

	converted, err := resourcesFromAkash(resources)
	require.NoError(t, err)
	require.Len(t, converted.GPU.Attributes, 1)

	converted.GPU.Attributes[0].Value = "false"

	assert.Equal(t, "true", resources.GPU.Attributes[0].Value)
}
