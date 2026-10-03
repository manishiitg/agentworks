package server

import (
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// authorizeWorkflowFolderGrants applies the Work folder rule to a workflow's
// folder_access: a non-admin may only attach folders inside the roots an
// administrator assigned to them, exactly as when adding a Work folder.
// Owning a workflow is not enough, or a member could grant its agents the
// service account's own folders or another person's home. Grants that are
// already on the workflow and unchanged are kept as they are.
func authorizeWorkflowFolderGrants(r *http.Request, previous, next []workflowtypes.WorkflowFolderGrant) error {
	return checkWorkflowFolderGrants(previous, next, workFolderRootsForClaims(r.Context(), GetUserFromContext(r.Context())), currentUserIsAdmin(r))
}

func checkWorkflowFolderGrants(previous, next []workflowtypes.WorkflowFolderGrant, roots []string, isAdmin bool) error {
	unchanged := make(map[string]bool, len(previous))
	for _, grant := range previous {
		unchanged[filepath.Clean(grant.Path)+"\x00"+grant.Access] = true
	}
	for i, grant := range next {
		if unchanged[filepath.Clean(grant.Path)+"\x00"+grant.Access] {
			continue
		}
		if _, err := workproduct.ValidateGrantForUser(grant.Path, grant.Alias, grant.Access, roots, isAdmin); err != nil {
			return fmt.Errorf("folder_access[%d] %q: %w", i, grant.Path, err)
		}
	}
	return nil
}
