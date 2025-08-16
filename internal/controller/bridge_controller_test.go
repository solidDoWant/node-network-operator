package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
)

var _ = Describe("Bridge Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		request := reconcile.Request{
			NamespacedName: typeNamespacedName,
		}
		bridge := &bridgeoperatorv1alpha1.Bridge{}

		BeforeEach(func() {
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
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance Bridge")
			resource := &bridgeoperatorv1alpha1.Bridge{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).ToNot(Succeed(), "Resource should be deleted after reconciliation")
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))

			By("Verifying the resource is in the expected state")
			resource := &bridgeoperatorv1alpha1.Bridge{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			Expect(resource.Spec.InterfaceName).To(Equal("test-bridge0"), "The InterfaceName should match the expected value")
			Expect(meta.IsStatusConditionTrue(resource.Status.Conditions, "Ready")).To(BeTrue(), "The resource should be ready: %#v", resource)
		})
		It("should reject changes to immutable fields", func() {
			By("Attempting to change an immutable field")

			Expect(NewBridgeReconciler(k8sCluster).Reconcile(ctx, request)).To(Equal(reconcile.Result{}))

			resource := &bridgeoperatorv1alpha1.Bridge{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			resource.Spec.InterfaceName = "new-bridge-name"
			Expect(k8sClient.Update(ctx, resource)).ToNot(Succeed(), "Updating an immutable field should fail")

			// Verify the original value remains unchanged
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Spec.InterfaceName).To(Equal("test-bridge0"), "The InterfaceName should remain unchanged")
		})
	})
})
