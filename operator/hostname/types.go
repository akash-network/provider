package hostname

import (
	"time"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	mtypes "pkg.akt.dev/go/node/market/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

const (
	akashIngressClassName = "akash-ingress-class"
)

type managedHostname struct {
	lastEvent    chostname.ResourceEvent
	presentLease mtypes.LeaseID

	presentServiceName  string
	presentExternalPort uint32
	lastChangeAt        time.Time
}

type leaseIDHostnameConnection struct {
	leaseID      mtypes.LeaseID
	hostname     string
	externalPort int32
	serviceName  string
}

func (lh leaseIDHostnameConnection) GetHostname() string {
	return lh.hostname
}

func (lh leaseIDHostnameConnection) GetLeaseID() mtypes.LeaseID {
	return lh.leaseID
}

func (lh leaseIDHostnameConnection) GetExternalPort() int32 {
	return lh.externalPort
}

func (lh leaseIDHostnameConnection) GetServiceName() string {
	return lh.serviceName
}

type hostnameResourceEvent struct {
	eventType ctypes.ProviderResourceEvent
	hostname  string

	owner        sdktypes.Address
	dseq         uint64
	oseq         uint32
	gseq         uint32
	provider     sdktypes.Address
	serviceName  string
	externalPort uint32
}

func (ev hostnameResourceEvent) GetLeaseID() mtypes.LeaseID {
	return mtypes.LeaseID{
		Owner:    ev.owner.String(),
		DSeq:     ev.dseq,
		GSeq:     ev.gseq,
		OSeq:     ev.oseq,
		Provider: ev.provider.String(),
	}
}

func (ev hostnameResourceEvent) GetHostname() string {
	return ev.hostname
}

func (ev hostnameResourceEvent) GetEventType() ctypes.ProviderResourceEvent {
	return ev.eventType
}

func (ev hostnameResourceEvent) GetServiceName() string {
	return ev.serviceName
}

func (ev hostnameResourceEvent) GetExternalPort() uint32 {
	return ev.externalPort
}
