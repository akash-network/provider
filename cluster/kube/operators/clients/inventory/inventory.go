package inventory

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/tendermint/tendermint/libs/log"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cinventory "github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

var _ ctypes.Inventory = (*inventory)(nil)

func newInventory(log log.Logger, clState inventoryV1.Cluster) *inventory {
	inv := &inventory{
		Cluster: clState,
		log:     log,
	}

	return inv
}

func (inv *inventory) dup() inventory {
	dup := inventory{
		Cluster: *inv.Cluster.Dup(),
		log:     inv.log,
	}

	return dup
}

// tryAdjust cluster inventory
// It returns two boolean values. First indicates if node-wide resources satisfy (true) requirements
// Seconds indicates if cluster-wide resources satisfy (true) requirements
func (inv *inventory) tryAdjust(node int, res *types.Resources) (*crd.SchedulerParams, bool, bool) {
	nd := inv.Nodes[node].Dup()
	sparams := &crd.SchedulerParams{}

	if !tryAdjustCPU(&nd.Resources.CPU.Quantity, res.CPU) {
		return nil, false, true
	}

	if !tryAdjustGPU(&nd.Resources.GPU, res.GPU, sparams) {
		return nil, false, true
	}

	if !nd.Resources.Memory.Quantity.SubNLZ(res.Memory.Quantity) {
		return nil, false, true
	}

	storageClasses := inv.Storage.Dup()

	for i, storage := range res.Storage {
		attrs, err := cinventory.ParseStorageAttributes(storage.Attributes)
		if err != nil {
			return nil, false, false
		}

		if !attrs.Persistent {
			if attrs.Class == "ram" {
				if !nd.Resources.Memory.Quantity.SubNLZ(storage.Quantity) {
					return nil, false, true
				}
			} else {
				// ephemeral storage
				if !tryAdjustEphemeralStorage(&nd.Resources.EphemeralStorage, &res.Storage[i]) {
					return nil, false, true
				}
			}

			continue
		}

		if !nd.IsStorageClassSupported(attrs.Class) {
			return nil, false, true
		}

		// if !nd.tryAdjustVolumesAttached(types.NewResourceValue(1)) {
		// 	return nil, false, true

		// }

		storageAdjusted := false

		for idx := range storageClasses {
			if storageClasses[idx].Info.Class == attrs.Class {
				if !storageClasses[idx].Quantity.SubNLZ(storage.Quantity) {
					// cluster storage does not have enough space thus break to error
					return nil, false, false
				}
				storageAdjusted = true
				break
			}
		}

		// requested storage class is not present in the cluster
		// there is no point to adjust inventory further
		if !storageAdjusted {
			return nil, false, false
		}
	}

	// all requirements for current group have been satisfied
	// commit and move on
	inv.Nodes[node] = nd
	inv.Storage = storageClasses

	if reflect.DeepEqual(sparams, &crd.SchedulerParams{}) {
		return nil, true, true
	}

	return sparams, true, true
}

func tryAdjustCPU(rp *inventoryV1.ResourcePair, res *types.CPU) bool {
	return rp.SubMilliNLZ(res.Units)
}

func tryAdjustGPU(rp *inventoryV1.GPU, res *types.GPU, sparams *crd.SchedulerParams) bool {
	reqCnt := res.Units.Value()

	if reqCnt == 0 {
		return true
	}

	if rp.Quantity.Available().Value() == 0 {
		return false
	}

	attrs, err := cinventory.ParseGPUAttributes(res.Attributes)
	if err != nil {
		return false
	}

	for _, info := range rp.Info {
		models, exists := attrs[info.Vendor]
		if !exists {
			continue
		}

		attr, exists := models.ExistsOrWildcard(info.Name)
		if !exists {
			continue
		}

		if attr != nil {
			if attr.RAM != "" && attr.RAM != info.MemorySize {
				continue
			}

			if attr.Interface != "" && attr.RAM != info.Interface {
				continue
			}
		}

		reqCnt--
		if reqCnt == 0 {
			vendor := strings.ToLower(info.Vendor)

			if !rp.Quantity.SubNLZ(res.Units) {
				return false
			}

			sParamsEnsureGPU(sparams)
			sparams.Resources.GPU.Vendor = vendor
			sparams.Resources.GPU.Model = info.Name

			switch vendor {
			case builder.GPUVendorNvidia:
				sparams.RuntimeClass = runtimeClassNvidia
			default:
			}

			key := fmt.Sprintf("vendor/%s/model/%s", vendor, info.Name)
			if attr != nil {
				if attr.RAM != "" {
					key = fmt.Sprintf("%s/ram/%s", key, attr.RAM)
				}

				if attr.Interface != "" {
					key = fmt.Sprintf("%s/interface/%s", key, attr.Interface)
				}
			}

			res.Attributes = types.Attributes{
				{
					Key:   key,
					Value: "true",
				},
			}

			return true
		}
	}

	return false
}

