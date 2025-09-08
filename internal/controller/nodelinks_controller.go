package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/dominikbraun/graph"
	"github.com/elliotchance/pie/v2"
	nodenetworkoperatorv1alpha1 "github.com/solidDoWant/node-network-operator/api/v1alpha1"
	"github.com/solidDoWant/node-network-operator/internal/graphsearch"
	"github.com/solidDoWant/node-network-operator/internal/links"
	"github.com/vishvananda/netlink"
)

var nodeLinksFinalizerName = fmt.Sprintf("nodelinks.%s/finalizer", nodenetworkoperatorv1alpha1.GroupVersion.Group)

// NodeLinksReconciler reconciles a NodeLinks object
type NodeLinksReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	nodeName string
	recorder record.EventRecorder
}

func NewNodeLinksReconciler(cluster cluster.Cluster, nodeName string) *NodeLinksReconciler {
	return &NodeLinksReconciler{
		Client:   cluster.GetClient(),
		Scheme:   cluster.GetScheme(),
		nodeName: nodeName,
		recorder: cluster.GetEventRecorderFor("nodelinks-controller"),
	}
}

// +kubebuilder:rbac:groups=nodenetworkoperator.soliddowant.dev,resources=nodelinks,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=nodenetworkoperator.soliddowant.dev,resources=nodelinks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nodenetworkoperator.soliddowant.dev,resources=nodelinks/finalizers,verbs=create;patch
// +kubebuilder:rbac:groups=nodenetworkoperator.soliddowant.dev,resources=links,verbs=list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *NodeLinksReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("nodelinks", req.Name)
	logf.IntoContext(ctx, log)

	// Fetch the NodeLinks instance
	var nodeLinks nodenetworkoperatorv1alpha1.NodeLinks
	if err := r.Get(ctx, req.NamespacedName, &nodeLinks); err != nil {
		log.Error(err, "unable to fetch NodeLinks")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Make a copy of the NodeLinks instance to avoid modifying the original. This allows for computing
	// diffs for patch operations.
	clusterStateNodeLinks := nodeLinks.DeepCopy()

	if !nodeLinks.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, clusterStateNodeLinks, &nodeLinks)
	}

	return r.handleUpsert(ctx, clusterStateNodeLinks, &nodeLinks)
}

func (r *NodeLinksReconciler) handleUpsert(ctx context.Context, clusterStateNodeLinks, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("action", "upsert")
	logf.IntoContext(ctx, log)

	if !controllerutil.AddFinalizer(nodeLinks, nodeLinksFinalizerName) {
		log.V(1).Info("adding finalizer to NodeLinks resource", "finalizer", nodeLinksFinalizerName)
		if err := r.patchResource(ctx, clusterStateNodeLinks, nodeLinks); err != nil {
			condition := &metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "FinalizerUpdateFailed",
				Message: fmt.Sprintf("Failed to update NodeLinks status with finalizer: %v", err),
			}
			return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, condition, err, "failed to update LiNodeLinksnk status with finalizer")
		}
	}

	// This is the update process:
	// 0. Retrieve the referenced Link resources from the cluster, validate - linear complexity
	// 1. Build the tree described above, get nodes in topological order - linear complexity
	// 2. Iterate over the tree in topological order, and for each link: - sorting is O(n + m) where m is number of edges (dependency relations), iteration is linear
	// 	1. If a dependency is not in the tree, set it down and mark it as errored in the status (continue)
	//  2. If link has a dependent that is errored, set it down and mark it as errored in the status (continue)
	// 3. Build a list of undesired links (if deletion this is all links) - constant complexity
	// 4. Bring down the undesired links. This allows for deletion of these links in any order, covering (1). - linear complexity
	// 5. Delete the undesired links in any order - linear complexity
	// 6. Update the tracked links in the NodeLinks status
	// 7. Iterate over the tree in topological order, and for each link: - linear
	//  1. Upsert the link
	// 8. Update the resource-wide status
	// 9. Update the cluster resource

	// When removing a link (so that it is listed in the state but not in the spec), the desired state of any dependent tree must be one of the following:
	// 1. The dependent tree is also removed
	// 2. The dependent tree no longer has _any_ reference where it previously had a reference to the removed link
	// 3. The dependent tree has a reference to a link that is not the removed link
	//
	// In the case of (1), no action is needed **provided that the previously dependent link is removed first, or all dependent links are down first**.
	// In the case of (2), no action is needed because the association in netlink will be broken (possibly via dependent deletion) when the link is removed.
	// In the case of (3), no action is needed **provided that the dependent tree is updated first**.

	linkResources, err := r.getLinkResources(ctx, nodeLinks)
	if err != nil {
		condition := &metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "LinkRetrievalFailed",
			Message: fmt.Sprintf("Failed to retrieve Link resources: %v", err),
		}
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, condition, err, "failed to retrieve Link resources")
	}

	if err := r.validateLinks(nodeLinks, linkResources); err != nil {
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, nil, err, "link validation failed")
	}

	linkGraph, err := r.buildDesiredLinkGraph(linkResources)
	if err != nil {
		condition := &metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "LinkGraphBuildFailed",
			Message: fmt.Sprintf("Failed to build desired link graph: %v", err),
		}
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, condition, err, "failed to build desired link graph")
	}

	sortedLinkResourceNames, err := r.getTopolgicallySortedLinkNames(linkGraph)
	if err != nil {
		condition := &metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "LinkGraphTopologicalSortFailed",
			Message: fmt.Sprintf("Failed to topologically sort desired link graph: %v", err),
		}
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, condition, err, "failed to topologically sort desired link graph")
	}

	if err := r.updateDependentsMissingDependencies(ctx, nodeLinks, sortedLinkResourceNames, linkResources); err != nil {
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, nil, err, "failed to update links with missing dependencies")
	}

	if err := r.deleteUndesiredLinks(ctx, nodeLinks, linkResources); err != nil {
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, nil, err, "failed to delete undesired links")
	}

	readyCondition := metav1.Condition{
		Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
		Status:  metav1.ConditionUnknown,
		Reason:  "ReadyForUpsert",
		Message: "Validation complete, ready to upsert links",
	}
	meta.SetStatusCondition(&nodeLinks.Status.Conditions, readyCondition)

	if err := r.updateLastAttemptedLinks(ctx, clusterStateNodeLinks, nodeLinks, linkResources); err != nil {
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, nil, err, "failed to update last attempted links")
	}

	if err := r.upsertLinks(ctx, nodeLinks, sortedLinkResourceNames, linkResources, linkGraph); err != nil {
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, nil, err, "failed to upsert links")
	}

	// Update the status of the NodeLinks resource
	condition := metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "ReconcileSuccessful",
	}
	meta.SetStatusCondition(&nodeLinks.Status.Conditions, condition)
	return ctrl.Result{}, r.patchResource(ctx, clusterStateNodeLinks, nodeLinks)
}

