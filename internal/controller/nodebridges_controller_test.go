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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
)

var _ = Describe("NodeBridges Controller", func() {
	Context("When reconciling a resource", func() {
		const nodeName = "test-node"
		const bridgeName = "test-bridge"
		const interfaceName = "nb-test-iface0"
		const mtu = int32(1234)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: nodeName,
		}
		request := reconcile.Request{
			NamespacedName: typeNamespacedName,
		}

		BeforeEach(withTestNetworkNamespace(func() {
			By("creating a matching node for the NodeBridges resource")
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed(), "Failed to create node %s", nodeName)

			By("creating a bridge resource that matches the node")
			bridge := &bridgeoperatorv1alpha1.Bridge{
				ObjectMeta: metav1.ObjectMeta{
					Name: bridgeName,
				},
				Status: bridgeoperatorv1alpha1.BridgeStatus{
					MatchedNodes: []string{nodeName},
				},
				Spec: bridgeoperatorv1alpha1.BridgeSpec{
					InterfaceName: interfaceName,
					MTU:           ptr.To(mtu),
				},
			}
			Expect(k8sClient.Create(ctx, bridge)).To(Succeed(), "Failed to create bridge resource")

			By("creating the custom resource for the Kind NodeBridges")
			nodeBridges := &bridgeoperatorv1alpha1.NodeBridges{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: bridgeoperatorv1alpha1.NodeBridgesSpec{
					MatchingBridges: []string{bridge.Name},
				},
			}
			Expect(k8sClient.Create(ctx, nodeBridges)).To(Succeed(), "Failed to create NodeBridges resource %s", nodeName)
		}))

		AfterEach(withTestNetworkNamespace(func() {
			By("Cleanup the specific resource instance NodeBridges")
			var nodeBridges bridgeoperatorv1alpha1.NodeBridges
			Expect(k8sClient.Get(ctx, typeNamespacedName, &nodeBridges)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &nodeBridges)).To(Succeed())

			By("Reconcile the NodeBridges resource to ensure cleanup occurs")
			Expect(NewNodeBridgesReconciler(k8sCluster, nodeName).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))
			Eventually(k8sClient.Get(ctx, typeNamespacedName, &nodeBridges)).ShouldNot(Succeed(), "NodeBridges resource should be deleted after reconciliation")
			_, err := netlink.LinkByName(interfaceName)
			Expect(err).To(BeAssignableToTypeOf(netlink.LinkNotFoundError{}), "The bridge link should not exist after reconciliation")

			By("Cleanup the specific node instance")
			var node corev1.Node
			Expect(k8sClient.Get(ctx, typeNamespacedName, &node)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &node)).To(Succeed())

			By("Cleanup the bridge resource")
			var bridge bridgeoperatorv1alpha1.Bridge
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bridgeName}, &bridge)).To(Succeed())
			bridge.Finalizers = nil // Remove finalizers to avoid blocking deletion
			Expect(k8sClient.Update(ctx, &bridge)).To(Succeed(), "Failed to remove finalizers from bridge resource %s", bridgeName)
			Expect(k8sClient.Delete(ctx, &bridge)).To(Succeed(), "Failed to delete bridge resource %s", bridgeName)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bridgeName}, &bridge)).ToNot(Succeed(), "Bridge resource should be deleted after reconciliation")
		}))
		It("should successfully reconcile the resource", withTestNetworkNamespace(func() {
			By("Reconciling the created resource")

			Expect(NewNodeBridgesReconciler(k8sCluster, nodeName).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))

			var nodeBridges bridgeoperatorv1alpha1.NodeBridges
			Expect(k8sClient.Get(ctx, typeNamespacedName, &nodeBridges)).To(Succeed(), "Failed to get NodeBridges resource %s", nodeName)

			Expect(nodeBridges.Finalizers).To(ContainElement(nodeBridgesFinalizerName), "The NodeBridges resource should have the finalizer")
			Expect(nodeBridges.Status.LastAttemptedBridgeLinks).To(ContainElement(interfaceName), "The NodeBridges status should contain the interface name of the bridge")
			Expect(nodeBridges.Status.LinkConditions).To(HaveKey(interfaceName), "The NodeBridges status should contain link conditions for the interface")
			Expect(meta.IsStatusConditionTrue(nodeBridges.Status.LinkConditions[interfaceName], "Ready")).To(BeTrue(), "The link condition should be ready")
			Expect(meta.IsStatusConditionTrue(nodeBridges.Status.Conditions, "Ready")).To(BeTrue(), "The NodeBridges resource should be ready")

			By("Checking the link properties")
			bridgeLink, err := netlink.LinkByName(interfaceName)
			Expect(err).ToNot(HaveOccurred(), "Failed to get the bridge link by name %s", interfaceName)
			Expect(bridgeLink).ToNot(BeNil(), "The bridge link should exist after reconciliation")
			Expect(bridgeLink.Attrs().MTU).To(Equal(int(mtu)), "The bridge link should have the correct MTU")
			Expect(bridgeLink.Type()).To(Equal("bridge"), "The bridge link should be of type bridge")

		}))
	})
})
