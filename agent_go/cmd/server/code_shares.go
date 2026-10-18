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
	codeRoleRun    codeRole = "run"
	codeRoleEditor codeRole = "editor" // minimum used by existing mutation gates
	codeRoleOwner  codeRole = "owner"
)

func (r codeRole) atLeast(other codeRole) bool { return r == codeRoleOwner }

func (r codeRole) workflowAccess() WorkflowAccessLevel {
	if r == codeRoleOwner {
		return WorkflowAccessOwner
	}
	if r == codeRoleRun {
		return WorkflowAccessRead
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
	records, err := defaultProjectOwners().All()
	if err != nil {
		return codeRoleNone
	}
	for _, record := range records {
		if record.Product != "code" || record.OwnerID != sanitizeUserIDForPath(ownerID) ||
			(record.ProjectID != strings.TrimSpace(projectID) && record.Folder != strings.TrimSpace(projectID)) {
			continue
		}
		root := projectStorageRoot(record)
		if codeProjectMode(ctx, root) == "local" {
			return codeRoleNone
		}
		if !userAllowedProduct(currentProjectCallerClaims(ctx, callerID), "code") {
			return codeRoleNone
		}
		if crewIsCoOwner(root, callerID) {
			return codeRoleOwner
		}
		if crewSharedWith(root, callerID) {
			return codeRoleRun
		}
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
	writeAgentProfileError(w, http.StatusGone, "Use Share on this project's dashboard or share action=set over MCP. This legacy sharing endpoint is retired.")
}

func (api *StreamingAPI) handlePutCodeShares(w http.ResponseWriter, r *http.Request) {
	api.handleGetCodeShares(w, r)
}
