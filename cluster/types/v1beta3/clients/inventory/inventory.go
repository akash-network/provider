package inventory

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"k8s.io/apimachinery/pkg/api/resource"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	"github.com/akash-network/akash-api/go/node/types/unit"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"

	"github.com/akash-network/node/sdl"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

const (
	// 5 CPUs, 5Gi memory for null client.
	nullClientCPU     = 5000
	nullClientGPU     = 2
	nullClientMemory  = 32 * unit.Gi
	nullClientStorage = 512 * unit.Gi
)

type Client interface {
	// ResultChan returns a chan which will receive all the events. If an error occurs
	// or Stop() is called, the implementation will close this channel and
	// release any resources used by the watch.
	ResultChan() <-chan ctypes.Inventory
}

type nullInventory struct {
	inv *inventory
}

type resourcePair struct {
	allocatable sdk.Int
	allocated   sdk.Int
}

type storageClassState struct {
	resourcePair
	isDefault bool
}

func (rp *resourcePair) dup() resourcePair {
	return resourcePair{
		allocatable: rp.allocatable.AddRaw(0),
		allocated:   rp.allocated.AddRaw(0),
	}
}

func (rp *resourcePair) subNLZ(val types.ResourceValue) bool {
	avail := rp.available()

	res := avail.Sub(val.Val)
	if res.IsNegative() {
		return false
	}

	*rp = resourcePair{
		allocatable: rp.allocatable.AddRaw(0),
		allocated:   rp.allocated.Add(val.Val),
	}

	return true
}

func (rp *resourcePair) available() sdk.Int {
	return rp.allocatable.Sub(rp.allocated)
}

type node struct {
	id               string
	cpu              resourcePair
	gpu              resourcePair
	memory           resourcePair
	ephemeralStorage resourcePair
}

type clusterStorage map[string]*storageClassState

func (cs clusterStorage) dup() clusterStorage {
	res := make(clusterStorage)
	for k, v := range cs {
		res[k] = &storageClassState{
			resourcePair: v.resourcePair.dup(),
			isDefault:    v.isDefault,
		}
	}

	return res
}

type inventory struct {
	storage clusterStorage
	nodes   []*node
}

var (
	_ Client           = (*nullInventory)(nil)
	_ ctypes.Inventory = (*inventory)(nil)
)

func NewNull(nodes ...string) Client {
	inv := &inventory{
		nodes: make([]*node, 0, len(nodes)),
		storage: map[string]*storageClassState{
			"beta2": {
				resourcePair: resourcePair{
					allocatable: sdk.NewInt(nullClientStorage),
					allocated:   sdk.NewInt(nullClientStorage - (10 * unit.Gi)),
				},
				isDefault: true,
			},
		},
	}

	for _, ndName := range nodes {
		nd := &node{
			id: ndName,
			cpu: resourcePair{
				allocatable: sdk.NewInt(nullClientCPU),
				allocated:   sdk.NewInt(100),
			},
			gpu: resourcePair{
				allocatable: sdk.NewInt(nullClientGPU),
				allocated:   sdk.NewInt(1),
			},
			memory: resourcePair{
				allocatable: sdk.NewInt(nullClientMemory),
				allocated:   sdk.NewInt(1 * unit.Gi),
			},
			ephemeralStorage: resourcePair{
				allocatable: sdk.NewInt(nullClientStorage),
				allocated:   sdk.NewInt(10 * unit.Gi),
			},
		}

		inv.nodes = append(inv.nodes, nd)
	}

	if len(inv.nodes) == 0 {
		inv.nodes = append(inv.nodes, &node{
			id: "solo",
			cpu: resourcePair{
				allocatable: sdk.NewInt(nullClientCPU),
				allocated:   sdk.NewInt(nullClientCPU - 100),
			},
			gpu: resourcePair{
				allocatable: sdk.NewInt(nullClientGPU),
				allocated:   sdk.NewInt(1),
			},
			memory: resourcePair{
				allocatable: sdk.NewInt(nullClientMemory),
				allocated:   sdk.NewInt(nullClientMemory - unit.Gi),
			},
			ephemeralStorage: resourcePair{
				allocatable: sdk.NewInt(nullClientStorage),
				allocated:   sdk.NewInt(nullClientStorage - (10 * unit.Gi)),
			},
		})
	}

	return &nullInventory{
		inv: inv,
	}
}

func (inv *nullInventory) ResultChan() <-chan ctypes.Inventory {
	ch := make(chan ctypes.Inventory, 1)

	ch <- inv.inv.dup()

	return ch
}

