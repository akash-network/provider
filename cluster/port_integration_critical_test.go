package cluster

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/testutil"
	"github.com/akash-network/provider/cluster/portreserve"
	"github.com/stretchr/testify/require"
)

// TestEndToEndPortConsistency tests the complete user journey:
// Bidding -> Query proposed ports -> Select provider -> Deploy -> Get same ports
func TestEndToEndPortConsistency(t *testing.T) {
	// Setup: Create isolated store for this test to avoid cross-test coupling
	tempFile, err := os.CreateTemp("", "akash-port-test-*.json")
	require.NoError(t, err)
	tempPath := tempFile.Name()
	tempFile.Close()

	// Cleanup temp file after test
	t.Cleanup(func() {
		os.Remove(tempPath)
	})

	store := portreserve.NewStoreWithFile(tempPath)
	store.SetPortRange(30100, 30200) // Use test range
	pm := NewPortManager(testutil.Logger(t), store)

	// User creates deployment
	orderID := mtypes.OrderID{Owner: "akash1user", DSeq: 12345, GSeq: 1, OSeq: 1}
	leaseID := mtypes.LeaseID{Owner: "akash1user", DSeq: 12345, GSeq: 1, OSeq: 1, Provider: "akash1provider"}

	// Phase 1: Provider bids (inventory service reserves ports)
	reservedPorts := pm.ReserveForOrder(orderID, 4, portreserve.DefaultReservationTTL)
	require.Len(t, reservedPorts, 4, "Provider should reserve 4 ports during bidding")

	// Phase 2: User queries proposed ports via HTTP endpoint
	proposedPorts := pm.PortsForOrder(orderID)
	require.Equal(t, reservedPorts, proposedPorts,
		"CRITICAL: User must see the exact ports that will be deployed")

	// User sees these ports in UI: [30100, 30101, 30102, 30103]
	t.Logf("User sees proposed ports: %v", proposedPorts)

	// Phase 3: User selects this provider, lease is created
	pm.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)

	// Phase 4: Deployment happens - services request ports one by one
	webPorts := pm.AllocatePorts(leaseID, "web", 2)
	require.Len(t, webPorts, 2, "Web service should get 2 ports")
	require.Equal(t, proposedPorts[0:2], webPorts,
		"Web service must get first 2 proposed ports")

	apiPorts := pm.AllocatePorts(leaseID, "api", 1)
	require.Len(t, apiPorts, 1, "API service should get 1 port")
	require.Equal(t, proposedPorts[2:3], apiPorts,
		"API service must get third proposed port")

	dbPorts := pm.AllocatePorts(leaseID, "db", 1)
	require.Len(t, dbPorts, 1, "DB service should get 1 port")
	require.Equal(t, proposedPorts[3:4], dbPorts,
		"DB service must get fourth proposed port")

	// Final verification: User's deployment has exactly the ports they expected
	finalPorts := append(append(webPorts, apiPorts...), dbPorts...)
	require.Equal(t, proposedPorts, finalPorts,
		"CRITICAL: Final deployed ports must exactly match what user saw during bidding")

	t.Logf("SUCCESS: User got exactly the ports they expected: %v", finalPorts)
}

