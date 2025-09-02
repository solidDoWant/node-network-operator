package links

import (
	"context"
	"fmt"
	"net"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

type VXLANManager struct {
	link *nodenetworkoperatorv1alpha1.Link
}

var _ Manager = (*VXLANManager)(nil)

func NewVXLANManager(link *nodenetworkoperatorv1alpha1.Link) *VXLANManager {
	return &VXLANManager{
		link: link,
	}
}

func (m *VXLANManager) GetDependencies() []nodenetworkoperatorv1alpha1.LinkReference {
	dependencies := make([]nodenetworkoperatorv1alpha1.LinkReference, 0, 2)

	if m.link.Spec.VXLAN.Master != nil {
		dependencies = append(dependencies, *m.link.Spec.VXLAN.Master)
	}

	if m.link.Spec.VXLAN.Device != nil {
		dependencies = append(dependencies, *m.link.Spec.VXLAN.Device)
	}

	return dependencies
}

func (m *VXLANManager) IsUpsertNeeded(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) (bool, error) {
	link, err := netlink.LinkByName(m.link.Spec.LinkName)
	if err != nil {
		if IsLinkNotFoundError(err) {
			// VXLAN does not exist, upsert needed
			return true, nil
		}

		return false, fmt.Errorf("failed to get netlink link %q: %w", m.link.Spec.LinkName, err)
	}

	// Check if the existing VXLAN matches the desired configuration
	if link.Type() != "vxlan" {
		// Existing link is not a VXLAN, upsert needed
		return true, nil
	}

	vxlanLink, ok := link.(*netlink.Vxlan)
	if !ok {
		return false, fmt.Errorf("link %q is not a vxlan link", m.link.Spec.LinkName)
	}

	if m.link.Spec.VXLAN.MTU != nil && vxlanLink.LinkAttrs.MTU != int(*m.link.Spec.VXLAN.MTU) {
		return true, nil
	}

	needsUpdate, err := doesLinkRefNeedUpdate(m.link.Spec.VXLAN.Master, vxlanLink.LinkAttrs.MasterIndex, links)
	if err != nil {
		return false, fmt.Errorf("failed to check if master link needs update: %w", err)
	}
	if needsUpdate {
		return true, nil
	}

	if !vxlanLink.Group.Equal(net.ParseIP(m.link.Spec.VXLAN.RemoteIPAddress)) {
		return true, nil
	}

	if vxlanLink.VxlanId != int(m.link.Spec.VXLAN.VNID) {
		return true, nil
	}

	if vxlanLink.Port != int(m.link.Spec.VXLAN.RemotePort) {
		return true, nil
	}

	needsUpdate, err = doesLinkRefNeedUpdate(m.link.Spec.VXLAN.Device, vxlanLink.VtepDevIndex, links)
	if err != nil {
		return false, fmt.Errorf("failed to check if device link needs update: %w", err)
	}
	if needsUpdate {
		return true, nil
	}

	if vxlanLink.OperState != netlink.OperUp {
		return true, nil
	}

	return false, nil
}

func (m *VXLANManager) Upsert(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) error {
	vxlan := &netlink.Vxlan{
		LinkAttrs: netlink.NewLinkAttrs(),
		VxlanId:   int(m.link.Spec.VXLAN.VNID),
		Port:      int(m.link.Spec.VXLAN.RemotePort),
		PortLow:   int(m.link.Spec.VXLAN.SourcePort.Start),
		PortHigh:  int(m.link.Spec.VXLAN.SourcePort.End),
	}
	vxlan.Name = m.link.Spec.LinkName

	link, err := basicUpsertWithCheck(vxlan, m.forceReplace(vxlan))
	if err != nil {
		return fmt.Errorf("failed to upsert vxlan %q: %w", m.link.Spec.LinkName, err)
	}

	vxlan, ok := link.(*netlink.Vxlan)
	if !ok {
		return fmt.Errorf("failed to cast link %q to vxlan", m.link.Spec.LinkName)
	}

	if m.link.Spec.VXLAN.MTU != nil && vxlan.LinkAttrs.MTU != int(*m.link.Spec.VXLAN.MTU) {
		if err := netlink.LinkSetMTU(vxlan, int(*m.link.Spec.VXLAN.MTU)); err != nil {
			return fmt.Errorf("failed to set MTU for vxlan %q: %w", m.link.Spec.LinkName, err)
		}
	}

	needsUpdate, err := doesLinkRefNeedUpdate(m.link.Spec.VXLAN.Device, vxlan.VtepDevIndex, links)
	if err != nil {
		return fmt.Errorf("failed to check if device link needs update: %w", err)
	}
	if needsUpdate {
		desiredIndex, err := getDesiredReferencedLinkIndex(m.link.Spec.VXLAN.Device, links)
		if err != nil {
			return fmt.Errorf("failed to get desired device link index: %w", err)
		}

		if err := linkSetVXLANVtepDevIndex(vxlan, desiredIndex); err != nil {
			return fmt.Errorf("failed to set VXLAN VTEP device for %q: %w", m.link.Spec.LinkName, err)
		}
	}

	// Important: this must be done _after_ the link VTEP device index is set, as the kernel
	// requires that multicast group membership is done on the VTEP device.
	desiredRemoteAddress := net.ParseIP(m.link.Spec.VXLAN.RemoteIPAddress)
	if !vxlan.Group.Equal(desiredRemoteAddress) {
		if err := linkSetVXLANGroup(vxlan, desiredRemoteAddress); err != nil {
			return fmt.Errorf("failed to set VXLAN group for %q: %w", m.link.Spec.LinkName, err)
		}
	}

	needsUpdate, err = doesLinkRefNeedUpdate(m.link.Spec.VXLAN.Master, vxlan.LinkAttrs.MasterIndex, links)
	if err != nil {
		return fmt.Errorf("failed to check if master link needs update: %w", err)
	}
	if needsUpdate {
		desiredIndex, err := getDesiredReferencedLinkIndex(m.link.Spec.VXLAN.Master, links)
		if err != nil {
			return fmt.Errorf("failed to get desired master link index: %w", err)
		}

		if err := netlink.LinkSetMasterByIndex(vxlan, desiredIndex); err != nil {
			return fmt.Errorf("failed to set master for vxlan %q: %w", m.link.Spec.LinkName, err)
		}
	}

	if err := netlink.LinkSetUp(vxlan); err != nil {
		return fmt.Errorf("failed to set vxlan %q up: %w", m.link.Spec.LinkName, err)
	}

	return nil
}

func (m *VXLANManager) IsManaged() bool {
	return true
}

func (m *VXLANManager) forceReplace(desiredLink netlink.Link) func(existingLink netlink.Link) bool {
	return func(existingLink netlink.Link) bool {
		// Force replace if the existing link is not of the desired type
		// This cover many common link types
		if existingLink.Type() != desiredLink.Type() {
			return true
		}

		existingVXLAN, ok := existingLink.(*netlink.Vxlan)
		if !ok {
			// Existing link is not a VXLAN, force replace
			return true
		}

		desiredVXLAN, ok := desiredLink.(*netlink.Vxlan)
		if !ok {
			// Desired link is not a VXLAN, should not happen
			return true
		}

		// Force replace if the VNID has changed
		if existingVXLAN.VxlanId != desiredVXLAN.VxlanId {
			return true
		}

		// Force replace if source port range has changed
		if existingVXLAN.PortLow != desiredVXLAN.PortLow || existingVXLAN.PortHigh != desiredVXLAN.PortHigh {
			return true
		}

		// Force replace if the destination port has changed
		if existingVXLAN.Port != desiredVXLAN.Port {
			return true
		}

		// Force a replace if the IP family of the group has changed
		// The desired remote IP address will always be a set IPv4 address. Force a replacement when the
		// existing address is not an IPv4 address, and is not empty.
		if len(existingVXLAN.Group) != net.IPv4len && len(existingVXLAN.Group) != 0 {
			return true
		}

		return false
	}
}

// These functions are needed until https://github.com/vishvananda/netlink/pull/1123 is merged and released.

func linkSetVXLANGroup(link netlink.Link, group net.IP) error {
	req := nl.NewNetlinkRequest(unix.RTM_NEWLINK, unix.NLM_F_ACK)

	msg := nl.NewIfInfomsg(unix.AF_UNSPEC)
	msg.Index = int32(link.Attrs().Index)
	req.AddData(msg)

	linkInfo := nl.NewRtAttr(unix.IFLA_LINKINFO, nil)
	linkInfo.AddRtAttr(nl.IFLA_INFO_KIND, nl.NonZeroTerminated(link.Type()))

	data := linkInfo.AddRtAttr(nl.IFLA_INFO_DATA, nil)

	if v4Group := group.To4(); v4Group != nil {
		data.AddRtAttr(nl.IFLA_VXLAN_GROUP, []byte(v4Group))
	} else if v6group := group.To16(); v6group != nil {
		data.AddRtAttr(nl.IFLA_VXLAN_GROUP6, []byte(v6group))
	} else {
		return fmt.Errorf("invalid group address %q", group.String())
	}

	req.AddData(linkInfo)

	_, err := req.Execute(unix.NETLINK_ROUTE, 0)
	return err
}

func linkSetVXLANVtepDevIndex(link netlink.Link, ifIndex int) error {
	req := nl.NewNetlinkRequest(unix.RTM_NEWLINK, unix.NLM_F_ACK)

	msg := nl.NewIfInfomsg(unix.AF_UNSPEC)
	msg.Index = int32(link.Attrs().Index)
	req.AddData(msg)

	linkInfo := nl.NewRtAttr(unix.IFLA_LINKINFO, nil)
	linkInfo.AddRtAttr(nl.IFLA_INFO_KIND, nl.NonZeroTerminated(link.Type()))

	data := linkInfo.AddRtAttr(nl.IFLA_INFO_DATA, nil)
	data.AddRtAttr(nl.IFLA_VXLAN_LINK, nl.Uint32Attr(uint32(ifIndex)))

	req.AddData(linkInfo)

	_, err := req.Execute(unix.NETLINK_ROUTE, 0)
	return err
}