func (r *NodeLinksReconciler) handleDeletion(ctx context.Context, clusterStateNodeLinks, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("action", "delete")
	logf.IntoContext(ctx, log)

	// This controller's finalizer is not present, nothing to do.
	if !controllerutil.ContainsFinalizer(nodeLinks, nodeLinksFinalizerName) {
		return ctrl.Result{}, nil
	}

	if err := r.deleteLinks(ctx, nodeLinks, nodeLinks.Status.LastAttemptedNetlinkLinks); err != nil {
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, nil, err, "failed to delete all links during NodeLinks deletion")
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(nodeLinks, nodeLinksFinalizerName)
	if err := r.patchResource(ctx, clusterStateNodeLinks, nodeLinks); err != nil {
		condition := &metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "FinalizerUpdateFailed",
			Message: fmt.Sprintf("Failed to update NodeLinks status with finalizer: %v", err),
		}
		return r.handleError(ctx, clusterStateNodeLinks, nodeLinks, condition, err, "failed to remove finalizer from NodeLinks resource")
	}

	return ctrl.Result{}, nil
}

func (r *NodeLinksReconciler) getLinkResources(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks) (map[string]*nodenetworkoperatorv1alpha1.Link, error) {
	links := make(map[string]*nodenetworkoperatorv1alpha1.Link, len(nodeLinks.Spec.MatchingLinks))
	for _, linkName := range pie.Unique(nodeLinks.Spec.MatchingLinks) {
		var link nodenetworkoperatorv1alpha1.Link
		if err := r.Get(ctx, client.ObjectKey{Name: linkName}, &link); err != nil {
			return nil, fmt.Errorf("failed to get Link %q: %w", linkName, err)
		}
		links[link.Name] = &link
	}

	return links, nil
}

