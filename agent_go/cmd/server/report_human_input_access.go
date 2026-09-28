package server

import (
	"context"
	"net/http"
)

// reportHumanInputAccess is what a caller may do with the decisions stored
// for one workspace (its db/db.sqlite): read them, or answer, dismiss and
// create them. Workflows follow their access record (readers read; owners
// and editors write). A Crew's decisions, including the suggestions its
// other users leave, belong to its owner alone: one Crew user never sees
// another's suggestions.
//
// Before this the decisions routes checked nothing but a signed-in user, so
// anyone could read, answer or dismiss another workflow's decisions, some of
// which authorize changes (found building Crew suggestions, 2026-09-28).
func reportHumanInputAccess(ctx context.Context, workspacePath string) (read, write bool) {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID == "" {
		return false, false
	}
	if userAccessForClaims(claims).Admin {
		return true, true
	}
	if ref, ok := resolveCrewPath(ctx, claims.UserID, workspacePath); ok {
		owner := crewAccessFor(claims, ref) == crewAccessOwner
		return owner, owner
	}
	switch level, _ := workflowAccessForWorkspacePath(ctx, claims, workspacePath); level {
	case WorkflowAccessOwner, WorkflowAccessWrite:
		return true, true
	case WorkflowAccessRead:
		return true, false
	}
	return false, false
}

// requireReportHumanInputAccess guards a decisions route by the
// workspace_path it names (query string or JSON body).
func requireReportHumanInputAccess(write bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}
		read, canWrite := reportHumanInputAccess(r.Context(), requestWorkflowWorkspacePath(r))
		if !read || (write && !canWrite) {
			http.Error(w, "you do not have access to this workspace's decisions", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