// TestMultipleUsersNoCrossContamination tests that multiple users don't interfere
func TestMultipleUsersNoCrossContamination(t *testing.T) {
	store := portreserve.GetSharedStore()
	pm := NewPortManager(testutil.Logger(t), store)

	// User A creates deployment
	orderA := mtypes.OrderID{Owner: "akash1userA", DSeq: 100, GSeq: 1, OSeq: 1}
	leaseA := mtypes.LeaseID{Owner: "akash1userA", DSeq: 100, GSeq: 1, OSeq: 1, Provider: "akash1provider"}

	// User B creates deployment
	orderB := mtypes.OrderID{Owner: "akash1userB", DSeq: 200, GSeq: 1, OSeq: 1}
	leaseB := mtypes.LeaseID{Owner: "akash1userB", DSeq: 200, GSeq: 1, OSeq: 1, Provider: "akash1provider"}

	// Both users bid simultaneously
	portsA := pm.ReserveForOrder(orderA, 3, 5*time.Minute)
	portsB := pm.ReserveForOrder(orderB, 3, 5*time.Minute)

	require.Len(t, portsA, 3, "User A should get 3 ports")
	require.Len(t, portsB, 3, "User B should get 3 ports")

	// Critical: No port overlap between users
	for _, portA := range portsA {
		require.NotContains(t, portsB, portA,
			"CRITICAL: User A port %d must not be allocated to User B", portA)
	}

	// Both users query their proposed ports
	proposedA := pm.PortsForOrder(orderA)
	proposedB := pm.PortsForOrder(orderB)

	require.Equal(t, portsA, proposedA, "User A should see their reserved ports")
	require.Equal(t, portsB, proposedB, "User B should see their reserved ports")

	// Both users select provider and deploy
	pm.PromoteOrderToLease(orderA, leaseA, 2*time.Minute)
	pm.PromoteOrderToLease(orderB, leaseB, 2*time.Minute)

	// Both users deploy services
	deployedA := pm.AllocatePorts(leaseA, "service", 3)
	deployedB := pm.AllocatePorts(leaseB, "service", 3)

	require.Equal(t, portsA, deployedA, "User A should get their reserved ports")
	require.Equal(t, portsB, deployedB, "User B should get their reserved ports")

	// Final verification: No cross-contamination
	for _, portA := range deployedA {
		require.NotContains(t, deployedB, portA,
			"CRITICAL: No port sharing between different users")
	}
}

// TestProviderRestartPortPersistence tests that ports survive provider restarts
func TestProviderRestartPortPersistence(t *testing.T) {
	// Create temp file for persistence test
	tempFile, err := os.CreateTemp("", "akash-restart-test-*.json")
	require.NoError(t, err)
	tempPath := tempFile.Name()
	tempFile.Close()

	// Cleanup temp file after test
	t.Cleanup(func() {
		os.Remove(tempPath)
	})

	// Simulate first provider instance
	store1 := portreserve.NewStoreWithFile(tempPath)
	store1.SetPortRange(30100, 30200) // Use test range
	pm1 := NewPortManager(testutil.Logger(t), store1)

	orderID := mtypes.OrderID{Owner: "akash1user", DSeq: 999, GSeq: 1, OSeq: 1}

	// User bids and gets ports
	originalPorts := pm1.ReserveForOrder(orderID, 3, 5*time.Minute)
	require.Len(t, originalPorts, 3, "Should reserve ports in first instance")

	// Simulate provider restart - create new instances with same file
	store2 := portreserve.NewStoreWithFile(tempPath)
	store2.SetPortRange(30100, 30200) // Use same test range
	pm2 := NewPortManager(testutil.Logger(t), store2)

	// After restart, user should still see their ports
	persistedPorts := pm2.PortsForOrder(orderID)
	require.Equal(t, originalPorts, persistedPorts,
		"CRITICAL: Ports must survive provider restart")

	// New reservations should not conflict with persisted ones
	newOrder := mtypes.OrderID{Owner: "akash1user2", DSeq: 888, GSeq: 1, OSeq: 1}
	newPorts := pm2.ReserveForOrder(newOrder, 3, 5*time.Minute)
	require.Len(t, newPorts, 3, "Should allocate new ports after restart")

	// No collision with persisted ports
	for _, newPort := range newPorts {
		require.NotContains(t, originalPorts, newPort,
			"New ports must not conflict with persisted ports")
	}
}

