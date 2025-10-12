package cluster

import (
	"fmt"
	"sync"
	"testing"
	"time"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/testutil"
	"github.com/akash-network/provider/cluster/portreserve"
	"github.com/stretchr/testify/require"
)

// TestPortManagerOrderToLeaseConsistency tests the critical user-facing requirement:
// ports shown during bidding must be the same as deployed ports
func TestPortManagerOrderToLeaseConsistency(t *testing.T) {
	store := portreserve.NewStore()
	pm := NewPortManager(testutil.Logger(t), store)

	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	leaseID := mtypes.LeaseID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1, Provider: "provider"}

	// Step 1: Reserve ports during bidding (what inventory service does)
	reservedPorts := pm.ReserveForOrder(orderID, 4, 5*time.Minute)
	require.Len(t, reservedPorts, 4, "Should reserve 4 ports")

	// Step 2: User queries proposed ports (what HTTP endpoint returns)
	proposedPorts := pm.PortsForOrder(orderID)
	require.Equal(t, reservedPorts, proposedPorts,
		"CRITICAL: Proposed ports must match reserved ports")

	// Step 3: User selects provider, lease is created (what service.go does)
	pm.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)

	// Step 4: During deployment, services allocate ports (what kube client does)
	service1Ports := pm.AllocatePorts(leaseID, "web", 2)
	require.Len(t, service1Ports, 2, "Should allocate 2 ports for web service")
	require.Equal(t, reservedPorts[0:2], service1Ports,
		"Web service should get first 2 reserved ports")

	service2Ports := pm.AllocatePorts(leaseID, "api", 2)
	require.Len(t, service2Ports, 2, "Should allocate 2 ports for api service")
	require.Equal(t, reservedPorts[2:4], service2Ports,
		"API service should get last 2 reserved ports")

	// Step 5: No more ports should be available
	service3Ports := pm.AllocatePorts(leaseID, "db", 1)
	require.Empty(t, service3Ports, "Should have no more ports available")

	// Critical verification: All deployed ports match original reservation
	allDeployedPorts := append(service1Ports, service2Ports...)
	require.Equal(t, reservedPorts, allDeployedPorts,
		"CRITICAL: All deployed ports must exactly match originally reserved ports")
}

// TestPortManagerConcurrentAccess tests thread safety of port manager
func TestPortManagerConcurrentAccess(t *testing.T) {
	store := portreserve.NewStore()
	pm := NewPortManager(testutil.Logger(t), store)

	const numOrders = 20
	const portsPerOrder = 3

	var wg sync.WaitGroup
	results := make(chan []int32, numOrders)

	// Simulate concurrent bidding requests
	for i := 0; i < numOrders; i++ {
		wg.Add(1)
		go func(orderNum int) {
			defer wg.Done()
			orderID := mtypes.OrderID{
				Owner: "user",
				DSeq:  uint64(orderNum + 1),
				GSeq:  1,
				OSeq:  1,
			}
			ports := pm.ReserveForOrder(orderID, portsPerOrder, 5*time.Minute)
			results <- ports
		}(i)
	}

	wg.Wait()
	close(results)

	// Verify no port collisions occurred
	allPorts := make(map[int32]bool)
	totalPorts := 0

	for ports := range results {
		totalPorts += len(ports)
		for _, port := range ports {
			require.False(t, allPorts[port],
				"CRITICAL: Port %d allocated to multiple concurrent orders", port)
			allPorts[port] = true
		}
	}

	require.Equal(t, numOrders*portsPerOrder, totalPorts,
		"Should allocate correct total number of ports")
}

// TestPortManagerLeaseStateRaceCondition tests the race condition fix in AllocatePorts
func TestPortManagerLeaseStateRaceCondition(t *testing.T) {
	store := portreserve.NewStore()
	pm := NewPortManager(testutil.Logger(t), store)

	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	leaseID := mtypes.LeaseID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1, Provider: "provider"}

	// Reserve and promote ports
	pm.ReserveForOrder(orderID, 6, 5*time.Minute)
	pm.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)

	const numServices = 10
	var wg sync.WaitGroup
	results := make(chan []int32, numServices)

	// Simulate concurrent service deployments trying to allocate ports
	for i := 0; i < numServices; i++ {
		wg.Add(1)
		go func(serviceNum int) {
			defer wg.Done()
			serviceName := fmt.Sprintf("service-%d", serviceNum)
			// Each service tries to allocate 1 port
			ports := pm.AllocatePorts(leaseID, serviceName, 1)
			results <- ports
		}(i)
	}

	wg.Wait()
	close(results)

	// Count successful allocations and verify no duplicates
	successfulAllocations := 0
	allocatedPorts := make(map[int32]bool)

	for ports := range results {
		if len(ports) > 0 {
			successfulAllocations++
			port := ports[0]
			require.False(t, allocatedPorts[port],
				"CRITICAL: Port %d allocated to multiple services", port)
			allocatedPorts[port] = true
		}
	}

	// Should have exactly 6 successful allocations (the number of reserved ports)
	require.Equal(t, 6, successfulAllocations,
		"Should allocate exactly the number of reserved ports")
}

