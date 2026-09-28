package server

import (
	"context"
	"net/http"
	"path"
	"strings"
)

// workspaceProxyPolicy decides, per workspace path a proxied request names
// (URL, query or body field), whether the caller may reach it. It complements
// the cross-user rule (workspaceProxyPathIsOtherUser):
//   - the docs root, config/ and _system/ are admin-only;
//   - inside Workflow/<id> reads need workflow read access, writes need write
//     access, and workflow.json (its access record) only its owners;
//   - bulk routes (export, import, search, glob, folder copy) never run on the
//     whole workspace for non-admins.
type workspaceProxyPolicy struct {
	ctx    context.Context
	claims *UserClaims
	own    string
	admin  bool
	write  bool
	bulk   bool
}

// Routes that POST a read (a SQL query, a table listing).
var workspaceProxyReadOnlyPostRoutes = map[string]bool{
	"api/query": true, "api/db/tables": true,
}

// Routes that act on a whole subtree at once.
var workspaceProxyBulkRoutes = map[string]bool{
	"api/workspace/export": true, "api/workspace/import": true,
	"api/search": true, "api/glob": true, "api/folders/copy": true,
}

// Body/query keys naming a path that is only read, even on a write route.
var workspaceProxySourceKeys = map[string]bool{"source": true, "source_path": true}

func newWorkspaceProxyPolicy(r *http.Request, callerID string) workspaceProxyPolicy {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		claims = &UserClaims{UserID: callerID}
	}
	rel := strings.Trim(workspaceProxyRelativePath(r), "/")
	write := false
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		write = !workspaceProxyReadOnlyPostRoutes[rel]
	}
	return workspaceProxyPolicy{
		ctx:    r.Context(),
		claims: claims,
		own:    sanitizeUserIDForPath(callerID),
		admin:  userAccessForClaims(claims).Admin,
		write:  write,
		bulk:   workspaceProxyBulkRoutes[rel],
	}
}

// deniesPath reports whether a body or form path field must be refused.
func (p workspaceProxyPolicy) deniesPath(key, raw string) bool {
	return workspaceProxyPathIsOtherUser(raw, p.own) || p.denies(key, raw) != ""
}

// denies returns why raw may not be reached, or "" when it may.
func (p workspaceProxyPolicy) denies(key, raw string) string {
	if p.admin {
		return ""
	}
	write := p.write && !workspaceProxySourceKeys[key]
	clean := strings.Trim(path.Clean("/"+strings.TrimSpace(raw)), "/")
	if clean == "" || clean == "." {
		if write || p.bulk {
			return "the whole workspace is admin-only"
		}
		return ""
	}
	segments := strings.Split(clean, "/")
	switch segments[0] {
	case "config", "_system":
		return "server configuration is admin-only"
	case "Workflow":
		if len(segments) == 1 {
			if write || p.bulk {
				return "the Workflow root is admin-only"
			}
			return ""
		}
		level, manifest := workflowAccessForWorkspacePath(p.ctx, p.claims, "Workflow/"+segments[1])
		if manifest == nil {
			// No workflow there yet: creating one is covered by the account tier.
			if write && !workflowPermissionInfoForClaims(p.claims).CanWriteWorkflows {
				return "creating workflows needs write access"
			}
			return ""
		}
		if write {
			if len(segments) == 3 && segments[2] == "workflow.json" {
				if level != WorkflowAccessOwner {
					return "only the workflow's owners may change workflow.json"
				}
				return ""
			}
			if level != WorkflowAccessOwner && level != WorkflowAccessWrite {
				return "no write access to this workflow"
			}
			return ""
		}
		if level == "" || level == WorkflowAccessNone {
			return "no access to this workflow"
		}
	}
	return ""
}

// workspaceProxyURLTarget is the workspace path a document/folder/version
// route names in its URL, and whether it names one at all.
func workspaceProxyURLTarget(rel string) (string, bool) {
	remainder := strings.Trim(rel, "/")
	for _, prefix := range workspaceProxyRoutePathPrefixes {
		if after, ok := strings.CutPrefix(remainder, prefix); ok {
			return after, true
		}
	}
	return "", false
}
