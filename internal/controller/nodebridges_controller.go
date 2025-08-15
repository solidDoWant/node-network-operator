package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/elliotchance/pie/v2"
	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
	"github.com/vishvananda/netlink"
)

var cleanupConditionType = fmt.Sprintf("%s/%s", bridgeoperatorv1alpha1.GroupVersion.Group, "Cleanup")

// NodeBridgesReconciler reconciles a NodeBridges object
type NodeBridgesReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	nodeName string
	recorder record.EventRecorder
}

func NewNodeBridgesReconciler(mgr ctrl.Manager, nodeName string) *NodeBridgesReconciler {
	return &NodeBridgesReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		nodeName: nodeName,
		recorder: mgr.GetEventRecorderFor("nodebridges-controller"),
	}
}

// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=nodebridges,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=nodebridges/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=list

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *NodeBridgesReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Get the NodeBridges instance from the informer cache
	var nodeBridges bridgeoperatorv1alpha1.NodeBridges
	if err := r.Get(ctx, req.NamespacedName, &nodeBridges); err != nil {
		log.Error(err, "unable to fetch NodeBridges")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log = log.WithValues("nodeBridges", nodeBridges.Name)

	if !nodeBridges.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &nodeBridges)
	}

	bridgeResources, err := r.getBridgeResources(ctx, nodeBridges.Spec.MatchingBridges)
	if err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: fmt.Sprintf("Failed get all bridge resources: %v", err),
		}

		return r.handleError(ctx, &nodeBridges, condition, err, "failed to get all bridge resources")
	}

	if err := r.cleanupUndesiredLinks(ctx, &nodeBridges, bridgeResources); err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: fmt.Sprintf("Failed to cleanup all old, undesired links: %v", err),
		}

		return r.handleError(ctx, &nodeBridges, condition, err, "failed to cleanup links")
	}

	readyCondition := metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionUnknown,
		Reason: "ReconcileStarted",
	}
	meta.SetStatusCondition(&nodeBridges.Status.Conditions, readyCondition)

	if err := r.updatePreviouslyAttemptedBridges(ctx, &nodeBridges, bridgeResources); err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: fmt.Sprintf("Failed to update previously attempted bridges: %v", err),
		}

		return r.handleError(ctx, &nodeBridges, condition, err, "failed to update previously attempted bridges")
	}

	if err := r.ensureNoDuplicateInterfaceNames(ctx, &nodeBridges, bridgeResources); err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: fmt.Sprintf("One or more assigned bridges have share an interface name: %v", err),
		}

		return r.handleError(ctx, &nodeBridges, condition, err, "one or more assigned bridges have share an interface name")
	}

	errs := make([]error, 0, len(bridgeResources))
	for _, bridge := range bridgeResources {
		if err := r.upsertBridgeLink(ctx, &nodeBridges, bridge); err != nil {
			log.Error(err, "failed to upsert bridge link", "bridge", bridge.Name)

			errs = append(errs, fmt.Errorf("failed to upsert bridge link %s: %w", bridge.Name, err))
			continue
		}
	}

	err = errors.Join(errs...)
	if err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReconcileFailed",
			Message: fmt.Sprintf("Failed to reconcile all bridge links: %v", err),
		}

		return r.handleError(ctx, &nodeBridges, condition, err, "failed to reconcile all bridge links")
	}

	readyCondition.Status = metav1.ConditionTrue
	readyCondition.Reason = "ReconcileComplete"

	meta.SetStatusCondition(&nodeBridges.Status.Conditions, readyCondition)
	return ctrl.Result{}, r.updateStatus(ctx, &nodeBridges)
}

// handleError handles errors during reconciliation, updating the NodeBridges status with the error condition.
// If an error occurs while updating the conditions, it logs the error and fires an event with the error message.
func (r *NodeBridgesReconciler) handleError(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges, condition metav1.Condition, err error, msg string, args ...interface{}) (ctrl.Result, error) {
	logf.FromContext(ctx).Error(err, msg, args...)
	meta.SetStatusCondition(&nodeBridges.Status.Conditions, condition)
	return ctrl.Result{}, r.updateStatus(ctx, nodeBridges)
}