// TestPortManagerLeaseNotFound tests handling of lease allocation without promotion
func TestPortManagerLeaseNotFound(t *testing.T) {
	store := portreserve.NewStore()
	pm := NewPortManager(testutil.Logger(t), store)

	// Try to allocate ports for a lease that was never promoted from an order
	leaseID := mtypes.LeaseID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1, Provider: "provider"}

	ports := pm.AllocatePorts(leaseID, "service", 2)
	require.Empty(t, ports, "Should get no ports for non-existent lease")

	// This should not crash or cause issues
	morePorts := pm.AllocatePorts(leaseID, "another-service", 1)
	require.Empty(t, morePorts, "Should consistently return no ports")
}

// TestPortManagerCleanupExpiredReservations tests cleanup functionality
func TestPortManagerCleanupExpiredReservations(t *testing.T) {
	store := portreserve.NewStore()
	pm := NewPortManager(testutil.Logger(t), store)

	// Create orders with short TTL
	shortTTL := 100 * time.Millisecond
	order1 := mtypes.OrderID{Owner: "user1", DSeq: 1, GSeq: 1, OSeq: 1}
	order2 := mtypes.OrderID{Owner: "user2", DSeq: 2, GSeq: 1, OSeq: 1}

	ports1 := pm.ReserveForOrder(order1, 3, shortTTL)
	ports2 := pm.ReserveForOrder(order2, 3, 5*time.Minute) // Long TTL

	require.Len(t, ports1, 3, "Should reserve ports for order1")
	require.Len(t, ports2, 3, "Should reserve ports for order2")

	// Wait for first order to expire
	time.Sleep(150 * time.Millisecond)

	// Cleanup expired reservations
	cleaned := pm.CleanupExpired()
	require.Greater(t, cleaned, 0, "Should clean up expired reservations")

	// Verify expired order is gone, but valid order remains
	expiredPorts := pm.PortsForOrder(order1)
	require.Empty(t, expiredPorts, "Expired order should have no ports")

	validPorts := pm.PortsForOrder(order2)
	require.Equal(t, ports2, validPorts, "Valid order should still have ports")

	// New order should be able to reuse cleaned up ports
	order3 := mtypes.OrderID{Owner: "user3", DSeq: 3, GSeq: 1, OSeq: 1}
	newPorts := pm.ReserveForOrder(order3, 3, 5*time.Minute)
	require.Len(t, newPorts, 3, "Should reuse cleaned up ports")
}

// TestPortManagerDefensiveLoopTermination tests the infinite loop protection
func TestPortManagerDefensiveLoopTermination(t *testing.T) {
	// Create a mock store that returns non-zero ports indefinitely
	store := newMockInfiniteStore()
	pm := NewPortManager(testutil.Logger(t), store.Store)

	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	leaseID := mtypes.LeaseID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1, Provider: "provider"}

	// This should not hang due to defensive loop termination
	pm.ReserveForOrder(orderID, 1, 5*time.Minute)
	pm.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)

	// This should terminate due to maxIterations limit, not hang forever
	ports := pm.AllocatePorts(leaseID, "service", 1)

	// Should get some ports but not infinite
	require.NotEmpty(t, ports, "Should get some ports despite infinite store")
	require.LessOrEqual(t, len(ports), 1000, "Should not exceed maxIterations limit")
}

// mockInfiniteStore simulates a misbehaving store that never returns 0
type mockInfiniteStore struct {
	*portreserve.Store
	counter int32
}

func newMockInfiniteStore() *mockInfiniteStore {
	return &mockInfiniteStore{
		Store:   portreserve.NewStore(),
		counter: 0,
	}
}

func (m *mockInfiniteStore) NextForLease(leaseID mtypes.LeaseID) int32 {
	m.counter++
	return 30000 + m.counter // Always return a port, never 0
}

func (m *mockInfiniteStore) ReserveForOrder(id mtypes.OrderID, count int, ttl time.Duration) []int32 {
	ports := make([]int32, count)
	for i := 0; i < count; i++ {
		ports[i] = 30000 + int32(i)
	}
	return ports
}

func (m *mockInfiniteStore) PromoteOrderToLease(oid mtypes.OrderID, lid mtypes.LeaseID, ttl time.Duration) {
	// Do nothing, just to satisfy interface
}

// TestPortManagerStatsAndDiagnostics tests monitoring and debugging capabilities
func TestPortManagerStatsAndDiagnostics(t *testing.T) {
	store := portreserve.NewStore()
	pm := NewPortManager(testutil.Logger(t), store)

	// Initial stats should show empty state
	stats := pm.Stats()
	require.Equal(t, 0, stats["cached_lease_states"], "Should start with no cached states")

	// Create some reservations and promotions
	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	leaseID := mtypes.LeaseID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1, Provider: "provider"}

	pm.ReserveForOrder(orderID, 3, 5*time.Minute)
	pm.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)

	// Stats should reflect the cached lease state
	stats = pm.Stats()
	require.Equal(t, 1, stats["cached_lease_states"], "Should have one cached lease state")

	// Allocate some ports
	pm.AllocatePorts(leaseID, "service1", 2)

	// Release the lease
	pm.ReleaseLease(leaseID)

	// Stats should show cleanup
	stats = pm.Stats()
	require.Equal(t, 0, stats["cached_lease_states"], "Should clean up cached states")
}
