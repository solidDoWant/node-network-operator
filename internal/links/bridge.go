package links

import (
	"context"
	"fmt"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
	"github.com/vishvananda/netlink"
)

type BridgeManager struct {
	link *nodenetworkoperatorv1alpha1.Link
}

var _ Manager = (*BridgeManager)(nil)

func NewBridgeManager(link *nodenetworkoperatorv1alpha1.Link) *BridgeManager {
	return &BridgeManager{
		link: link,
	}
}

func (m *BridgeManager) GetDependencies() []nodenetworkoperatorv1alpha1.LinkReference {
	return nil
}

func (m *BridgeManager) IsUpsertNeeded(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) (bool, error) {
	link, err := netlink.LinkByName(m.link.Spec.LinkName)
	if err != nil {
		if IsLinkNotFoundError(err) {
			// Bridge does not exist, upsert needed
			return true, nil
		}

		return false, fmt.Errorf("failed to get netlink link %q: %w", m.link.Spec.LinkName, err)
	}

	// Check if the existing bridge matches the desired configuration
	if link.Type() != "bridge" {
		// Existing link is not a bridge, upsert needed
		return true, nil
	}

	linkAttrs := link.Attrs()
	if linkAttrs == nil {
		return false, fmt.Errorf("bridge %q has nil attributes", m.link.Spec.LinkName)
	}

	if m.link.Spec.Bridge.MTU != nil && linkAttrs.MTU != int(*m.link.Spec.Bridge.MTU) {
		return true, nil
	}

	return false, nil
}

func (m *BridgeManager) Upsert(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) error {
	bridge := &netlink.Bridge{
		LinkAttrs: netlink.NewLinkAttrs(),
	}
	bridge.Name = m.link.Spec.LinkName

	link, err := basicUpsert(bridge)
	if err != nil {
		return fmt.Errorf("failed to upsert bridge %q: %w", m.link.Spec.LinkName, err)
	}

	bridge, ok := link.(*netlink.Bridge)
	if !ok {
		return fmt.Errorf("failed to cast link %q to bridge", m.link.Spec.LinkName)
	}

	if m.link.Spec.Bridge.MTU != nil && bridge.LinkAttrs.MTU != int(*m.link.Spec.Bridge.MTU) {
		if err := netlink.LinkSetMTU(bridge, int(*m.link.Spec.Bridge.MTU)); err != nil {
			return fmt.Errorf("failed to set MTU for bridge %q: %w", m.link.Spec.LinkName, err)
		}
	}

	if err := netlink.LinkSetUp(bridge); err != nil {
		return fmt.Errorf("failed to set bridge %q up: %w", m.link.Spec.LinkName, err)
	}

	return nil
}