func tryAdjustEphemeralStorage(rp *inventoryV1.ResourcePair, res *types.Storage) bool {
	return rp.SubNLZ(res.Quantity)
}

// nolint: unused
func tryAdjustVolumesAttached(rp *inventoryV1.ResourcePair, res types.ResourceValue) bool {
	return rp.SubNLZ(res)
}

func (inv *inventory) Adjust(reservation ctypes.ReservationGroup, opts ...ctypes.InventoryOption) error {
	cfg := &ctypes.InventoryOptions{}
	for _, opt := range opts {
		cfg = opt(cfg)
	}

	origResources := reservation.Resources().GetResourceUnits()
	resources := make(dtypes.ResourceUnits, 0, len(origResources))
	adjustedResources := make(dtypes.ResourceUnits, 0, len(origResources))

	for _, res := range origResources {
		resources = append(resources, dtypes.ResourceUnit{
			Resources: res.Resources.Dup(),
			Count:     res.Count,
		})

		adjustedResources = append(adjustedResources, dtypes.ResourceUnit{
			Resources: res.Resources.Dup(),
			Count:     res.Count,
		})
	}

	cparams := make(crd.ReservationClusterSettings)

	currInventory := inv.dup()

	var err error

nodes:
	for nodeIdx := range currInventory.Nodes {
		for i := len(resources) - 1; i >= 0; i-- {
			adjustedGroup := false

			var adjusted *types.Resources
			if origResources[i].Count == resources[i].Count {
				adjusted = &adjustedResources[i].Resources
			} else {
				adjustedGroup = true
				res := adjustedResources[i].Resources.Dup()
				adjusted = &res
			}

			for ; resources[i].Count > 0; resources[i].Count-- {
				sparams, nStatus, cStatus := currInventory.tryAdjust(nodeIdx, adjusted)
				if !cStatus {
					// cannot satisfy cluster-wide resources, stop lookup
					break nodes
				}

				if !nStatus {
					// cannot satisfy node-wide resources, try with next node
					continue nodes
				}

				// at this point we expect all replicas of the same service to produce
				// same adjusted resource units as well as cluster params
				if adjustedGroup {
					if !reflect.DeepEqual(adjusted, &adjustedResources[i].Resources) {
						jFirstAdjusted, _ := json.Marshal(&adjustedResources[i].Resources)
						jCurrAdjusted, _ := json.Marshal(adjusted)

						inv.log.Error(fmt.Sprintf("resource mismatch between replicas within group:\n"+
							"\tfirst adjusted replica: %s\n"+
							"\tcurr adjusted replica: %s", string(jFirstAdjusted), string(jCurrAdjusted)))

						err = ctypes.ErrGroupResourceMismatch
						break nodes
					}

					// all replicas of the same service are expected to have same node selectors and runtimes
					// if they don't match then provider cannot bid
					if !reflect.DeepEqual(sparams, cparams[adjusted.ID]) {
						jFirstSparams, _ := json.Marshal(cparams[adjusted.ID])
						jCurrSparams, _ := json.Marshal(sparams)

						inv.log.Error(fmt.Sprintf("scheduler params mismatch between replicas within group:\n"+
							"\tfirst replica: %s\n"+
							"\tcurr replica: %s", string(jFirstSparams), string(jCurrSparams)))

						err = ctypes.ErrGroupResourceMismatch
						break nodes
					}
				} else {
					cparams[adjusted.ID] = sparams
				}
			}

			// all replicas resources are fulfilled when count == 0.
			// remove group from the list to prevent double request of the same resources
			if resources[i].Count == 0 {
				resources = append(resources[:i], resources[i+1:]...)
				goto nodes
			}
		}
	}

	if len(resources) == 0 {
		if !cfg.DryRun {
			*inv = currInventory
		}

		reservation.SetAllocatedResources(adjustedResources)
		reservation.SetClusterParams(cparams)

		return nil
	}

	if err != nil {
		return err
	}

	return ctypes.ErrInsufficientCapacity
}

