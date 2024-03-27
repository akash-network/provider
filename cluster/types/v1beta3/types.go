package v1beta3

import (
	"bufio"
	"context"
	"io"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	eventsv1 "k8s.io/api/events/v1"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
)

type ProviderResourceEvent string

const (
	ProviderResourceAdd    = ProviderResourceEvent("add")
	ProviderResourceUpdate = ProviderResourceEvent("update")
	ProviderResourceDelete = ProviderResourceEvent("delete")
)

var (
	// ErrInsufficientCapacity is the new error when capacity is insufficient
	ErrInsufficientCapacity  = errors.New("insufficient capacity")
	ErrGroupResourceMismatch = errors.New("group resource mismatch")
)

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
	Metrics() inventoryV1.Metrics
	Snapshot() inventoryV1.Cluster
	Dup() Inventory
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

// Status stores current leases and inventory statuses
type Status struct {
	Leases    uint32                       `json:"leases"`
	Inventory inventoryV1.InventoryMetrics `json:"inventory"`
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

//go:generate mockery --name HostnameServiceClient --output ./mocks
type HostnameServiceClient interface {
	ReserveHostnames(ctx context.Context, hostnames []string, leaseID mtypes.LeaseID) ([]string, error)
	ReleaseHostnames(leaseID mtypes.LeaseID) error
	CanReserveHostnames(hostnames []string, ownerAddr sdktypes.Address) error
	PrepareHostnamesForTransfer(ctx context.Context, hostnames []string, leaseID mtypes.LeaseID) error
}
