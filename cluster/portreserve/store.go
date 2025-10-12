package portreserve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
)

const (
	// DefaultReservationTTL is the default time-to-live for NodePort reservations during bidding
	DefaultReservationTTL = 5 * time.Minute
)

type orderKey struct {
	Owner string
	DSeq  uint64
	GSeq  uint32
	OSeq  uint32
}

type leaseKey struct {
	Owner    string
	DSeq     uint64
	GSeq     uint32
	OSeq     uint32
	Provider string
}

type reservation struct {
	Ports   []int32   `json:"ports"`
	Next    int       `json:"next"`
	Expires time.Time `json:"expires"`
}

// persistentStore represents the file format for cross-process storage
type persistentStore struct {
	ByOrder map[string]*reservation `json:"by_order"`
	Updated time.Time               `json:"updated"`
}

// Store manages NodePort reservations with thread-safe operations
type Store struct {
	mu           sync.Mutex
	byOrder      map[orderKey]*reservation
	byLease      map[leaseKey]*reservation
	defaultStart int32
	defaultEnd   int32
	storageFile  string
}

var (
	sharedStore *Store
	storeOnce   sync.Once
)

// NewStore creates a new port reservation store with default file path
func NewStore() *Store {
	return NewStoreWithFile("/tmp/akash-provider-ports.json")
}

// NewStoreWithFile creates a new port reservation store with custom file path
func NewStoreWithFile(path string) *Store {
	return &Store{
		byOrder:      make(map[orderKey]*reservation),
		byLease:      make(map[leaseKey]*reservation),
		defaultStart: 30100, // Start higher to avoid common conflicts
		defaultEnd:   32767,
		storageFile:  path,
	}
}

// GetSharedStore returns a singleton Store instance for cross-process consistency.
// This ensures all components in the same process share the same Store instance.
func GetSharedStore() *Store {
	storeOnce.Do(func() {
		sharedStore = NewStore()
		// Load existing data from file on first access
		sharedStore.loadFromFile()
	})
	return sharedStore
}

func makeOrderKey(id mtypes.OrderID) orderKey {
	gid := id.GroupID()
	return orderKey{Owner: gid.Owner, DSeq: gid.DSeq, GSeq: gid.GSeq, OSeq: id.OSeq}
}

func makeLeaseKey(id mtypes.LeaseID) leaseKey {
	return leaseKey{Owner: id.Owner, DSeq: id.DSeq, GSeq: id.GSeq, OSeq: id.OSeq, Provider: id.Provider}
}