// handleDeletion handles the deletion of NodeBridges resources, including status updates and error handling.
func (r *NodeBridgesReconciler) handleDeletion(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.Info("deleting NodeBridge resources")
	if err := r.performDeletion(ctx, nodeBridges); err != nil {
		condition := metav1.Condition{
			Type:    cleanupConditionType,
			Status:  metav1.ConditionFalse,
			Reason:  "CleanupFailed",
			Message: fmt.Sprintf("Failed to cleanup resources: %v", err),
		}
		return r.handleError(ctx, nodeBridges, condition, err, "failed to cleanup resources")
	}

	readyCondition := metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "Deleted",
		Message: "NodeBridges resource is being deleted",
	}
	cleanupCondition := metav1.Condition{
		Type:    cleanupConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "CleanupComplete",
		Message: "NodeBridges resource cleanup complete",
	}

	meta.SetStatusCondition(&nodeBridges.Status.Conditions, readyCondition)
	meta.SetStatusCondition(&nodeBridges.Status.Conditions, cleanupCondition)
	return ctrl.Result{}, r.updateStatus(ctx, nodeBridges)
}

// performDeletion performs the actual deletion of NodeBridges resources.
func (r *NodeBridgesReconciler) performDeletion(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges) error {
	errs := pie.Map(nodeBridges.Status.LastAttemptedBridges, func(bridgeName string) error {
		if err := r.cleanupBridgeLink(ctx, nodeBridges, bridgeName); err != nil {
			return fmt.Errorf("failed to cleanup bridge link %s: %w", bridgeName, err)
		}
		return nil
	})

	return errors.Join(errs...)
}

// ensureNoDuplicateInterfaceNames checks if any bridges share the same interface name.
// If duplicates are found, it updates the NodeBridges' link conditions with an error condition and returns an error.
func (r *NodeBridgesReconciler) ensureNoDuplicateInterfaceNames(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges, bridgeResources map[string]bridgeoperatorv1alpha1.Bridge) error {
	// Key is the interface name, value is a list of bridge names that share that interface name
	interfaceNames := make(map[string][]string, len(bridgeResources))

	for bridgeName, bridge := range bridgeResources {
		if existingMatchingBridges, ok := interfaceNames[bridge.Spec.InterfaceName]; ok {
			interfaceNames[bridge.Spec.InterfaceName] = append(existingMatchingBridges, bridgeName)
			continue
		}

		interfaceNames[bridge.Spec.InterfaceName] = []string{bridgeName}
	}

	// Check for duplicate interface names
	errs := make([]error, 0, len(bridgeResources))
	for interfaceName, matchingBridges := range interfaceNames {
		if len(matchingBridges) < 2 {
			continue
		}

		err := fmt.Errorf("interface name %q is shared by multiple bridges: %v", interfaceName, matchingBridges)
		errs = append(errs, err)

		// Update the link conditions to reflect the error
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "DuplicateInterfaceName",
			Message: fmt.Sprintf("Interface name %q is shared by multiple bridges: %v", interfaceName, matchingBridges),
		}
		r.setLinkCondition(nodeBridges, interfaceName, condition)
	}

	return errors.Join(errs...)
}

// updateStatus updates the NodeBridges status If the patch fails, it records an event and returns an error.
func (r *NodeBridgesReconciler) updateStatus(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges) error {
	log := logf.FromContext(ctx)

	// Always attempt to patch the status, as the status could have been updated prior to this call
	if err := r.Status().Patch(ctx, nodeBridges, client.Apply); err != nil {
		log.Error(err, "failed to patch NodeBridges status")
		r.recorder.Eventf(nodeBridges, "Warning", "StatusUpdateFailed",
			"Failed to update NodeBridges status: %v", err)
		return fmt.Errorf("failed to patch NodeBridges status: %w", err)
	}

	log.V(1).Info("NodeBridges status updated")
	return nil
}

