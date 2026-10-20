package server

import (
	"context"
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
		if record.Product != crewProfileID || record.OwnerID != sanitizeUserIDForPath(ownerID) ||
			(record.ProjectID != strings.TrimSpace(projectID) && record.Folder != strings.TrimSpace(projectID)) {
			continue
		}
		root := projectStorageRoot(record)

		if !userAllowedProduct(currentProjectCallerClaims(ctx, callerID), crewProfileID) {
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
