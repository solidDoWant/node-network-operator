package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LinkReference is a reference to another link resource.
type LinkReference struct {
	// There isn't currently a need for these fields, as there is only one link type.
	// for adding them in the future, without breaking the API.

	// // Group is the API group of the referenced link resource.
	// // +kubebuilder:validation:Optional
	// // +kubebuilder:default:nodenetworkoperator.soliddowant.dev
	// Group string `json:"group,omitempty"`

	// // Kind is the kind of the referenced link resource.
	// // +kubebuilder:validation:Optional
	// // +kubebuilder:default:Link
	// Kind string `json:"kind,omitempty"`

	// Name is the name of the referenced link resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Optional specifies whether the link reference is optional. If false, the link
	// will be brought down if the referenced link is not found, or is not ready.
	// Warning: setting this to "true" could situationally lead to packets not matching
	// the correct netfilter rules, resulting in traffic passed, dropped, or misrouted.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	Optional bool `json:"optional,omitempty"`

	// SetDownOnDependencyUpdate specifies whether the link should be brought down when
	// the dependency link is updated. Under certain specific conditions, when a dependency
	// link is updated, the dependent link may temporarily be a part of an undesired
	// dependency chain. This can lead to packets being sent over the wrong link, or dropped.
	// Setting this to true will cause the link to be brought down when the dependency link,
	// or any of its dependencies, are updated. It will be brought back up once all dependencies
	// are in their desired states.
	//
	// Here is an example of when this can happen when this option is false ("linkA -> linkB"
	// means "linkA is the master of linkB"):
	// Starting state: linkA -> linkB -> linkC, linkD, linkE
	// Desired state: linkA, linkD -> linkB, linkE -> linkC
	// If linkB is updated prior to linkC, then the interfaces will have an intermediate state of:
	// linkA, linkD -> linkB -> linkC, linkE
	// In this state, traffic that is supposed to go over linkE via the desired linkE -> linkC
	// chain will instead be sent over linkB. By setting this to "true", linkC will be brought
	// down when linkB is updated, and brought back up after it moves to linkE, preventing this
	// issue.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	SetDownOnDependencyChainUpdate bool `json:"setDownOnDependencyUpdate,omitempty"`
}

// PortRange defines a range of ports.
// +kubebuilder:validation:XValidation:rule="self.end >= self.start",message="end must be greater than or equal to start"
type PortRange struct {
	// Start is the starting port of the range.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Start int32 `json:"start,omitempty"`

	// End is the ending port of the range.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	End int32 `json:"end,omitempty"`
}

// VXLANSpecs defines the desired state of the link as a VXLAN.
type VXLANSpecs struct {
	// VNID is the Virtual Network Identifier for the underlay network.
	// Note: Only one VXLAN interface with a given VNID can exist on a node. This is not validated
	// by the operator, and is the responsibility of the user to ensure this is unique across
	// all VXLAN links on the node. Deploying multiple VXLAN links with the same VNID will result
	// in the operator repeatedly trying to create the link, and failing.
	// +kubebuilder:validation:Requried
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=16777215
	VNID int32 `json:"vnid,omitempty"`

	// RemoteIPAddress defines the remote VTEP(s) for VXLAN traffic. This can refer to a single
	// host, or a multicast group.
	// +kubebuilder:validation:Requried
	// +kubebuilder:validation:Format=ipv4
	RemoteIPAddress string `json:"remoteIPAddress,omitempty"`

	// RemotePort is the remote VTEP UDP port used for VXLAN encapsulation.
	// Multiple VTEPS can share the same port, as long as they have different VNIDs.
	// +kubebuilder:default=4789
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	RemotePort int32 `json:"port,omitempty"`

	// SourcePort is the source UDP port used for VXLAN encapsulation.
	// +kubebuilder:default={start: 4789, end: 4789}
	SourcePort *PortRange `json:"sourcePort,omitempty"`

	// Device is the network interface used for vxlan-encapsulated traffic.
	// If not specified, the default is to use the first interface that netlink can use when sending traffic to the
	// multicast address - effectively `ip route get <multicast address>`.
	// Note: Setting this will cause netlink to delete the vxlan link immediately if the referenced device is deleted.
	// +kubebuilder:validation:Optional
	Device *LinkReference `json:"device,omitempty"`

	// Master is the link that this VXLAN interface will be enslaved to.
	// This is typically a bridge interface.
	// +kubebuilder:validation:Optional
	Master *LinkReference `json:"master,omitempty"`

	// MTU is the maximum transmission unit for the VXLAN interface.
	// This should be at least as large as the largest frame payload that will be sent over the VXLAN tunnel, but less
	// that then MTU of the master (if any).
	// If not specified, the default MTU for the node will be used.
	// +kubebuilder:validation:Minimum=68
	// +kubebuilder:validation:Maximum=65535
	MTU *int32 `json:"mtu,omitempty"`
}

// BridgeSpec defines the desired state of the link as a bridge.
type BridgeSpec struct {
	// MTU is the maximum transmission unit for the bridge interface.
	// This should be at least as large as the largest frame payload that will be sent over the bridge.
	// If not specified, the default MTU for the node will be used.
	// +kubebuilder:validation:Minimum=68
	// +kubebuilder:validation:Maximum=65535
	MTU *int32 `json:"mtu,omitempty"`
}

// UnmanagedSpec defines that the link is unmanaged by the operator.
type UnmanagedSpec struct {
	// This struct is intentionally left empty.
	// It serves as a marker to indicate that the link is unmanaged.
}

// +kubebuilder:validation:ExactlyOneOf=bridge;vxlan;unmanaged
type LinkSpecs struct {
	// Bridge defines the desired state of the link as a bridge.
	// +optional
	Bridge *BridgeSpec `json:"bridge,omitempty"`

	// VXLAN defines the desired state of the link as a VXLAN.
	// +optional
	VXLAN *VXLANSpecs `json:"vxlan,omitempty"`

	// Unmanaged indicates that the link is unmanaged by the operator.
	// This can be used to reference links that are managed outside of the operator.
	// +optional
	Unmanaged *UnmanagedSpec `json:"unmanaged,omitempty"`
}

// LinkSpec defines the desired state of Link
type LinkSpec struct {
	// LinkName is the name that should be used for the actual network link.
	// If a link interface with this name already exists, it will be adopted.
	// If it does not exist, a new link will be created.
	// It is the responsibility of the user to ensure that the link name is unique across selected nodes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[^\s/]+$`
	// +kubebuilder:validation:MaxLength=15
	LinkName string `json:"interfaceName"`

	// NodeSelector is used to select nodes that the link should be deployed to.
	// +kubebuilder:validation:Optional
	NodeSelector metav1.LabelSelector `json:"nodeSelector,omitempty"`

	LinkSpecs `json:",inline"`
}

// LinkStatus defines the observed state of Link.
type LinkStatus struct {
	// Conditions is a list of conditions that apply to the bridge configuration.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// MatchedNodes is a list of node names that match the bridge's node selector.
	// This is used to track which nodes the bridge is deployed to.
	// +listType=atomic
	// +optional
	MatchedNodes []string `json:"matchedNodes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Link is the Schema for the links API
type Link struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Link
	// +required
	Spec LinkSpec `json:"spec"`

	// status defines the observed state of Link
	// +optional
	Status LinkStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// LinkList contains a list of Link
type LinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Link `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Link{}, &LinkList{})
}
