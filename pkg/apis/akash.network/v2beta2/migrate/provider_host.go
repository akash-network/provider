package migrate

import (
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta1"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func ProviderHostsSpecFromV2beta1(from v2beta1.ProviderHostSpec) v2beta2.ProviderHostSpec {
	to := v2beta2.ProviderHostSpec{
		Owner:        from.Owner,
		Provider:     from.Provider,
		Hostname:     from.Hostname,
		Dseq:         from.Dseq,
		Gseq:         from.Gseq,
		Oseq:         from.Oseq,
		ServiceName:  from.ServiceName,
		ExternalPort: from.ExternalPort,
	}

	return to
}
