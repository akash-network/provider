package v1beta3

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	eventsv1 "k8s.io/api/events/v1"

	"github.com/akash-network/node/sdl"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/akash-network/akash-api/go/util/units"
)

var (
	// ErrInsufficientCapacity is the new error when capacity is insufficient
	ErrInsufficientCapacity  = errors.New("insufficient capacity")
	ErrGroupResourceMismatch = errors.New("group resource mismatch")
)

// Status stores current leases and inventory statuses
type Status struct {
	Leases    uint32          `json:"leases"`
	Inventory InventoryStatus `json:"inventory"`
}

type InventoryMetricTotal struct {
	CPU              uint64           `json:"cpu"`
	GPU              uint64           `json:"gpu"`
	Memory           uint64           `json:"memory"`
	StorageEphemeral uint64           `json:"storage_ephemeral"`
	Storage          map[string]int64 `json:"storage,omitempty"`
}

type InventoryStorageStatus struct {
	Class string `json:"class"`
	Size  int64  `json:"size"`
}

// InventoryStatus stores active, pending and available units
type InventoryStatus struct {
	Active    []InventoryMetricTotal `json:"active,omitempty"`
	Pending   []InventoryMetricTotal `json:"pending,omitempty"`
	Available struct {
		Nodes   []InventoryNodeMetric    `json:"nodes,omitempty"`
		Storage []InventoryStorageStatus `json:"storage,omitempty"`
	} `json:"available,omitempty"`
	Error error `json:"error,omitempty"`
}

type InventoryNodeMetric struct {
	CPU              uint64 `json:"cpu"`
	GPU              uint64 `json:"gpu"`
	Memory           uint64 `json:"memory"`
	StorageEphemeral uint64 `json:"storage_ephemeral"`
}

type GPUModelAttributes struct {
	RAM       string
	Interface string
}

type GPUModels map[string]*GPUModelAttributes

type GPUAttributes map[string]GPUModels

type StorageAttributes struct {
	Persistent bool   `json:"persistent"`
	Class      string `json:"class,omitempty"`
}

func (m GPUModels) ExistsOrWildcard(model string) (*GPUModelAttributes, bool) {
	attr, exists := m[model]
	if exists {
		return attr, true
	}

	attr, exists = m["*"]

	return attr, exists
}

func ParseGPUAttributes(attrs types.Attributes) (GPUAttributes, error) {
	nvidia := make(GPUModels)
	amd := make(GPUModels)

	for _, attr := range attrs {
		tokens := strings.Split(attr.Key, "/")
		if len(tokens) < 4 || len(tokens)%2 != 0 {
			return GPUAttributes{}, fmt.Errorf("invalid GPU attribute") // nolint: goerr113
		}

		switch tokens[0] {
		case "vendor":
		default:
			return GPUAttributes{}, fmt.Errorf("unexpected GPU attribute type (%s)", tokens[0]) // nolint: goerr113
		}

		switch tokens[2] {
		case "model":
		default:
			return GPUAttributes{}, fmt.Errorf("unexpected GPU attribute type (%s)", tokens[2]) // nolint: goerr113
		}

		vendor := tokens[1]
		model := tokens[3]

		var mattrs *GPUModelAttributes

		if len(tokens) > 4 {
			mattrs = &GPUModelAttributes{}

			tokens = tokens[4:]

			for i := 0; i < len(tokens); i += 2 {
				key := tokens[i]
				val := tokens[i+1]

				switch key {
				case "ram":
					q, err := units.MemoryQuantityFromString(val)
					if err != nil {
						return GPUAttributes{}, err
					}

					mattrs.RAM = q.StringWithSuffix("Gi")
				case "interface":
					switch val {
					case "pcie":
					case "sxm":
					default:
						return GPUAttributes{}, fmt.Errorf("unsupported GPU interface (%s)", val) // nolint: goerr113
					}

					mattrs.Interface = val
				default:
				}
			}
		}

		switch vendor {
		case "nvidia":
			nvidia[model] = mattrs
		case "amd":
			amd[model] = mattrs
		default:
			return GPUAttributes{}, fmt.Errorf("unsupported GPU vendor (%s)", vendor) // nolint: goerr113
		}

	}

	res := make(GPUAttributes)
	if len(nvidia) > 0 {
		res["nvidia"] = nvidia
	}

	if len(amd) > 0 {
		res["amd"] = amd
	}

	return res, nil
}

func ParseStorageAttributes(attrs types.Attributes) (StorageAttributes, error) {
	attr := attrs.Find(sdl.StorageAttributePersistent)
	persistent, _ := attr.AsBool()
	attr = attrs.Find(sdl.StorageAttributeClass)
	class, _ := attr.AsString()

	if persistent && class == "" {
		return StorageAttributes{}, fmt.Errorf("persistent volume must specify storage class") // nolint: goerr113
	}

	res := StorageAttributes{
		Persistent: persistent,
		Class:      class,
	}

	return res, nil
}