// validateLinks validates the provided link resources. It returns an error if any validation fails. Validation cannot be corrected via reconciliation, so
// errors returned from this function should not trigger a requeue.
// Validation includes:
// * Ensuring that there are no duplicate link names in the spec
// * Ensuring that there are no self-referential links
func (r *NodeLinksReconciler) validateLinks(nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, linkResources map[string]*nodenetworkoperatorv1alpha1.Link) error {
	seenNetlinkLinkNames := make(map[string]string, len(linkResources))

	errs := make([]error, 0, len(linkResources))
	for resourceName, linkResource := range linkResources {
		// Validate the config as it relates to a specific link
		// Use an anonymous function to allow for easy error handling
		err := func() error {
			linkManager, err := r.getLinkManager(linkResource)
			if err != nil {
				return fmt.Errorf("failed to get link manager for Link %q: %w", resourceName, err)
			}

			// Skip this check for unmanaged links, as unmanaged links can refer to the same link name
			if linkManager.IsManaged() {
				// Verify that there are no duplicate link names in the spec
				if otherResourceName, ok := seenNetlinkLinkNames[linkResource.Spec.LinkName]; ok {
					return fmt.Errorf("link name %q is used by multiple Link resources: %q and %q", linkResource.Spec.LinkName, otherResourceName, resourceName)
				}
				seenNetlinkLinkNames[linkResource.Spec.LinkName] = resourceName
			}

			for _, dependencyLink := range linkManager.GetDependencies() {
				// Error if the dependency name is the same as the dependent name
				if dependencyLink.Name == resourceName {
					return fmt.Errorf("link %q has a self-referential dependency", resourceName)
				}
			}

			return nil
		}()

		// Update status condition for the link
		validConfigCondition := metav1.Condition{
			Type:   nodenetworkoperatorv1alpha1.NetlinkLinkConditionValidConfiguration,
			Status: metav1.ConditionTrue,
			Reason: "ConfigurationValidated",
		}
		readyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
			Status:  metav1.ConditionUnknown,
			Reason:  "ConfigurationValidated",
			Message: "Link configuration is valid but no actions have been taken yet",
		}

		if err != nil {
			validConfigCondition.Status = metav1.ConditionFalse
			validConfigCondition.Reason = "InvalidConfiguration"
			validConfigCondition.Message = fmt.Sprintf("Link configuration is invalid: %v", err)

			readyCondition.Status = metav1.ConditionFalse
			readyCondition.Reason = "InvalidConfiguration"
			readyCondition.Message = "Link cannot be ready because its configuration is invalid"

			errs = append(errs, err)
		}

		r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, validConfigCondition)
		r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCondition)
	}

	if err := errors.Join(errs...); err != nil {
		// Update resource-wide ready condition
		readyCondition := &metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidConfiguration",
			Message: "One or more Link configurations are invalid, see individual link conditions for details",
		}
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, *readyCondition)

		return fmt.Errorf("one or more Link configurations are invalid: %w", err)
	}

	return nil
}

// buildDesiredLinkGraph builds a graph of the desired state of links, based on the provided link resources.
// Vertices are link resource names. Edges show dependencies, that is an edge from A -> B means that A is a dependency of dependent B.
// This graph is a disjoint DAG (directional forest). Each disjoint subgraph can be topologically sorted/iterated over to get
// an order of operations for reconciling the links.
// If a dependency is not listed in the provided link resources, dependents are still added to the graph, but the dependency is not.
// Link configuration should be validated prior to calling this function.
func (r *NodeLinksReconciler) buildDesiredLinkGraph(links map[string]*nodenetworkoperatorv1alpha1.Link) (graph.Graph[string, string], error) {
	// Vertices are link resource names. Edges show dependencies, that is an edge from A -> B means that A is a dependency of dependent B.
	// Calling `PreventCycles()` has negative performance implications, but the impact should be minimal because graphs will only have a few vertices,
	// and significantly fewer edges. There shouldn't be any cycles created without a bug in the operator, but this is a safety measure, as some of
	// the operations perform rely on the graph being acyclic.
	g := graph.New(graph.StringHash, graph.Directed(), graph.Acyclic(), graph.PreventCycles())

	// Add all vertices first
	for linkName := range links {
		if err := g.AddVertex(linkName); err != nil {
			return nil, fmt.Errorf("failed to add vertex for Link %q: %w", linkName, err)
		}
	}

	// Add edges for dependencies
	// Because the slice is sorted, it is trivial to track which links have been visited. For a given link name `linkToCheck`, and the current
	// link `currentLink`, the `linkToCheck` link has been visited if and only if cmp.Less(linkToCheck, currentLink) is true.
	linkNames := pie.Keys(links)
	slices.Sort(linkNames)
	for _, dependentLinkName := range linkNames {
		link := links[dependentLinkName]
		linkManager, err := r.getLinkManager(link)
		if err != nil {
			return nil, fmt.Errorf("failed to get link manager for Link %q: %w", dependentLinkName, err)
		}

		for _, dependencyLink := range linkManager.GetDependencies() {
			// Store the link reference in the edge data
			setData := func(p *graph.EdgeProperties) {
				if p == nil {
					return
				}

				p.Data = dependencyLink
			}

			// Add an edge from the dependency to the dependent. The dependency link points to the dependent link.
			// This should be the correct order based on https://pkg.go.dev/github.com/dominikbraun/graph#readme-create-a-directed-acyclic-graph-of-integers
			if err := g.AddEdge(dependencyLink.Name, dependentLinkName, setData); err != nil {
				return nil, fmt.Errorf("failed to add edge from Link %q to dependent Link %q: %w", dependencyLink.Name, dependentLinkName, err)
			}
		}
	}

	return g, nil
}

