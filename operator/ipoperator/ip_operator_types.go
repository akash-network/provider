package ipoperator

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

/*
	Types that are used only within the IP Address operator locally
*/

type managedIP struct {
	presentLease        mtypes.LeaseID
	presentServiceName  string
	lastEvent           ctypes.IPResourceEvent
	presentSharingKey   string
	presentExternalPort uint32
	presentPort         uint32
	lastChangedAt       time.Time
	presentProtocol     v2beta2.ServiceProtocol
}

type barrier struct {
	enabled int32
	active  int32
}

func (b *barrier) enable() {
	atomic.StoreInt32(&b.enabled, 1)
}

func (b *barrier) disable() {
	atomic.StoreInt32(&b.enabled, 0)
}

func (b *barrier) enter() bool {
	isEnabled := atomic.LoadInt32(&b.enabled) == 1
	if !isEnabled {
		return false
	}

	atomic.AddInt32(&b.active, 1)
	return true
}

func (b *barrier) exit() {
	atomic.AddInt32(&b.active, -1)
}

func (b *barrier) waitUntilClear(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			clear := 0 == atomic.LoadInt32(&b.active)
			if clear {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
