package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LinkReference is a reference to another link resource.
type LinkReference struct{}

// VXLANSpecs defines the desired state of the link as a VXLAN.
type VXLANSpecs struct {
	// VNID is the Virtual Network Identifier for the underlay network.
	// +kubebuilder:validation:Requried
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=16777215
	VNID *int32 `json:"vnid,omitempty"`

	// Address is the multicast address used for VXLAN encapsulation. The default address is scoped to the local
	// subnet, and will not be forwarded by routers to other subnets.
	// +kubebuilder:validation:Requried
	// +kubebuilder:validation:Format=ipv4
	MulticastAddress string `json:"multicastAddress,omitempty"`

	// Port is the UDP port used for VXLAN encapsulation.
	// +kubebuilder:default=4789
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// Device is the network interface used for vxlan-encapsulated traffic.
	// If not specified, the default is to use the first interface that netlink can use when sending traffic to the
	// multicast address - effectively `ip route get <multicast address>`.
	// +kubebuilder:validation:Optional
	Device *LinkReference `json:"device,omitempty"`

	// RemoteIPAddress defines the remote VTEP(s) for VXLAN traffic. This can refer to a single
	// host, or a multicast group.
	RemoteIPAddress string `json:"remoteIPAddress,omitempty"`
}

// TODO rename, this is just to prevent a conflict during dev
// BridgeSpec2 defines the desired state of the link as a bridge.
type BridgeSpec2 struct {
	// MTU is the maximum transmission unit for the bridge interface.
	// This should be at least as large as the largest frame payload that will be sent over the bridge.
	// If not specified, the default MTU for the node will be used.
	// +kubebuilder:validation:Minimum=68
	// +kubebuilder:validation:Maximum=65535
	MTU *int32 `json:"mtu,omitempty"`
}

// +kubebuilder:validation:ExactlyOneOf=bridge;vxlan
type LinkSpecs struct {
	// Bridge defines the desired state of the link as a bridge.
	// +optional
	Bridge *BridgeSpec2 `json:"bridge,omitempty"`

	// VXLAN defines the desired state of the link as a VXLAN.
	// +optional
	VXLAN *VXLANSpecs `json:"vxlan,omitempty"`
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
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
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

// TODO important:
// * Add ability to block traffic forwarding between devices. iptable support maybe?
// * Add ability to manage addresses
// * Maybe add wg support?