// getTopologicallySortedLinkNames returns the list of link resource names in topological order. This can be used to
// determine the order in which links should be reconciled.
func (r *NodeLinksReconciler) getTopolgicallySortedLinkNames(g graph.Graph[string, string]) ([]string, error) {
	return graph.TopologicalSort(g)
}

// updateDependentsMissingDependencies sets down links that have invalid dependencies (non-optional dependencies that are missing).
// sortedLinkResourceNames should be the names of _all_ links in the graph, in topological order. This allows for a single pass over the links.
// An error is only returned if reconciliation should not proceed. Other errors are logged and the status is updated.
func (r *NodeLinksReconciler) updateDependentsMissingDependencies(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, sortedLinkResourceNames []string, linkResources map[string]*nodenetworkoperatorv1alpha1.Link) error {
	allDependenciesExist := true

	errs := pie.Map(sortedLinkResourceNames, func(linkResourceName string) error {
		linkResource := linkResources[linkResourceName]

		linkManager, err := r.getLinkManager(linkResource)
		if err != nil {
			// This should never happen because the links have already been validated
			return fmt.Errorf("failed to get link manager for Link %q: %w", linkResourceName, err)
		}

		dependencies := linkManager.GetDependencies()

		requiredLinkReferences := pie.Filter(dependencies, func(dependency nodenetworkoperatorv1alpha1.LinkReference) bool {
			return !dependency.Optional
		})

		missingLinkReferences := pie.Filter(requiredLinkReferences, func(dependency nodenetworkoperatorv1alpha1.LinkReference) bool {
			if _, ok := linkResources[dependency.Name]; ok {
				// Dependency exists, nothing to do
				return false
			}

			return true
		})

		missingRequiredDependencyNames := pie.Map(missingLinkReferences, func(dependency nodenetworkoperatorv1alpha1.LinkReference) string {
			return dependency.Name
		})

		// Bring links down and update the status if there are missing required dependencies
		if len(missingRequiredDependencyNames) > 0 {
			allDependenciesExist = false

			// This point should never be hit for unmanaged links, because unmanaged links cannot have dependencies.
			// However, this is a safety measure in case of a bug in the operator.
			if linkManager.IsManaged() {
				if err := r.bringDownLink(ctx, nodeLinks, linkResource.Spec.LinkName); err != nil {
					return fmt.Errorf("failed to bring down link %q due to missing dependencies: %w", linkResource.Spec.LinkName, err)
				}
			} else {
				logf.FromContext(ctx).Error(nil, "unmanaged link has missing required dependencies, this should never happen", "linkResource", linkResource.Name, "missingDependencies", missingRequiredDependencyNames)
			}

			readyCondition := metav1.Condition{
				Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "MissingDependencyLinks",
				Message: "Link is missing dependency links",
			}
			r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCondition)

			dependencyCondition := metav1.Condition{
				Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionDependencyLinksAvailable,
				Status:  metav1.ConditionFalse,
				Reason:  "MissingDependencyLinks",
				Message: fmt.Sprintf("Link is missing required dependency links: %v", missingRequiredDependencyNames),
			}
			r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, dependencyCondition)

			return nil
		}

		readyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
			Status:  metav1.ConditionUnknown,
			Reason:  "AllDependencyLinksAvailable",
			Message: "All dependency links are available but no actions have been taken yet",
		}
		r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCondition)

		requiredDependencyNames := pie.Map(requiredLinkReferences, func(dependency nodenetworkoperatorv1alpha1.LinkReference) string {
			return dependency.Name
		})

		dependencyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionDependencyLinksAvailable,
			Status:  metav1.ConditionTrue,
			Reason:  "AllDependencyLinksAvailable",
			Message: fmt.Sprintf("Required dependency links are available: %v", requiredDependencyNames),
		}
		r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, dependencyCondition)
		return nil
	})

	if err := errors.Join(errs...); err != nil {
		readyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "DependencyCheckFailed",
			Message: "Failed to check links for missing dependencies",
		}
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, readyCondition)

		return fmt.Errorf("failed to update links with missing dependencies: %w", err)
	}

	if !allDependenciesExist {
		// Update resource-wide ready condition
		readyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MissingDependencyLinks",
			Message: "One or more links are missing required dependency links, see individual link conditions for details",
		}
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, readyCondition)

		return nil
	}

	return nil
}