// TestHighLoadPortAllocation tests system under high concurrent load
func TestHighLoadPortAllocation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping high load test in short mode")
	}

	// Create isolated store for this test
	tempFile, err := os.CreateTemp("", "akash-load-test-*.json")
	require.NoError(t, err)
	tempPath := tempFile.Name()
	tempFile.Close()

	// Cleanup temp file after test
	t.Cleanup(func() {
		os.Remove(tempPath)
	})

	store := portreserve.NewStoreWithFile(tempPath)
	store.SetPortRange(30100, 32000) // Larger range for load test
	pm := NewPortManager(testutil.Logger(t), store)

	const numUsers = 100
	const portsPerUser = 5

	type userResult struct {
		userID   int
		reserved []int32
		proposed []int32
		deployed []int32
		success  bool
	}

	var wg sync.WaitGroup
	results := make(chan userResult, numUsers)

	// Simulate high load: many users bidding simultaneously
	for i := 0; i < numUsers; i++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			orderID := mtypes.OrderID{
				Owner: fmt.Sprintf("akash1user%d", userID),
				DSeq:  uint64(userID + 1000),
				GSeq:  1,
				OSeq:  1,
			}
			leaseID := mtypes.LeaseID{
				Owner:    orderID.Owner,
				DSeq:     orderID.DSeq,
				GSeq:     orderID.GSeq,
				OSeq:     orderID.OSeq,
				Provider: "akash1provider",
			}

			// Reserve ports
			reserved := pm.ReserveForOrder(orderID, portsPerUser, 5*time.Minute)
			if len(reserved) != portsPerUser {
				results <- userResult{userID: userID, success: false}
				return
			}

			// Query proposed ports
			proposed := pm.PortsForOrder(orderID)
			if !equalSlices(reserved, proposed) {
				results <- userResult{userID: userID, success: false}
				return
			}

			// Promote and deploy
			pm.PromoteOrderToLease(orderID, leaseID, 2*time.Minute)
			deployed := pm.AllocatePorts(leaseID, "service", portsPerUser)

			success := len(deployed) == portsPerUser && equalSlices(reserved, deployed)
			results <- userResult{
				userID:   userID,
				reserved: reserved,
				proposed: proposed,
				deployed: deployed,
				success:  success,
			}
		}(i)
	}

	wg.Wait()
	close(results)

	// Analyze results
	successCount := 0
	allPorts := make(map[int32]int) // port -> user count

	for result := range results {
		if result.success {
			successCount++
			for _, port := range result.deployed {
				allPorts[port]++
			}
		}
	}

	// Verify no port was allocated to multiple users
	for port, count := range allPorts {
		require.Equal(t, 1, count,
			"CRITICAL: Port %d allocated to %d users (should be 1)", port, count)
	}

	// Should have 100% success rate under load
	successRate := float64(successCount) / float64(numUsers)
	if successRate != 1.0 {
		t.Logf("FAILURE DIAGNOSTICS:")
		t.Logf("Success rate: %.2f%% (%d/%d)", successRate*100, successCount, numUsers)
		t.Logf("Failed users: %d", numUsers-successCount)
		t.Logf("Port allocation state: %+v", allPorts)
		// TODO: Add more diagnostics like goroutine traces if needed
	}
	require.Equal(t, 1.0, successRate,
		"Should have >95%% success rate under high load, got %.2f%%", successRate*100)

	t.Logf("High load test: %d/%d users successful (%.1f%%)",
		successCount, numUsers, successRate*100)
}

// TestPortExhaustionGracefulDegradation tests behavior when running out of ports
func TestPortExhaustionGracefulDegradation(t *testing.T) {
	// Create store with limited port range
	store := portreserve.NewStore()
	store.SetPortRange(30100, 30110) // Only 11 ports available
	pm := NewPortManager(testutil.Logger(t), store)

	const portsPerOrder = 3
	maxOrders := 11 / portsPerOrder // Should be able to handle 3 full orders

	var successfulOrders []mtypes.OrderID

	// Fill up the port space
	for i := 0; i < maxOrders+2; i++ { // Try more than should fit
		orderID := mtypes.OrderID{
			Owner: fmt.Sprintf("user%d", i),
			DSeq:  uint64(i + 1),
			GSeq:  1,
			OSeq:  1,
		}

		ports := pm.ReserveForOrder(orderID, portsPerOrder, 5*time.Minute)
		if len(ports) == portsPerOrder {
			successfulOrders = append(successfulOrders, orderID)
		}
	}

	// Should have exactly maxOrders successful reservations
	require.Equal(t, maxOrders, len(successfulOrders),
		"Should handle exactly %d orders with %d ports each", maxOrders, portsPerOrder)

	// Try one more - should fail gracefully
	extraOrder := mtypes.OrderID{Owner: "extra", DSeq: 999, GSeq: 1, OSeq: 1}
	extraPorts := pm.ReserveForOrder(extraOrder, portsPerOrder, 5*time.Minute)
	require.Empty(t, extraPorts, "Should gracefully handle port exhaustion")

	// Release one order and try again - should succeed
	pm.ReleaseOrder(successfulOrders[0])

	retryPorts := pm.ReserveForOrder(extraOrder, portsPerOrder, 5*time.Minute)
	require.Len(t, retryPorts, portsPerOrder,
		"Should succeed after releasing ports")
}

// Helper function to compare slices
func equalSlices(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SetPortRange is now available in the portreserve.Store
