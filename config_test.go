package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConfig_NodePortQuantityReachesInventory guards support#135: the value
// configured via --cluster-node-port-quantity must land in the field the
// cluster inventory actually reads (cluster.Config.InventoryExternalPortQuantity),
// otherwise the setting is silently ignored.
func TestConfig_NodePortQuantityReachesInventory(t *testing.T) {
	const want = uint(7)

	cfg := NewDefaultConfig()
	cfg.SetClusterExternalPortQuantity(want)

	require.Equal(t, want, cfg.InventoryExternalPortQuantity,
		"node port quantity must reach the inventory config")
}
