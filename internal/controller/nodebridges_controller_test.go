package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
)

var _ = Describe("NodeBridges Controller", func() {
	Context("When reconciling a resource", func() {
		const nodeName = "test-node"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: nodeName,
		}
		request := reconcile.Request{
			NamespacedName: typeNamespacedName,
		}

		BeforeEach(func() {
			By("creating a matching node for the NodeBridges resource")
			var node corev1.Node
			if err := k8sClient.Get(ctx, typeNamespacedName, &node); err != nil && errors.IsNotFound(err) {
				resource := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}

			By("creating the custom resource for the Kind NodeBridges")
			var nodeBridges bridgeoperatorv1alpha1.NodeBridges
			err := k8sClient.Get(ctx, typeNamespacedName, &nodeBridges)
			if err != nil && errors.IsNotFound(err) {
				resource := &bridgeoperatorv1alpha1.NodeBridges{
					ObjectMeta: metav1.ObjectMeta{
						Name: nodeName,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			var nodeBridges bridgeoperatorv1alpha1.NodeBridges
			Expect(k8sClient.Get(ctx, typeNamespacedName, &nodeBridges)).To(Succeed())

			By("Cleanup the specific resource instance NodeBridges")
			Expect(k8sClient.Delete(ctx, &nodeBridges)).To(Succeed())

			var node corev1.Node
			Expect(k8sClient.Get(ctx, typeNamespacedName, &node)).To(Succeed())

			By("Cleanup the specific node instance")
			Expect(k8sClient.Delete(ctx, &node)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			Expect(NewNodeBridgesReconciler(k8sCluster, nodeName).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))
		})
	})
})
