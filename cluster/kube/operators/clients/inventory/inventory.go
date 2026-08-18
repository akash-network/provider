package inventory

import (
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
	inventoryV1 "pkg.akt.dev/go/inventory/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cinventory "github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

var (
	_ ctypes.Inventory = (*inventory)(nil)

	quantityZero = resource.NewQuantity(0, resource.DecimalSI)
)

func newInventory(clState inventoryV1.Cluster) *inventory {
	raw := *clState.Dup()
	sanitized := *clState.Dup()
	sanitizeCluster(&sanitized)

	inv := &inventory{
		Cluster:   raw,
		sanitized: sanitized,
	}

	return inv
}

func (inv *inventory) dup() inventory {
	dup := inventory{
		Cluster:   *inv.Cluster.Dup(),
		sanitized: *inv.sanitized.Dup(),
	}

	return dup
}

func (inv *inventory) sanitizedDup() inventory {
	dup := inventory{
		Cluster:   *inv.sanitized.Dup(),
		sanitized: *inv.sanitized.Dup(),
	}

	return dup
}

func (inv *inventory) Dup() ctypes.Inventory {
	dup := inv.dup()

	return &dup
}

func sanitizeCluster(cluster *inventoryV1.Cluster) {
	for idx := range cluster.Nodes {
		sanitizeNodeResources(&cluster.Nodes[idx].Resources)
	}

	for idx := range cluster.Storage {
		sanitizeResourcePair(&cluster.Storage[idx].Quantity)
	}
}

func sanitizeNodeResources(resources *inventoryV1.NodeResources) {
	sanitizeResourcePair(&resources.CPU.Quantity)
	sanitizeResourcePair(&resources.Memory.Quantity)
	sanitizeResourcePair(&resources.GPU.Quantity)
	sanitizeResourcePair(&resources.EphemeralStorage)
	sanitizeResourcePair(&resources.VolumesAttached)
	sanitizeResourcePair(&resources.VolumesMounted)
	sanitizeResourcePair(&resources.GPUInterconnect)
}

func sanitizeResourcePair(pair *inventoryV1.ResourcePair) {
	sanitizeQuantity(pair.Capacity)
	sanitizeQuantity(pair.Allocatable)
	sanitizeQuantity(pair.Allocated)
}

func sanitizeQuantity(quantity *resource.Quantity) {
	// Zero-value ResourcePairs (e.g. GPUInterconnect on nodes without an
	// RDMA device plugin, or sparse test fixtures) carry nil quantities.
	if quantity == nil {
		return
	}
	if quantity.Cmp(*quantityZero) < 0 {
		quantity.Set(0)
	}
}

// tryAdjust cluster inventory
// It returns two boolean values. First indicates if node-wide resources satisfy (true) requirements
// Seconds indicates if cluster-wide resources satisfy (true) requirements
//
// teeType/ccRuntimeClass carry the validated confidential-compute selection
// through to tryAdjustGPU.
//
// requiredFabric is the placement-level interconnect fabric pin extracted by the
// caller from `Reservation.Resources()` via PlacementRequiredFabric. Empty
// string means no fabric pin (any interconnect fabric satisfies the bid); a
// non-empty value must equal node.Capabilities.InterconnectFabric for the bid to
// stick. Pulled at the Adjust level so the type-switch only runs once per
// bid, not once per (node, replica) attempt.
func (inv *inventory) tryAdjust(
	node int,
	res *rtypes.Resources,
	teeType ctypes.TEEType,
	ccRuntimeClass builder.RuntimeClass,
	requiredFabric string,
	requiresInterconnect bool,
	interconnectGroup string,
	groupClaims map[string]map[int]bool,
) (*crd.SchedulerParams, bool, bool) {
	// AKT-443: enforce per-interconnect_group node separation at fit time.
	// Resources sharing a group label must land on distinct nodes (so the
	// workload builder's pod anti-affinity is satisfiable). Reject the
	// node early if this group has already claimed it; the surrounding
	// Adjust loop will then walk to the next node. (Indexing a nil inner
	// map yields false, so no presence check is needed.)
	if interconnectGroup != "" && groupClaims[interconnectGroup][node] {
		return nil, false, true
	}

	// Interconnect suitability depends only on immutable node capabilities,
	// so reject unsuitable nodes before paying for the full node Dup below —
	// on mostly-non-RDMA clusters this skips the deep copy for every
	// non-interconnect node on every replica attempt.
	if requiresInterconnect && !interconnectCapsSuitable(inv.Nodes[node].Capabilities, requiredFabric) {
		return nil, false, true
	}

	nd := inv.Nodes[node].Dup()
	sparams := &crd.SchedulerParams{}

	if !tryAdjustCPU(&nd.Resources.CPU.Quantity, res.CPU) {
		return nil, false, true
	}

	// tryAdjustInterconnect before tryAdjustGPU so the interconnect stamp lands even if
	// GPU adjust is about to clobber res.GPU.Attributes. We rely on the
	// caller's `requiresInterconnect` flag (pulled from the pristine resource)
	// rather than re-reading attributes here — see the helper's doc.
	if !tryAdjustInterconnect(&nd.Resources.GPUInterconnect, nd.Capabilities, res, sparams, requiredFabric, requiresInterconnect) {
		return nil, false, true
	}

	if !tryAdjustGPU(&nd.Resources.GPU, res.GPU, sparams, teeType, ccRuntimeClass) {
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

	// Confidential compute: set runtime class for CPU-only CC (GPU path
	// sets it in tryAdjustGPU) and reserve sidecar resources.
	if teeType.IsCC() {
		if sparams.RuntimeClass == "" {
			sparams.RuntimeClass = ccRuntimeClass
		}

		sidecarCPU := rtypes.NewResourceValue(uint64(builder.SidecarCPULimitMillicores))
		if !nd.Resources.CPU.Quantity.SubMilliNLZ(sidecarCPU) {
			return nil, false, true
		}

		sidecarMemBytes := builder.SidecarMemoryLimitBytes
		if sparams.RuntimeClass.Is(builder.WithGPU()) {
			sidecarMemBytes = builder.SidecarGPUMemoryLimitBytes
		}
		sidecarMem := rtypes.NewResourceValue(uint64(sidecarMemBytes)) //nolint:gosec // positive constant
		if !nd.Resources.Memory.Quantity.SubNLZ(sidecarMem) {
			return nil, false, true
		}
	}

	// all requirements for current group have been satisfied
	// commit and move on
	inv.Nodes[node] = nd
	inv.Storage = storageClasses

	// AKT-443: register this resource's interconnect_group claim on the node so
	// peers in the same group are forced onto distinct nodes by the early
	// rejection at the top of this function. We register only after all
	// the per-node-resource gates have passed, so a partial-fit attempt
	// never poisons the group→nodes map.
	if interconnectGroup != "" {
		if groupClaims[interconnectGroup] == nil {
			groupClaims[interconnectGroup] = map[int]bool{}
		}
		groupClaims[interconnectGroup][node] = true
	}

	if reflect.DeepEqual(sparams, &crd.SchedulerParams{}) {
		return nil, true, true
	}

	return sparams, true, true
}

func tryAdjustCPU(rp *inventoryV1.ResourcePair, res *rtypes.CPU) bool {
	return rp.SubMilliNLZ(res.Units)
}

func tryAdjustGPU(rp *inventoryV1.GPU, res *rtypes.GPU, sparams *crd.SchedulerParams, teeType ctypes.TEEType, ccRuntimeClass builder.RuntimeClass) bool {
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
			if (attr.RAM != "") && (attr.RAM != info.MemorySize) {
				continue
			}

			if (attr.Interface != "") && (attr.Interface != ctypes.FilterGPUInterface(info.Interface)) {
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

			if teeType.IsCC() {
				sparams.RuntimeClass = ccRuntimeClass
			} else {
				switch vendor {
				case builder.GPUVendorNvidia:
					sparams.RuntimeClass = runtimeClassNvidia
				default:
				}
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

			res.Attributes = attrtypes.Attributes{
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

// tryAdjustInterconnect pins one interconnect HCA per GPU unit (the locked 1:1 invariant)
// when the per-resource opt-in `gpu.attributes.interconnect=true` is set, and
// stamps the SchedulerParams the workload builder later turns into a
// kubelet resource request plus NCCL env vars.
//
// Returns true on a no-op (resource does not require interconnect), on a
// successful allocation, and false when the node is unsuitable. False
// here is node-scoped (`nStatus=false` upstream) so the caller will try
// the next node, not abort the bid.
//
// Suitability gates:
//   - The placement-level fabric pin (if any) matches the node's fabric.
//   - The node actually advertises an interconnect fabric and a kubelet extended
//     resource name (NodeCapabilities from P-1's inventory operator).
//   - There is GPU interconnect capacity for `gpu.units` HCAs (1:1).
//
// GPU presence is guaranteed by ResourceRequiresInterconnect — it only returns
// true when res.GPU is non-nil with the interconnect=true attribute. The
// chain-SDK SDL parser additionally rejects gpu.units==0 with interconnect=true.
// tryAdjustInterconnect takes `required` as an explicit bool instead of
// re-reading res.GPU.Attributes because tryAdjustGPU clobbers
// res.Attributes on the FIRST replica's pass (replaces the attribute
// slice with a single synthesized vendor entry). On replica 2+, the
// adjusted dup is taken from the already-clobbered slice, so a
// ResourceRequiresInterconnect check here would falsely report "no interconnect needed"
// and skip the SchedulerParams.Resources.Interconnect stamp — the per-replica
// DeepEqual in Adjust would then reject the bid with
// ErrGroupResourceMismatch. The caller pulls `required` from the
// pristine origResources once before any mutation runs.
func tryAdjustInterconnect(
	rp *inventoryV1.ResourcePair,
	capabilities inventoryV1.NodeCapabilities,
	res *rtypes.Resources,
	sparams *crd.SchedulerParams,
	requiredFabric string,
	required bool,
) bool {
	if !required {
		return true
	}

	if !interconnectCapsSuitable(capabilities, requiredFabric) {
		return false
	}

	if res.GPU == nil || !rp.SubNLZ(res.GPU.Units) {
		return false
	}

	sParamsEnsureResources(sparams)
	sparams.Resources.Interconnect = &crd.SchedulerResourceInterconnect{
		Enabled:         true,
		Units:           res.GPU.Units.Value(),
		ResourceName:    capabilities.InterconnectResourceName,
		Fabric:          capabilities.InterconnectFabric,
		NCCLHCAPrefixes: append([]string(nil), capabilities.NCCLHCAPrefixes...),
	}

	return true
}

func tryAdjustEphemeralStorage(rp *inventoryV1.ResourcePair, res *rtypes.Storage) bool {
	return rp.SubNLZ(res.Quantity)
}

// nolint: unused
func tryAdjustVolumesAttached(rp *inventoryV1.ResourcePair, res rtypes.ResourceValue) bool {
	return rp.SubNLZ(res)
}

func (inv *inventory) Adjust(reservation ctypes.ReservationGroup, opts ...ctypes.InventoryOption) error {
	cfg := &ctypes.InventoryOptions{}
	for _, opt := range opts {
		cfg = opt(cfg)
	}

	ccRuntimeClass := builder.RuntimeClass("")
	if cfg.TEEType.IsCC() {
		var err error
		ccRuntimeClass, err = builder.RuntimeClassForTEEType(cfg.TEEType, cfg.TEEPlatform)
		if err != nil {
			return err
		}
	}

	origResources := reservation.Resources().GetResourceUnits()
	resources := make(dvbeta.ResourceUnits, 0, len(origResources))
	adjustedResources := make(dvbeta.ResourceUnits, 0, len(origResources))

	for _, res := range origResources {
		resources = append(resources, dvbeta.ResourceUnit{
			Resources: res.Resources.Dup(),
			Count:     res.Count,
		})

		adjustedResources = append(adjustedResources, dvbeta.ResourceUnit{
			Resources: res.Resources.Dup(),
			Count:     res.Count,
		})
	}

	cparams := make(crd.ReservationClusterSettings)

	currInventory := inv.sanitizedDup()

	// Extract the deployment-group's interconnect fabric pin once. tryAdjust
	// consults it per (node, replica) attempt; computing it here keeps the
	// ResourceGroup type-switch off the hot path.
	requiredFabric, _ := PlacementRequiredFabric(reservation.Resources())

	// Per-resource interconnect opt-in flag + group label, both from one
	// attribute walk. Read from origResources because tryAdjustGPU mutates
	// res.GPU.Attributes on each pass, dropping the interconnect opt-in.
	// For services with count > 1 the second-and-later replica's adjusted
	// slice is a Dup of the already-clobbered state, so re-reading
	// attributes inside tryAdjustInterconnect would falsely report "no
	// interconnect needed." Computed once here, threaded through (AKT-443).
	requiresInterconnect := make([]bool, len(origResources))
	interconnectGroup := make([]string, len(origResources))
	for i := range origResources {
		interconnectGroup[i], requiresInterconnect[i] = ResourceInterconnectGroup(origResources[i].Resources)

		// The group name is stamped verbatim as a pod label value and
		// anti-affinity selector by the workload builder. Kubernetes
		// rejects label values outside [A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?
		// or longer than 63 chars at admission — refuse the bid up front
		// rather than winning a lease whose workloads can never deploy.
		if requiresInterconnect[i] {
			if errs := validation.IsValidLabelValue(interconnectGroup[i]); len(errs) > 0 {
				return fmt.Errorf("%w: %q: %s",
					ctypes.ErrInvalidInterconnectGroup, interconnectGroup[i], strings.Join(errs, "; "))
			}
		}
	}

	// AKT-443: per-interconnect_group set of node indices already claimed in this
	// bid attempt. Scoped to this Adjust call (one bid) so two unrelated
	// orders cannot interfere. A successful tryAdjust commits the entry;
	// a rejection on any subsequent gate never touches it.
	groupClaims := map[string]map[int]bool{}

	var err error

nodes:
	for nodeIdx := range currInventory.Nodes {
		for i := len(resources) - 1; i >= 0; i-- {
			adjustedGroup := false

			var adjusted *rtypes.Resources
			if origResources[i].Count == resources[i].Count {
				adjusted = &adjustedResources[i].Resources
			} else {
				adjustedGroup = true
				res := adjustedResources[i].Resources.Dup()
				adjusted = &res
			}

			for ; resources[i].Count > 0; resources[i].Count-- {
				sparams, nStatus, cStatus := currInventory.tryAdjust(nodeIdx, adjusted, cfg.TEEType, ccRuntimeClass, requiredFabric, requiresInterconnect[i], interconnectGroup[i], groupClaims)
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
						err = ctypes.ErrGroupResourceMismatch
						break nodes
					}

					// all replicas of the same service are expected to have same node selectors and runtimes
					// if they don't match then provider cannot bid
					if !reflect.DeepEqual(sparams, cparams[adjusted.ID]) {
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
			inv.sanitized = *currInventory.Cluster.Dup()
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
	return *inv.sanitized.Dup()
}

func (inv *inventory) Metrics() inventoryV1.Metrics {
	sanitized := inv.sanitizedDup()

	cpuTotal := uint64(0)
	gpuTotal := uint64(0)
	memoryTotal := uint64(0)
	storageEphemeralTotal := uint64(0)
	storageTotal := make(map[string]uint64)

	cpuAvailable := uint64(0)
	gpuAvailable := uint64(0)
	memoryAvailable := uint64(0)
	storageEphemeralAvailable := uint64(0)
	storageAvailable := make(map[string]uint64)

	ret := inventoryV1.Metrics{
		Nodes: make([]inventoryV1.NodeMetrics, 0, len(sanitized.Nodes)),
	}

	for _, nd := range sanitized.Nodes {
		invNode := inventoryV1.NodeMetrics{
			Name: nd.Name,
			Allocatable: inventoryV1.ResourcesMetric{
				CPU:              uint64(nd.Resources.CPU.Quantity.Allocatable.MilliValue()), // nolint: gosec
				GPU:              uint64(nd.Resources.GPU.Quantity.Allocatable.Value()),      // nolint: gosec
				Memory:           uint64(nd.Resources.Memory.Quantity.Allocatable.Value()),   // nolint: gosec
				StorageEphemeral: uint64(nd.Resources.EphemeralStorage.Allocatable.Value()),  // nolint: gosec
			},
		}

		cpuTotal += uint64(nd.Resources.CPU.Quantity.Allocatable.MilliValue())             // nolint: gosec
		gpuTotal += uint64(nd.Resources.GPU.Quantity.Allocatable.Value())                  // nolint: gosec
		memoryTotal += uint64(nd.Resources.Memory.Quantity.Allocatable.Value())            // nolint: gosec
		storageEphemeralTotal += uint64(nd.Resources.EphemeralStorage.Allocatable.Value()) // nolint: gosec

		avail := nd.Resources.CPU.Quantity.Available()
		invNode.Available.CPU = uint64(avail.MilliValue()) // nolint: gosec
		cpuAvailable += invNode.Available.CPU

		avail = nd.Resources.GPU.Quantity.Available()
		invNode.Available.GPU = uint64(avail.Value()) // nolint: gosec
		gpuAvailable += invNode.Available.GPU

		avail = nd.Resources.Memory.Quantity.Available()
		invNode.Available.Memory = uint64(avail.Value()) // nolint: gosec
		memoryAvailable += invNode.Available.Memory

		avail = nd.Resources.EphemeralStorage.Available()
		invNode.Available.StorageEphemeral = uint64(avail.Value()) // nolint: gosec
		storageEphemeralAvailable += invNode.Available.StorageEphemeral

		ret.Nodes = append(ret.Nodes, invNode)
	}

	for _, class := range sanitized.Storage {
		tmp := class.Quantity.Allocatable.DeepCopy()
		storageTotal[class.Info.Class] = uint64(tmp.Value()) //nolint: gosec

		tmp = *class.Quantity.Available()
		storageAvailable[class.Info.Class] = uint64(tmp.Value()) //nolint: gosec
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
