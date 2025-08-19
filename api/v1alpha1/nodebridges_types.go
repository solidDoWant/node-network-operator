package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeBridgesSpec defines the desired state of NodeBridges.
type NodeBridgesSpec struct {
	// MatchingBridges is a list of bridge names that match the node's configuration.
	// +listType=set
	// +optional
	MatchingBridges []string `json:"matchingBridges,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// NodeBridgesStatusConditions is a list of conditions that apply to a specific node bridge configuration.
// +listType=map
// +listMapKey=type
type NodeBridgesStatusConditions []metav1.Condition

// NodeBridgesStatus defines the observed state of NodeBridges.
type NodeBridgesStatus struct {
	// Conditions is a list of conditions that apply to the resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LinkConditions is a list of conditions that apply to the node bridges.
	// The key is the bridge link name, and the value is the condition status.
	// +optional
	LinkConditions map[string]NodeBridgesStatusConditions `json:"linkConditions,omitempty"`

	// LastAttemptedBridgeLinks is a list of bridge links that were for which deployment was previously
	// attempted on the node. This is useful for tracking changes in the node's bridge configuration.
	// +listType=set
	// +optional
	LastAttemptedBridgeLinks []string `json:"deployedLinks,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// NodeBridges is the Schema for the nodebridges API.
type NodeBridges struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeBridgesSpec   `json:"spec,omitempty"`
	Status NodeBridgesStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeBridgesList contains a list of NodeBridges.
type NodeBridgesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeBridges `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeBridges{}, &NodeBridgesList{})
}
