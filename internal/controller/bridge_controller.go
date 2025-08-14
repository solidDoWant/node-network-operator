package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"syscall"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
	"github.com/vishvananda/netlink"
)

// BridgeReconciler reconciles a Bridge object
type BridgeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	nodeName string
	recorder record.EventRecorder
}

func NewBridgeReconciler(mgr ctrl.Manager, nodeName string) *BridgeReconciler {
	return &BridgeReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		nodeName: nodeName,
		recorder: mgr.GetEventRecorderFor("bridge-controller"),
	}
}

// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=bridges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=bridges/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=bridges/finalizers,verbs=patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=list

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *BridgeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Get the bridge resource from the informer cache
	var bridge bridgeoperatorv1alpha1.Bridge
	if err := r.Get(ctx, req.NamespacedName, &bridge); err != nil {
		log.Error(err, "unable to fetch Bridge")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log = log.WithValues("bridge", bridge.Spec.InterfaceName)

	// Ignore if the bridge resource does not match the current node
	matchesNode, err := r.doesResourceMatchNode(ctx, &bridge)
	if err != nil {
		log.Error(err, "failed to check if bridge resource matches node")

		condition := metav1.Condition{
			Type:    r.nodeName + "/Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "NodeCheckFailed",
			Message: fmt.Sprintf("Failed to check if bridge resource matches node: %v", err),
		}

		if err := r.updateBridgeStatus(ctx, &bridge, condition); err != nil {
			log.Error(err, "failed to update bridge status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	if !matchesNode {
		log.V(1).Info("bridge resource does not match current node, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	log.Info("reconciling bridge resource")

	// Handle deletion of the bridge resource
	if !bridge.DeletionTimestamp.IsZero() {
		if err := r.handleDeletion(ctx, &bridge); err != nil {
			log.Error(err, "failed to delete of bridge resources")

			condition := metav1.Condition{
				Type:    r.nodeName + "/Cleanup",
				Status:  metav1.ConditionFalse,
				Reason:  "CleanupFailed",
				Message: fmt.Sprintf("Failed to clean up bridge %s: %v", bridge.Spec.InterfaceName, err),
			}

			if err := r.updateBridgeStatus(ctx, &bridge, condition); err != nil {
				log.Error(err, "failed to update bridge status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// Ensure the finalizer exists on the Bridge resource
	// This must occur prior to changing the underlying state, so that cleanup can be ensured prior
	// to "taking ownership" of the bridge.
	if err := r.ensureFinalizerExists(ctx, &bridge); err != nil {
		log.Error(err, "failed to ensure finalizer exists")

		condition := metav1.Condition{
			Type:    r.nodeName + "/Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "FinalizerSetupFailed",
			Message: fmt.Sprintf("Failed to ensure finalizer exists for bridge: %v", err),
		}

		if err := r.updateBridgeStatus(ctx, &bridge, condition); err != nil {
			log.Error(err, "failed to update bridge status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Ensure the bridge matches the desired state specified in the Bridge resource
	if err := r.ensureBridgeMatchesDesiredState(ctx, &bridge); err != nil {
		log.Error(err, "failed to ensure bridge matches desired state")

		condition := metav1.Condition{
			Type:    r.nodeName + "/Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "BridgeSetupFailed",
			Message: fmt.Sprintf("Failed to ensure bridge matches desired state: %v", err),
		}

		if err := r.updateBridgeStatus(ctx, &bridge, condition); err != nil {
			log.Error(err, "failed to update bridge status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Update the status of the bridge resource
	condition := metav1.Condition{
		Type:   r.nodeName + "/Ready",
		Status: metav1.ConditionTrue,
	}

	if err := r.updateBridgeStatus(ctx, &bridge, condition); err != nil {
		log.Error(err, "failed to update bridge status")
		return ctrl.Result{}, err
	}

	log.Info("bridge reconciled successfully")
	return ctrl.Result{}, nil
}

func (r *BridgeReconciler) doesResourceMatchNode(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(&bridge.Spec.NodeSelector)
	if err != nil {
		return false, fmt.Errorf("failed to convert label selector: %w", err)
	}

	var nodes corev1.NodeList
	if err := r.List(context.Background(), &nodes, client.MatchingLabelsSelector{Selector: selector}, client.MatchingLabels{}); err != nil {
		return false, fmt.Errorf("failed to list nodes: %w", err)
	}

	return slices.ContainsFunc(nodes.Items, func(node corev1.Node) bool {
		return node.Name == r.nodeName
	}), nil
}

func (r *BridgeReconciler) handleDeletion(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge) error {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bridge, r.getFinalizerName()) {
		log.V(1).Info("bridge resource is being deleted, but finalizer is not present, skipping")
		return nil
	}

	log.Info("cleaning up for bridge resource deletion")

	// Get the bridge link by name
	link, err := r.getBridge(ctx, bridge.Spec.InterfaceName)
	if err != nil {
		if errors.Is(err, syscall.ENODEV) {
			log.V(1).Info("bridge link does not exist, skipping deletion")
			return nil
		}
		log.Error(err, "failed to get bridge link for deletion")
		return err
	}

	// Delete the bridge link
	if err := netlink.LinkDel(link); err != nil && !errors.Is(err, syscall.ENODEV) {
		log.Error(err, "failed to delete bridge link")
		return err
	}

	log.Info("bridge link deleted")

	// Remove the finalizer from the Bridge resource
	controllerutil.RemoveFinalizer(bridge, r.getFinalizerName())
	if err := r.Update(ctx, bridge); err != nil {
		log.Error(err, "failed to remove finalizer from Bridge resource")
		return err
	}

	log.Info("finalizer removed from Bridge resource")
	return nil
}

func (r *BridgeReconciler) getFinalizerName() string {
	return fmt.Sprintf("%s/%s", bridgeoperatorv1alpha1.GroupVersion.Group, r.nodeName)
}

func (r *BridgeReconciler) removeFinalizer(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge) error {
	if !controllerutil.RemoveFinalizer(bridge, r.getFinalizerName()) {
		return nil
	}

	if err := r.Update(ctx, bridge); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}

	return nil
}

// Ensure the finalizer exists on the Bridge resource, so that it can be cleaned up properly
func (r *BridgeReconciler) ensureFinalizerExists(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge) error {
	if !controllerutil.AddFinalizer(bridge, r.getFinalizerName()) {
		return nil
	}

	if err := r.SubResource("finalizer").Patch(ctx, bridge, client.Apply); err != nil {
		return fmt.Errorf("failed to add finalizer: %w", err)
	}

	return nil
}

// Ensures that the bridge matches the desired state specified in the Bridge resource.
func (r *BridgeReconciler) ensureBridgeMatchesDesiredState(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge) error {
	if err := r.ensureBridgeExists(ctx, bridge.Spec.InterfaceName); err != nil {
		return fmt.Errorf("failed to ensure bridge exists: %w", err)
	}

	link, err := r.getBridge(ctx, bridge.Spec.InterfaceName)
	if err != nil {
		return fmt.Errorf("unable to get bridge %s: %w", bridge.Spec.InterfaceName, err)
	}

	if bridge.Spec.MTU != nil {
		mtu := int(*bridge.Spec.MTU)
		if err := r.ensureBridgeMTU(ctx, link, mtu); err != nil {
			return fmt.Errorf("failed to ensure bridge has MTU of %d: %w", mtu, err)
		}
	}

	if err := r.ensureBridgeUp(ctx, link); err != nil {
		return fmt.Errorf("failed to ensure bridge is up: %w", err)
	}

	return nil
}

// Add the bridge, ignoring if it already exists
func (r *BridgeReconciler) ensureBridgeExists(ctx context.Context, bridgeName string) error {
	log := logf.FromContext(ctx)

	newBridgeAttrs := netlink.NewLinkAttrs()
	newBridgeAttrs.Name = bridgeName
	newBridgeLink := &netlink.Bridge{LinkAttrs: newBridgeAttrs}

	err := netlink.LinkAdd(newBridgeLink)
	if err == nil {
		log.Info("bridge link created", "bridge", bridgeName)
		return nil
	}

	if errors.Is(err, syscall.EEXIST) {
		log.V(1).Info("link already exists", "link", bridgeName)
		return nil
	}

	return fmt.Errorf("unable to add bridge link %s: %w", bridgeName, err)
}

func (r *BridgeReconciler) getBridge(ctx context.Context, bridgeName string) (*netlink.Bridge, error) {
	// Read the bridge link info back from netlink, populating fields set by the kernel
	link, err := netlink.LinkByName(bridgeName)
	if err != nil {
		return nil, fmt.Errorf("unable to get link %s: %w", bridgeName, err)
	}

	// Verify that the link is a bridge
	if bridgeLink, ok := link.(*netlink.Bridge); ok {
		return bridgeLink, nil
	}

	return nil, fmt.Errorf("link %s is not a bridge", bridgeName)
}

// Ensure the bridge link has the correct MTU
func (r *BridgeReconciler) ensureBridgeMTU(ctx context.Context, bridgeLink *netlink.Bridge, desiredMTU int) error {
	log := logf.FromContext(ctx)

	bridgeAttrs := bridgeLink.Attrs()
	if bridgeAttrs == nil {
		return fmt.Errorf("bridge link has no attributes")
	}

	if bridgeAttrs.MTU == desiredMTU {
		return nil
	}

	if err := netlink.LinkSetMTU(bridgeLink, desiredMTU); err != nil {
		return fmt.Errorf("unable to set MTU for bridge link %s: %w", bridgeAttrs.Name, err)
	}
	log.Info("MTU set for bridge link", "bridge", bridgeAttrs.Name, "mtu", desiredMTU)

	return nil
}

// Ensure the bridge link is up
func (r *BridgeReconciler) ensureBridgeUp(ctx context.Context, bridgeLink *netlink.Bridge) error {
	log := logf.FromContext(ctx)

	bridgeAttrs := bridgeLink.Attrs()
	if bridgeAttrs == nil {
		return fmt.Errorf("bridge link has no attributes")
	}

	if bridgeAttrs.Flags&net.FlagUp != 0 {
		log.V(1).Info("bridge link is already up", "bridge", bridgeAttrs.Name)
		return nil
	}

	if err := netlink.LinkSetUp(bridgeLink); err != nil {
		return fmt.Errorf("unable to set bridge link %s up: %w", bridgeLink.Attrs().Name, err)
	}
	log.Info("bridge link set up", "bridge", bridgeLink.Attrs().Name)

	return nil
}

// updateBridgeStatus updates the status of the Bridge resource with the given condition.
func (r *BridgeReconciler) updateBridgeStatus(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge, condition metav1.Condition) error {
	log := logf.FromContext(ctx)

	if !meta.SetStatusCondition(&bridge.Status.Conditions, condition) {
		return nil
	}

	if err := r.Status().Patch(ctx, bridge, client.Apply); err != nil {
		log.Error(err, "unable to patch Bridge status")
		r.recorder.Eventf(bridge, "Warning", "StatusUpdateFailed", "Failed to update status for bridge: %v", err)
		return err
	}

	log.Info("bridge status updated", "condition", condition.Type, "status", condition.Status)
	return nil
}

// TODO add doc note about how deletion is handled. When the bridge is deleted, any enslaved interfaces
// will remain in their current state, but will no longer be enslaved. This means that any up interfaces
// will be able to reach the host. There is no way around this because other systems may add interfaces
// to the bridge, and add/change state of enslaved interfaces between any check and the deletion of the bridge.
// There is also an unavoidable race condition where
// 1. Another process (or controller) retrieves the bridge, storing the interface ID
// 2. The bridge is deleted
// 3. An unrelated bridge (with or without the same name) is created, using the same interface ID
// 4. The other process attaches another link to the bridge, which is now a different bridge

// SetupWithManager sets up the controller with the Manager.
func (r *BridgeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Watch Bridge resources for spec changes
		For(&bridgeoperatorv1alpha1.Bridge{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("bridge").
		WithOptions(controller.TypedOptions[reconcile.Request]{
			// Ignore leader election. This controller should run once per node.
			NeedLeaderElection: ptr.To(false),
		}).
		Complete(r)
}
