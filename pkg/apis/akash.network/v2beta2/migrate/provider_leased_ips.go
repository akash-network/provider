package migrate

import (
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta1"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func ProviderIPsSpecFromV2beta1(from v2beta1.ProviderLeasedIPSpec) v2beta2.ProviderLeasedIPSpec {
	to := v2beta2.ProviderLeasedIPSpec{
		LeaseID:      LeaseIDFromV2beta1(from.LeaseID),
		ServiceName:  from.ServiceName,
		Port:         from.Port,
		ExternalPort: from.ExternalPort,
		SharingKey:   from.SharingKey,
		Protocol:     from.Protocol,
	}

	return to
}
