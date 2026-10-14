package v2beta2

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mtypes "pkg.akt.dev/go/node/market/v1"
)

// ProviderHost
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderHost struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec ProviderHostSpec `json:"spec,omitempty"`
}

// ProviderHostList
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderHostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ProviderHost `json:"items"`
}

type ProviderHostStatus struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

// ProviderHostSpec keeps the lease ID fields flat: the CRD schema and the
// objects stored in clusters encode dseq as a number, unlike LeaseID.
type ProviderHostSpec struct {
	Owner        string `json:"owner"`
	Provider     string `json:"provider"`
	Hostname     string `json:"hostname"`
	Dseq         uint64 `json:"dseq"`
	Gseq         uint32 `json:"gseq"`
	Oseq         uint32 `json:"oseq"`
	ServiceName  string `json:"service_name"`
	ExternalPort uint32 `json:"external_port"`
}

// LeaseID returns the lease the hostname belongs to, validating the owner and provider addresses.
func (s ProviderHostSpec) LeaseID() (mtypes.LeaseID, error) {
	owner, err := sdk.AccAddressFromBech32(s.Owner)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	provider, err := sdk.AccAddressFromBech32(s.Provider)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	return mtypes.LeaseID{
		Owner:    owner.String(),
		DSeq:     s.Dseq,
		GSeq:     s.Gseq,
		OSeq:     s.Oseq,
		Provider: provider.String(),
	}, nil
}
