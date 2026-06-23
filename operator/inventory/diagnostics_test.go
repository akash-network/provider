package inventory

import (
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	inventoryV1 "pkg.akt.dev/go/inventory/v1"
)

func TestSubStorageAllocatedResourceLogsAndKeepsNegative(t *testing.T) {
	allocated := resource.MustParse("1")
	subtracted := resource.MustParse("2")
	inventoryBefore := map[string]string{
		"class":     "local-path",
		"allocated": "1",
	}

	entries := make([]capturedLogEntry, 0, 1)
	log := logr.New(&captureSink{entries: &entries})

	subStorageAllocatedResource(log, "rancher", "local-path", "pv-1", "pv-uid", &allocated, subtracted, inventoryBefore)

	require.Negative(t, allocated.Value())
	require.Len(t, entries, 1)
	require.ErrorIs(t, entries[0].err, errNegativeAllocatedResource)

	values := logValues(entries[0].values)
	require.Equal(t, "rancher", values["driver"])
	require.Equal(t, "local-path", values["storage_class"])
	require.Equal(t, "pv-1", values["persistent_volume"])
	require.Equal(t, "pv-uid", values["persistent_volume_id"])

	var before map[string]string
	require.NoError(t, json.Unmarshal([]byte(values["inventory_before"].(string)), &before))
	require.Equal(t, inventoryBefore, before)

	var change allocatedStorageChange
	require.NoError(t, json.Unmarshal([]byte(values["change"].(string)), &change))
	require.Equal(t, "rancher", change.Driver)
	require.Equal(t, "local-path", change.StorageClass)
	require.Equal(t, "pv-1", change.PersistentVolume)
	require.Equal(t, "pv-uid", change.PersistentVolumeID)
	require.Equal(t, "1", change.AllocatedBefore)
	require.Equal(t, "2", change.Subtracted)
	require.Equal(t, "-1", change.AllocatedAfter)
}

func TestLogNegativeClusterInventory(t *testing.T) {
	cluster := inventoryV1.Cluster{
		Nodes: inventoryV1.Nodes{
			{
				Name: "node1",
				Resources: inventoryV1.NodeResources{
					CPU: inventoryV1.CPU{
						Quantity: inventoryV1.NewResourcePairMilli(1000, 1000, -250, resource.DecimalSI),
					},
					Memory: inventoryV1.Memory{
						Quantity: inventoryV1.NewResourcePair(1024, -1, 0, resource.DecimalSI),
					},
					GPU:              inventoryV1.GPU{Quantity: inventoryV1.NewResourcePair(1, 1, 0, resource.DecimalSI)},
					EphemeralStorage: inventoryV1.NewResourcePair(10, 10, 0, resource.DecimalSI),
					VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
					VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
			},
		},
		Storage: inventoryV1.ClusterStorage{
			{
				Quantity: inventoryV1.NewResourcePair(10, -1, 0, resource.DecimalSI),
				Info:     inventoryV1.StorageInfo{Class: "default"},
			},
		},
	}

	entries := make([]capturedLogEntry, 0, 1)
	log := logr.New(&captureSink{entries: &entries})

	logNegativeClusterInventory(log, &cluster, "unit-test")

	require.Len(t, entries, 1)
	require.ErrorIs(t, entries[0].err, errNegativeInventoryResource)

	values := logValues(entries[0].values)
	require.Equal(t, "unit-test", values["source"])

	var loggedCluster inventoryV1.Cluster
	require.NoError(t, json.Unmarshal([]byte(values["inventory"].(string)), &loggedCluster))
	require.Equal(t, int64(-250), loggedCluster.Nodes[0].Resources.CPU.Quantity.Allocated.MilliValue())
	require.Equal(t, int64(-1), loggedCluster.Storage[0].Quantity.Allocatable.Value())

	var negatives []negativeInventoryResource
	require.NoError(t, json.Unmarshal([]byte(values["negative_resources"].(string)), &negatives))
	require.ElementsMatch(t, []negativeInventoryResource{
		{
			Scope:    "node",
			Node:     "node1",
			Resource: "cpu",
			Field:    "allocated",
			Value:    "-250m",
		},
		{
			Scope:    "node",
			Node:     "node1",
			Resource: "memory",
			Field:    "allocatable",
			Value:    "-1",
		},
		{
			Scope:        "storage",
			StorageClass: "default",
			Resource:     "storage",
			Field:        "allocatable",
			Value:        "-1",
		},
	}, negatives)
}