// upsertLinks reconciles the desired state of links on the node. This includes bringing up links that are not in the desired state,
// updating links that are not in the desired state, and bringing down links that depend on links that are not in the desired state.
// sortedLinkResourceNames should be the names of _all_ links in the graph, in topological order. This allows for a single pass over the links.
// An error is only returned if reconciliation should not proceed. Other errors are logged and the status is updated.
func (r *NodeLinksReconciler) upsertLinks(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, sortedLinkResourceNames []string, linkResources map[string]*nodenetworkoperatorv1alpha1.Link, linkGraph graph.Graph[string, string]) error {
	errs := pie.Map(sortedLinkResourceNames, func(linkResourceName string) error {
		if err := r.upsertLink(ctx, nodeLinks, linkResourceName, linkResources, linkGraph); err != nil {
			logf.FromContext(ctx).Error(err, "failed to upsert link", "linkResource", linkResourceName)
			return err
		}
		return nil
	})

	if err := errors.Join(errs...); err != nil {
		readyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UpsertFailed",
			Message: "Failed to upsert one or more links, see individual link conditions for details",
		}
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, readyCondition)

		return err
	}

	return nil
}

// upsertLink reconciles the desired state of a single link on the node. Link configuration is handled by the link manager.
func (r *NodeLinksReconciler) upsertLink(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, linkResourceName string, linkResources map[string]*nodenetworkoperatorv1alpha1.Link, linkGraph graph.Graph[string, string]) error {
	linkResource := linkResources[linkResourceName]
	log := logf.FromContext(ctx).WithValues("linkResource", linkResource.Name, "netlinkLinkName", linkResource.Spec.LinkName)

	linkManager, err := r.getLinkManager(linkResource)
	if err != nil {
		// This should never happen because the links have already been validated
		return fmt.Errorf("failed to get link manager for Link %q: %w", linkResource.Name, err)
	}

	linkConditions, ok := nodeLinks.Status.NetlinkLinkConditions[linkResource.Spec.LinkName]
	if !ok {
		log.V(1).Info("no conditions found for link, assuming not ready", "link", linkResource.Spec.LinkName)

		readyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
			Status:  metav1.ConditionUnknown,
			Reason:  "MaybeMissingDependencyLinks",
			Message: "Link conditions not found, assuming link is not ready. This is likely a bug.",
		}
		r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCondition)

		return fmt.Errorf("no conditions found for link %q, assuming not ready", linkResource.Spec.LinkName)
	}
	if !meta.IsStatusConditionTrue(linkConditions, nodenetworkoperatorv1alpha1.NetlinkLinkConditionDependencyLinksAvailable) {
		return fmt.Errorf("link %q has missing dependencies", linkResource.Spec.LinkName)
	}

	log.V(1).Info("checking if upsert is needed for link")
	upsertNeeded, err := linkManager.IsUpsertNeeded(ctx, nodeLinks, linkResources)
	if err != nil {
		readyCOndition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UpsertCheckFailed",
			Message: fmt.Sprintf("Failed to determine if upsert is needed: %v", err),
		}
		r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCOndition)

		return fmt.Errorf("failed to determine if upsert is needed for Link %q: %w", linkResource.Name, err)
	}

	if upsertNeeded {
		log.V(1).Info("upsert needed for link")
		// Bring down any links in the dependent tree with SetDownOnDependencyChainUpdate=true
		if ok {
			if err := r.bringDownDependents(ctx, nodeLinks, linkResourceName, linkResources, linkGraph); err != nil {
				readyCondition := metav1.Condition{
					Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
					Status:  metav1.ConditionFalse,
					Reason:  "DependentLinkSetDownFailed",
					Message: fmt.Sprintf("Failed to bring down all dependent links for chain update: %v", err),
				}
				r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCondition)

				return fmt.Errorf("failed to bring down dependent links of Link %q: %w", linkResource.Name, err)
			}
		}

		if err := linkManager.Upsert(ctx, nodeLinks, linkResources); err != nil {
			readyCondition := metav1.Condition{
				Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "UpsertFailed",
				Message: fmt.Sprintf("Failed to upsert link: %v", err),
			}
			r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCondition)

			return fmt.Errorf("failed to upsert Link %q: %w", linkResource.Name, err)
		}

		// Dependency links should not be brought back up, as their desired state may depend on changes to other links.
		// They will be brought back up when their own reconciliation is reached in the topological order.
	} else {
		log.V(1).Info("upsert not needed for link")
	}

	readyCondition := metav1.Condition{
		Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "ReconcileSuccessful",
		Message: "Link is in the desired state",
	}
	r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, readyCondition)

	operationalStateCondition := metav1.Condition{
		Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionOperationallyUp,
		Status:  metav1.ConditionTrue,
		Reason:  "LinkUp",
		Message: "Link is operationally up",
	}
	r.setLinkCondition(nodeLinks, linkResource.Spec.LinkName, operationalStateCondition)

	return nil
}

