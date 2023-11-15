package hostnameoperator

import (
	"time"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

type managedHostname struct {
	lastEvent    ctypes.HostnameResourceEvent
	presentLease mtypes.LeaseID

	presentServiceName  string
	presentExternalPort uint32
	lastChangeAt        time.Time
}
