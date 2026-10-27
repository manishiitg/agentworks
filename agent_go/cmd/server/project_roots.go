package server

import (
	"context"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Project roots (a workflow or Crew folder) for paths and sessions, who may manage a project's apps, and
// owner-scoped MCP server names.

// cleanProjectRoot normalizes a workflow or Crew root. It returns "" for
// any path that is not one.
func cleanProjectRoot(raw string) string {
	root := strings.Trim(strings.TrimSpace(raw), "/")
	if root == "" || strings.ContainsAny(root, "\\\x00") || path.Clean(root) != root {
		return ""
	}
	segments := strings.Split(root, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}

	switch {
	case len(segments) == 2 && segments[0] == "Workflow" && !strings.HasPrefix(segments[1], "."):
		return root
	case len(segments) == 2 && segments[0] == crewSharedRootName:

		return root
	}
	return ""
}

// projectRootOf returns the workflow or Crew root a path lies in (a run
// folder or file inside it included), or "".
func projectRootOf(raw string) string {
	segments := strings.Split(strings.Trim(strings.TrimSpace(raw), "/"), "/")
	switch {
	case len(segments) >= 2 && (segments[0] == "Workflow" || segments[0] == crewSharedRootName):
		return cleanProjectRoot(strings.Join(segments[:2], "/"))
	}
	return ""
}

// projectRootForSession is the workflow, Relay, Crew or Code a session works in, taken only from data the server
// recorded for the session (never from anything the client sent); "" when it has none (a chat outside any place).
func (api *StreamingAPI) projectRootForSession(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if pin, ok, _ := codeSessionPinFor(sessionID); ok {
		if root := projectRootOf(pin.CodeRoot); root != "" {
			return root
		}
	}
	if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
		for _, candidate := range []string{cfg.WorkflowPath, cfg.WorkingDir} {
			if root := projectRootOf(candidate); root != "" {
				return root
			}
		}
	}
	if api != nil {
		api.sessionWorkspaceMu.RLock()
		folder := api.sessionWorkspaceFolders[sessionID]
		api.sessionWorkspaceMu.RUnlock()
		if root := projectRootOf(folder); root != "" {
			return root
		}
	}
	return ""
}

func (api *StreamingAPI) mcpProjectRoot(ctx context.Context, sessionID string) string {
	if root, _ := ctx.Value(externalCrewMCPPlaceKey{}).(string); root != "" {
		return root
	}
	return api.projectRootForSession(sessionID)
}

// canManageProjectApps reports whether userID may add connections to root:
// they must be able to edit that workflow, or own that Crew or Code. It is
// checked on add and again on every use, so losing edit access stops their
// connection there at once.
func canManageProjectApps(ctx context.Context, userID, root string) bool {
	root = cleanProjectRoot(root)
	if root == "" || strings.TrimSpace(userID) == "" {
		return false
	}
	claims := userClaimsForDirectoryID(userID)
	if claims == nil {
		return false
	}
	if strings.HasPrefix(root, "Workflow/") {
		level, _ := workflowAccessForWorkspacePath(ctx, claims, root)
		return level == WorkflowAccessOwner || level == WorkflowAccessWrite
	}
	ref, ok := resolveCrewPath(ctx, userID, root)
	return ok && crewAccessFor(claims, ref) == crewAccessOwner
}

// userClaimsForDirectoryID builds the claims access checks need (access
// lists name usernames and emails) for a user who is not the caller. A
// disabled account has none.
func userClaimsForDirectoryID(userID string) *UserClaims {
	claims := &UserClaims{UserID: userID, Username: userID}
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		if rec := dir.byID(userID); rec != nil {
			if rec.Disabled {
				return nil
			}
			claims.Username, claims.Email = rec.Username, rec.Email
		}
	}
	return claims
}

// isOwnerScopedMCPName reports whether name has the personal-server
// shape (u<32 hex>__<name>), whoever it belongs to.
func isOwnerScopedMCPName(name string) bool {
	if len(name) < 36 || name[0] != 'u' || name[33:35] != "__" {
		return false
	}
	for _, c := range name[1:33] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// plainMCPName is the name a connection has without its store prefix
// (u<32 hex>__<name> -> <name>).
func plainMCPName(internal string) string {
	if isOwnerScopedMCPName(internal) {
		return internal[35:]
	}
	return internal
}
