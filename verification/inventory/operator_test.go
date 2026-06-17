package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cinventory "github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
)

func TestOperatorMaterialSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := cinventory.NewNull(ctx, "node-1")
	source, err := NewOperatorMaterialSource(ctx, OperatorMaterialSourceConfig{
		Inventory:       client,
		SoftwareVersion: "v1.2.3",
	})
	require.NoError(t, err)

	reqCtx, reqCancel := context.WithTimeout(ctx, time.Second)
	defer reqCancel()

	material, err := source.SnapshotMaterial(reqCtx)
	require.NoError(t, err)
	require.Len(t, material.Cluster.Nodes, 1)
	require.Equal(t, "node-1", material.Cluster.Nodes[0].Name)
	require.Equal(t, "v1.2.3", material.SoftwareVersion)
	require.Len(t, material.EvidenceSections, 1)
	require.Equal(t, EvidenceSectionInventoryOperator, material.EvidenceSections[0].Name)
	require.NotEmpty(t, material.EvidenceSections[0].Payload)
}

func TestOperatorMaterialSourceValidatesConfig(t *testing.T) {
	source, err := NewOperatorMaterialSource(context.Background(), OperatorMaterialSourceConfig{})
	require.ErrorIs(t, err, errMissingInventoryClient)
	require.Nil(t, source)
}
