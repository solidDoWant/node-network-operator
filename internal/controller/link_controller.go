package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
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

var (
	linkFinalizerName = fmt.Sprintf("link.%s/finalizer", bridgeoperatorv1alpha1.GroupVersion.Group)
)

// type linkTypeReconciler interface {
// 	func upsertLink(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link) (ctrl.Result, error)

// }

// LinkReconciler reconciles a Link object
type LinkReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func NewLinkReconciler(cluster cluster.Cluster) *LinkReconciler {
	return &LinkReconciler{
		Client:   cluster.GetClient(),
		Scheme:   cluster.GetScheme(),
		recorder: cluster.GetEventRecorderFor("link-controller"),
	}
}

// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=links,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=links/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=links/finalizers,verbs=update
// +kubebuilder:rbac:groups=bridgeoperator.soliddowant.dev,resources=nodelinks,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *LinkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("link", req.Name)
	logf.IntoContext(ctx, log)

	// Fetch the Link instance
	var link bridgeoperatorv1alpha1.Link
	if err := r.Get(ctx, req.NamespacedName, &link); err != nil {
		log.Error(err, "unable to fetch Link")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Make a copy of the Link instance to avoid modifying the original. This allows for computing
	// diffs for patch operations.
	clusterStateLink := link.DeepCopy()

	// Handle deletion
	if !link.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, clusterStateLink, &link)
	}

	return r.handleUpsert(ctx, clusterStateLink, &link)
}

func (r *LinkReconciler) handleUpsert(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("action", "upsert")
	logf.IntoContext(ctx, log)

	if controllerutil.AddFinalizer(link, linkFinalizerName) {
		log.V(1).Info("adding finalizer to Link resource", "finalizer", linkFinalizerName)
		if err := r.patchResource(ctx, clusterStateLink, link); err != nil {
			condition := metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "FinalizerUpdateFailed",
				Message: fmt.Sprintf("Failed to update Link status with finalizer: %v", err),
			}
			return r.handleError(ctx, clusterStateLink, link, condition, err, "failed to update Link status with finalizer")
		}
	}

	if err := r.updateNodeLinks(ctx, clusterStateLink, link); err != nil {
		condition := metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "NodeLinksUpdateFailed",
			Message: fmt.Sprintf("Failed to update all NodeLinks resources: %v", err),
		}
		return r.handleError(ctx, clusterStateLink, link, condition, err, "failed to update NodeLinks resources for all nodes")
	}

	// Update the status of the bridge resource
	condition := metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "ReconcileSuccessful",
	}
	meta.SetStatusCondition(&link.Status.Conditions, condition)
	return ctrl.Result{}, r.patchResource(ctx, clusterStateLink, link)
}

func (r *LinkReconciler) handleDeletion(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("action", "delete")
	logf.IntoContext(ctx, log)

	if !controllerutil.ContainsFinalizer(link, linkFinalizerName) {
		log.V(1).Info("no finalizer present, not performing cleanup")
		return ctrl.Result{}, nil
	}

	log.Info("deleting Link resources")
	if err := r.removeFromUnmatchedNodeLinks(ctx, link, nil); err != nil {
		condition := metav1.Condition{
			Type:    "Cleanup",
			Status:  metav1.ConditionFalse,
			Reason:  "NodeLinksCleanupFailed",
			Message: fmt.Sprintf("Failed to remove bridge from all NodeLinks resources: %v", err),
		}

		return r.handleError(ctx, clusterStateLink, link, condition, err, "failed to remove bridge from all NodeLinks resources")
	}

	readyCondition := metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "Deleted",
		Message: fmt.Sprintf("Link %s has been deleted", link.Name),
	}
	cleanupCondition := metav1.Condition{
		Type:    "Cleanup",
		Status:  metav1.ConditionTrue,
		Reason:  "CleanupSuccessful",
		Message: fmt.Sprintf("Link %s has been cleaned up successfully", link.Name),
	}

	meta.SetStatusCondition(&link.Status.Conditions, readyCondition)
	meta.SetStatusCondition(&link.Status.Conditions, cleanupCondition)

	// Remove the finalizer, allowing the resource to be deleted
	controllerutil.RemoveFinalizer(link, linkFinalizerName)

	return ctrl.Result{}, r.patchResource(ctx, clusterStateLink, link)
}

// updateNodeLinks updates the NodeLinks desired state based on the Link's node selector. Nodes/NodeLinks that are no longer matching
// will be updated to remove the Link from their desired state.
func (r *LinkReconciler) updateNodeLinks(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link) error {
	nodes, err := r.getMatchingNodes(ctx, link)
	if err != nil {
		return fmt.Errorf("failed to get matching node names: %w", err)
	}

	condition := metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionUnknown,
		Reason: "ReconcileStarted",
	}
	meta.SetStatusCondition(&link.Status.Conditions, condition)

	if err := r.removeFromUnmatchedNodeLinks(ctx, link, nodes); err != nil {
		return fmt.Errorf("failed to remove link from all undesired NodeLinks resources: %w", err)
	}

	if err := r.registerWithMatchedNodeLinks(ctx, clusterStateLink, link, nodes); err != nil {
		return fmt.Errorf("failed to register link with NodeLinks resources: %w", err)
	}

	return nil
}

