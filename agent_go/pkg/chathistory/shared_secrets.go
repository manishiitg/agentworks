package chathistory

import "sync/atomic"

// The hosting server may preserve a moved project's original private-state
// namespace. Standalone stores retain exact normalized paths. This only routes
// state; callers still perform their existing access checks.
var workflowStateRootResolver atomic.Value

func SetWorkflowStateRootResolver(resolve func(string) string) {
	if resolve != nil {
		workflowStateRootResolver.Store(resolve)
	}
}

// SharedWorkflowSecretsUserID is the reserved pseudo-user under which
// workflow-scoped secrets are stored so that every user with access to the
// workflow resolves the same values:
//
//	_users/_shared/workflow_secrets/<sha256(workflowPath)>.json
//
// It deliberately reuses the per-user layout instead of introducing a new
// top-level folder: every guard that already keeps _users/ out of an agent's
// reach (root-listing filter, DB query rejection, folder-guard read
// allowlists, the git-push secret-file check on "workflow_secrets/") covers
// this location by construction, with nothing new to keep in sync. No real
// user ID can collide with it -- directory IDs are sha256 hex, OAuth/bot IDs
// are lowercase slugs, and neither is ever underscore-prefixed.
//
// Values under this ID are encrypted with the workflow path as the AES-GCM
// additional data (see cmd/server sharedWorkflowSecretAAD), not a user ID,
// so the store's userID parameter is only a storage prefix here.
const SharedWorkflowSecretsUserID = "_shared"

// NormalizeWorkflowSecretPath exposes the store's canonical form of a workflow
// path so callers that bind a value to the path (as AES-GCM additional data)
// use exactly the string the store keys on.
func NormalizeWorkflowSecretPath(workflowPath string) (string, error) {
	return normalizeWorkflowSecretPath(workflowPath)
}
