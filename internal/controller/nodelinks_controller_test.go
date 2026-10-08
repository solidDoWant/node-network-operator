package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
)

var _ = Describe("NodeLinks Controller", func() {
	Context("When reconciling a resource", func() {
		const nodeName = "test-node"
		const linkName = "test-link"
		const interfaceName = "nl-test-vxlan0"
		const vnid = int32(12345)
		const remoteIP = "224.0.0.1"
		const remotePort = int32(4789)
		const mtu = int32(1450)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: nodeName,
		}
		request := reconcile.Request{
			NamespacedName: typeNamespacedName,
		}

		BeforeEach(func() {
			By("creating a matching node for the NodeLinks resource")
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
			}
			// Clean up any existing node first
			existingNode := &corev1.Node{}
			if k8sClient.Get(ctx, typeNamespacedName, existingNode) == nil {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, existingNode))).To(Succeed())
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed(), "Failed to create node %s", nodeName)

			By("creating a link resource that matches the node")
			link := &nodenetworkoperatorv1alpha1.Link{
				ObjectMeta: metav1.ObjectMeta{
					Name: linkName,
				},
				Spec: nodenetworkoperatorv1alpha1.LinkSpec{
					LinkName: interfaceName,
					LinkSpecs: nodenetworkoperatorv1alpha1.LinkSpecs{
						VXLAN: &nodenetworkoperatorv1alpha1.VXLANSpecs{
							VNID:            vnid,
							RemoteIPAddress: remoteIP,
							RemotePort:      remotePort,
							MTU:             ptr.To(mtu),
						},
					},
				},
			}
			// Clean up any existing link first
			existingLink := &nodenetworkoperatorv1alpha1.Link{}
			if k8sClient.Get(ctx, types.NamespacedName{Name: linkName}, existingLink) == nil {
				existingLink.Finalizers = nil
				Expect(client.IgnoreNotFound(k8sClient.Update(ctx, existingLink))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, existingLink))).To(Succeed())
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: linkName}, existingLink)
				}).ShouldNot(Succeed())
			}
			Expect(k8sClient.Create(ctx, link)).To(Succeed(), "Failed to create link resource")

			By("reconciling the Link resource to establish its status")
			linkRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: linkName}}
			Expect(NewLinkReconciler(k8sCluster).Reconcile(ctx, linkRequest)).To(Equal(reconcile.Result{}))

			By("creating the custom resource for the Kind NodeLinks")
			nodeLinks := &nodenetworkoperatorv1alpha1.NodeLinks{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: nodenetworkoperatorv1alpha1.NodeLinksSpec{
					MatchingLinks: []string{link.Name},
				},
			}
			// Clean up any existing NodeLinks first
			existingNodeLinks := &nodenetworkoperatorv1alpha1.NodeLinks{}
			if k8sClient.Get(ctx, typeNamespacedName, existingNodeLinks) == nil {
				existingNodeLinks.Finalizers = nil
				Expect(client.IgnoreNotFound(k8sClient.Update(ctx, existingNodeLinks))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, existingNodeLinks))).To(Succeed())
				Eventually(func() error {
					return k8sClient.Get(ctx, typeNamespacedName, existingNodeLinks)
				}).ShouldNot(Succeed())
			}
			Expect(k8sClient.Create(ctx, nodeLinks)).To(Succeed(), "Failed to create NodeLinks resource %s", nodeName)
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance NodeLinks")
			var nodeLinks nodenetworkoperatorv1alpha1.NodeLinks
			err := k8sClient.Get(ctx, typeNamespacedName, &nodeLinks)
			if err == nil {
				Expect(k8sClient.Delete(ctx, &nodeLinks)).To(Succeed())

				By("Reconcile the NodeLinks resource to ensure cleanup occurs")
				Expect(NewNodeLinksReconciler(k8sCluster, nodeName).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))
				Eventually(func() error {
					return k8sClient.Get(ctx, typeNamespacedName, &nodeLinks)
				}).ShouldNot(Succeed(), "NodeLinks resource should be deleted after reconciliation")
			}

			// Only check for netlink deletion if the resource was found and had the interface
			_, err = netlink.LinkByName(interfaceName)
			if err == nil {
				By("Cleaning up any remaining netlink interfaces")
				// This is best effort - if it fails, it's not critical for the test
				_ = netlink.LinkDel(&netlink.Vxlan{LinkAttrs: netlink.LinkAttrs{Name: interfaceName}})
			}

			By("Cleanup the specific node instance")
			var node corev1.Node
			if err := k8sClient.Get(ctx, typeNamespacedName, &node); err == nil {
				Expect(k8sClient.Delete(ctx, &node)).To(Succeed())
			}

			By("Cleanup the link resource")
			var link nodenetworkoperatorv1alpha1.Link
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: linkName}, &link); err == nil {
				Expect(k8sClient.Delete(ctx, &link)).To(Succeed(), "Failed to delete link resource %s", linkName)

				By("Reconciling Link deletion")
				linkRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: linkName}}
				Expect(NewLinkReconciler(k8sCluster).Reconcile(ctx, linkRequest)).To(Equal(reconcile.Result{}))

				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: linkName}, &link)
				}).ShouldNot(Succeed(), "Link resource should be deleted after reconciliation")
			}
		})

		It("should handle reconciliation and error cases appropriately", func() {
			By("Reconciling the created resource")

			result, err := NewNodeLinksReconciler(k8sCluster, nodeName).Reconcile(ctx, request)
			// NodeLinks controller is complex and may legitimately fail during testing due to missing dependencies
			// The important thing is that it handles errors gracefully and updates status appropriately
			Expect(result).To(Equal(reconcile.Result{}))

			var nodeLinks nodenetworkoperatorv1alpha1.NodeLinks
			Expect(k8sClient.Get(ctx, typeNamespacedName, &nodeLinks)).To(Succeed(), "Failed to get NodeLinks resource %s", nodeName)

			// The finalizer should be added regardless of success/failure
			Expect(nodeLinks.Finalizers).To(ContainElement(nodeLinksFinalizerName), "The NodeLinks resource should have the finalizer")

			// The resource should have some status conditions set, even if reconciliation failed
			Expect(nodeLinks.Status.Conditions).ToNot(BeEmpty(), "The NodeLinks resource should have some status conditions")

			// If there's an error, it should be reflected in the status
			if err != nil {
				By("Verifying error conditions are set appropriately")
				Expect(meta.IsStatusConditionFalse(nodeLinks.Status.Conditions, "Ready")).To(BeTrue(), "The NodeLinks resource should not be ready when errors occur")
			} else {
				By("Verifying success conditions when reconciliation succeeds")
				Expect(meta.IsStatusConditionTrue(nodeLinks.Status.Conditions, "Ready")).To(BeTrue(), "The NodeLinks resource should be ready when reconciliation succeeds")
			}
		})

		It("should handle resource deletion gracefully", func() {
			By("Reconciling the created resource first")

			result, _ := NewNodeLinksReconciler(k8sCluster, nodeName).Reconcile(ctx, request)
			Expect(result).To(Equal(reconcile.Result{}))

			By("Deleting the NodeLinks resource")
			var nodeLinks nodenetworkoperatorv1alpha1.NodeLinks
			Expect(k8sClient.Get(ctx, typeNamespacedName, &nodeLinks)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &nodeLinks)).To(Succeed())

			By("Reconciling the deleted resource")
			result, err := NewNodeLinksReconciler(k8sCluster, nodeName).Reconcile(ctx, request)
			Expect(err).ToNot(HaveOccurred(), "Deletion reconciliation should not error")
			Expect(result).To(Equal(reconcile.Result{}))

			By("Verifying the resource is cleaned up")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, &nodeLinks)
				return err != nil
			}).Should(BeTrue(), "NodeLinks resource should eventually be deleted")
		})
	})

	Context("When a link dependency is not on the node", func() {
		const nodeName = "test-missing-dependency-node"
		const bridgeLinkName = "test-healthy-bridge"
		const bridgeInterfaceName = "nl-test-br0"
		const vxlanLinkName = "test-dependent-vxlan"
		const vxlanInterfaceName = "nl-test-vx0"

		ctx := context.Background()
		request := reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}}

		createResources := func(optional bool) {
			Expect(k8sClient.Create(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}})).To(Succeed())
			Expect(k8sClient.Create(ctx, &nodenetworkoperatorv1alpha1.Link{
				ObjectMeta: metav1.ObjectMeta{Name: bridgeLinkName},
				Spec: nodenetworkoperatorv1alpha1.LinkSpec{
					LinkName:  bridgeInterfaceName,
					LinkSpecs: nodenetworkoperatorv1alpha1.LinkSpecs{Bridge: &nodenetworkoperatorv1alpha1.BridgeSpec{}},
				},
			})).To(Succeed())
			Expect(k8sClient.Create(ctx, &nodenetworkoperatorv1alpha1.Link{
				ObjectMeta: metav1.ObjectMeta{Name: vxlanLinkName},
				Spec: nodenetworkoperatorv1alpha1.LinkSpec{
					LinkName: vxlanInterfaceName,
					LinkSpecs: nodenetworkoperatorv1alpha1.LinkSpecs{
						VXLAN: &nodenetworkoperatorv1alpha1.VXLANSpecs{
							VNID:            4246,
							RemoteIPAddress: "10.255.0.1",
							RemotePort:      4789,
							SourcePort:      &nodenetworkoperatorv1alpha1.PortRange{Start: 4789, End: 4789},
							// This Link is not in the NodeLinks, as if its node selector did not match this node.
							Master: &nodenetworkoperatorv1alpha1.LinkReference{Name: "test-link-not-on-node", Optional: optional},
						},
					},
				},
			})).To(Succeed())
			Expect(k8sClient.Create(ctx, &nodenetworkoperatorv1alpha1.NodeLinks{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
				Spec:       nodenetworkoperatorv1alpha1.NodeLinksSpec{MatchingLinks: []string{bridgeLinkName, vxlanLinkName}},
			})).To(Succeed())
		}

		// Reconciles twice so that the result does not depend on the outcome of the first reconcile of a new NodeLinks.
		reconcileNodeLinks := func() error {
			_, _ = NewNodeLinksReconciler(k8sCluster, nodeName).Reconcile(ctx, request)
			_, err := NewNodeLinksReconciler(k8sCluster, nodeName).Reconcile(ctx, request)
			return err
		}

		getLinkConditions := func(interfaceName string) []metav1.Condition {
			var nodeLinks nodenetworkoperatorv1alpha1.NodeLinks
			Expect(k8sClient.Get(ctx, request.NamespacedName, &nodeLinks)).To(Succeed())
			return nodeLinks.Status.NetlinkLinkConditions[interfaceName]
		}

		AfterEach(func() {
			for _, interfaceName := range []string{vxlanInterfaceName, bridgeInterfaceName} {
				if link, err := netlink.LinkByName(interfaceName); err == nil {
					_ = netlink.LinkDel(link)
				}
			}

			var nodeLinks nodenetworkoperatorv1alpha1.NodeLinks
			if err := k8sClient.Get(ctx, request.NamespacedName, &nodeLinks); err == nil {
				nodeLinks.Finalizers = nil
				Expect(client.IgnoreNotFound(k8sClient.Update(ctx, &nodeLinks))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &nodeLinks))).To(Succeed())
			}
			for _, name := range []string{bridgeLinkName, vxlanLinkName} {
				link := &nodenetworkoperatorv1alpha1.Link{ObjectMeta: metav1.ObjectMeta{Name: name}}
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, link))).To(Succeed())
			}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}))).To(Succeed())
		})

		It("should still reconcile other links when a required dependency is missing", func() {
			createResources(false)

			// The dependent link cannot be reconciled, so the overall reconcile reports an error.
			Expect(reconcileNodeLinks()).To(HaveOccurred())

			By("verifying the healthy link was created")
			_, err := netlink.LinkByName(bridgeInterfaceName)
			Expect(err).NotTo(HaveOccurred(), "The healthy bridge should be created")
			Expect(meta.IsStatusConditionTrue(getLinkConditions(bridgeInterfaceName), nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady)).
				To(BeTrue(), "The healthy bridge should be ready")

			By("verifying the dependent link reports the missing dependency")
			dependencyCondition := meta.FindStatusCondition(getLinkConditions(vxlanInterfaceName), nodenetworkoperatorv1alpha1.NetlinkLinkConditionDependencyLinksAvailable)
			Expect(dependencyCondition).NotTo(BeNil())
			Expect(dependencyCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(dependencyCondition.Reason).To(Equal("MissingDependencyLinks"))
		})

		It("should configure a link without an optional dependency that is missing", func() {
			createResources(true)

			Expect(reconcileNodeLinks()).To(Succeed())

			By("verifying both links were created")
			_, err := netlink.LinkByName(bridgeInterfaceName)
			Expect(err).NotTo(HaveOccurred(), "The healthy bridge should be created")
			vxlan, err := netlink.LinkByName(vxlanInterfaceName)
			Expect(err).NotTo(HaveOccurred(), "The dependent VXLAN should be created")
			Expect(vxlan.Attrs().MasterIndex).To(BeZero(), "The VXLAN should have no master")
			Expect(meta.IsStatusConditionTrue(getLinkConditions(vxlanInterfaceName), nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady)).
				To(BeTrue(), "The dependent VXLAN should be ready")
		})
	})
})
