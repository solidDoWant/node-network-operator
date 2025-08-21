package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeLinksSpec defines the desired state of NodeLinks
type NodeLinksSpec struct {
	// MatchingLinks is a list of bridge names that match the node's configuration.
	// +listType=set
	// +optional
	MatchingLinks []string `json:"matchinglinks,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// NodeLinksStatus defines the observed state of NodeLinks.
type NodeLinksStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// NodeLinks is the Schema for the nodelinks API
type NodeLinks struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of NodeLinks
	// +required
	Spec NodeLinksSpec `json:"spec"`

	// status defines the observed state of NodeLinks
	// +optional
	Status NodeLinksStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// NodeLinksList contains a list of NodeLinks
type NodeLinksList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeLinks `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeLinks{}, &NodeLinksList{})
}
