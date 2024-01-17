package v2beta2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModerationFilter store metadata, specifications and status of the ModerationFilter
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ModerationFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec []ModerationFilterSpec `json:"spec,omitempty"`
}

// ModerationFilterList stores metadata and items list of moderationfilter
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ModerationFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ModerationFilter `json:"items"`
}

// ModerationFilterSpec stores LeaseID, Group and metadata details
type ModerationFilterSpec struct {
	Type			string			`json:"type"`
	Allow			bool 				`json:"allow"`
	Pattern		string 			`json:"pattern"`
}