func (r *LinkReconciler) removeFromUnmatchedNodeLinks(ctx context.Context, link *bridgeoperatorv1alpha1.Link, newNodes []corev1.Node) error {
	desiredNodeNames := pie.Map(newNodes, func(node corev1.Node) string {
		return node.Name
	})

	_, undesiredNodeLinksNames := pie.Diff(link.Status.MatchedNodes, desiredNodeNames)

	errs := pie.Map(undesiredNodeLinksNames, func(nodeLinksName string) error {
		var nodeLinks bridgeoperatorv1alpha1.NodeLinks
		if err := r.Get(ctx, client.ObjectKey{Name: nodeLinksName}, &nodeLinks); err != nil {
			if apierrors.IsNotFound(err) {
				// NodeLinks resource does not exist, nothing to do
				return nil
			}

			return fmt.Errorf("failed to get NodeLinks resource for node %s: %w", nodeLinksName, err)
		}
		clusterStatenodeLinks := nodeLinks.DeepCopy()

		// Check if the node still exists. If it does not, which can happen if the node was deleted without foreground cascade deletion,
		// then the NodeLinks finalizer should be removed so that the resource can be deleted. If the node is deleted, it is assumed
		// that the daemonset controller that was running on that node was terminated, and will be unable to remove the finalizer itself.
		// Because this controller respects leadership elections, there should only be one controller that is attempting to remove the
		// finalizer.
		if !nodeLinks.DeletionTimestamp.IsZero() && controllerutil.ContainsFinalizer(&nodeLinks, nodeLinksFinalizerName) {
			var node corev1.Node
			if err := r.Get(ctx, client.ObjectKey{Name: nodeLinksName}, &node); err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to get node %s while checking if the NodeLinks finalizer should be removed: %w", nodeLinksName, err)
				}

				// Node does not exist, remove the finalizer so that the NodeLinks resource can be deleted
				controllerutil.RemoveFinalizer(&nodeLinks, nodeLinksFinalizerName)
			}
		}

		if !slices.Contains(nodeLinks.Spec.MatchingLinks, link.Name) {
			// Skip the resource if it does not contain the link
			return nil
		}

		nodeLinks.Spec.MatchingLinks = pie.Filter(nodeLinks.Spec.MatchingLinks, func(linkName string) bool {
			return linkName != link.Name
		})

		// Patch or delete the NodeBridges resource, depending on whether it still has any matching links
		var err error
		if len(nodeLinks.Spec.MatchingLinks) == 0 {
			logf.FromContext(ctx).Info("deleting NodeLinks resource", "node", nodeLinksName)
			err = r.Delete(ctx, &nodeLinks)
		} else {
			logf.FromContext(ctx).Info("patching NodeLinks resource", "node", nodeLinksName)
			err = r.Patch(ctx, &nodeLinks, client.MergeFrom(clusterStatenodeLinks))
		}
		if err != nil {
			return client.IgnoreNotFound(err)
		}

		// Remove the link from the status of the Link resource
		link.Status.MatchedNodes = pie.Filter(link.Status.MatchedNodes, func(nodeName string) bool {
			return nodeName != nodeLinksName
		})
		return nil
	})

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("failed to remove link from all undesired NodeLinks resources: %w", err)
	}

	return nil
}

// registerWithMatchedNodeLinks registers the link with the NodeLinks resources for all provided nodes.
func (r *LinkReconciler) registerWithMatchedNodeLinks(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link, matchedNodes []corev1.Node) error {
	// Doing this first ensures that the link status always contains at least the nodes that match the link's node selector
	// even if one or more NodeLinks resources fail to be created or updated.
	// This is important because the bridge status is used to track which NodeBridges resources may need to be cleaned up upon deletion.
	link.Status.MatchedNodes = pie.Map(matchedNodes, func(node corev1.Node) string {
		return node.Name
	})
	if err := r.patchResource(ctx, clusterStateLink, link); err != nil {
		return err
	}

	errs := pie.Map(matchedNodes, func(node corev1.Node) error {
		return r.registerWithNodeLinks(ctx, clusterStateLink, link, node)
	})

	return errors.Join(errs...)
}