func (inv *inventory) Snapshot() inventoryV1.Cluster {
	return *inv.Cluster.Dup()
}

func (inv *inventory) Metrics() inventoryV1.Metrics {
	cpuTotal := uint64(0)
	gpuTotal := uint64(0)
	memoryTotal := uint64(0)
	storageEphemeralTotal := uint64(0)
	storageTotal := make(map[string]int64)

	cpuAvailable := uint64(0)
	gpuAvailable := uint64(0)
	memoryAvailable := uint64(0)
	storageEphemeralAvailable := uint64(0)
	storageAvailable := make(map[string]int64)

	ret := inventoryV1.Metrics{
		Nodes: make([]inventoryV1.NodeMetrics, 0, len(inv.Nodes)),
	}

	for _, nd := range inv.Nodes {
		invNode := inventoryV1.NodeMetrics{
			Name: nd.Name,
			Allocatable: inventoryV1.ResourcesMetric{
				CPU:              uint64(nd.Resources.CPU.Quantity.Allocatable.MilliValue()),
				GPU:              uint64(nd.Resources.GPU.Quantity.Allocatable.Value()),
				Memory:           uint64(nd.Resources.Memory.Quantity.Allocatable.Value()),
				StorageEphemeral: uint64(nd.Resources.EphemeralStorage.Allocatable.Value()),
			},
		}

		cpuTotal += uint64(nd.Resources.CPU.Quantity.Allocatable.MilliValue())
		gpuTotal += uint64(nd.Resources.GPU.Quantity.Allocatable.Value())
		memoryTotal += uint64(nd.Resources.Memory.Quantity.Allocatable.Value())
		storageEphemeralTotal += uint64(nd.Resources.EphemeralStorage.Allocatable.Value())

		avail := nd.Resources.CPU.Quantity.Available()
		invNode.Available.CPU = uint64(avail.MilliValue())
		cpuAvailable += invNode.Available.CPU

		avail = nd.Resources.GPU.Quantity.Available()
		invNode.Available.GPU = uint64(avail.Value())
		gpuAvailable += invNode.Available.GPU

		avail = nd.Resources.Memory.Quantity.Available()
		invNode.Available.Memory = uint64(avail.Value())
		memoryAvailable += invNode.Available.Memory

		avail = nd.Resources.EphemeralStorage.Available()
		invNode.Available.StorageEphemeral = uint64(avail.Value())
		storageEphemeralAvailable += invNode.Available.StorageEphemeral

		ret.Nodes = append(ret.Nodes, invNode)
	}

	for _, class := range inv.Storage {
		tmp := class.Quantity.Allocatable.DeepCopy()
		storageTotal[class.Info.Class] = tmp.Value()

		tmp = *class.Quantity.Available()
		storageAvailable[class.Info.Class] = tmp.Value()
	}

	ret.TotalAllocatable = inventoryV1.MetricTotal{
		CPU:              cpuTotal,
		GPU:              gpuTotal,
		Memory:           memoryTotal,
		StorageEphemeral: storageEphemeralTotal,
		Storage:          storageTotal,
	}

	ret.TotalAvailable = inventoryV1.MetricTotal{
		CPU:              cpuAvailable,
		GPU:              gpuAvailable,
		Memory:           memoryAvailable,
		StorageEphemeral: storageEphemeralAvailable,
		Storage:          storageAvailable,
	}

	return ret
}

func sParamsEnsureGPU(sparams *crd.SchedulerParams) {
	sParamsEnsureResources(sparams)

	if sparams.Resources.GPU == nil {
		sparams.Resources.GPU = &crd.SchedulerResourceGPU{}
	}
}

func sParamsEnsureResources(sparams *crd.SchedulerParams) {
	if sparams.Resources == nil {
		sparams.Resources = &crd.SchedulerResources{}
	}
}