// getBridgeResources retrieves the bridge resources by their names. The returned map contains the bridge name as the key and the Bridge resource as the value.
func (r *NodeBridgesReconciler) getBridgeResources(ctx context.Context, bridgeResourceNames []string) (map[string]bridgeoperatorv1alpha1.Bridge, error) {
	bridges := make(map[string]bridgeoperatorv1alpha1.Bridge, len(bridgeResourceNames))
	for _, bridgeName := range bridgeResourceNames {
		var bridge bridgeoperatorv1alpha1.Bridge
		if err := r.Get(ctx, client.ObjectKey{Name: bridgeName}, &bridge); err != nil {
			return nil, fmt.Errorf("failed to get bridge resource %s: %w", bridgeName, err)
		}
		bridges[bridgeName] = bridge
	}

	return bridges, nil
}

// cleanupUndesiredLinks cleans up any links that are not desired based on the current NodeBridges configuration.
func (r *NodeBridgesReconciler) cleanupUndesiredLinks(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges, desiredBridgeResources map[string]bridgeoperatorv1alpha1.Bridge) error {
	_, linkNamesToRemove := pie.Diff(nodeBridges.Status.LastAttemptedBridges, pie.Keys(desiredBridgeResources))

	errs := make([]error, 0, len(linkNamesToRemove))
	for _, linkNameToRemove := range linkNamesToRemove {
		err := r.cleanupBridgeLink(ctx, nodeBridges, linkNameToRemove)
		if err == nil {
			continue
		}

		errs = append(errs, fmt.Errorf("failed to cleanup bridge link %s: %w", linkNameToRemove, err))
	}

	return errors.Join(errs...)
}

// updatePreviouslyAttemptedBridges updates the NodeBridges status with the bridges that were previously attempted.
// This function is called after previous attempts have been confirmed to be successful or failed.
func (r *NodeBridgesReconciler) updatePreviouslyAttemptedBridges(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges, desiredBridgeResources map[string]bridgeoperatorv1alpha1.Bridge) error {
	nodeBridges.Status.LastAttemptedBridges = pie.Keys(desiredBridgeResources)
	logf.FromContext(ctx).V(1).Info("updating previously attempted bridges", "attemptedBridges", nodeBridges.Status.LastAttemptedBridges)
	return r.updateStatus(ctx, nodeBridges)
}

// cleanupBridgeLink cleans up a bridge link by name, removing it from the system and updating the NodeBridges status.
// To reduce API calls, this does not actually update the resource via the API, but rather updates the status in memory. It is expected
// that the caller will handle the API update after this function returns, regardless of whether the cleanup was successful or not.
func (r *NodeBridgesReconciler) cleanupBridgeLink(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges, bridgeName string) error {
	err := r.performBridgeLinkCleanup(ctx, bridgeName)
	if err == nil {
		delete(nodeBridges.Status.LinkConditions, bridgeName)
		return nil
	}

	// Update the link conditions to reflect the cleanup failure
	cleanupCondition := metav1.Condition{
		Type:    cleanupConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  "CleanupFailed",
		Message: fmt.Sprintf("Failed to cleanup bridge link %s: %v", bridgeName, err),
	}
	readyCondition := *cleanupCondition.DeepCopy()
	readyCondition.Type = "Ready"

	r.setLinkCondition(nodeBridges, bridgeName, cleanupCondition)
	r.setLinkCondition(nodeBridges, bridgeName, readyCondition)
	return err
}

// performBridgeLinkCleanup performs the actual cleanup of a bridge link, including removing it from the system.
// This does not update the NodeBridges' link conditions.
func (r *NodeBridgesReconciler) performBridgeLinkCleanup(ctx context.Context, bridgeName string) error {
	log := logf.FromContext(ctx)

	// Get the bridge link by name
	link, err := r.getBridgeLink(ctx, bridgeName)
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
	return nil
}

