package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/resource"
	inventoryV1 "pkg.akt.dev/go/inventory/v1"
)

var (
	errNegativeAllocatedResource = errors.New("inventory allocated resource went negative")
	errNegativeInventoryResource = errors.New("inventory resource went negative")
	quantityZero                 = resource.NewQuantity(0, resource.DecimalSI)
)

type allocatedStorageChange struct {
	Driver             string `json:"driver"`
	StorageClass       string `json:"storage_class"`
	PersistentVolume   string `json:"persistent_volume"`
	PersistentVolumeID string `json:"persistent_volume_id"`
	AllocatedBefore    string `json:"allocated_before"`
	Subtracted         string `json:"subtracted"`
	AllocatedAfter     string `json:"allocated_after"`
}

type negativeInventoryResource struct {
	Scope        string `json:"scope"`
	Node         string `json:"node,omitempty"`
	StorageClass string `json:"storage_class,omitempty"`
	Resource     string `json:"resource"`
	Field        string `json:"field"`
	Value        string `json:"value"`
}

type storageAccountingLogEntry struct {
	Driver         string `json:"driver"`
	Class          string `json:"class"`
	Allocated      string `json:"allocated"`
	IsCeph         bool   `json:"is_ceph,omitempty"`
	IsRancher      bool   `json:"is_rancher,omitempty"`
	IsAkashManaged bool   `json:"is_akash_managed"`
	Pool           string `json:"pool,omitempty"`
	ClusterID      string `json:"cluster_id,omitempty"`
}

func jsonLogValue(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("json marshal error %v", err)
	}

	return string(data)
}

func subStorageAllocatedResource(
	log logr.Logger,
	driver string,
	class string,
	persistentVolume string,
	persistentVolumeID string,
	allocated *resource.Quantity,
	val resource.Quantity,
	inventoryBefore interface{},
) {
	before := allocated.DeepCopy()
	after := allocated.DeepCopy()
	after.Sub(val)

	if after.Cmp(*quantityZero) < 0 {
		change := allocatedStorageChange{
			Driver:             driver,
			StorageClass:       class,
			PersistentVolume:   persistentVolume,
			PersistentVolumeID: persistentVolumeID,
			AllocatedBefore:    before.String(),
			Subtracted:         val.String(),
			AllocatedAfter:     after.String(),
		}

		log.Error(errNegativeAllocatedResource, "inventory storage allocated resource went negative",
			"driver", driver,
			"storage_class", class,
			"persistent_volume", persistentVolume,
			"persistent_volume_id", persistentVolumeID,
			"inventory_before", jsonLogValue(inventoryBefore),
			"change", jsonLogValue(change))
	}

	*allocated = after
}

func logNegativeClusterInventory(log logr.Logger, cluster *inventoryV1.Cluster, source string) {
	negatives := negativeInventoryResources(cluster)
	if len(negatives) == 0 {
		return
	}

	log.Error(errNegativeInventoryResource, "inventory contains negative resource quantity",
		"source", source,
		"inventory", jsonLogValue(cluster),
		"negative_resources", jsonLogValue(negatives))
}

func negativeInventoryResources(cluster *inventoryV1.Cluster) []negativeInventoryResource {
	var result []negativeInventoryResource

	for _, node := range cluster.Nodes {
		result = appendNegativeResourcePair(result, "node", node.Name, "", "cpu", &node.Resources.CPU.Quantity)
		result = appendNegativeResourcePair(result, "node", node.Name, "", "memory", &node.Resources.Memory.Quantity)
		result = appendNegativeResourcePair(result, "node", node.Name, "", "gpu", &node.Resources.GPU.Quantity)
		result = appendNegativeResourcePair(result, "node", node.Name, "", "ephemeral_storage", &node.Resources.EphemeralStorage)
		result = appendNegativeResourcePair(result, "node", node.Name, "", "volumes_attached", &node.Resources.VolumesAttached)
		result = appendNegativeResourcePair(result, "node", node.Name, "", "volumes_mounted", &node.Resources.VolumesMounted)
	}

	for _, storage := range cluster.Storage {
		result = appendNegativeResourcePair(result, "storage", "", storage.Info.Class, "storage", &storage.Quantity)
	}

	return result
}

func appendNegativeResourcePair(result []negativeInventoryResource, scope, node, class, name string, pair *inventoryV1.ResourcePair) []negativeInventoryResource {
	result = appendNegativeQuantity(result, scope, node, class, name, "capacity", pair.Capacity)
	result = appendNegativeQuantity(result, scope, node, class, name, "allocatable", pair.Allocatable)
	result = appendNegativeQuantity(result, scope, node, class, name, "allocated", pair.Allocated)

	return result
}

func appendNegativeQuantity(result []negativeInventoryResource, scope, node, class, name, field string, quantity *resource.Quantity) []negativeInventoryResource {
	if quantity == nil || quantity.Cmp(*quantityZero) >= 0 {
		return result
	}

	return append(result, negativeInventoryResource{
		Scope:        scope,
		Node:         node,
		StorageClass: class,
		Resource:     name,
		Field:        field,
		Value:        quantity.String(),
	})
}

func cephStorageInventoryLogValue(scs cephStorageClasses) []storageAccountingLogEntry {
	classes := make([]string, 0, len(scs))
	for class := range scs {
		classes = append(classes, class)
	}
	sort.Strings(classes)

	result := make([]storageAccountingLogEntry, 0, len(classes))
	for _, class := range classes {
		params := scs[class]
		allocated := ""
		if params.allocated != nil {
			allocated = params.allocated.String()
		}

		result = append(result, storageAccountingLogEntry{
			Driver:         "ceph",
			Class:          class,
			Allocated:      allocated,
			IsCeph:         params.isCeph,
			IsAkashManaged: params.isAkashManaged,
			Pool:           params.pool,
			ClusterID:      params.clusterID,
		})
	}

	return result
}

func rancherStorageInventoryLogValue(scs rancherStorageClasses) []storageAccountingLogEntry {
	classes := make([]string, 0, len(scs))
	for class := range scs {
		classes = append(classes, class)
	}
	sort.Strings(classes)

	result := make([]storageAccountingLogEntry, 0, len(classes))
	for _, class := range classes {
		params := scs[class]
		result = append(result, storageAccountingLogEntry{
			Driver:         "rancher",
			Class:          class,
			Allocated:      params.allocated.String(),
			IsRancher:      params.isRancher,
			IsAkashManaged: params.isAkashManaged,
		})
	}

	return result
}
