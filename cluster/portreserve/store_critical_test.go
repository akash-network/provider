package portreserve

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/stretchr/testify/require"
)

// TestPortCollisionPrevention tests the most critical user-facing bug:
// ensuring no two reservations get the same port
func TestPortCollisionPrevention(t *testing.T) {
	store := NewStore()

	// Create two different orders
	order1 := mtypes.OrderID{Owner: "user1", DSeq: 1, GSeq: 1, OSeq: 1}
	order2 := mtypes.OrderID{Owner: "user2", DSeq: 2, GSeq: 1, OSeq: 1}

	// Reserve ports for first order
	ports1 := store.ReserveForOrder(order1, 5, 5*time.Minute)
	require.Len(t, ports1, 5, "Should reserve 5 ports for order1")

	// Reserve ports for second order - should get different ports
	ports2 := store.ReserveForOrder(order2, 5, 5*time.Minute)
	require.Len(t, ports2, 5, "Should reserve 5 ports for order2")

	// Critical: Ensure no port collision
	portSet := make(map[int32]bool)
	for _, port := range ports1 {
		require.False(t, portSet[port], "Port %d already allocated to order1", port)
		portSet[port] = true
	}
	for _, port := range ports2 {
		require.False(t, portSet[port], "Port %d collision between order1 and order2", port)
		portSet[port] = true
	}
}

// TestOrderToLeasePromotionConsistency tests the critical path where
// users expect the same ports they saw during bidding
func TestOrderToLeasePromotionConsistency(t *testing.T) {
	store := NewStore()

	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	leaseID := mtypes.LeaseID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1, Provider: "provider"}

	// Reserve ports during bidding
	originalPorts := store.ReserveForOrder(orderID, 3, 5*time.Minute)
	require.Len(t, originalPorts, 3, "Should reserve 3 ports")

	// User queries proposed ports (this is what they see in bid-proposed-ports)
	queriedPorts := store.PortsForOrder(orderID)
	require.Equal(t, originalPorts, queriedPorts, "Queried ports must match reserved ports")

	// Promote order to lease (when user selects this provider)
	store.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)

	// Critical: Ensure promoted ports are exactly the same
	var allocatedPorts []int32
	for {
		port := store.NextForLease(leaseID)
		if port == 0 {
			break
		}
		allocatedPorts = append(allocatedPorts, port)
	}

	require.Equal(t, originalPorts, allocatedPorts,
		"CRITICAL: Deployed ports must exactly match the ports shown during bidding")
}

// TestPortExhaustionHandling tests what happens when provider runs out of ports
func TestPortExhaustionHandling(t *testing.T) {
	store := NewStore()
	// Set a very small port range for testing
	store.defaultStart = 30100
	store.defaultEnd = 30105 // Only 6 ports available

	// Reserve all available ports
	order1 := mtypes.OrderID{Owner: "user1", DSeq: 1, GSeq: 1, OSeq: 1}
	ports1 := store.ReserveForOrder(order1, 6, 5*time.Minute)
	require.Len(t, ports1, 6, "Should get all 6 available ports")

	// Try to reserve more ports - should get empty result, not crash
	order2 := mtypes.OrderID{Owner: "user2", DSeq: 2, GSeq: 1, OSeq: 1}
	ports2 := store.ReserveForOrder(order2, 1, 5*time.Minute)
	require.Empty(t, ports2, "Should get no ports when exhausted")

	// Release some ports and try again
	store.ReleaseOrder(order1)
	ports3 := store.ReserveForOrder(order2, 3, 5*time.Minute)
	require.Len(t, ports3, 3, "Should get ports after release")
}

// TestConcurrentPortAllocation tests thread safety under concurrent access
func TestConcurrentPortAllocation(t *testing.T) {
	store := NewStore()

	const numGoroutines = 10
	const portsPerGoroutine = 5

	var wg sync.WaitGroup
	allPorts := make(chan []int32, numGoroutines)

	// Launch concurrent port reservations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			orderID := mtypes.OrderID{
				Owner: "user",
				DSeq:  uint64(id + 1),
				GSeq:  1,
				OSeq:  1,
			}
			ports := store.ReserveForOrder(orderID, portsPerGoroutine, 5*time.Minute)
			allPorts <- ports
		}(i)
	}

	wg.Wait()
	close(allPorts)

	// Collect all allocated ports and check for collisions
	portSet := make(map[int32]bool)
	totalPorts := 0

	for ports := range allPorts {
		totalPorts += len(ports)
		for _, port := range ports {
			require.False(t, portSet[port],
				"CRITICAL: Port %d allocated to multiple concurrent requests", port)
			portSet[port] = true
		}
	}

	require.Equal(t, numGoroutines*portsPerGoroutine, totalPorts,
		"Should allocate correct total number of ports")
}

