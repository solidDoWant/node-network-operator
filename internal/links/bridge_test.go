package links

import (
	"context"
	"os"
	"testing"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
	"github.com/vishvananda/netlink"
)

// Run in an isolated network namespace, e.g. `sudo unshare -n go test ./internal/links/...`.
func TestBridgeAdminStateDrift(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to manage netlink links")
	}

	const interfaceName = "br-admin-drift"

	link := &nodenetworkoperatorv1alpha1.Link{
		Spec: nodenetworkoperatorv1alpha1.LinkSpec{
			LinkName:  interfaceName,
			LinkSpecs: nodenetworkoperatorv1alpha1.LinkSpecs{Bridge: &nodenetworkoperatorv1alpha1.BridgeSpec{}},
		},
	}
	manager := NewBridgeManager(link)

	t.Cleanup(func() {
		_ = netlink.LinkDel(&netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: interfaceName}})
	})

	if err := manager.Upsert(context.Background(), nil, nil); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	bridge, err := netlink.LinkByName(interfaceName)
	if err != nil {
		t.Fatalf("failed to get link: %v", err)
	}
	if err := netlink.LinkSetDown(bridge); err != nil {
		t.Fatalf("failed to set link down: %v", err)
	}

	needed, err := manager.IsUpsertNeeded(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("upsert check failed: %v", err)
	}
	if !needed {
		t.Fatal("expected an administratively down bridge to require an upsert")
	}

	if err := manager.Upsert(context.Background(), nil, nil); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	needed, err = manager.IsUpsertNeeded(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("upsert check failed: %v", err)
	}
	if needed {
		t.Fatal("upsert still needed after upsert")
	}
}