func (inv *InventoryMetricTotal) AddResources(res dtypes.ResourceUnit) {
	cpu := sdk.NewIntFromUint64(inv.CPU)
	gpu := sdk.NewIntFromUint64(inv.GPU)
	mem := sdk.NewIntFromUint64(inv.Memory)
	ephemeralStorage := sdk.NewIntFromUint64(inv.StorageEphemeral)

	if res.CPU != nil {
		cpu = cpu.Add(res.CPU.Units.Val.MulRaw(int64(res.Count)))
	}

	if res.GPU != nil {
		gpu = gpu.Add(res.GPU.Units.Val.MulRaw(int64(res.Count)))
	}

	if res.Memory != nil {
		mem = mem.Add(res.Memory.Quantity.Val.MulRaw(int64(res.Count)))
	}

	for _, storage := range res.Storage {
		if storageClass, found := storage.Attributes.Find(sdl.StorageAttributeClass).AsString(); !found {
			ephemeralStorage = ephemeralStorage.Add(storage.Quantity.Val.MulRaw(int64(res.Count)))
		} else {
			val := sdk.NewIntFromUint64(uint64(inv.Storage[storageClass]))
			val = val.Add(storage.Quantity.Val.MulRaw(int64(res.Count)))
			inv.Storage[storageClass] = val.Int64()
		}
	}

	inv.CPU = cpu.Uint64()
	inv.GPU = gpu.Uint64()
	inv.Memory = mem.Uint64()
	inv.StorageEphemeral = ephemeralStorage.Uint64()
}

type InventoryNode struct {
	Name        string              `json:"name"`
	Allocatable InventoryNodeMetric `json:"allocatable"`
	Available   InventoryNodeMetric `json:"available"`
}

type InventoryMetrics struct {
	Nodes            []InventoryNode      `json:"nodes"`
	TotalAllocatable InventoryMetricTotal `json:"total_allocatable"`
	TotalAvailable   InventoryMetricTotal `json:"total_available"`
}

// ServiceStatus stores the current status of service
type ServiceStatus struct {
	Name      string   `json:"name"`
	Available int32    `json:"available"`
	Total     int32    `json:"total"`
	URIs      []string `json:"uris"`

	ObservedGeneration int64 `json:"observed_generation"`
	Replicas           int32 `json:"replicas"`
	UpdatedReplicas    int32 `json:"updated_replicas"`
	ReadyReplicas      int32 `json:"ready_replicas"`
	AvailableReplicas  int32 `json:"available_replicas"`
}

type ForwardedPortStatus struct {
	Host         string                   `json:"host,omitempty"`
	Port         uint16                   `json:"port"`
	ExternalPort uint16                   `json:"externalPort"`
	Proto        manifest.ServiceProtocol `json:"proto"`
	Name         string                   `json:"name"`
}

// LeaseStatus includes list of services with their status
type LeaseStatus struct {
	Services       map[string]*ServiceStatus        `json:"services"`
	ForwardedPorts map[string][]ForwardedPortStatus `json:"forwarded_ports"` // Container services that are externally accessible
}

type InventoryOptions struct {
	DryRun bool
}

type InventoryOption func(*InventoryOptions) *InventoryOptions

func WithDryRun() InventoryOption {
	return func(opts *InventoryOptions) *InventoryOptions {
		opts.DryRun = true
		return opts
	}
}

type Inventory interface {
	Adjust(ReservationGroup, ...InventoryOption) error
	Metrics() InventoryMetrics
	Snapshot() inventoryV1.Cluster
}

// ServiceLog stores name, stream and scanner
type ServiceLog struct {
	Name    string
	Stream  io.ReadCloser
	Scanner *bufio.Scanner
}

type LeaseEventObject struct {
	Kind      string `json:"kind" yaml:"kind"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Name      string `json:"name" yaml:"name"`
}

type LeaseEvent struct {
	Type                string           `json:"type" yaml:"type"`
	ReportingController string           `json:"reportingController,omitempty" yaml:"reportingController"`
	ReportingInstance   string           `json:"reportingInstance,omitempty" yaml:"reportingInstance"`
	Reason              string           `json:"reason" yaml:"reason"`
	Note                string           `json:"note" yaml:"note"`
	Object              LeaseEventObject `json:"object" yaml:"object"`
}

type EventsWatcher interface {
	Shutdown()
	Done() <-chan struct{}
	ResultChan() <-chan *eventsv1.Event
	SendEvent(*eventsv1.Event) bool
}

type eventsFeed struct {
	ctx    context.Context
	cancel func()
	feed   chan *eventsv1.Event
}

var _ EventsWatcher = (*eventsFeed)(nil)

func NewEventsFeed(ctx context.Context) EventsWatcher {
	ctx, cancel := context.WithCancel(ctx)
	return &eventsFeed{
		ctx:    ctx,
		cancel: cancel,
		feed:   make(chan *eventsv1.Event),
	}
}

func (e *eventsFeed) Shutdown() {
	e.cancel()
}

func (e *eventsFeed) Done() <-chan struct{} {
	return e.ctx.Done()
}

func (e *eventsFeed) SendEvent(evt *eventsv1.Event) bool {
	select {
	case e.feed <- evt:
		return true
	case <-e.ctx.Done():
		return false
	}
}

func (e *eventsFeed) ResultChan() <-chan *eventsv1.Event {
	return e.feed
}

type ExecResult interface {
	ExitCode() int
}
