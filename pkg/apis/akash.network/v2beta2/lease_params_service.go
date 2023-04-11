package v2beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LeaseParamsService
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type LeaseParamsService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   LeaseParamsServiceSpec   `json:"spec,omitempty"`
	Status LeaseParamsServiceStatus `json:"status,omitempty"`
}

// LeaseParamsServiceList
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type LeaseParamsServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LeaseParamsService `json:"items"`
}

type Params struct {
	RuntimeClass string `json:"runtime_class"`
}

type ParamsService struct {
	Name   string `json:"name"`
	Params Params `json:"params"`
}

type ParamsServices []ParamsService

type LeaseParamsServiceSpec struct {
	LeaseID  LeaseID        `json:"lease_id"`
	Services ParamsServices `json:"services"`
}

type LeaseParamsServiceStatus struct {
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}
