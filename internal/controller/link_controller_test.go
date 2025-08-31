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

var _ = Describe("Link Controller", func() {
	Context("When reconciling a resource without any nodes", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		request := reconcile.Request{
			NamespacedName: typeNamespacedName,
		}
		link := &bridgeoperatorv1alpha1.Link{}

		BeforeEach(withTestNetworkNamespace(func() {
			By("creating the custom resource for the Kind Link")
			err := k8sClient.Get(ctx, typeNamespacedName, link)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &bridgeoperatorv1alpha1.Link{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
						Labels:    map[string]string{"app": "link-controller"},
					},
					Spec: bridgeoperatorv1alpha1.LinkSpec{
						LinkName: "test-vxlan0",
						LinkSpecs: bridgeoperatorv1alpha1.LinkSpecs{
							VXLAN: &bridgeoperatorv1alpha1.VXLANSpecs{
								VNID:            12345,
								RemoteIPAddress: "224.0.0.1",
								RemotePort:      4789,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		}))

		AfterEach(withTestNetworkNamespace(func() {
			By("Cleanup the specific resource instance Link")
			resource := &bridgeoperatorv1alpha1.Link{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			Expect(NewLinkReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).ToNot(Succeed(), "Resource should be deleted after reconciliation")
		}))

		It("should successfully reconcile the resource", withTestNetworkNamespace(func() {
			By("Reconciling the created resource")

			Expect(NewLinkReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))

			By("Verifying the resource is in the expected state")
			resource := &bridgeoperatorv1alpha1.Link{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			Expect(resource.Spec.LinkName).To(Equal("test-vxlan0"), "The LinkName should match the expected value")
			Expect(meta.IsStatusConditionTrue(resource.Status.Conditions, "Ready")).To(BeTrue(), "The resource should be ready: %#v", resource)
			Expect(resource.Finalizers).To(ContainElement(linkFinalizerName), "The resource should have the finalizer")
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

		linkConfigs := []struct {
			resourceSpec         bridgeoperatorv1alpha1.LinkSpec
			expectedMatchedNodes []string
		}{
			{
				resourceSpec: bridgeoperatorv1alpha1.LinkSpec{
					LinkName: "test-vxlan0",
					LinkSpecs: bridgeoperatorv1alpha1.LinkSpecs{
						VXLAN: &bridgeoperatorv1alpha1.VXLANSpecs{
							VNID:            12345,
							RemoteIPAddress: "224.0.0.1",
							RemotePort:      4789,
							MTU:             ptr.To(int32(1450)),
						},
					},
				},
				expectedMatchedNodes: []string{nodes[0].Name, nodes[1].Name, nodes[2].Name},
			},
			{
				resourceSpec: bridgeoperatorv1alpha1.LinkSpec{
					LinkName: "test-vxlan1",
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
					LinkSpecs: bridgeoperatorv1alpha1.LinkSpecs{
						VXLAN: &bridgeoperatorv1alpha1.VXLANSpecs{
							VNID:            54321,
							RemoteIPAddress: "224.0.0.2",
							RemotePort:      4790,
							MTU:             ptr.To(int32(1400)),
						},
					},
				},
				expectedMatchedNodes: []string{nodes[2].Name},
			},
		}

		type link struct {
			resource             *bridgeoperatorv1alpha1.Link
			typeNamespacedName   types.NamespacedName
			request              reconcile.Request
			expectedMatchedNodes []string
		}

		links := make(map[string]link, len(linkConfigs))
		for i, config := range linkConfigs {
			name := fmt.Sprintf("%s%d", resourceNamePrefix, i)
			namespacedName := types.NamespacedName{
				Name: name,
			}

			links[name] = link{
				resource: &bridgeoperatorv1alpha1.Link{
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
			By("creating the custom resources for the Kind Link")
			for _, link := range links {
				Expect(k8sClient.Create(ctx, link.resource.DeepCopy())).To(Succeed(), "Failed to create resource %s", link.resource.Name)
			}

			By("creating nodes for the Link resource")
			for _, node := range nodes {
				Expect(k8sClient.Create(ctx, node.DeepCopy())).To(Succeed(), "Failed to create node %s", node.Name)
			}
		}))

		AfterEach(withTestNetworkNamespace(func() {
			reconciler := NewLinkReconciler(k8sCluster)

			By("Cleanup the nodes")
			var nodes corev1.NodeList
			Expect(k8sClient.List(ctx, &nodes)).To(Succeed(), "Failed to list nodes")
			for _, node := range nodes.Items {
				By(fmt.Sprintf("Cleanup the node %s", node.Name))
				Expect(k8sClient.Delete(ctx, &node)).To(Succeed(), "Failed to delete node %s", node.Name)
				var getNode corev1.Node
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &getNode)).ToNot(Succeed(), "Node should be deleted after reconciliation")

				By(fmt.Sprintf("Cleanup the NodeLinks for node %s", node.Name))
				// While these are owned by the node, they are not deleted automatically because envtest does not deploy the kube-controller-manager.
				// See https://github.com/kubernetes-sigs/controller-runtime/issues/3083 for details.
				var nodeLinks bridgeoperatorv1alpha1.NodeLinks
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &nodeLinks)).To(Succeed(), "Failed to get node links for node %s", node.Name)
				// This test does not need to verify the finalizer logic of the NodeLinks resource.
				nodeLinks.Finalizers = nil
				Expect(k8sClient.Update(ctx, &nodeLinks)).To(Succeed(), "Failed to remove finalizers from node links for node %s", node.Name)
				Expect(k8sClient.Delete(ctx, &nodeLinks)).To(Succeed(), "Failed to delete node links for node %s", node.Name)
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &nodeLinks)).ToNot(Succeed(), "NodeLinks should be deleted after reconciliation")

				By(fmt.Sprintf("Reconcile the Link resources after node %s deletion", node.Name))
				for _, link := range links {
					Expect(reconciler.Reconcile(ctx, link.request)).To(Equal(reconcile.Result{}))
				}
			}

			By("Cleanup the specific resource instance Link")
			var clusterLinks bridgeoperatorv1alpha1.LinkList
			Expect(k8sClient.List(ctx, &clusterLinks)).To(Succeed(), "Failed to list Link resources")
			for _, clusterLink := range clusterLinks.Items {
				Expect(k8sClient.Delete(ctx, &clusterLink)).To(Succeed(), "Failed to delete Link resource %s", clusterLink.Name)

				link := links[clusterLink.Name]
				Expect(reconciler.Reconcile(ctx, link.request)).To(Equal(reconcile.Result{}))
				Expect(k8sClient.Get(ctx, link.typeNamespacedName, link.resource)).ToNot(Succeed(), "Resource should be deleted after reconciliation")
			}
		}))

		It("should successfully reconcile the resources with nodes", withTestNetworkNamespace(func() {
			By("Reconciling the created resources")

			for name, link := range links {
				Expect(NewLinkReconciler(k8sCluster).Reconcile(ctx, link.request)).To(Equal(reconcile.Result{}), "Failed to reconcile resource %s", name)

				By(fmt.Sprintf("Verifying the resource %s is in the expected state", name))
				var resource bridgeoperatorv1alpha1.Link
				Expect(k8sClient.Get(ctx, link.typeNamespacedName, &resource)).To(Succeed(), "Failed to get resource %s", name)
				Expect(resource.Status.MatchedNodes).To(ConsistOf(link.expectedMatchedNodes), "The MatchedNodes should match the expected nodes for resource %s", name)
				Expect(resource.Finalizers).To(ContainElement(linkFinalizerName), "The resource should have the finalizer")

				// Verify that the nodelinks resources contain the expected links
				By(fmt.Sprintf("Verifying the NodeLinks for resource %s", name))
				for _, node := range nodes {
					var nodeLinks bridgeoperatorv1alpha1.NodeLinks
					Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &nodeLinks)).To(Succeed(), "Failed to get node links for node %s", node.Name)

					if slices.Contains(link.expectedMatchedNodes, node.Name) {
						Expect(nodeLinks.Spec.MatchingLinks).To(ContainElement(resource.Name), "The MatchingLinks should contain the resource %s for node %s", resource.Name, node.Name)
						continue
					}
					Expect(nodeLinks.Spec.MatchingLinks).ToNot(ContainElement(resource.Name), "The MatchingLinks should not contain the resource %s for node %s", resource.Name, node.Name)
				}
			}
		}))

		It("should successfully handle node deletion", withTestNetworkNamespace(func() {
			By("Reconciling the created resources")
			for name, link := range links {
				Expect(NewLinkReconciler(k8sCluster).Reconcile(ctx, link.request)).To(Equal(reconcile.Result{}), "Failed to reconcile resource %s", name)
			}

			By("Deleting a node and verifying the resource updates")
			nodeToDelete := nodes[2]
			Expect(k8sClient.Delete(ctx, nodeToDelete)).To(Succeed(), "Failed to delete node %s", nodeToDelete.Name)
			var deletedNode corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeToDelete.Name}, &deletedNode)).ToNot(Succeed(), "Node should be deleted after reconciliation")

			for name, link := range links {
				Expect(NewLinkReconciler(k8sCluster).Reconcile(ctx, link.request)).To(Equal(reconcile.Result{}), "Failed to reconcile resource %s after node deletion", name)

				By(fmt.Sprintf("Verifying the resource %s updates after node deletion", name))
				var resource bridgeoperatorv1alpha1.Link
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, link.typeNamespacedName, &resource)).To(Succeed(), "Failed to get resource %s after node deletion", name)
					g.Expect(resource.Status.MatchedNodes).ToNot(ContainElement(nodeToDelete.Name), "The MatchedNodes should not contain the deleted node %s for resource %s", nodeToDelete.Name, name)
				}).Should(Succeed())

				By(fmt.Sprintf("Verifying the NodeLinks for resource %s after node deletion", name))
				var nodeLinks bridgeoperatorv1alpha1.NodeLinks

				if len(resource.Status.MatchedNodes) == 0 {
					// If the resource has no matched nodes, it should not have a NodeLinks resource
					// Normally the resource would exist pending deletion, but it is not reconciled at any point so the finalizer is never added.
					Eventually(k8sClient.Get(ctx, types.NamespacedName{Name: nodeToDelete.Name}, &nodeLinks)).ShouldNot(Succeed(), "NodeLinks should not exist for node %s after deletion", nodeToDelete.Name)
					continue
				}

				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeToDelete.Name}, &nodeLinks)).To(Succeed(), "Failed to get node links for node %s after deletion", nodeToDelete.Name)
				Expect(nodeLinks.Spec.MatchingLinks).ToNot(ContainElement(resource.Name), "The MatchingLinks should not contain the resource %s for deleted node %s", resource.Name, nodeToDelete.Name)
			}
		}))
	})
})
