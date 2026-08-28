package hostname

import (
	"context"

	mtypes "pkg.akt.dev/go/node/market/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

type LeaseIDConnection interface {
	GetLeaseID() mtypes.LeaseID
	GetHostname() string
	GetExternalPort() int32
	GetServiceName() string
}

// LeaseIDHostnameConnection is a concrete implementation of LeaseIDConnection
// used by both the cluster client and hostname operator for tracking hostname
// to deployment connections.
type LeaseIDHostnameConnection struct {
	LeaseID      mtypes.LeaseID
	Hostname     string
	ExternalPort int32
	ServiceName  string
}

var _ LeaseIDConnection = LeaseIDHostnameConnection{}

func (lh LeaseIDHostnameConnection) GetHostname() string {
	return lh.Hostname
}

func (lh LeaseIDHostnameConnection) GetLeaseID() mtypes.LeaseID {
	return lh.LeaseID
}

func (lh LeaseIDHostnameConnection) GetExternalPort() int32 {
	return lh.ExternalPort
}

func (lh LeaseIDHostnameConnection) GetServiceName() string {
	return lh.ServiceName
}

type ResourceEvent interface {
	GetLeaseID() mtypes.LeaseID
	GetEventType() ctypes.ProviderResourceEvent
	GetHostname() string
	GetServiceName() string
	GetExternalPort() uint32
}

type Client interface {
	Check(ctx context.Context) error
	String() string
	Stop()
}

type ActiveHostname struct {
	ID       mtypes.LeaseID
	Hostname string
}

type ConnectToDeploymentDirective struct {
	Hostname    string
	LeaseID     mtypes.LeaseID
	ServiceName string
	ServicePort int32
	ReadTimeout uint32
	SendTimeout uint32
	NextTimeout uint32
	MaxBodySize uint32
	NextTries   uint32
	NextCases   []string
	// New nginx proxy buffering/tuning options. The sizes are bytes and zero == unset
	// (annotation omitted; nginx default applies); ProxyConnectTimeout is milliseconds.
	// ProxyBufferingDisable mirrors the manifest's buffering_disabled: false leaves the
	// nginx default (buffering on), true renders proxy_buffering off.
	ProxyBufferingDisable bool
	ProxyBufferSize       uint32
	ProxyBuffersNumber    uint32
	ProxyBuffersSize      uint32
	ProxyBusyBuffersSize  uint32
	ProxyConnectTimeout   uint32
}

// IngressProxyBuffers is the validated proxy buffer sizing for the community
// ingress-nginx path, in bytes. A zero field means "do not emit that annotation".
type IngressProxyBuffers struct {
	BufferSize uint32
	Number     uint32
	BusySize   uint32
}

// IngressProxyBuffers derives the proxy buffer annotations that are safe to emit on
// the community ingress-nginx path. Unlike NGF (which sets proxy_buffers <n> <size>
// directly), ingress-nginx sizes each pooled buffer from proxy-buffer-size and has no
// per-buffer-size annotation, so consistency is checked against BufferSize. nginx
// rejects the whole config on reload unless proxy_busy_buffers_size is within
// [proxy_buffer_size, (buffers-1)*proxy_buffer_size] with at least 2 buffers, and a
// rejected reload freezes config for every tenant on the shared controller. So the
// buffer size is always safe on its own, but the number/busy pair is dropped unless
// it forms a set nginx will accept. When only one of them is set the other is filled
// from the effective default (ingress-nginx uses 4 buffers; nginx uses 2*buffer_size
// for busy), and the filled-in values are returned - not the raw ones - so the emitted
// annotations are internally consistent regardless of the controller's own defaults.
func (d ConnectToDeploymentDirective) IngressProxyBuffers() IngressProxyBuffers {
	out := IngressProxyBuffers{}

	bufferSize := d.ProxyBufferSize
	if bufferSize > 0 {
		out.BufferSize = bufferSize
	}

	number := d.ProxyBuffersNumber
	busy := d.ProxyBusyBuffersSize
	if (number == 0 && busy == 0) || bufferSize == 0 {
		return out
	}

	if number == 0 {
		number = 4 // ingress-nginx default proxy-buffers-number
	}
	if number < 2 {
		return out
	}

	if busy == 0 {
		busy = 2 * bufferSize // nginx default proxy_busy_buffers_size
	}
	if busy < bufferSize || busy > (number-1)*bufferSize {
		return out
	}

	out.Number = number
	out.BusySize = busy
	return out
}