// bringDownDependents brings down all links in the dependent tree that have SetDownOnDependencyChainUpdate=true.
// An error is only returned if reconciliation should not proceed. Other errors are logged and the status is updated.
func (r *NodeLinksReconciler) bringDownDependents(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, linkResourceName string, linkResources map[string]*nodenetworkoperatorv1alpha1.Link, linkGraph graph.Graph[string, string]) error {
	linkResource := linkResources[linkResourceName]
	return graphsearch.BFSWithEdge(linkGraph, linkResource.Name, func(dependentResourceName string, edge *graph.Edge[string]) error {
		// Skip the root node
		if dependentResourceName == linkResource.Name {
			return nil
		}

		if edge == nil {
			// This should never happen because the graph is built with edges for all dependencies
			return fmt.Errorf("edge is nil for dependency from Link %q to dependent Link %q", linkResource.Name, dependentResourceName)
		}

		// Get the link reference from the edge data
		dependencyLinkRef, ok := edge.Properties.Data.(nodenetworkoperatorv1alpha1.LinkReference)
		if !ok {
			// This should never happen because the graph is built with edges for all dependencies
			return fmt.Errorf("failed to get link reference from edge data for dependency from Link %q to dependent Link %q", linkResource.Name, dependentResourceName)
		}

		// Skip dependents that don't have SetDownOnDependencyChainUpdate=true
		if !dependencyLinkRef.SetDownOnDependencyChainUpdate {
			return nil
		}

		// Set the dependent link down
		dependentLinkResource, ok := linkResources[dependentResourceName]
		if !ok {
			// This should never happen because the links have already been validated
			return fmt.Errorf("failed to get link resource for Link %q", dependentResourceName)
		}

		linkManager, err := r.getLinkManager(dependentLinkResource)
		if err != nil {
			// This should never happen because the links have already been validated
			return fmt.Errorf("failed to get link manager for Link %q: %w", dependentResourceName, err)
		}

		// This should never happen because unmanaged links cannot have dependencies, but this is a safety measure in case of a bug in the operator.
		if !linkManager.IsManaged() {
			logf.FromContext(ctx).Error(nil, "unmanaged link has SetDownOnDependencyChainUpdate=true, this should never happen", "linkResource", dependentLinkResource.Name)
			return fmt.Errorf("link %q has SetDownOnDependencyChainUpdate=true on a reference but is unmanaged, this should never happen", dependentLinkResource.Name)
		}

		if err := r.bringDownLink(ctx, nodeLinks, dependentLinkResource.Spec.LinkName); err != nil {
			return fmt.Errorf("failed to bring down dependent link %q: %w", dependentLinkResource.Spec.LinkName, err)
		}

		return nil
	})
}

// deleteUndesiredLinks removes links that are in the NodeLinks status but not in the desired state.
func (r *NodeLinksReconciler) deleteUndesiredLinks(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) error {
	return r.deleteLinks(ctx, nodeLinks, r.getUndesiredLinks(nodeLinks, links))
}

func (r *NodeLinksReconciler) deleteLinks(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, linkNamesToRemove []string) error {
	// Bring down the undesired links first, preventing a corner case where a dependency link is removed while a dependent link is forwarding traffic.
	errs := pie.Map(linkNamesToRemove, func(linkName string) error {
		if err := r.bringDownLink(ctx, nodeLinks, linkName); err != nil {
			condition := metav1.Condition{
				Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "LinkSetDownFailed",
				Message: fmt.Sprintf("Failed to bring down link before deletion: %v", err),
			}
			r.setLinkCondition(nodeLinks, linkName, condition)

			return fmt.Errorf("failed to bring down undesired link %q: %w", linkName, err)
		}

		readyCondition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "LinkBeingDeleted",
			Message: "Link is being deleted",
		}
		r.setLinkCondition(nodeLinks, linkName, readyCondition)

		return nil
	})
	if err := errors.Join(errs...); err != nil {
		condition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "LinkSetDownFailed",
			Message: "Failed to bring down one or more undesired links before deletion, see individual link conditions for details",
		}
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, condition)

		return fmt.Errorf("failed to bring down all undesired links: %w", err)
	}

	// Delete the undesired links
	errs = pie.Map(linkNamesToRemove, func(linkName string) error {
		if err := r.deleteLink(ctx, linkName); err != nil {
			condition := metav1.Condition{
				Type:    nodenetworkoperatorv1alpha1.NetlinkLinkConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "LinkDeletionFailed",
				Message: fmt.Sprintf("Failed to delete link: %v", err),
			}
			r.setLinkCondition(nodeLinks, linkName, condition)

			return fmt.Errorf("failed to delete undesired links: %w", err)
		}

		// Remove the link conditions from the status
		delete(nodeLinks.Status.NetlinkLinkConditions, linkName)

		return nil
	})
	if err := errors.Join(errs...); err != nil {
		condition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "LinkDeletionFailed",
			Message: "Failed to delete one or more undesired links, see individual link conditions for details",
		}
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, condition)

		return fmt.Errorf("failed to delete all undesired links: %w", err)
	}

	return nil
}