// TestTTLExpirationEdgeCases tests time-based edge cases that could cause issues
func TestTTLExpirationEdgeCases(t *testing.T) {
	store := NewStore()

	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}

	// Reserve ports with very short TTL
	shortTTL := 100 * time.Millisecond
	ports := store.ReserveForOrder(orderID, 3, shortTTL)
	require.Len(t, ports, 3, "Should reserve ports")

	// Immediately check - should still be valid
	queriedPorts := store.PortsForOrder(orderID)
	require.Equal(t, ports, queriedPorts, "Ports should be immediately available")

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should be expired now
	expiredPorts := store.PortsForOrder(orderID)
	require.Empty(t, expiredPorts, "Ports should be expired")

	// New reservation should be able to reuse the expired ports
	newOrder := mtypes.OrderID{Owner: "user2", DSeq: 2, GSeq: 1, OSeq: 1}
	newPorts := store.ReserveForOrder(newOrder, 3, 5*time.Minute)
	require.Len(t, newPorts, 3, "Should reuse expired ports")

	// Critical: The new ports should include some of the previously expired ports
	// (since we start from the same range)
	require.Equal(t, ports[0], newPorts[0],
		"Should reuse the first expired port (sequential allocation)")
}

// TestCrossProcessConsistency tests file-based persistence for multiple processes
func TestCrossProcessConsistency(t *testing.T) {
	tempDir := t.TempDir()
	storageFile := filepath.Join(tempDir, "test-ports.json")

	// Create first store instance (simulating first process)
	store1 := NewStore()
	store1.storageFile = storageFile

	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	ports1 := store1.ReserveForOrder(orderID, 3, 5*time.Minute)
	require.Len(t, ports1, 3, "Store1 should reserve ports")

	// Create second store instance (simulating second process)
	store2 := NewStore()
	store2.storageFile = storageFile

	// Load from file to simulate process startup
	store2.loadFromFile()

	// Query from second store - should see the same ports
	ports2 := store2.PortsForOrder(orderID)
	require.Equal(t, ports1, ports2,
		"CRITICAL: Second process must see ports reserved by first process")

	// Try to reserve overlapping ports from second store - should avoid collision
	newOrder := mtypes.OrderID{Owner: "user2", DSeq: 2, GSeq: 1, OSeq: 1}
	newPorts := store2.ReserveForOrder(newOrder, 3, 5*time.Minute)
	require.Len(t, newPorts, 3, "Store2 should reserve different ports")

	// Ensure no collision between processes
	for _, port := range newPorts {
		require.NotContains(t, ports1, port,
			"CRITICAL: Cross-process port collision detected for port %d", port)
	}
}

// TestLeasePortAllocationSequence tests the complete sequence from order to deployment
func TestLeasePortAllocationSequence(t *testing.T) {
	store := NewStore()

	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	leaseID := mtypes.LeaseID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1, Provider: "provider"}

	// Step 1: Reserve ports during bidding
	originalPorts := store.ReserveForOrder(orderID, 4, 5*time.Minute)
	require.Len(t, originalPorts, 4, "Should reserve 4 ports")

	// Step 2: User sees these ports in bid-proposed-ports
	proposedPorts := store.PortsForOrder(orderID)
	require.Equal(t, originalPorts, proposedPorts, "User should see correct proposed ports")

	// Step 3: User selects provider, lease is created, ports are promoted
	store.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)

	// Step 4: During deployment, services request ports one by one
	service1Port := store.NextForLease(leaseID)
	require.Equal(t, originalPorts[0], service1Port, "Service 1 should get first reserved port")

	service2Port := store.NextForLease(leaseID)
	require.Equal(t, originalPorts[1], service2Port, "Service 2 should get second reserved port")

	service3Port := store.NextForLease(leaseID)
	require.Equal(t, originalPorts[2], service3Port, "Service 3 should get third reserved port")

	service4Port := store.NextForLease(leaseID)
	require.Equal(t, originalPorts[3], service4Port, "Service 4 should get fourth reserved port")

	// Step 5: No more ports should be available
	noMorePorts := store.NextForLease(leaseID)
	require.Zero(t, noMorePorts, "Should have no more ports available")

	// Critical: Verify the complete sequence matches user expectations
	deployedPorts := []int32{service1Port, service2Port, service3Port, service4Port}
	require.Equal(t, originalPorts, deployedPorts,
		"CRITICAL: Final deployed ports must exactly match originally proposed ports")
}

// TestFileCorruptionRecovery tests recovery from corrupted persistence files
func TestFileCorruptionRecovery(t *testing.T) {
	tempDir := t.TempDir()
	storageFile := filepath.Join(tempDir, "corrupted-ports.json")

	// Write corrupted JSON to file
	corruptedData := `{"by_order": {"invalid json`
	err := os.WriteFile(storageFile, []byte(corruptedData), 0600)
	require.NoError(t, err, "Should write corrupted file")

	// Create store with corrupted file
	store := NewStore()
	store.storageFile = storageFile

	// Should not crash on corrupted file, should continue working
	orderID := mtypes.OrderID{Owner: "user", DSeq: 1, GSeq: 1, OSeq: 1}
	ports := store.ReserveForOrder(orderID, 3, 5*time.Minute)
	require.Len(t, ports, 3, "Should work despite corrupted persistence file")

	// Should be able to query the ports
	queriedPorts := store.PortsForOrder(orderID)
	require.Equal(t, ports, queriedPorts, "Should work in memory despite file corruption")
}