func (r *NodeBridgesReconciler) getBridgeLink(ctx context.Context, bridgeName string) (*netlink.Bridge, error) {
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

// ensureBridgeExists ensures that a bridge with the given name exists, and is configured correctly. It updates the NodeBridges status with the result of the operation.
func (r *NodeBridgesReconciler) upsertBridgeLink(ctx context.Context, nodeBridges *bridgeoperatorv1alpha1.NodeBridges, bridge bridgeoperatorv1alpha1.Bridge) error {
	err := r.ensureBridgeInDesiredState(ctx, bridge)
	condition := metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "BridgeConfigured",
		Message: fmt.Sprintf("Bridge %s is configured successfully", bridge.Spec.InterfaceName),
	}
	if err != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "BridgeConfigurationFailed"
		condition.Message = fmt.Sprintf("Failed to configure bridge %s: %v", bridge.Spec.InterfaceName, err)
	}

	r.setLinkCondition(nodeBridges, bridge.Spec.InterfaceName, condition)
	return err
}

// ensureBridgeInDesiredState ensures that the bridge is in the desired state, including checking if it exists, has the correct MTU, and is up.
func (r *NodeBridgesReconciler) ensureBridgeInDesiredState(ctx context.Context, bridge bridgeoperatorv1alpha1.Bridge) error {
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
func (r *NodeBridgesReconciler) ensureBridgeExists(ctx context.Context, bridgeName string) error {
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

func (r *NodeBridgesReconciler) getBridge(ctx context.Context, bridgeName string) (*netlink.Bridge, error) {
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
func (r *NodeBridgesReconciler) ensureBridgeMTU(ctx context.Context, bridgeLink *netlink.Bridge, desiredMTU int) error {
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
func (r *NodeBridgesReconciler) ensureBridgeUp(ctx context.Context, bridgeLink *netlink.Bridge) error {
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

// setLinkCondition sets a condition for a specific link in the NodeBridges status.
func (r *NodeBridgesReconciler) setLinkCondition(nodeBridges *bridgeoperatorv1alpha1.NodeBridges, linkName string, condition metav1.Condition) {
	linkConditions, ok := nodeBridges.Status.LinkConditions[linkName]
	if !ok {
		linkConditions = make(bridgeoperatorv1alpha1.NodeBridgesStatusConditions, 0, 1)
	}
	castedLinkConditions := []metav1.Condition(linkConditions)
	meta.SetStatusCondition(&castedLinkConditions, condition)
	nodeBridges.Status.LinkConditions[linkName] = castedLinkConditions
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeBridgesReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&bridgeoperatorv1alpha1.NodeBridges{},
			builder.WithPredicates(
				predicate.GenerationChangedPredicate{},
				// Filter out events that are not for the current node
				predicate.NewPredicateFuncs(func(object client.Object) bool {
					return object.GetName() == r.nodeName
				}),
			),
		).
		// Watch for changes to Bridge resources and enqueue requests for NodeBridges selected by the bridge's node selector.
		Watches(
			&bridgeoperatorv1alpha1.Bridge{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				bridge, ok := obj.(*bridgeoperatorv1alpha1.Bridge)
				if !ok {
					return nil
				}

				selector, err := metav1.LabelSelectorAsSelector(&bridge.Spec.NodeSelector)
				if err != nil {
					return nil
				}

				var nodes corev1.NodeList
				if err := r.List(ctx, &nodes, client.MatchingLabelsSelector{Selector: selector}); err != nil {
					return nil
				}

				return pie.Map(nodes.Items, func(node corev1.Node) reconcile.Request {
					return reconcile.Request{
						NamespacedName: client.ObjectKey{
							// NodeBridges resources are cluster namespaced and named after the node that
							// they are associated with.
							Name: node.Name,
						},
					}
				})
			}),
			// This resource is only impacted by spec changes
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Named("nodebridges").
		WithOptions(controller.TypedOptions[reconcile.Request]{
			// Ignore leader election. This controller should run once per node.
			NeedLeaderElection: ptr.To(false),
		}).
		Complete(r)
}
