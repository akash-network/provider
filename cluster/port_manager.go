package cluster

import (
	"sync"
	"time"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/provider/cluster/portreserve"
	"github.com/tendermint/tendermint/libs/log"
)

// PortManager interface defines all port allocation operations.
// This unified interface improves testability and eliminates the need for separate PortAllocator.
type PortManager interface {
	// Order operations (bidding phase)
	ReserveForOrder(orderID mtypes.OrderID, count int, ttl time.Duration) []int32
	PortsForOrder(orderID mtypes.OrderID) []int32
	ReleaseOrder(orderID mtypes.OrderID)

	// Lease operations (deployment phase)
	PromoteOrderToLease(orderID mtypes.OrderID, leaseID mtypes.LeaseID, ttl time.Duration)
	AllocatePorts(leaseID mtypes.LeaseID, serviceName string, count int) []int32
	ReleaseLease(leaseID mtypes.LeaseID)

	// Maintenance operations
	CleanupExpired() int
	Stats() map[string]interface{}
}

// portManager implements the PortManager interface.
// It manages all port allocation operations directly without separate allocator instances.
type portManager struct {
	store      *portreserve.Store
	log        log.Logger
	leaseState map[string]*leasePortState // Cached port state per lease
	mu         sync.RWMutex               // Protects leaseState map
}

// leasePortState tracks port allocation state for a specific lease
type leasePortState struct {
	ports []int32 // Pre-loaded ports for this lease
	index int     // Next port index to allocate
}

// NewPortManager creates a new PortManager singleton
func NewPortManager(log log.Logger, store *portreserve.Store) PortManager {
	return &portManager{
		store:      store,
		log:        log,
		leaseState: make(map[string]*leasePortState),
	}
}

// Order operations (bidding phase) - delegate directly to store
func (pm *portManager) ReserveForOrder(orderID mtypes.OrderID, count int, ttl time.Duration) []int32 {
	pm.log.Info("Reserving ports for order", "orderID", orderID, "count", count)
	return pm.store.ReserveForOrder(orderID, count, ttl)
}

func (pm *portManager) PortsForOrder(orderID mtypes.OrderID) []int32 {
	return pm.store.PortsForOrder(orderID)
}

func (pm *portManager) ReleaseOrder(orderID mtypes.OrderID) {
	pm.log.Info("Releasing order ports", "orderID", orderID)
	pm.store.ReleaseOrder(orderID)
}

// Lease operations (deployment phase)
func (pm *portManager) PromoteOrderToLease(orderID mtypes.OrderID, leaseID mtypes.LeaseID, ttl time.Duration) {
	pm.log.Info("Promoting order to lease", "orderID", orderID, "leaseID", leaseID)
	pm.store.PromoteOrderToLease(orderID, leaseID, ttl)

	// Pre-load ports for this lease
	pm.ensureLeaseState(leaseID)
}

func (pm *portManager) AllocatePorts(leaseID mtypes.LeaseID, serviceName string, count int) []int32 {
	if count <= 0 {
		return nil
	}

	leaseKey := leaseID.String()
	pm.mu.Lock()
	defer pm.mu.Unlock()

	state, exists := pm.leaseState[leaseKey]
	if !exists {
		pm.log.Info("AllocatePorts: lease state not found, attempting to load from store", "leaseID", leaseID)
		// Try to load lease state from store (for existing leases that weren't promoted)
		pm.mu.Unlock()
		pm.ensureLeaseState(leaseID)
		pm.mu.Lock()

		state, exists = pm.leaseState[leaseKey]
		if !exists {
			pm.log.Error("AllocatePorts: no ports available for lease (not reserved or already allocated)", "leaseID", leaseID)
			return nil
		}
	}

	// Check if we have enough ports available
	available := len(state.ports) - state.index
	if available < count {
		pm.log.Error("Not enough ports available", "leaseID", leaseID, "requested", count, "available", available)
		return nil
	}

	// Allocate the next 'count' ports
	result := make([]int32, count)
	copy(result, state.ports[state.index:state.index+count])
	state.index += count

	pm.log.Info("Allocated ports for service", "leaseID", leaseID, "service", serviceName, "ports", result)
	return result
}

// ensureLeaseState loads ports for a lease if not already cached
func (pm *portManager) ensureLeaseState(leaseID mtypes.LeaseID) {
	leaseKey := leaseID.String()

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.leaseState[leaseKey]; exists {
		return // Already loaded
	}

	// Load all ports for this lease from store
	var ports []int32
	for {
		port := pm.store.NextForLease(leaseID)
		if port == 0 {
			break
		}
		ports = append(ports, port)
	}

	pm.leaseState[leaseKey] = &leasePortState{
		ports: ports,
		index: 0,
	}

	pm.log.Info("Loaded lease port state", "leaseID", leaseID, "totalPorts", len(ports), "ports", ports)
}

// ReleaseLease removes the cached lease state and releases its resources
func (pm *portManager) ReleaseLease(leaseID mtypes.LeaseID) {
	leaseKey := leaseID.String()

	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.leaseState, leaseKey)
	pm.store.ReleaseLease(leaseID) // Also release from underlying store

	pm.log.Debug("released lease state", "leaseID", leaseID, "remainingCached", len(pm.leaseState))
}

// Stats returns statistics about the port manager
func (pm *portManager) Stats() map[string]interface{} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	return map[string]interface{}{
		"cached_lease_states": len(pm.leaseState),
	}
}

// CleanupExpired removes expired port reservations and returns count of cleaned items
func (pm *portManager) CleanupExpired() int {
	return pm.store.CleanupExpired()
}

// Cleanup removes expired lease allocators (optional maintenance)
func (pm *portManager) Cleanup() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// This could be enhanced to remove allocators for closed leases
	// For now, we rely on explicit ReleaseLease calls
}
