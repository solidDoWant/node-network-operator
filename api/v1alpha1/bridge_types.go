package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BridgeSpec defines the desired state of Bridge.
type BridgeSpec struct {
	// InterfaceName is the name of the network interface to be used for the bridge.
	// If a bridge interface with this name already exists, it will be adopted.
	// If it does not exist, a new interface will be created.
	// It is the responsibility of the user to ensure that the interface name is unique across the node.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[^\s/]+$`
	// +kubebuilder:validation:MaxLength=15
	// TODO dont let this change once it has been set
	InterfaceName string `json:"interfaceName"`

	// MTU is the maximum transmission unit for the bridge interface.
	// This should be at least as large as the largest frame payload that will be sent over the bridge.
	// If not specified, the default MTU for the node will be used.
	// +kubebuilder:validation:Minimum=68
	// +kubebuilder:validation:Maximum=65535
	// TODO don't let this be unset once it has been set
	MTU *int32 `json:"mtu,omitempty"`

	// NodeSelector is used to select nodes that the bridge should be deployed to.
	// +kubebuilder:validation:Optional
	NodeSelector metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// BridgeStatus defines the observed state of Bridge.
type BridgeStatus struct {
	// Conditions is a list of conditions that apply to the bridge configuration.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Bridge is the Schema for the bridges API.
type Bridge struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BridgeSpec   `json:"spec,omitempty"`
	Status BridgeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BridgeList contains a list of Bridge.
type BridgeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bridge `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Bridge{}, &BridgeList{})
}
