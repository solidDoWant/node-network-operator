package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
)

var _ = Describe("Bridge Controller", func() {
	Context("When reconciling a resource without any nodes", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		request := reconcile.Request{
			NamespacedName: typeNamespacedName,
		}
		bridge := &bridgeoperatorv1alpha1.Bridge{}

		BeforeEach(withTestNetworkNamespace(func() {
			By("creating the custom resource for the Kind Bridge")
			err := k8sClient.Get(ctx, typeNamespacedName, bridge)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &bridgeoperatorv1alpha1.Bridge{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
						Labels:    map[string]string{"app": "bridge-controller"},
					},
					Spec: bridgeoperatorv1alpha1.BridgeSpec{
						InterfaceName: "test-bridge0",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		}))

		AfterEach(withTestNetworkNamespace(func() {
			By("Cleanup the specific resource instance Bridge")
			resource := &bridgeoperatorv1alpha1.Bridge{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).ToNot(Succeed(), "Resource should be deleted after reconciliation")
		}))
		It("should successfully reconcile the resource", withTestNetworkNamespace(func() {
			By("Reconciling the created resource")

			Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))

			By("Verifying the resource is in the expected state")
			resource := &bridgeoperatorv1alpha1.Bridge{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			Expect(resource.Spec.InterfaceName).To(Equal("test-bridge0"), "The InterfaceName should match the expected value")
			Expect(meta.IsStatusConditionTrue(resource.Status.Conditions, "Ready")).To(BeTrue(), "The resource should be ready: %#v", resource)
		}))
		It("should reject changes to immutable fields", withTestNetworkNamespace(func() {
			By("Attempting to change an immutable field")

			Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))

			resource := &bridgeoperatorv1alpha1.Bridge{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			resource.Spec.InterfaceName = "new-bridge-name"
			Expect(k8sClient.Update(ctx, resource)).ToNot(Succeed(), "Updating an immutable field should fail")

			// Verify the original value remains unchanged
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Spec.InterfaceName).To(Equal("test-bridge0"), "The InterfaceName should remain unchanged")
		}))
	})

	Context("When reconciling resources with nodes", func() {
		const resourceNamePrefix = "test-resource-"
		const nodeNamePrefix = "test-node-"

		ctx := context.Background()

		nodeLabels := []map[string]string{
			{},                           // Match the first resource's NodeSelector
			{"exactLabel": "exactValue"}, // Match the first resource's NodeSelector
			{"exactLabel": "exactValue", "matchLabel": "value1"}, // Match both resources' NodeSelector
		}

		nodes := make([]*corev1.Node, len(nodeLabels))
		for i, nodeLabels := range nodeLabels {
			nodes[i] = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   fmt.Sprintf("%s%d", nodeNamePrefix, i),
					Labels: nodeLabels,
				},
			}
		}

		bridgeConfigs := []struct {
			resourceSpec         bridgeoperatorv1alpha1.BridgeSpec
			expectedMatchedNodes []string
		}{
			{
				resourceSpec: bridgeoperatorv1alpha1.BridgeSpec{
					InterfaceName: "test-bridge0",
					MTU:           ptr.To(int32(1234)),
				},
				expectedMatchedNodes: []string{nodes[0].Name, nodes[1].Name, nodes[2].Name},
			},
			{
				resourceSpec: bridgeoperatorv1alpha1.BridgeSpec{
					InterfaceName: "test-bridge1",
					MTU:           ptr.To(int32(5678)),
					NodeSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{"exactLabel": "exactValue"},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "matchLabel",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"value1", "value2"},
							},
						},
					},
				},
				expectedMatchedNodes: []string{nodes[2].Name},
			},
		}

		type bridge struct {
			resource             *bridgeoperatorv1alpha1.Bridge
			typeNamespacedName   types.NamespacedName
			request              reconcile.Request
			expectedMatchedNodes []string
		}

		bridges := make(map[string]bridge, len(bridgeConfigs))
		for i, config := range bridgeConfigs {
			name := fmt.Sprintf("%s%d", resourceNamePrefix, i)
			namespacedName := types.NamespacedName{
				Name: name,
			}

			bridges[name] = bridge{
				resource: &bridgeoperatorv1alpha1.Bridge{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Spec: config.resourceSpec,
				},
				typeNamespacedName: namespacedName,
				request: reconcile.Request{
					NamespacedName: namespacedName,
				},
				expectedMatchedNodes: config.expectedMatchedNodes,
			}
		}

		BeforeEach(withTestNetworkNamespace(func() {
			By("creating the custom resources for the Kind Bridge")
			for _, bridge := range bridges {
				Expect(k8sClient.Create(ctx, bridge.resource.DeepCopy())).To(Succeed(), "Failed to create resource %s", bridge.resource.Name)
			}

			By("creating nodes for the Bridge resource")
			for _, node := range nodes {
				Expect(k8sClient.Create(ctx, node.DeepCopy())).To(Succeed(), "Failed to create node %s", node.Name)
			}
		}))

		AfterEach(withTestNetworkNamespace(func() {
			reconciler := NewBridgeReconciler(k8sCluster)

			By("Cleanup the nodes")
			var nodes corev1.NodeList
			Expect(k8sClient.List(ctx, &nodes)).To(Succeed(), "Failed to list nodes")
			for _, node := range nodes.Items {
				Expect(k8sClient.Delete(ctx, &node)).To(Succeed(), "Failed to delete node %s", node.Name)

				var getNode corev1.Node
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &getNode)).ToNot(Succeed(), "Node should be deleted after reconciliation")

				// While these are owned by the node, they are not deleted automatically because envtest does not deploy the kube-controller-manager.
				// See https://github.com/kubernetes-sigs/controller-runtime/issues/3083 for details.
				var nodeBridges bridgeoperatorv1alpha1.NodeBridges
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &nodeBridges)).To(Succeed(), "Failed to get node bridges for node %s", node.Name)
				Expect(k8sClient.Delete(ctx, &nodeBridges)).To(Succeed(), "Failed to delete node bridges for node %s", node.Name)

				for _, bridge := range bridges {
					Expect(reconciler.Reconcile(ctx, bridge.request)).To(Equal(reconcile.Result{}))
				}
			}

			By("Cleanup the specific resource instance Bridge")
			var clusterBridges bridgeoperatorv1alpha1.BridgeList
			Expect(k8sClient.List(ctx, &clusterBridges)).To(Succeed(), "Failed to list Bridge resources")
			for _, clusterBridge := range clusterBridges.Items {
				Expect(k8sClient.Delete(ctx, &clusterBridge)).To(Succeed(), "Failed to delete Bridge resource %s", clusterBridge.Name)

				bridge := bridges[clusterBridge.Name]
				Expect(reconciler.Reconcile(ctx, bridge.request)).To(Equal(reconcile.Result{}))
				Expect(k8sClient.Get(ctx, bridge.typeNamespacedName, bridge.resource)).ToNot(Succeed(), "Resource should be deleted after reconciliation")
			}
		}))

		It("should successfully reconcile the resources with nodes", withTestNetworkNamespace(func() {
			By("Reconciling the created resources")

			for name, bridge := range bridges {
				Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, bridge.request)).To(Equal(reconcile.Result{}), "Failed to reconcile resource %s", name)

				By(fmt.Sprintf("Verifying the resource %s is in the expected state", name))
				var resource bridgeoperatorv1alpha1.Bridge
				Expect(k8sClient.Get(ctx, bridge.typeNamespacedName, &resource)).To(Succeed(), "Failed to get resource %s", name)
				Expect(resource.Status.MatchedNodes).To(ConsistOf(bridge.expectedMatchedNodes), "The MatchedNodes should match the expected nodes for resource %s", name)

				// Verify that the nodebridges resources contain the expected bridges
				By(fmt.Sprintf("Verifying the NodeBridges for resource %s", name))
				for _, node := range nodes {
					var nodeBridges bridgeoperatorv1alpha1.NodeBridges
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &nodeBridges)).To(Succeed(), "Failed to get node bridges for node %s", node.Name)

					if slices.Contains(bridge.expectedMatchedNodes, node.Name) {
						Expect(nodeBridges.Spec.MatchingBridges).To(ContainElement(resource.Name), "The MatchedBridges should contain the resource %s for node %s", resource.Name, node.Name)
						continue
					}
					Expect(nodeBridges.Spec.MatchingBridges).ToNot(ContainElement(resource.Name), "The MatchedBridges should not contain the resource %s for node %s", resource.Name, node.Name)
				}
			}
		}))

		It("should successfully handle node deletion", withTestNetworkNamespace(func() {
			By("Reconciling the created resources")
			for name, bridge := range bridges {
				Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, bridge.request)).To(Equal(reconcile.Result{}), "Failed to reconcile resource %s", name)
			}

			By("Deleting a node and verifying the resource updates")
			nodeToDelete := nodes[2]
			Expect(k8sClient.Delete(ctx, nodeToDelete)).To(Succeed(), "Failed to delete node %s", nodeToDelete.Name)
			var deletedNode corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeToDelete.Name}, &deletedNode)).ToNot(Succeed(), "Node should be deleted after reconciliation")

			for name, bridge := range bridges {
				Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, bridge.request)).To(Equal(reconcile.Result{}), "Failed to reconcile resource %s after node deletion", name)

				By(fmt.Sprintf("Verifying the resource %s updates after node deletion", name))
				var resource bridgeoperatorv1alpha1.Bridge
				Expect(k8sClient.Get(ctx, bridge.typeNamespacedName, &resource)).To(Succeed(), "Failed to get resource %s after node deletion", name)
				Expect(resource.Status.MatchedNodes).ToNot(ContainElement(nodeToDelete.Name), "The MatchedNodes should not contain the deleted node %s for resource %s", nodeToDelete.Name, name)

				By(fmt.Sprintf("Verifying the NodeBridges for resource %s after node deletion", name))
				var nodeBridges bridgeoperatorv1alpha1.NodeBridges
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeToDelete.Name}, &nodeBridges)).To(Succeed(), "Failed to get node bridges for node %s after deletion", nodeToDelete.Name)
				Expect(nodeBridges.Spec.MatchingBridges).ToNot(ContainElement(resource.Name), "The MatchedBridges should not contain the resource %s for deleted node %s", resource.Name, nodeToDelete.Name)
			}
		}))
	})
})