func (r *NodeLinksReconciler) updateLastAttemptedLinks(ctx context.Context, clusterStateNodeLinks, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) error {
	desiredNetlinkLinkNames := make([]string, 0, len(links))
	for _, link := range links {
		linkManager, err := r.getLinkManager(link)
		if err != nil {
			return fmt.Errorf("failed to get link manager for Link %q: %w", link.Name, err)
		}

		// This is critically important: only managed links should be tracked in LastAttemptedNetlinkLinks.
		// If unmanaged links are included here, then they will be subject to updates and removals via the operator.
		if linkManager.IsManaged() {
			desiredNetlinkLinkNames = append(desiredNetlinkLinkNames, link.Spec.LinkName)
		}
	}
	nodeLinks.Status.LastAttemptedNetlinkLinks = desiredNetlinkLinkNames

	if err := r.patchResource(ctx, clusterStateNodeLinks, nodeLinks); err != nil {
		condition := metav1.Condition{
			Type:    nodenetworkoperatorv1alpha1.NodeLinkConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "StatusUpdateFailed",
			Message: fmt.Sprintf("Failed to update NodeLinks status with last attempted links: %v", err),
		}
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, condition)

		return fmt.Errorf("failed to update NodeLinks status with last attempted links: %w", err)
	}

	return nil
}

// getUndesiredLinks returns the list of managed netlink link names that are deployed on the node but are not in the desired state.
func (r *NodeLinksReconciler) getUndesiredLinks(nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, links map[string]*nodenetworkoperatorv1alpha1.Link) []string {
	desiredNetlinkLinkNames := make([]string, 0, len(links))
	for _, link := range links {
		desiredNetlinkLinkNames = append(desiredNetlinkLinkNames, link.Spec.LinkName)
	}

	_, linkNamesToRemove := pie.Diff(nodeLinks.Status.LastAttemptedNetlinkLinks, desiredNetlinkLinkNames)
	return linkNamesToRemove
}

// bringDownLink brings down the provided netlink link. This is used when removing links from the node.
// If a link is not found, it is ignored.
func (r *NodeLinksReconciler) bringDownLink(ctx context.Context, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, linkName string) error {
	log := logf.FromContext(ctx).WithValues("link", linkName)

	log.V(1).Info("bringing down link")

	link, err := netlink.LinkByName(linkName)
	if err != nil {
		if links.IsLinkNotFoundError(err) {
			return nil
		}
		return fmt.Errorf("failed to get netlink link %q: %w", linkName, err)
	}

	if err := netlink.LinkSetDown(link); err != nil {
		return fmt.Errorf("failed to set netlink link %q down: %w", linkName, err)
	}
	log.V(1).Info("link brought down")

	operationalCondition := metav1.Condition{
		Type:   nodenetworkoperatorv1alpha1.NetlinkLinkConditionOperationallyUp,
		Status: metav1.ConditionFalse,
		Reason: "LinkBroughtDown",
	}
	r.setLinkCondition(nodeLinks, linkName, operationalCondition)

	return nil
}

// deleteLink deletes the provided netlink link. This is used when removing links from the node.
// If a link is not found, it is ignored.
func (r *NodeLinksReconciler) deleteLink(ctx context.Context, linkName string) error {
	log := logf.FromContext(ctx).WithValues("link", linkName)

	log.V(1).Info("deleting link")

	link, err := netlink.LinkByName(linkName)
	if err != nil {
		if links.IsLinkNotFoundError(err) {
			return nil
		}
		return fmt.Errorf("failed to get netlink link %q: %w", linkName, err)
	}

	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("failed to delete netlink link %q: %w", linkName, err)
	}
	log.V(1).Info("link deleted")

	return nil
}

func (r *NodeLinksReconciler) getLinkManager(link *nodenetworkoperatorv1alpha1.Link) (links.Manager, error) {
	switch {
	case link.Spec.Bridge != nil:
		return links.NewBridgeManager(link), nil
	case link.Spec.VXLAN != nil:
		return links.NewVXLANManager(link), nil
	case link.Spec.Unmanaged != nil:
		return links.NewUnmanagedManager(), nil
	default:
		return nil, fmt.Errorf("link uses not-yet-supported link type (bug)")
	}
}

// handleError handles errors during reconciliation, updating the Link status with the error condition.
// If an error occurs while updating the conditions, it logs the error and fires an event with the error message.
func (r *NodeLinksReconciler) handleError(ctx context.Context, clusterStateNodeLinks, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, condition *metav1.Condition,
	err error, msg string, args ...any) (ctrl.Result, error) {
	logf.FromContext(ctx).Error(err, msg, args...)

	if condition != nil {
		meta.SetStatusCondition(&nodeLinks.Status.Conditions, *condition)
	}

	return ctrl.Result{}, errors.Join(err, r.patchResource(ctx, clusterStateNodeLinks, nodeLinks))
}

