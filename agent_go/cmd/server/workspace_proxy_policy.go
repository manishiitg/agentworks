package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// workspaceProxyPolicy decides, per workspace path a proxied request names
// (URL, query or body field), whether the caller may reach it. It complements
// the cross-user rule (workspaceProxyPathIsOtherUser):
//   - the docs root, config/ and _system/ are admin-only;
//   - inside Workflow/<id> reads need workflow read access, writes need write
//     access, and workflow.json (its access record) only its owners;
//   - bulk routes (search, glob, folder copy) never run on the whole
//     workspace for non-admins.
type workspaceProxyPolicy struct {
	ctx         context.Context
	claims      *UserClaims
	own         string
	admin       bool
	write       bool
	bulk        bool
	delete      bool
	clearFolder bool
}

// Routes that POST a read (a SQL query, a table listing).
var workspaceProxyReadOnlyPostRoutes = map[string]bool{
	"api/query": true, "api/db/tables": true,
}

// Routes that act on a whole subtree at once.
var workspaceProxyBulkRoutes = map[string]bool{
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
		ctx:         r.Context(),
		claims:      claims,
		own:         sanitizeUserIDForPath(callerID),
		admin:       userAccessForClaims(claims).Admin,
		write:       write,
		bulk:        workspaceProxyBulkRoutes[rel],
		delete:      r.Method == http.MethodDelete,
		clearFolder: r.Method == http.MethodDelete && strings.HasPrefix(rel, "api/folders/") && strings.HasSuffix(rel, "/files"),
	}
}

// deniesPath reports whether a body or form path field must be refused.
func (p workspaceProxyPolicy) deniesPath(key, raw string) bool {
	return workspaceProxyPathIsOtherUser(raw, p.own) || p.denies(key, raw) != ""
}

// workspaceProxyServerOwnedFiles are written only by the server, never by a
// browser -- not even an admin's: legacy Code sharing data and the admin
// audit log, which must not be editable by the admins it records.
var workspaceProxyServerOwnedFiles = []string{codeSharesFilePath(), "config/code-admin-audit"}

// serverOwnedWrite reports whether a write to clean would change a
// server-owned file: the file itself, anything inside it, or config/ as a
// whole (deleting or moving the folder that holds them).
func serverOwnedWrite(clean string) bool {
	if clean == "config" {
		return true
	}
	for _, owned := range workspaceProxyServerOwnedFiles {
		if clean == owned || strings.HasPrefix(clean, owned+"/") {
			return true
		}
	}
	return false
}

// denies returns why raw may not be reached, or "" when it may.
func (p workspaceProxyPolicy) denies(key, raw string) string {
	write := p.write && !workspaceProxySourceKeys[key]
	clean := workspaceProxyCleanPath(raw)
	deleteTarget := clean
	if p.clearFolder {
		deleteTarget = strings.TrimSuffix(deleteTarget, "/files")
	}
	if p.delete && write && codeFilesDeletionProtected(deleteTarget) {
		return "the Code workspace root and project metadata cannot be deleted from Files; use Delete Code to remove the workspace"
	}
	if write && serverOwnedWrite(clean) {
		return "this file is written only by the server"
	}
	// A Crew being moved by the Crew move command is not written by anyone else meanwhile.
	if write && crewMoveBlocksPath(clean) {
		return errCrewBeingMoved.Error()
	}
	// Relay releases are server-owned snapshots. Their nested path has no
	// manifest at Workflow/.relay_releases, so normal workflow path lookup
	// cannot safely authorize access to them.
	if clean == "Workflow/.relay_releases" || strings.HasPrefix(clean, "Workflow/.relay_releases/") {
		if write {
			return "Relay releases are written only by the server"
		}
		if !p.admin {
			return "Relay releases are not available through the workspace proxy"
		}
	}
	if p.admin {
		return ""
	}
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

// The Files proxy must not bypass project deletion, which owns conversation,
// running-session and credential cleanup. This also covers deleting a parent
// folder or clearing its contents through /folders/<path>/files.
func codeFilesDeletionProtected(clean string) bool {
	if ref := workspaceref.MustParse(clean); ref.HasOwner() && ref.Logical() != "" {
		clean = ref.Logical()
	}
	root := codeproduct.ProjectsRoot
	if clean == root || (clean != "" && strings.HasPrefix(root, clean+"/")) {
		return true
	}
	rel, inside := strings.CutPrefix(clean, root+"/")
	if !inside {
		return false
	}
	parts := strings.Split(rel, "/")
	return len(parts) == 1 || (len(parts) == 2 && (parts[1] == "product.json" || parts[1] == "workflow.json"))
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
