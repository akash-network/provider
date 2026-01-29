package ip

import (
	"context"
	"sync/atomic"
	"time"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	manifest "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
)

/*
	Types that are used only within the IP Address operator locally
*/

type managedIP struct {
	presentLease        mtypes.LeaseID
	presentServiceName  string
	lastEvent           cip.ResourceEvent
	presentSharingKey   string
	presentExternalPort uint32
	presentPort         uint32
	lastChangedAt       time.Time
	presentProtocol     manifest.ServiceProtocol
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
			if atomic.LoadInt32(&b.active) == 0 {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type ipResourceEvent struct {
	lID          mtypes.LeaseID
	eventType    ctypes.ProviderResourceEvent
	serviceName  string
	port         uint32
	externalPort uint32
	sharingKey   string
	providerAddr sdktypes.Address
	ownerAddr    sdktypes.Address
	protocol     manifest.ServiceProtocol
}

func (ev ipResourceEvent) GetLeaseID() mtypes.LeaseID {
	return ev.lID
}

func (ev ipResourceEvent) GetEventType() ctypes.ProviderResourceEvent {
	return ev.eventType
}

func (ev ipResourceEvent) GetServiceName() string {
	return ev.serviceName
}

func (ev ipResourceEvent) GetPort() uint32 {
	return ev.port
}

func (ev ipResourceEvent) GetExternalPort() uint32 {
	return ev.externalPort
}

func (ev ipResourceEvent) GetSharingKey() string {
	return ev.sharingKey
}

func (ev ipResourceEvent) GetProtocol() manifest.ServiceProtocol {
	return ev.protocol
}
