package server

import (
	"context"
	"net/http"
	"strings"
)

// Code is private to its owner. Legacy config/code-shares.json is retained
// as inert data and remains protected from workspace-proxy access.
type codeRole string

const (
	codeRoleNone   codeRole = ""
	codeRoleEditor codeRole = "editor" // minimum used by existing mutation gates
	codeRoleOwner  codeRole = "owner"
)

func (r codeRole) atLeast(other codeRole) bool { return r == codeRoleOwner }

func (r codeRole) workflowAccess() WorkflowAccessLevel {
	if r == codeRoleOwner {
		return WorkflowAccessOwner
	}
	return WorkflowAccessNone
}

func codeSharesFilePath() string { return "config/code-shares.json" }

// Every normal Code surface uses this check; account administrators and
// reviewers use separately audited, read-only inspection endpoints.
func codeRoleFor(ctx context.Context, callerID, ownerID, projectID string) codeRole {
	if strings.TrimSpace(callerID) != "" && strings.TrimSpace(ownerID) != "" &&
		sanitizeUserIDForPath(callerID) == sanitizeUserIDForPath(ownerID) {
		return codeRoleOwner
	}
	return codeRoleNone
}

// Compatibility tombstones: old clients cannot recreate a share or discover
// the owner of an arbitrary project through these endpoints.
func (api *StreamingAPI) handleGetCodeShares(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeAgentProfileError(w, http.StatusGone, "Code workspaces are private to their owner; sharing is no longer available")
}

func (api *StreamingAPI) handlePutCodeShares(w http.ResponseWriter, r *http.Request) {
	api.handleGetCodeShares(w, r)
}