// registerWithNodeLinks registers the link with the NodeLinks resource for the given node.
func (r *LinkReconciler) registerWithNodeLinks(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link, node corev1.Node) error {
	nodeLinks := bridgeoperatorv1alpha1.NodeLinks{
		ObjectMeta: metav1.ObjectMeta{
			Name: node.Name,
		},
	}

	_, err := controllerutil.CreateOrPatch(ctx, r.Client, &nodeLinks, func() error {
		if !nodeLinks.DeletionTimestamp.IsZero() {
			// Don't do anything if the NodeBridges resource is being deleted
			return nil
		}

		if err := controllerutil.SetOwnerReference(&node, &nodeLinks, r.Scheme, controllerutil.WithBlockOwnerDeletion(true)); err != nil {
			return fmt.Errorf("failed to set owner reference: %w", err)
		}

		if !slices.Contains(nodeLinks.Spec.MatchingLinks, link.Name) {
			nodeLinks.Spec.MatchingLinks = append(nodeLinks.Spec.MatchingLinks, link.Name)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create or patch NodeBridges resource for node %s: %w", node.Name, err)
	}

	return nil
}

// getmatchingNodes retrieves all nodes that match the Link's node selector.
func (r *LinkReconciler) getMatchingNodes(ctx context.Context, link *bridgeoperatorv1alpha1.Link) ([]corev1.Node, error) {
	nodeSelector, err := metav1.LabelSelectorAsSelector(&link.Spec.NodeSelector)
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

// handleError handles errors during reconciliation, updating the Link status with the error condition.
// If an error occurs while updating the conditions, it logs the error and fires an event with the error message.
func (r *LinkReconciler) handleError(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link, condition metav1.Condition, err error, msg string, args ...any) (ctrl.Result, error) {
	logf.FromContext(ctx).Error(err, msg, args...)
	meta.SetStatusCondition(&link.Status.Conditions, condition)
	return ctrl.Result{}, errors.Join(err, r.patchResource(ctx, clusterStateLink, link))
}

// This cannot be extracted to a common function because it needs to be aware of the type of the resource being patched.
// There is not a way to write this generically without several leaky assertions due to Go's lack of covariant support
// (https://github.com/golang/go/issues/7512). Instead, this is copy/pasted for each reconciler that needs it, with only
// the function signature (resource type, var names) differing.

// patchResource updates the resource. If only a status update is needed, it patches the status only. This handles errors upon
// update, and the result can be directly returned from the Reconcile function.
// The clusterStateLink is the current state of the resource as stored in the cluster, and is used for computing patch diffs.
// It will be updated with the new state after a successful patch operation.
func (r *LinkReconciler) patchResource(ctx context.Context, clusterStateLink, link *bridgeoperatorv1alpha1.Link) error {
	log := logf.FromContext(ctx)

	// Determine whether the entire resource needs a patch or just the status
	// Ignore status changes, these always need to be applied and will always differ
	newStatus := link.Status.DeepCopy()
	link.Status = clusterStateLink.Status
	onlyStatusPatchIsNeeded := equality.Semantic.DeepEqual(clusterStateLink, link)
	link.Status = *newStatus

	var err error
	if onlyStatusPatchIsNeeded {
		log = log.WithValues("patchType", "status")
		log.V(1).Info(fmt.Sprintf("updating %T status resource only", clusterStateLink))
		logf.IntoContext(ctx, log)

		err = r.Status().Patch(ctx, link, client.MergeFrom(clusterStateLink))
	} else {
		log = log.WithValues("patchType", "full")
		log.V(1).Info(fmt.Sprintf("updating full %T resource", clusterStateLink))
		logf.IntoContext(ctx, log)

		err = r.Patch(ctx, link, client.MergeFrom(clusterStateLink))
	}

	if err != nil {
		log.Error(err, fmt.Sprintf("failed to patch %T status", clusterStateLink))
		r.recorder.Eventf(link, "Warning", "StatusUpdateFailed", "Failed to update %T status: %v", clusterStateLink, err)
		return fmt.Errorf("failed to patch %T status: %w", clusterStateLink, err)
	}

	// Update the oldNodeBridges to reflect the new object state
	*clusterStateLink = *link.DeepCopy()

	log.V(1).Info(fmt.Sprintf("%T status updated", clusterStateLink))
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LinkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	labelChangedPredicate := predicate.LabelChangedPredicate{}

	// Watch Link resources for spec changes
	return ctrl.NewControllerManagedBy(mgr).
		For(&bridgeoperatorv1alpha1.Link{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Watch node changes for changes that would affect node selector matching
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				node, ok := o.(*corev1.Node)
				if !ok || node == nil {
					return nil
				}

				return []reconcile.Request{
					{
						NamespacedName: client.ObjectKey{
							Name: node.GetName(),
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
		// Watch NodeBridges pending deletion, and reconcile matching bridges so that their status fields are updated
		Watches(
			&bridgeoperatorv1alpha1.NodeLinks{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				nodeLinks, ok := o.(*bridgeoperatorv1alpha1.NodeLinks)
				if !ok || nodeLinks == nil {
					return nil
				}

				return pie.Map(nodeLinks.Spec.MatchingLinks, func(linkName string) reconcile.Request {
					return reconcile.Request{
						NamespacedName: client.ObjectKey{
							Name: linkName,
						},
					}
				})
			}),
			builder.WithPredicates(
				// Only trigger on events where the NodeBridges resource is being deleted.
				predicate.Funcs{
					CreateFunc: func(tce event.TypedCreateEvent[client.Object]) bool {
						return false
					},
					DeleteFunc: func(tce event.TypedDeleteEvent[client.Object]) bool {
						return false
					},
					UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
						return tue.ObjectNew.GetDeletionTimestamp() != nil
					},
					GenericFunc: func(tge event.TypedGenericEvent[client.Object]) bool {
						return false
					},
				},
			),
		).
		Named("link").
		Complete(r)
}
