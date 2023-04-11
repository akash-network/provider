package v2beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderHost
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderHost struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ProviderHostSpec   `json:"spec,omitempty"`
	Status ProviderHostStatus `json:"status,omitempty"`
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
