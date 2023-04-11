package v2beta2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	atestutil "github.com/akash-network/node/testutil"

	mtestutil "github.com/akash-network/provider/testutil/manifest/v2beta2"
)

func Test_Manifest_encoding(t *testing.T) {
	for _, spec := range mtestutil.Generators {
		// ensure decode(encode(obj)) == obj

		lid := atestutil.LeaseID(t)
		mgroup := spec.Generator.Group(t)

		kmani, err := NewManifest("foo", lid, &mgroup)
		require.NoError(t, err, spec.Name)

		deployment, err := kmani.Deployment()
		require.NoError(t, err, spec.Name)

		assert.Equal(t, lid, deployment.LeaseID(), spec.Name)
		assert.Equal(t, mgroup, deployment.ManifestGroup(), spec.Name)
	}
}