func (s *Store) saveToFile() error {
	store := &persistentStore{
		ByOrder: make(map[string]*reservation),
		Updated: time.Now(),
	}

	// Convert orderKey to string for JSON serialization
	for k, v := range s.byOrder {
		keyStr := fmt.Sprintf("%s/%d/%d/%d", k.Owner, k.DSeq, k.GSeq, k.OSeq)
		store.ByOrder[keyStr] = v
	}

	data, err := json.Marshal(store)
	if err != nil {
		return fmt.Errorf("failed to marshal port store data: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(s.storageFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Atomic write: write to temp file first, then rename
	tempFile, err := os.CreateTemp(dir, "akash-provider-ports-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Set restrictive permissions (readable/writable only by owner)
	if err := tempFile.Chmod(0600); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	// Write data to temp file
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to write data to temp file: %w", err)
	}

	// Ensure data is written to disk
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	// Close temp file
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomically replace the target file
	if err := os.Rename(tempPath, s.storageFile); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename temp file to target: %w", err)
	}

	return nil
}

func (s *Store) loadFromFile() {
	data, err := os.ReadFile(s.storageFile)
	if err != nil {
		return
	}

	var store persistentStore
	if err := json.Unmarshal(data, &store); err != nil {
		return
	}

	// Convert string keys back to orderKey and merge with current state
	for keyStr, v := range store.ByOrder {
		// Parse keyStr format: "owner/dseq/gseq/oseq" using string split
		parts := strings.Split(keyStr, "/")
		if len(parts) == 4 {
			owner := parts[0]
			dseq, err1 := strconv.ParseUint(parts[1], 10, 64)
			gseq, err2 := strconv.ParseUint(parts[2], 10, 32)
			oseq, err3 := strconv.ParseUint(parts[3], 10, 32)

			if err1 == nil && err2 == nil && err3 == nil {
				key := orderKey{Owner: owner, DSeq: dseq, GSeq: uint32(gseq), OSeq: uint32(oseq)}
				// Only load if not expired and not already in memory
				if time.Now().Before(v.Expires) {
					if _, exists := s.byOrder[key]; !exists {
						// Check if this order was already promoted to a lease
						// Don't resurrect orders that are now leases
						hasActiveLease := false
						for leaseKey, leaseRes := range s.byLease {
							if leaseKey.Owner == owner && leaseKey.DSeq == dseq &&
								leaseKey.GSeq == uint32(gseq) && leaseKey.OSeq == uint32(oseq) &&
								time.Now().Before(leaseRes.Expires) {
								hasActiveLease = true
								break
							}
						}

						// Only load order if no active lease exists
						if !hasActiveLease {
							s.byOrder[key] = v
						}
					}
				}
			}
		}
	}
}

func (s *Store) inUseLocked(p int32) bool {
	now := time.Now()

	// Check ports in order reservations (bidding phase)
	for _, r := range s.byOrder {
		for _, v := range r.Ports {
			if v == p && now.Before(r.Expires) {
				return true
			}
		}
	}

	// Check ports in lease reservations (deployment phase)
	for _, r := range s.byLease {
		for _, v := range r.Ports {
			if v == p && now.Before(r.Expires) {
				return true
			}
		}
	}

	return false
}

// ReserveForOrder reserves count NodePorts for the given order and TTL.
func (s *Store) ReserveForOrder(id mtypes.OrderID, count int, ttl time.Duration) []int32 {
	if count <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ok := makeOrderKey(id)

	// Reuse existing valid reservation
	if r, exists := s.byOrder[ok]; exists {
		if time.Now().Before(r.Expires) && len(r.Ports) >= count {
			return append([]int32(nil), r.Ports...)
		}
		delete(s.byOrder, ok)
	}

	// Very simple sequential allocator in default range (PoC only)
	ports := make([]int32, 0, count)
	var p int32 = s.defaultStart

	for p <= s.defaultEnd && len(ports) < count {
		if !s.inUseLocked(p) {
			ports = append(ports, p)
		}
		p++
	}

	res := &reservation{Ports: ports, Next: 0, Expires: time.Now().Add(ttl)}
	s.byOrder[ok] = res

	// Save to file for cross-process access
	if err := s.saveToFile(); err != nil {
		// Log error but don't fail the operation - ports are still reserved in memory
		fmt.Printf("Warning: failed to persist port reservation to file: %v\n", err)
	}

	return append([]int32(nil), ports...)
}

// PortsForOrder returns a copy of all reserved ports for an order (non-consuming).
func (s *Store) PortsForOrder(id mtypes.OrderID) []int32 {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load from file for cross-process access
	s.loadFromFile()

	ok := makeOrderKey(id)

	if r, exists := s.byOrder[ok]; exists {
		if time.Now().Before(r.Expires) {
			cp := make([]int32, len(r.Ports))
			copy(cp, r.Ports)
			return cp
		}
	}
	return nil
}

// PromoteOrderToLease moves an order reservation to a lease reservation.
func (s *Store) PromoteOrderToLease(oid mtypes.OrderID, lid mtypes.LeaseID, ttl time.Duration) {
	fmt.Printf("DEBUG: PromoteOrderToLease called - OrderID=%+v, LeaseID=%+v\n", oid, lid)
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := makeOrderKey(oid)
	lk := makeLeaseKey(lid)
	fmt.Printf("DEBUG: PromoteOrderToLease - orderKey=%+v, leaseKey=%+v\n", ok, lk)

	if r, exists := s.byOrder[ok]; exists {
		fmt.Printf("DEBUG: PromoteOrderToLease - found order reservation with ports %v\n", r.Ports)
		r.Expires = time.Now().Add(ttl)
		s.byLease[lk] = r
		delete(s.byOrder, ok)
		fmt.Printf("DEBUG: PromoteOrderToLease - promoted to lease, expires: %v\n", r.Expires)

		// Persist the order deletion to disk
		if err := s.saveToFile(); err != nil {
			fmt.Printf("Warning: failed to persist order promotion to file: %v\n", err)
		}
	} else {
		fmt.Printf("DEBUG: PromoteOrderToLease - no order reservation found for key %+v\n", ok)
	}
}

// NextForLease returns the next reserved NodePort for a lease, or 0 if none.
func (s *Store) NextForLease(lid mtypes.LeaseID) int32 {
	fmt.Printf("DEBUG: NextForLease called - LeaseID=%+v\n", lid)
	s.mu.Lock()
	defer s.mu.Unlock()
	lk := makeLeaseKey(lid)
	fmt.Printf("DEBUG: NextForLease - leaseKey=%+v\n", lk)

	if r, exists := s.byLease[lk]; exists {
		fmt.Printf("DEBUG: NextForLease - found lease reservation with ports %v, next=%d\n", r.Ports, r.Next)
		if r.Next < len(r.Ports) {
			v := r.Ports[r.Next]
			r.Next++
			fmt.Printf("DEBUG: NextForLease - returning port %d, next now %d\n", v, r.Next)
			return v
		}
		fmt.Printf("DEBUG: NextForLease - no more ports available (next=%d, len=%d)\n", r.Next, len(r.Ports))
	} else {
		fmt.Printf("DEBUG: NextForLease - no lease reservation found for key %+v\n", lk)
	}
	return 0
}

// ReleaseOrder removes an order reservation (bid didn't win).
func (s *Store) ReleaseOrder(orderID mtypes.OrderID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ok := makeOrderKey(orderID)
	delete(s.byOrder, ok)
	if err := s.saveToFile(); err != nil {
		// Log error but don't fail the operation - order is still released in memory
		fmt.Printf("Warning: failed to persist order release to file: %v\n", err)
	}
}

// ReleaseLease removes a lease reservation (lease closed).
func (s *Store) ReleaseLease(leaseID mtypes.LeaseID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lk := makeLeaseKey(leaseID)
	delete(s.byLease, lk)
	// Note: We don't persist lease state to file since it's runtime only
}

// CleanupExpired removes expired reservations from both order and lease maps.
func (s *Store) CleanupExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cleaned := 0

	// Clean expired orders
	for k, r := range s.byOrder {
		if now.After(r.Expires) {
			delete(s.byOrder, k)
			cleaned++
		}
	}

	// Clean expired leases
	for k, r := range s.byLease {
		if now.After(r.Expires) {
			delete(s.byLease, k)
			cleaned++
		}
	}

	// Save changes if any orders were cleaned
	if cleaned > 0 {
		if err := s.saveToFile(); err != nil {
			// Log error but don't fail the cleanup - expired items are still removed from memory
			fmt.Printf("Warning: failed to persist cleanup to file: %v\n", err)
		}
	}

	return cleaned
}

// SetPortRange sets the port allocation range (for testing)
func (s *Store) SetPortRange(start, end int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultStart = start
	s.defaultEnd = end
}