// setLinkCondition sets a condition for a specific link in the NodeLinks status.
func (r *NodeLinksReconciler) setLinkCondition(nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks, netlinkLinkName string, condition metav1.Condition) {
	linkConditions, ok := nodeLinks.Status.NetlinkLinkConditions[netlinkLinkName]
	if !ok {
		linkConditions = make(nodenetworkoperatorv1alpha1.NodeLinksStatusConditions, 0, 1)
	}
	castedLinkConditions := []metav1.Condition(linkConditions)
	meta.SetStatusCondition(&castedLinkConditions, condition)

	if nodeLinks.Status.NetlinkLinkConditions == nil {
		nodeLinks.Status.NetlinkLinkConditions = make(map[string]nodenetworkoperatorv1alpha1.NodeLinksStatusConditions, 1)
	}

	nodeLinks.Status.NetlinkLinkConditions[netlinkLinkName] = castedLinkConditions
}

// This cannot be extracted to a common function because it needs to be aware of the type of the resource being patched.
// There is not a way to write this generically without several leaky assertions due to Go's lack of covariant support
// (https://github.com/golang/go/issues/7512). Instead, this is copy/pasted for each reconciler that needs it, with only
// the function signature (resource type, var names) differing.

// patchResource updates the resource. If only a status update is needed, it patches the status only. This handles errors upon
// update, and the result can be directly returned from the Reconcile function.
// The clusterStateLink is the current state of the resource as stored in the cluster, and is used for computing patch diffs.
// It will be updated with the new state after a successful patch operation.
func (r *NodeLinksReconciler) patchResource(ctx context.Context, clusterStateNodeLinks, nodeLinks *nodenetworkoperatorv1alpha1.NodeLinks) error {
	log := logf.FromContext(ctx)

	// Determine whether the entire resource needs a patch or just the status
	// Ignore status changes, these always need to be applied and will always differ
	newStatus := nodeLinks.Status.DeepCopy()
	nodeLinks.Status = clusterStateNodeLinks.Status
	onlyStatusPatchIsNeeded := equality.Semantic.DeepEqual(clusterStateNodeLinks, nodeLinks)
	nodeLinks.Status = *newStatus

	var err error
	if onlyStatusPatchIsNeeded {
		log = log.WithValues("patchType", "status")
		log.V(1).Info(fmt.Sprintf("updating %T status resource only", clusterStateNodeLinks))
		logf.IntoContext(ctx, log)

		err = r.Status().Patch(ctx, nodeLinks, client.MergeFrom(clusterStateNodeLinks))
	} else {
		log = log.WithValues("patchType", "full")
		log.V(1).Info(fmt.Sprintf("updating full %T resource", clusterStateNodeLinks))
		logf.IntoContext(ctx, log)

		err = r.Patch(ctx, nodeLinks, client.MergeFrom(clusterStateNodeLinks))
	}

	if err != nil {
		log.Error(err, fmt.Sprintf("failed to patch %T status", clusterStateNodeLinks))
		r.recorder.Eventf(nodeLinks, "Warning", "StatusUpdateFailed", "Failed to update %T status: %v", clusterStateNodeLinks, err)
		return fmt.Errorf("failed to patch %T status: %w", clusterStateNodeLinks, err)
	}

	// Update the clusterStateNodeLinks to reflect the new object state
	*clusterStateNodeLinks = *nodeLinks.DeepCopy()

	log.V(1).Info(fmt.Sprintf("%T status updated", clusterStateNodeLinks))
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeLinksReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&nodenetworkoperatorv1alpha1.NodeLinks{},
			builder.WithPredicates(
				predicate.GenerationChangedPredicate{},
				// Filter out events that are not for the current node
				predicate.NewPredicateFuncs(func(object client.Object) bool {
					return object.GetName() == r.nodeName
				}),
			),
		).
		// Watch Link resources and enqueue matched NodeLinks resource for reconciliation
		Watches(
			&nodenetworkoperatorv1alpha1.Link{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				link, ok := o.(*nodenetworkoperatorv1alpha1.Link)
				if !ok {
					logf.FromContext(ctx).Error(nil, "expected Link resource in watch handler")
					return nil
				}

				// Skip resources that do not match this node
				if !slices.Contains(link.Status.MatchedNodes, r.nodeName) {
					return nil
				}

				return []reconcile.Request{{
					NamespacedName: types.NamespacedName{
						Name: r.nodeName,
					},
				}}
			}),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Named("nodelinks").
		WithOptions(controller.TypedOptions[reconcile.Request]{
			// Ignore leader election. This controller should run once per node.
			NeedLeaderElection: ptr.To(false),
		}).
		Complete(r)
}
