package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
)

var _ = Describe("Link Webhook", func() {
	var (
		obj       *bridgeoperatorv1alpha1.Link
		oldObj    *bridgeoperatorv1alpha1.Link
		validator LinkCustomValidator
	)

	BeforeEach(func() {
		obj = &bridgeoperatorv1alpha1.Link{}
		oldObj = &bridgeoperatorv1alpha1.Link{}
		validator = LinkCustomValidator{}
		Expect(validator).NotTo(BeNil(), "Expected validator to be initialized")
		Expect(oldObj).NotTo(BeNil(), "Expected oldObj to be initialized")
		Expect(obj).NotTo(BeNil(), "Expected obj to be initialized")
	})

	Context("When creating or updating Link under Validating Webhook", func() {
		It("Should deny creation if the node selector is syntactically invalid", func() {
			By("simulating an invalid creation scenario")
			obj.Spec.NodeSelector = v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{
						Key: "some-key",
						Values: []string{
							"some-value",
						},
						Operator: "invalid operator value",
					},
				},
			}
			Expect(validator.ValidateCreate(ctx, obj)).Error().To(HaveOccurred())
		})

		It("Should admit creation if node selector is valid", func() {
			By("simulating an invalid creation scenario")
			obj.Spec.NodeSelector = v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{
						Key: "some-key",
						Values: []string{
							"some-value",
						},
						Operator: v1.LabelSelectorOpIn,
					},
				},
			}
			Expect(validator.ValidateCreate(ctx, obj)).To(BeNil())
		})

		It("Should validate updates correctly", func() {
			By("simulating an invalid update scenario")
			obj.Spec.NodeSelector = v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{
						Key: "some-key",
						Values: []string{
							"some-value",
						},
						Operator: "invalid operator value",
					},
				},
			}
			Expect(validator.ValidateUpdate(ctx, oldObj, obj)).Error().To(HaveOccurred())

			By("simulating an valid update scenario")
			obj.Spec.NodeSelector = v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{
						Key: "some-key",
						Values: []string{
							"some-value",
						},
						Operator: v1.LabelSelectorOpIn,
					},
				},
			}
			Expect(validator.ValidateUpdate(ctx, oldObj, obj)).To(BeNil())
		})
	})

})
