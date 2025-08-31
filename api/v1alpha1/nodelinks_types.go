package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeLinksSpec defines the desired state of NodeLinks
type NodeLinksSpec struct {
	// MatchingLinks is a list of bridge names that match the node's configuration.
	// +listType=set
	// +optional
	MatchingLinks []string `json:"matchingLinks,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

const (
	NodeLinkConditionReady = "Ready"
)

const (
	NetlinkLinkConditionReady                    = "Ready"
	NetlinkLinkConditionValidConfiguration       = "ValidConfiguration"
	NetlinkLinkConditionDependencyLinksAvailable = "DependencyLinksAvailable"
	NetlinkLinkConditionOperationallyUp          = "OperationallyUp"
)

// NodeLinksStatusConditions is a list of conditions that apply to a specific node link configuration.
// +listType=map
// +listMapKey=type
type NodeLinksStatusConditions []metav1.Condition

// NodeLinksStatus defines the observed state of NodeLinks.
type NodeLinksStatus struct {
	// Conditions is a list of conditions that apply to the nodelinks configuration.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// NetlinkLinkConditions is a list of conditions that apply to the node netlink links.
	// The key is the netlink link name, and the value is the condition status.
	// +optional
	NetlinkLinkConditions map[string]NodeLinksStatusConditions `json:"netlinkLinkConditions,omitempty"`

	// LastAttemptedNetlinkLinks is a list of links that were for which deployment was previously
	// attempted on the node. This is useful for tracking changes in the node's link configuration.
	// This refers to netlink (interface) links, not Link resources.
	// +listType=set
	// +optional
	LastAttemptedNetlinkLinks []string `json:"deployedLinks,omitempty"`
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
