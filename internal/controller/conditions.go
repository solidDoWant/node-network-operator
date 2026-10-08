package controller

// Condition reasons used by more than one reconciler path.
const (
	reasonFinalizerUpdateFailed  = "FinalizerUpdateFailed"
	reasonReconcileSuccessful    = "ReconcileSuccessful"
	reasonInvalidConfiguration   = "InvalidConfiguration"
	reasonMissingDependencyLinks = "MissingDependencyLinks"
)