func (inv *inventory) Adjust(reservation ctypes.ReservationGroup, _ ...ctypes.InventoryOption) error {
	resources := make(dtypes.ResourceUnits, len(reservation.Resources().GetResourceUnits()))
	copy(resources, reservation.Resources().GetResourceUnits())

	currInventory := inv.dup()

nodes:
	for nodeName, nd := range currInventory.nodes {
		// with persistent storage go through iff there is capacity available
		// there is no point to go through any other node without available storage
		currResources := resources[:0]

		for _, res := range resources {
			for ; res.Count > 0; res.Count-- {
				var adjusted bool

				cpu := nd.cpu.dup()
				if adjusted = cpu.subNLZ(res.Resources.CPU.Units); !adjusted {
					continue nodes
				}

				gpu := nd.gpu.dup()
				if res.Resources.GPU != nil {
					if adjusted = gpu.subNLZ(res.Resources.GPU.Units); !adjusted {
						continue nodes
					}
				}

				memory := nd.memory.dup()
				if adjusted = memory.subNLZ(res.Resources.Memory.Quantity); !adjusted {
					continue nodes
				}

				ephemeralStorage := nd.ephemeralStorage.dup()
				storageClasses := currInventory.storage.dup()

				for idx, storage := range res.Resources.Storage {
					attr := storage.Attributes.Find(sdl.StorageAttributePersistent)

					if persistent, _ := attr.AsBool(); !persistent {
						if adjusted = ephemeralStorage.subNLZ(storage.Quantity); !adjusted {
							continue nodes
						}
						continue
					}

					attr = storage.Attributes.Find(sdl.StorageAttributeClass)
					class, _ := attr.AsString()

					if class == sdl.StorageClassDefault {
						for name, params := range storageClasses {
							if params.isDefault {
								class = name

								for i := range storage.Attributes {
									if storage.Attributes[i].Key == sdl.StorageAttributeClass {
										res.Resources.Storage[idx].Attributes[i].Value = class
										break
									}
								}
								break
							}
						}
					}

					cstorage, activeStorageClass := storageClasses[class]
					if !activeStorageClass {
						continue nodes
					}

					if adjusted = cstorage.subNLZ(storage.Quantity); !adjusted {
						// cluster storage does not have enough space thus break to error
						break nodes
					}
				}

				// all requirements for current group have been satisfied
				// commit and move on
				currInventory.nodes[nodeName] = &node{
					id:               nd.id,
					cpu:              cpu,
					gpu:              gpu,
					memory:           memory,
					ephemeralStorage: ephemeralStorage,
				}
			}

			if res.Count > 0 {
				currResources = append(currResources, res)
			}
		}

		resources = currResources
	}

	if len(resources) == 0 {
		*inv = *currInventory

		return nil
	}

	return ctypes.ErrInsufficientCapacity
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
		Nodes: make([]inventoryV1.NodeMetrics, 0, len(inv.nodes)),
	}

	for _, nd := range inv.nodes {
		invNode := inventoryV1.NodeMetrics{
			Name: nd.id,
			Allocatable: inventoryV1.ResourcesMetric{
				CPU:              nd.cpu.allocatable.Uint64(),
				Memory:           nd.memory.allocatable.Uint64(),
				StorageEphemeral: nd.ephemeralStorage.allocatable.Uint64(),
			},
		}

		cpuTotal += nd.cpu.allocatable.Uint64()
		gpuTotal += nd.gpu.allocatable.Uint64()

		memoryTotal += nd.memory.allocatable.Uint64()
		storageEphemeralTotal += nd.ephemeralStorage.allocatable.Uint64()

		tmp := nd.cpu.allocatable.Sub(nd.cpu.allocated)
		invNode.Available.CPU = tmp.Uint64()
		cpuAvailable += invNode.Available.CPU

		tmp = nd.gpu.allocatable.Sub(nd.gpu.allocated)
		invNode.Available.GPU = tmp.Uint64()
		gpuAvailable += invNode.Available.GPU

		tmp = nd.memory.allocatable.Sub(nd.memory.allocated)
		invNode.Available.Memory = tmp.Uint64()
		memoryAvailable += invNode.Available.Memory

		tmp = nd.ephemeralStorage.allocatable.Sub(nd.ephemeralStorage.allocated)
		invNode.Available.StorageEphemeral = tmp.Uint64()
		storageEphemeralAvailable += invNode.Available.StorageEphemeral

		ret.Nodes = append(ret.Nodes, invNode)
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

func (inv *inventory) Snapshot() inventoryV1.Cluster {
	res := inventoryV1.Cluster{
		Nodes:   make(inventoryV1.Nodes, 0, len(inv.nodes)),
		Storage: make(inventoryV1.ClusterStorage, 0, len(inv.storage)),
	}

	for _, nd := range inv.nodes {
		res.Nodes = append(res.Nodes, inventoryV1.Node{
			Name: nd.id,
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePair(nd.cpu.allocatable.Int64(), nd.cpu.allocated.Int64(), "m"),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(nd.memory.allocatable.Int64(), nd.memory.allocated.Int64(), resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(nd.gpu.allocatable.Int64(), nd.gpu.allocated.Int64(), resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(nd.ephemeralStorage.allocatable.Int64(), nd.ephemeralStorage.allocated.Int64(), resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
			},
			Capabilities: inventoryV1.NodeCapabilities{},
		})
	}

	for class, storage := range inv.storage {
		res.Storage = append(res.Storage, inventoryV1.Storage{
			Quantity: inventoryV1.NewResourcePair(storage.allocatable.Int64(), storage.allocated.Int64(), resource.DecimalSI),
			Info: inventoryV1.StorageInfo{
				Class: class,
			},
		})
	}

	return res
}

func (inv *inventory) dup() *inventory {
	res := &inventory{
		nodes: make([]*node, 0, len(inv.nodes)),
	}

	for _, nd := range inv.nodes {
		res.nodes = append(res.nodes, &node{
			id:               nd.id,
			cpu:              nd.cpu.dup(),
			gpu:              nd.gpu.dup(),
			memory:           nd.memory.dup(),
			ephemeralStorage: nd.ephemeralStorage.dup(),
		})
	}

	return res
}
