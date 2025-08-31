package links

import (
	"context"
	"fmt"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
	"github.com/vishvananda/netlink"
)

// Manager is an interface for managing a specific network link.
type Manager interface {
	// GetDependencies returns the list of link resource names that this link depends on.
	GetDependencies() []nodenetworkoperatorv1alpha1.LinkReference

	// IsUpsertNeeded returns true if the link needs to be upserted.
	IsUpsertNeeded(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) (bool, error)

	// Upsert brings the link to the desired state. This will only be called if IsUpsertNeeded returns true.
	Upsert(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) error
}

// doesLinkRefNeedUpdate checks if the current link reference needs to be updated to match the desired configuration.
// Returns true if an update is needed, false otherwise.
func doesLinkRefNeedUpdate(linkRef *nodenetworkoperatorv1alpha1.LinkReference, currentIndex int, linkResources map[string]*nodenetworkoperatorv1alpha1.Link) (bool, error) {
	if linkRef != nil {
		if currentIndex == 0 {
			// Link does not reference another link, but one is specified
			return true, nil
		}

		referencedLink, err := netlink.LinkByIndex(currentIndex)
		if err != nil {
			if IsLinkNotFoundError(err) {
				// Referenced link does not exist, upsert needed
				return true, nil
			}

			return false, fmt.Errorf("failed to get referenced link by index %d: %w", currentIndex, err)
		}

		linkResource, ok := linkResources[linkRef.Name]
		if !ok {
			return false, fmt.Errorf("referenced link resource %q not found", linkRef.Name)
		}

		referencedLinkAttrs := referencedLink.Attrs()
		if referencedLinkAttrs == nil {
			return false, fmt.Errorf("referenced link %q has nil attributes", referencedLink.Attrs().Name)
		}

		if referencedLinkAttrs.Name != linkResource.Spec.LinkName {
			// Referenced link name does not match the desired configuration
			return true, nil
		}
	} else if currentIndex != 0 {
		// Link has a master, but none is specified
		return true, nil
	}

	return false, nil
}

// basicUpsert creates or recreates the given link to match the desired configuration.
func basicUpsert(desiredLink netlink.Link) (netlink.Link, error) {
	return basicUpsertWithCheck(desiredLink, func(existingLink netlink.Link) bool {
		// Force replace if the existing link is not of the desired type
		// This cover many common link types
		return existingLink.Type() != desiredLink.Type()
	})
}

// basicUpsert creates or recreates the given link to match the desired configuration. It uses
// the provided shouldForceReplace function to determine if the existing link should be replaced.
func basicUpsertWithCheck(desiredLink netlink.Link, shouldForceReplace func(existingLink netlink.Link) bool) (netlink.Link, error) {
	desiredLinkAttrs := desiredLink.Attrs()
	if desiredLinkAttrs == nil {
		return nil, fmt.Errorf("desired link has nil attributes")
	}

	retrievedLink, err := netlink.LinkByName(desiredLinkAttrs.Name)

	switch {
	case err == nil:
		if !shouldForceReplace(retrievedLink) {
			break
		}

		if err := netlink.LinkDel(retrievedLink); err != nil {
			return nil, fmt.Errorf("failed to delete existing %s link %q: %w", retrievedLink.Type(), desiredLinkAttrs.Name, err)
		}
		fallthrough
	case IsLinkNotFoundError(err):
		if err := netlink.LinkAdd(desiredLink); err != nil && !IsLinkExistsError(err) {
			return nil, fmt.Errorf("failed to create %s %q: %w", desiredLink.Type(), desiredLinkAttrs.Name, err)
		}

		retrievedLink, err = netlink.LinkByName(desiredLinkAttrs.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get netlink link %q after creation: %w", desiredLinkAttrs.Name, err)
		}

		if retrievedLink.Type() != desiredLink.Type() {
			return nil, fmt.Errorf("upserted link %q exists but is not a %s", desiredLinkAttrs.Name, desiredLink.Type())
		}
	default:
		return nil, fmt.Errorf("failed to get netlink link %q: %w", desiredLinkAttrs.Name, err)
	}

	return retrievedLink, nil
}

// getDesiredReferencedLinkIndex returns the index of the link referenced by linkRef in the desired configuration.
// If linkRef is nil, returns 0. Most (all?) netlink functions interpret a link index of 0 as "no link" or "unset reference".
func getDesiredReferencedLinkIndex(linkRef *nodenetworkoperatorv1alpha1.LinkReference, linkResources map[string]*nodenetworkoperatorv1alpha1.Link) (int, error) {
	if linkRef == nil {
		return 0, nil
	}
	desiredResource, ok := linkResources[linkRef.Name]
	if !ok {
		// This shouldn't happen, as the dependency check should have caught this.
		return 0, fmt.Errorf("referenced link resource %q not found", linkRef.Name)
	}

	desiredLinkName := desiredResource.Spec.LinkName
	desiredLink, err := netlink.LinkByName(desiredLinkName)
	if err != nil {
		return 0, fmt.Errorf("failed to get desired link %q: %w", desiredLinkName, err)
	}

	desiredLinkAttrs := desiredLink.Attrs()
	if desiredLinkAttrs == nil {
		return 0, fmt.Errorf("desired link %q has nil attributes", desiredLinkName)
	}

	return desiredLinkAttrs.Index, nil
}
