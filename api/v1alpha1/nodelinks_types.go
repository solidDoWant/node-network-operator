package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NodeLinksSpec defines the desired state of NodeLinks
type NodeLinksSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// foo is an example field of NodeLinks. Edit nodelinks_types.go to remove/update
	// +optional
	Foo *string `json:"foo,omitempty"`
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
