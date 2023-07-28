package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/pager"

	"github.com/tendermint/tendermint/libs/log"

	types "github.com/akash-network/akash-api/go/node/types/v1beta3"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const (
	inventoryOperatorQueryTimeout = 5 * time.Second
)

type clusterNodes map[string]*node

type inventory struct {
	storageClasses clusterStorage
	nodes          clusterNodes
	log            log.Logger
}

var _ ctypes.Inventory = (*inventory)(nil)

func newInventory(log log.Logger, storage clusterStorage, nodes map[string]*node) *inventory {
	inv := &inventory{
		storageClasses: storage,
		nodes:          nodes,
		log:            log,
	}

	return inv
}

func (inv *inventory) dup() inventory {
	dup := inventory{
		storageClasses: inv.storageClasses.dup(),
		nodes:          inv.nodes.dup(),
		log:            inv.log,
	}

	return dup
}

// tryAdjust cluster inventory
// It returns two boolean values. First indicates if node-wide resources satisfy (true) requirements
// Seconds indicates if cluster-wide resources satisfy (true) requirements
func (inv *inventory) tryAdjust(node string, res *types.Resources) (*crd.SchedulerParams, bool, bool) {
	nd := inv.nodes[node].dup()
	sparams := &crd.SchedulerParams{}

	if !nd.tryAdjustCPU(res.CPU) {
		return nil, false, true
	}

	if !nd.tryAdjustGPU(res.GPU, sparams) {
		return nil, false, true
	}

	if !nd.tryAdjustMemory(res.Memory) {
		return nil, false, true
	}

	storageClasses := inv.storageClasses.dup()

	for i, storage := range res.Storage {
		attrs, err := ctypes.ParseStorageAttributes(storage.Attributes)
		if err != nil {
			return nil, false, false
		}

		if !attrs.Persistent {
			if !nd.tryAdjustEphemeralStorage(&res.Storage[i]) {
				return nil, false, true
			}
			continue
		}

		if !nd.capabilities.Storage.HasClass(attrs.Class) {
			return nil, false, true
		}

		// if !nd.tryAdjustVolumesAttached(types.NewResourceValue(1)) {
		// 	return nil, false, true
		// }

		// no need to check if storageClass map has class present as it has been validated
		// for particular node during inventory fetch
		if !storageClasses[attrs.Class].subNLZ(storage.Quantity) {
			// cluster storage does not have enough space thus break to error
			return nil, false, false
		}
	}

	// all requirements for current group have been satisfied
	// commit and move on
	inv.nodes[node] = nd
	inv.storageClasses = storageClasses

	if reflect.DeepEqual(sparams, &crd.SchedulerParams{}) {
		return nil, true, true
	}

	return sparams, true, true
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

	cparams := crd.ClusterSettings{
		SchedulerParams: make([]*crd.SchedulerParams, len(reservation.Resources().GetResourceUnits())),
	}

	currInventory := inv.dup()

	var err error

nodes:
	for nodeName := range currInventory.nodes {
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
				sparams, nStatus, cStatus := currInventory.tryAdjust(nodeName, adjusted)
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
					if !reflect.DeepEqual(sparams, cparams.SchedulerParams[i]) {
						jFirstSparams, _ := json.Marshal(cparams.SchedulerParams[i])
						jCurrSparams, _ := json.Marshal(sparams)

						inv.log.Error(fmt.Sprintf("scheduler params mismatch between replicas within group:\n"+
							"\tfirst replica: %s\n"+
							"\tcurr replica: %s", string(jFirstSparams), string(jCurrSparams)))

						err = ctypes.ErrGroupResourceMismatch
						break nodes
					}
				} else {
					cparams.SchedulerParams[i] = sparams
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

func (inv *inventory) Metrics() ctypes.InventoryMetrics {
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

	ret := ctypes.InventoryMetrics{
		Nodes: make([]ctypes.InventoryNode, 0, len(inv.nodes)),
	}

	for nodeName, nd := range inv.nodes {
		invNode := ctypes.InventoryNode{
			Name: nodeName,
			Allocatable: ctypes.InventoryNodeMetric{
				CPU:              uint64(nd.cpu.allocatable.MilliValue()),
				GPU:              uint64(nd.gpu.allocatable.Value()),
				Memory:           uint64(nd.memory.allocatable.Value()),
				StorageEphemeral: uint64(nd.ephemeralStorage.allocatable.Value()),
			},
		}

		cpuTotal += uint64(nd.cpu.allocatable.MilliValue())
		gpuTotal += uint64(nd.gpu.allocatable.Value())
		memoryTotal += uint64(nd.memory.allocatable.Value())
		storageEphemeralTotal += uint64(nd.ephemeralStorage.allocatable.Value())

		avail := nd.cpu.available()
		invNode.Available.CPU = uint64(avail.MilliValue())
		cpuAvailable += invNode.Available.CPU

		avail = nd.gpu.available()
		invNode.Available.GPU = uint64(avail.Value())
		gpuAvailable += invNode.Available.GPU

		avail = nd.memory.available()
		invNode.Available.Memory = uint64(avail.Value())
		memoryAvailable += invNode.Available.Memory

		avail = nd.ephemeralStorage.available()
		invNode.Available.StorageEphemeral = uint64(avail.Value())
		storageEphemeralAvailable += invNode.Available.StorageEphemeral

		ret.Nodes = append(ret.Nodes, invNode)
	}

	for class, storage := range inv.storageClasses {
		tmp := storage.allocatable.DeepCopy()
		storageTotal[class] = tmp.Value()

		tmp = storage.available()
		storageAvailable[class] = tmp.Value()
	}

	ret.TotalAllocatable = ctypes.InventoryMetricTotal{
		CPU:              cpuTotal,
		GPU:              gpuTotal,
		Memory:           memoryTotal,
		StorageEphemeral: storageEphemeralTotal,
		Storage:          storageTotal,
	}

	ret.TotalAvailable = ctypes.InventoryMetricTotal{
		CPU:              cpuAvailable,
		GPU:              gpuAvailable,
		Memory:           memoryAvailable,
		StorageEphemeral: storageEphemeralAvailable,
		Storage:          storageAvailable,
	}

	return ret
}

func (c *client) Inventory(ctx context.Context) (ctypes.Inventory, error) {
	cstorage, err := c.fetchStorage(ctx)
	if err != nil {
		// log inventory operator error but keep going to fetch nodes
		// as provider still may make bids on orders without persistent storage
		c.log.Error("checking storage inventory", "error", err.Error())
	}

	knodes, err := c.fetchActiveNodes(ctx, cstorage)
	if err != nil {
		return nil, err
	}

	return newInventory(c.log.With("kube", "inventory"), cstorage, knodes), nil
}

func (c *client) fetchStorage(ctx context.Context) (clusterStorage, error) {
	ctx, cancel := context.WithTimeout(ctx, inventoryOperatorQueryTimeout)
	defer cancel()

	cstorage := make(clusterStorage)

	// discover inventory operator
	// empty namespace mean search through all namespaces
	svcResult, err := c.kc.CoreV1().Services(corev1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: builder.AkashManagedLabelName + "=true" +
			",app.kubernetes.io/name=akash" +
			",app.kubernetes.io/instance=inventory" +
			",app.kubernetes.io/component=operator",
	})
	if err != nil {
		return nil, err
	}

	if len(svcResult.Items) == 0 {
		return nil, nil
	}

	result := c.kc.CoreV1().RESTClient().Get().
		Namespace(svcResult.Items[0].Namespace).
		Resource("services").
		Name(svcResult.Items[0].Name + ":api").
		SubResource("proxy").
		Suffix("inventory").
		Do(ctx)

	if err := result.Error(); err != nil {
		return nil, err
	}

	inv := &crd.Inventory{}

	if err := result.Into(inv); err != nil {
		return nil, err
	}

	statusPairs := make([]interface{}, 0, len(inv.Status.Messages))
	for idx, msg := range inv.Status.Messages {
		statusPairs = append(statusPairs, fmt.Sprintf("msg%d", idx))
		statusPairs = append(statusPairs, msg)
	}

	if len(statusPairs) > 0 {
		c.log.Info("inventory request performed with warnings", statusPairs...)
	}

	for _, storage := range inv.Spec.Storage {
		if !isSupportedStorageClass(storage.Class) {
			continue
		}

		cstorage[storage.Class] = rpNewFromAkash(storage.ResourcePair)
	}

	return cstorage, nil
}

// todo write unmarshaler
func parseNodeCapabilities(labels map[string]string, cStorage clusterStorage) *crd.NodeInfoCapabilities {
	capabilities := &crd.NodeInfoCapabilities{}

	for k := range labels {
		tokens := strings.Split(k, "/")
		if len(tokens) != 2 && tokens[0] != builder.AkashManagedLabelName {
			continue
		}

		tokens = strings.Split(tokens[1], ".")
		if len(tokens) < 2 || tokens[0] != "capabilities" {
			continue
		}

		tokens = tokens[1:]
		switch tokens[0] {
		case "gpu":
			if len(tokens) < 2 {
				continue
			}

			tokens = tokens[1:]
			if tokens[0] == "vendor" {
				capabilities.GPU.Vendor = tokens[1]
				if tokens[2] == "model" {
					capabilities.GPU.Model = tokens[3]
				}
			}
		case "storage":
			if len(tokens) < 2 {
				continue
			}

			switch tokens[1] {
			case "class":
				capabilities.Storage.Classes = append(capabilities.Storage.Classes, tokens[2])
			default:
			}
		}
	}

	// parse storage classes with legacy mode if new mode is not detected
	if len(capabilities.Storage.Classes) == 0 {
		if value, defined := labels[builder.AkashNetworkStorageClasses]; defined {
			for _, class := range strings.Split(value, ".") {
				if _, avail := cStorage[class]; avail {
					capabilities.Storage.Classes = append(capabilities.Storage.Classes, class)
				}
			}
		}
	}

	return capabilities
}

func (c *client) fetchActiveNodes(ctx context.Context, cstorage clusterStorage) (map[string]*node, error) {
	// todo filter nodes by akash.network label
	knodes, err := wrapKubeCall("nodes-list", func() (*corev1.NodeList, error) {
		return c.kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	})
	if err != nil {
		return nil, err
	}

	podListOptions := metav1.ListOptions{
		FieldSelector: "status.phase==Running",
	}
	podsClient := c.kc.CoreV1().Pods(metav1.NamespaceAll)
	podsPager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return podsClient.List(ctx, opts)
	})

	retnodes := make(map[string]*node)
	for _, knode := range knodes.Items {
		if !c.nodeIsActive(knode) {
			continue
		}

		capabilities := parseNodeCapabilities(knode.Labels, cstorage)

		retnodes[knode.Name] = newNode(&knode.Status, capabilities)
	}

	// Go over each pod and sum the resources for it into the value for the pod it lives on
	err = podsPager.EachListItem(ctx, podListOptions, func(obj runtime.Object) error {
		pod := obj.(*corev1.Pod)
		nodeName := pod.Spec.NodeName

		entry, validNode := retnodes[nodeName]
		if !validNode {
			return nil
		}

		for _, container := range pod.Spec.Containers {
			entry.addAllocatedResources(container.Resources.Requests)
		}

		// Add overhead for running a pod to the sum of requests
		// https://kubernetes.io/docs/concepts/scheduling-eviction/pod-overhead/
		entry.addAllocatedResources(pod.Spec.Overhead)

		retnodes[nodeName] = entry // Map is by value, so store the copy back into the map

		return nil
	})

	if err != nil {
		return nil, err
	}

	return retnodes, nil
}

func (c *client) nodeIsActive(node corev1.Node) bool {
	ready := false
	issues := 0

	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			if cond.Status == corev1.ConditionTrue {
				ready = true
			}
		case corev1.NodeMemoryPressure:
			fallthrough
		case corev1.NodeDiskPressure:
			fallthrough
		case corev1.NodePIDPressure:
			fallthrough
		case corev1.NodeNetworkUnavailable:
			if cond.Status != corev1.ConditionFalse {
				c.log.Error("node in poor condition",
					"node", node.Name,
					"condition", cond.Type,
					"status", cond.Status)

				issues++
			}
		}
	}

	// If the node has been tainted, don't consider it active.
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			issues++
		}
	}

	return ready && issues == 0
}

func isSupportedStorageClass(name string) bool {
	switch name {
	case "default":
		fallthrough
	case "beta1":
		fallthrough
	case "beta2":
		fallthrough
	case "beta3":
		return true
	default:
		return false
	}
}
