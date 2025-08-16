package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/elliotchance/pie/v2"
	bridgeoperatorv1alpha1 "github.com/solidDoWant/bridge-operator/api/v1alpha1"
)

var bridgeFinalizerName = fmt.Sprintf("%s/%s", bridgeoperatorv1alpha1.GroupVersion.Group, "bridge-finalizer")

// BridgeReconciler reconciles a Bridge object
type BridgeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func NewBridgeReconciler(cluster cluster.Cluster) *BridgeReconciler {
	return &BridgeReconciler{
		Client:   cluster.GetClient(),
		Scheme:   cluster.GetScheme(),
		recorder: cluster.GetEventRecorderFor("bridge-controller"),
	}
}

// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=bridges,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=bridges/status,verbs=patch
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=bridges/finalizers,verbs=patch
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=nodebridges,verbs=get;list;create;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *BridgeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Get the bridge resource from the informer cache
	var bridge bridgeoperatorv1alpha1.Bridge
	if err := r.Get(ctx, req.NamespacedName, &bridge); err != nil {
		logf.FromContext(ctx).Error(err, "unable to fetch Bridge")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	oldBridge := bridge.DeepCopy()

	// Handle deletion of the bridge resource
	if !bridge.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, oldBridge, &bridge)
	}

	if err := r.ensureFinalizerExists(ctx, oldBridge, &bridge); err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "FinalizerSetupFailed",
			Message: fmt.Sprintf("Failed to ensure finalizer exists: %v", err),
		}

		return r.handleError(ctx, oldBridge, &bridge, condition, err, "failed to ensure finalizer exists for bridge")
	}

	if err := r.updateNodeBridges(ctx, oldBridge, &bridge); err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "NodeBridgesUpdateFailed",
			Message: fmt.Sprintf("Failed to update all NodeBridges resources: %v", err),
		}
		return r.handleError(ctx, oldBridge, &bridge, condition, err, "failed to update NodeBridges resources for all nodes")
	}

	// Update the status of the bridge resource
	condition := metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "ReconcileSuccessful",
	}
	meta.SetStatusCondition(&bridge.Status.Conditions, condition)
	return ctrl.Result{}, r.updateStatus(ctx, oldBridge, &bridge)
}

// handleError handles errors during reconciliation, updating the Bridge status with the error condition.
// If an error occurs while updating the conditions, it logs the error and fires an event with the error message.
func (r *BridgeReconciler) handleError(ctx context.Context, oldBridge, newBridge *bridgeoperatorv1alpha1.Bridge, condition metav1.Condition, err error, msg string, args ...any) (ctrl.Result, error) {
	logf.FromContext(ctx).Error(err, msg, args...)
	meta.SetStatusCondition(&newBridge.Status.Conditions, condition)
	return ctrl.Result{}, errors.Join(err, r.updateStatus(ctx, oldBridge, newBridge))
}

func (r *BridgeReconciler) handleDeletion(ctx context.Context, oldBridge, newBridge *bridgeoperatorv1alpha1.Bridge) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(newBridge, bridgeFinalizerName) {
		log.V(1).Info("no finalizer present, not performing cleanup")
		return ctrl.Result{}, nil
	}

	log.Info("deleting Bridge resources")
	if err := r.removeFromNodeBridges(ctx, newBridge, nil); err != nil {
		condition := metav1.Condition{
			Type:    "Cleanup",
			Status:  metav1.ConditionFalse,
			Reason:  "NodeBridgesCleanupFailed",
			Message: fmt.Sprintf("Failed to remove bridge from all NodeBridges resources: %v", err),
		}

		return r.handleError(ctx, oldBridge, newBridge, condition, err, "failed to remove bridge from all NodeBridges resources")
	}

	readyCondition := metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "Deleted",
		Message: fmt.Sprintf("Bridge %s has been deleted", newBridge.Spec.InterfaceName),
	}
	cleanupCondition := metav1.Condition{
		Type:    "Cleanup",
		Status:  metav1.ConditionTrue,
		Reason:  "CleanupSuccessful",
		Message: fmt.Sprintf("Bridge %s has been cleaned up successfully", newBridge.Spec.InterfaceName),
	}

	meta.SetStatusCondition(&newBridge.Status.Conditions, readyCondition)
	meta.SetStatusCondition(&newBridge.Status.Conditions, cleanupCondition)

	// Remove the finalizer, allowing the resource to be deleted
	controllerutil.RemoveFinalizer(newBridge, bridgeFinalizerName)
	if err := r.Patch(ctx, newBridge, client.MergeFrom(oldBridge)); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from bridge resource %s: %w", newBridge.Name, err)
	}

	return ctrl.Result{}, nil
	// return ctrl.Result{}, client.IgnoreNotFound(r.updateStatus(ctx, oldBridge, newBridge))
}

