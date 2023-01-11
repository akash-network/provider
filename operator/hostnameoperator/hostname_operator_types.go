package hostnameoperator

import (
	"time"

	mtypes "github.com/akash-network/node/x/market/types/v1beta2"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta2"
)

type managedHostname struct {
	lastEvent    ctypes.HostnameResourceEvent
	presentLease mtypes.LeaseID

	presentServiceName  string
	presentExternalPort uint32
	lastChangeAt        time.Time
}
