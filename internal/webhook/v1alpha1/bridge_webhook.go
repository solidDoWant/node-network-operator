package v1alpha1

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var bridgelog = logf.Log.WithName("bridge-resource")

// SetupBridgeWebhookWithManager registers the webhook for Bridge in the manager.
func SetupBridgeWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&bridgeoperatorv1alpha1.Bridge{}).
		WithValidator(&BridgeCustomValidator{}).
		Complete()
}

// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-bridgeoperator-soliddowant-dev-v1alpha1-bridge,mutating=false,failurePolicy=fail,sideEffects=None,groups=bridgeoperator.soliddowant.dev,resources=bridges,verbs=create;update,versions=v1alpha1,name=vbridge-v1alpha1.kb.io,admissionReviewVersions=v1

// BridgeCustomValidator struct is responsible for validating the Bridge resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type BridgeCustomValidator struct{}

var _ webhook.CustomValidator = &BridgeCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Bridge.
func (v *BridgeCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validate(obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Bridge.
func (v *BridgeCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return v.validate(newObj)
}

func (v *BridgeCustomValidator) validate(obj runtime.Object) (admission.Warnings, error) {
	bridge, ok := obj.(*bridgeoperatorv1alpha1.Bridge)
	if !ok {
		return nil, fmt.Errorf("expected a Bridge object for the newObj but got %T", obj)
	}

	bridgelog.Info("Validation for Bridge", "name", bridge.GetName())

	if err := v.validateNodeSelector(bridge); err != nil {
		return nil, err
	}

	return nil, nil
}

func (v *BridgeCustomValidator) validateNodeSelector(bridge *bridgeoperatorv1alpha1.Bridge) error {
	_, err := metav1.LabelSelectorAsSelector(&bridge.Spec.NodeSelector)
	if err != nil {
		return fmt.Errorf("invalid node selector: %w", err)
	}

	return nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Bridge.
func (v *BridgeCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// This is a no-op, but needed to satisfy the interface.
	return nil, nil
}