// Ensure the finalizer exists on the Bridge resource, so that it can be cleaned up properly
func (r *BridgeReconciler) ensureFinalizerExists(ctx context.Context, oldBridge, newBridge *bridgeoperatorv1alpha1.Bridge) error {
	if !controllerutil.AddFinalizer(newBridge, bridgeFinalizerName) {
		return nil
	}

	err := r.Patch(ctx, newBridge, client.MergeFrom(oldBridge))
	if err != nil {
		return fmt.Errorf("failed to add finalizer: %w", err)
	}
	*oldBridge = *newBridge.DeepCopy()

	return nil
}

func (r *BridgeReconciler) getMatchingNodes(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge) ([]corev1.Node, error) {
	nodeSelector, err := metav1.LabelSelectorAsSelector(&bridge.Spec.NodeSelector)
	if err != nil {
		// This should be caught by the validation webhook
		return nil, fmt.Errorf("failed to convert node selector: %w", err)
	}

	var matchingNodes corev1.NodeList
	if err := r.List(ctx, &matchingNodes, client.MatchingLabelsSelector{Selector: nodeSelector}); err != nil {
		return nil, err
	}

	return matchingNodes.Items, nil
}

func (r *BridgeReconciler) updateNodeBridges(ctx context.Context, oldBridge, newBridge *bridgeoperatorv1alpha1.Bridge) error {
	nodes, err := r.getMatchingNodes(ctx, newBridge)
	if err != nil {
		return fmt.Errorf("failed to get matching node names: %w", err)
	}

	condition := metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionUnknown,
		Reason: "ReconcileStarted",
	}
	meta.SetStatusCondition(&newBridge.Status.Conditions, condition)

	if err := r.removeFromNodeBridges(ctx, newBridge, nodes); err != nil {
		return fmt.Errorf("failed to remove bridge from all undesired NodeBridges resources: %w", err)
	}

	if err := r.registerWithNodeBridges(ctx, oldBridge, newBridge, nodes); err != nil {
		return fmt.Errorf("failed to register bridge with NodeBridges resources: %w", err)
	}

	return nil
}

func (r *BridgeReconciler) removeFromNodeBridges(ctx context.Context, bridge *bridgeoperatorv1alpha1.Bridge, newNodes []corev1.Node) error {
	desiredNodeBridgesNames := pie.Map(newNodes, func(node corev1.Node) string {
		return node.Name
	})

	_, undesiredNodeBridgesNames := pie.Diff(bridge.Status.MatchedNodes, desiredNodeBridgesNames)

	errs := pie.Map(undesiredNodeBridgesNames, func(nodeBridgesName string) error {
		var nodeBridges bridgeoperatorv1alpha1.NodeBridges
		if err := r.Get(ctx, client.ObjectKey{Name: nodeBridgesName}, &nodeBridges); err != nil {
			if apierrors.IsNotFound(err) {
				// NodeBridges resource does not exist, nothing to do
				return nil
			}

			return fmt.Errorf("failed to get NodeBridges resource for node %s: %w", nodeBridgesName, err)
		}
		oldNodeBridges := nodeBridges.DeepCopy()

		if !slices.Contains(nodeBridges.Spec.MatchingBridges, bridge.Name) {
			// Skip the resource if it does not contain the bridge
			return nil
		}

		nodeBridges.Spec.MatchingBridges = pie.Filter(nodeBridges.Spec.MatchingBridges, func(bridgeName string) bool {
			return bridgeName != bridge.Name
		})

		if err := r.Patch(ctx, &nodeBridges, client.MergeFrom(oldNodeBridges)); err != nil {
			if apierrors.IsNotFound(err) {
				// NodeBridges resource does not exist, nothing to do
				return nil
			}

			return fmt.Errorf("failed to patch NodeBridges resource for node %s: %w", nodeBridgesName, err)
		}

		// Remove the bridge from the status of the Bridge resource
		bridge.Status.MatchedNodes = pie.Filter(bridge.Status.MatchedNodes, func(nodeName string) bool {
			return nodeName != nodeBridgesName
		})
		return nil
	})

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("failed to remove bridge from all undesired NodeBridges resources: %w", err)
	}

	return nil
}

