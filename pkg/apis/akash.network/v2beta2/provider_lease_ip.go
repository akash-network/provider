package v2beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderLeasedIP
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderLeasedIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec ProviderLeasedIPSpec `json:"spec,omitempty"`
}

// ProviderLeasedIPList
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ProviderLeasedIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ProviderLeasedIP `json:"items"`
}

type ProviderLeasedIPStatus struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

type ProviderLeasedIPSpec struct {
	LeaseID      LeaseID `json:"lease_id"`
	ServiceName  string  `json:"service_name"`
	Port         uint32  `json:"port"`
	ExternalPort uint32  `json:"external_port"`
	SharingKey   string  `json:"sharing_key"`
	Protocol     string  `json:"protocol"`
}
