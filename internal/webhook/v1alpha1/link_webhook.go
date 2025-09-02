package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var linklog = logf.Log.WithName("link-resource")

// SetupLinkWebhookWithManager registers the webhook for Link in the manager.
func SetupLinkWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&nodenetworkoperatorv1alpha1.Link{}).
		WithValidator(&LinkCustomValidator{}).
		Complete()
}

// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-nodenetworkoperator-soliddowant-dev-v1alpha1-link,mutating=false,failurePolicy=fail,sideEffects=None,groups=nodenetworkoperator.soliddowant.dev,resources=links,verbs=create;update,versions=v1alpha1,name=vlink-v1alpha1.kb.io,admissionReviewVersions=v1

// LinkCustomValidator struct is responsible for validating the Link resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type LinkCustomValidator struct{}

var _ webhook.CustomValidator = &LinkCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Link.
func (v *LinkCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validate(obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Link.
func (v *LinkCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return v.validate(newObj)
}

func (v *LinkCustomValidator) validate(obj runtime.Object) (admission.Warnings, error) {
	link, ok := obj.(*nodenetworkoperatorv1alpha1.Link)
	if !ok {
		return nil, fmt.Errorf("expected a Link object for the newObj but got %T", obj)
	}

	linklog.Info("Validation for Link", "name", link.GetName())

	errs := make([]error, 0, 2)
	if err := v.validateNodeSelector(link); err != nil {
		errs = append(errs, err)
	}

	if err := v.validateLinkSpecs(link); err != nil {
		errs = append(errs, err)
	}

	return nil, errors.Join(errs...)
}

func (v *LinkCustomValidator) validateNodeSelector(link *nodenetworkoperatorv1alpha1.Link) error {
	_, err := metav1.LabelSelectorAsSelector(&link.Spec.NodeSelector)
	if err != nil {
		return fmt.Errorf("invalid node selector: %w", err)
	}

	return nil
}

func (v *LinkCustomValidator) validateLinkSpecs(link *nodenetworkoperatorv1alpha1.Link) error {
	switch {
	case link.Spec.VXLAN != nil:
		return v.validateVXLAN(link)
	default:
		return nil
	}
}

func (v *LinkCustomValidator) validateVXLAN(link *nodenetworkoperatorv1alpha1.Link) error {
	return v.validateVXLANRemote(link)
}

func (v *LinkCustomValidator) validateVXLANRemote(link *nodenetworkoperatorv1alpha1.Link) error {
	remoteAddress := net.ParseIP(link.Spec.VXLAN.RemoteIPAddress)
	if remoteAddress.IsMulticast() && link.Spec.VXLAN.Device == nil {
		return fmt.Errorf("VXLAN remote IP %s is multicast, so a device must be specified (netlink requirement)", link.Spec.VXLAN.RemoteIPAddress)
	}

	return nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Link.
func (v *LinkCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// This is a no-op, but needed to satisfy the interface.
	return nil, nil
}