func (r *BridgeReconciler) registerWithNodeBridges(ctx context.Context, oldBridge, newBridge *bridgeoperatorv1alpha1.Bridge, nodes []corev1.Node) error {
	// Doing this first ensures that the bridge status always contains at least the nodes that match the bridge's node selector
	// even if one or more NodeBridges resources fail to be created or updated.
	// This is important because the bridge status is used to track which NodeBridges resources may need to be cleaned up upon deletion.
	newBridge.Status.MatchedNodes = pie.Map(nodes, func(node corev1.Node) string {
		return node.Name
	})
	if err := r.updateStatus(ctx, oldBridge, newBridge); err != nil {
		return fmt.Errorf("failed to update bridge status with matched nodes: %w", err)
	}

	errs := pie.Map(nodes, func(node corev1.Node) error {
		nodeBridges := bridgeoperatorv1alpha1.NodeBridges{
			ObjectMeta: metav1.ObjectMeta{
				Name: node.Name,
			},
		}

		if _, err := controllerutil.CreateOrPatch(ctx, r.Client, &nodeBridges, func() error {
			if !nodeBridges.DeletionTimestamp.IsZero() {
				// Don't do anything if the NodeBridges resource is being deleted
				return nil
			}

			if err := controllerutil.SetOwnerReference(&node, &nodeBridges, r.Scheme, controllerutil.WithBlockOwnerDeletion(true)); err != nil {
				return fmt.Errorf("failed to set owner reference: %w", err)
			}

			if slices.Contains(nodeBridges.Spec.MatchingBridges, newBridge.Name) {
				nodeBridges.Spec.MatchingBridges = append(nodeBridges.Spec.MatchingBridges, newBridge.Name)
			}

			return nil
		}); err != nil {
			return fmt.Errorf("failed to create or patch NodeBridges resource for node %s: %w", node.Name, err)
		}

		return nil
	})

	return errors.Join(errs...)
}

// updateStatus updates the status of the Bridge resource with the given condition.
func (r *BridgeReconciler) updateStatus(ctx context.Context, oldBridge, newBridge *bridgeoperatorv1alpha1.Bridge) error {
	log := logf.FromContext(ctx)

	if err := r.Status().Patch(ctx, newBridge, client.MergeFrom(oldBridge)); err != nil {
		log.Error(err, "unable to patch Bridge status")
		r.recorder.Eventf(newBridge, "Warning", "StatusUpdateFailed", "Failed to update status for bridge: %v", err)
		return fmt.Errorf("failed to update bridge status: %w", err)
	}
	*oldBridge = *newBridge.DeepCopy()

	log.V(1).Info("bridge status updated")
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
	labelChangedPredicate := predicate.LabelChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		// Watch Bridge resources for spec changes
		For(&bridgeoperatorv1alpha1.Bridge{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				return []reconcile.Request{
					{
						NamespacedName: client.ObjectKey{
							Name: o.GetName(),
						},
					},
				}
			}),
			builder.WithPredicates(
				// Trigger on any change that would affect what the node selector matches.
				predicate.Funcs{
					CreateFunc: func(tce event.TypedCreateEvent[client.Object]) bool {
						return true
					},
					DeleteFunc: func(tce event.TypedDeleteEvent[client.Object]) bool {
						return true
					},
					UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
						return labelChangedPredicate.Update(tue)
					},
					GenericFunc: func(tge event.TypedGenericEvent[client.Object]) bool {
						return false
					},
				},
			),
		).
		Named("bridge").
		Complete(r)
}
