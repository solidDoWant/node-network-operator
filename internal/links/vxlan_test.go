package links

import (
	"context"
	"os"
	"testing"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
	"github.com/vishvananda/netlink"
	"k8s.io/utils/ptr"
)

// Run in an isolated network namespace, e.g. `sudo unshare -n go test ./internal/links/...`.
func TestVXLANLearning(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to manage netlink links")
	}

	const interfaceName = "vxlan-learn"

	newLink := func(learning *bool) *nodenetworkoperatorv1alpha1.Link {
		return &nodenetworkoperatorv1alpha1.Link{
			Spec: nodenetworkoperatorv1alpha1.LinkSpec{
				LinkName: interfaceName,
				LinkSpecs: nodenetworkoperatorv1alpha1.LinkSpecs{
					VXLAN: &nodenetworkoperatorv1alpha1.VXLANSpecs{
						VNID:            4242,
						RemoteIPAddress: "10.255.0.1",
						RemotePort:      4789,
						SourcePort:      &nodenetworkoperatorv1alpha1.PortRange{Start: 4789, End: 4789},
						Learning:        learning,
					},
				},
			},
		}
	}

	getVXLAN := func(t *testing.T) *netlink.Vxlan {
		t.Helper()
		link, err := netlink.LinkByName(interfaceName)
		if err != nil {
			t.Fatalf("failed to get link: %v", err)
		}
		vxlan, ok := link.(*netlink.Vxlan)
		if !ok {
			t.Fatalf("link is a %s, not a vxlan", link.Type())
		}
		return vxlan
	}

	upsert := func(t *testing.T, link *nodenetworkoperatorv1alpha1.Link) {
		t.Helper()
		manager := NewVXLANManager(link)
		if err := manager.Upsert(context.Background(), nil, nil); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}
		needed, err := manager.IsUpsertNeeded(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("upsert check failed: %v", err)
		}
		if needed {
			t.Fatal("upsert still needed after upsert")
		}
	}

	t.Cleanup(func() {
		_ = netlink.LinkDel(&netlink.Vxlan{LinkAttrs: netlink.LinkAttrs{Name: interfaceName}})
	})

	t.Run("defaults to learning", func(t *testing.T) {
		upsert(t, newLink(nil))
		if !getVXLAN(t).Learning {
			t.Fatal("expected learning to be enabled")
		}
	})

	t.Run("disables learning in place", func(t *testing.T) {
		index := getVXLAN(t).Index
		link := newLink(ptr.To(false))

		needed, err := NewVXLANManager(link).IsUpsertNeeded(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("upsert check failed: %v", err)
		}
		if !needed {
			t.Fatal("expected learning drift to require an upsert")
		}

		upsert(t, link)
		vxlan := getVXLAN(t)
		if vxlan.Learning {
			t.Fatal("expected learning to be disabled")
		}
		if vxlan.Index != index {
			t.Fatal("expected link to be updated in place, not replaced")
		}
	})

	t.Run("enables learning in place", func(t *testing.T) {
		index := getVXLAN(t).Index

		upsert(t, newLink(ptr.To(true)))
		vxlan := getVXLAN(t)
		if !vxlan.Learning {
			t.Fatal("expected learning to be enabled")
		}
		if vxlan.Index != index {
			t.Fatal("expected link to be updated in place, not replaced")
		}
	})
}

func TestVXLANSourcePortRange(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to manage netlink links")
	}

	const interfaceName = "vxlan-srcport"

	newLink := func(start, end int32) *nodenetworkoperatorv1alpha1.Link {
		return &nodenetworkoperatorv1alpha1.Link{
			Spec: nodenetworkoperatorv1alpha1.LinkSpec{
				LinkName: interfaceName,
				LinkSpecs: nodenetworkoperatorv1alpha1.LinkSpecs{
					VXLAN: &nodenetworkoperatorv1alpha1.VXLANSpecs{
						VNID:            4243,
						RemoteIPAddress: "10.255.0.1",
						RemotePort:      4789,
						SourcePort:      &nodenetworkoperatorv1alpha1.PortRange{Start: start, End: end},
					},
				},
			},
		}
	}

	t.Cleanup(func() {
		_ = netlink.LinkDel(&netlink.Vxlan{LinkAttrs: netlink.LinkAttrs{Name: interfaceName}})
	})

	if err := NewVXLANManager(newLink(4789, 4789)).Upsert(context.Background(), nil, nil); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	link := newLink(10000, 20000)
	manager := NewVXLANManager(link)

	needed, err := manager.IsUpsertNeeded(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("upsert check failed: %v", err)
	}
	if !needed {
		t.Fatal("expected source port range drift to require an upsert")
	}

	if err := manager.Upsert(context.Background(), nil, nil); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	existing, err := netlink.LinkByName(interfaceName)
	if err != nil {
		t.Fatalf("failed to get link: %v", err)
	}
	vxlan, ok := existing.(*netlink.Vxlan)
	if !ok {
		t.Fatalf("link is a %s, not a vxlan", existing.Type())
	}
	if vxlan.PortLow != 10000 || vxlan.PortHigh != 20000 {
		t.Fatalf("expected source port range 10000-20000, got %d-%d", vxlan.PortLow, vxlan.PortHigh)
	}

	needed, err = manager.IsUpsertNeeded(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("upsert check failed: %v", err)
	}
	if needed {
		t.Fatal("upsert still needed after upsert")
	}
}
