package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

type WorkflowAccessLevel string

// Account capabilities bound workflow access; each manifest owns its sharing.
const (
	WorkflowAccessRead  WorkflowAccessLevel = "read"
	WorkflowAccessWrite WorkflowAccessLevel = "write"
	WorkflowAccessOwner WorkflowAccessLevel = "owner"
)

type WorkflowPermissionInfo struct {
	WorkflowAccess             WorkflowAccessLevel `json:"workflow_access"`
	CanRunWorkflows            bool                `json:"can_run_workflows"`
	CanWriteWorkflows          bool                `json:"can_write_workflows"`
	CanManageWorkflowAccess    bool                `json:"can_manage_workflow_access"`
	WorkflowPermissionsEnabled bool                `json:"workflow_permissions_enabled"`
}

func normalizeWorkflowPermissionKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func workflowAccessForClaims(claims *UserClaims) WorkflowAccessLevel {
	if level, ok := workflowAccessForBotRouteClaims(claims, nil); ok {
		return level
	}
	if claims == nil {
		return WorkflowAccessOwner
	}
	return workflowAccessForIdentity(claims.UserID, claims.Username, claims.Email)
}

func workflowAccessForIdentity(userID, username, email string) WorkflowAccessLevel {
	// The user directory (config/users.json) is authoritative for anyone it
	// knows: an admin is owner, a creator or editor is write, and a viewer
	// is read. Creation is intentionally separate so an editor can own an
	// assigned workflow without creating new ones.
	// Unknown identities retain the account capability floor below. Per-item
	// access still comes from workflow owners, editors and sharing.
	if rec := directoryUserFor(userID, username, email); rec != nil {
		acc := accessForRecord(rec)
		switch {
		case acc.Admin:
			return WorkflowAccessOwner
		case acc.CanEdit:
			return WorkflowAccessWrite
		default:
			return WorkflowAccessRead
		}
	}
	// An identity outside an existing directory is not the deployment owner.
	if userDirectoryHasUsers() {
		return WorkflowAccessWrite
	}
	return WorkflowAccessOwner
}

func workflowPermissionInfoForClaims(claims *UserClaims) WorkflowPermissionInfo {
	return workflowPermissionInfo(workflowAccessForClaims(claims))
}

func workflowPermissionInfo(access WorkflowAccessLevel) WorkflowPermissionInfo {
	canWrite := access == WorkflowAccessWrite || access == WorkflowAccessOwner
	canManage := access == WorkflowAccessOwner
	return WorkflowPermissionInfo{
		WorkflowAccess:             access,
		CanRunWorkflows:            true,
		CanWriteWorkflows:          canWrite,
		CanManageWorkflowAccess:    canManage,
		WorkflowPermissionsEnabled: userDirectoryHasPasswordUsers(),
	}
}

func userInfoWithWorkflowPermissions(info UserInfo) UserInfo {
	info.Role = roleForClaims(&UserClaims{UserID: info.ID, Username: info.Username, Email: info.Email})
	access := workflowAccessForIdentity(info.ID, info.Username, info.Email)
	perms := workflowPermissionInfo(access)
	info.WorkflowAccess = string(perms.WorkflowAccess)
	info.CanRunWorkflows = perms.CanRunWorkflows
	info.CanWriteWorkflows = perms.CanWriteWorkflows
	info.CanManageWorkflowAccess = perms.CanManageWorkflowAccess

	if products, restricted := userProductPolicy(info.ID, info.Username, info.Email); restricted {
		info.AllowedProducts = products
	}
	if productAccess, ok := userProductAccessForIdentity(info.ID, info.Username, info.Email); ok {
		info.AllowedWorkflowIDs = productAccess.WorkflowIDs
	}
	if rec := directoryUserFor(info.ID, info.Username, info.Email); rec != nil {
		info.IsAdmin = rec.Admin
		info.CanCreate = rec.Admin || rec.CanCreate
		info.IsCrewReviewer = rec.CrewReviewer && !rec.Disabled
		info.IsVaultReader = rec.VaultReader && !rec.Disabled
	} else {
		acc := userAccessForClaims(&UserClaims{UserID: info.ID, Username: info.Username, Email: info.Email})
		info.IsAdmin = acc.Admin
		info.CanCreate = acc.CanCreate
	}
	return info
}

func workflowPermissionResponseFields(perms WorkflowPermissionInfo) map[string]interface{} {
	return map[string]interface{}{
		"workflow_access":              perms.WorkflowAccess,
		"can_run_workflows":            perms.CanRunWorkflows,
		"can_write_workflows":          perms.CanWriteWorkflows,
		"can_manage_workflow_access":   perms.CanManageWorkflowAccess,
		"workflow_permissions_enabled": perms.WorkflowPermissionsEnabled,
	}
}

func currentUserCanWriteWorkflows(r *http.Request) bool {
	return workflowPermissionInfoForClaims(GetUserFromContext(r.Context())).CanWriteWorkflows
}

// currentUserCanCreateWorkflows admits the create endpoints for an account that may create workflows or Relays; each
// handler then checks the product it creates (requireCreateIn).
func currentUserCanCreateWorkflows(r *http.Request) bool {
	acc := userAccessForClaims(GetUserFromContext(r.Context()))
	return acc.CanCreateIn("agentworks") || acc.CanCreateIn("relays")
}

// requireCreateIn refuses the request unless the account may create in product (PLAT-767). It reports whether to go on.
func requireCreateIn(w http.ResponseWriter, r *http.Request, product string) bool {
	if userAccessForClaims(GetUserFromContext(r.Context())).CanCreateIn(product) {
		return true
	}
	writeWorkflowPermissionDenied(w, "create")
	return false
}

// workflowKindProduct is the product a workflow manifest of this kind belongs to.
func workflowKindProduct(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "relay") {
		return "relays"
	}
	return "agentworks"
}

func requireWorkflowCreateAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" || currentUserCanCreateWorkflows(r) {
			next(w, r)
			return
		}
		writeWorkflowPermissionDenied(w, "create")
	}
}

func currentUserCanManageWorkflowAccess(r *http.Request) bool {
	return workflowPermissionInfoForClaims(GetUserFromContext(r.Context())).CanManageWorkflowAccess
}

func requireWorkflowWriteAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			next(w, r)
			return
		}
		if !currentUserCanWriteWorkflows(r) {
			writeWorkflowPermissionDenied(w, "write")
			return
		}
		// The account tier is not enough: the named workflow's own access
		// record must grant write (owners and editors). A path with no
		// workflow yet is creation, which the account tier covers.
		if workspacePath := requestWorkflowWorkspacePath(r); workspacePath != "" {
			if strings.Contains("/"+filepath.ToSlash(filepath.Clean(workspacePath))+"/", "/.relay_releases/") {
				writeWorkflowPermissionDenied(w, "write")
				return
			}
			level, manifest := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), workspacePath)
			if manifest != nil && level != WorkflowAccessOwner && level != WorkflowAccessWrite {
				writeWorkflowPermissionDenied(w, "write")
				return
			}
		}
		next(w, r)
	}
}

// requestWorkflowWorkspacePath is the workspace_path a workflow route names,
// from the query string or a JSON body (which is restored for the handler).
func requestWorkflowWorkspacePath(r *http.Request) string {
	if value := strings.TrimSpace(r.URL.Query().Get("workspace_path")); value != "" {
		return value
	}
	if r.Body == nil || !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var body struct {
		WorkspacePath string `json:"workspace_path"`
	}
	_ = json.Unmarshal(raw, &body)
	return strings.TrimSpace(body.WorkspacePath)
}

func requireWorkflowOwnerAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" || currentUserCanManageWorkflowAccess(r) {
			next(w, r)
			return
		}
		writeWorkflowPermissionDenied(w, "owner")
	}
}

func writeWorkflowPermissionDenied(w http.ResponseWriter, required string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":           "workflow permission denied",
		"required_access": required,
	})
}

func (api *StreamingAPI) handleListAuthUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !currentUserCanManageWorkflowAccess(r) {
		writeWorkflowPermissionDenied(w, "owner")
		return
	}

	type authUserResponse struct {
		ID                      string `json:"id"`
		Username                string `json:"username"`
		Email                   string `json:"email,omitempty"`
		Provider                string `json:"provider"`
		WorkflowAccess          string `json:"workflow_access"`
		CanRunWorkflows         bool   `json:"can_run_workflows"`
		CanWriteWorkflows       bool   `json:"can_write_workflows"`
		CanManageWorkflowAccess bool   `json:"can_manage_workflow_access"`
	}

	users := GetHardcodedUsers()
	names := make([]string, 0, len(users))
	for name := range users {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]authUserResponse, 0, len(names))
	for _, name := range names {
		user := users[name]
		access := workflowAccessForIdentity(user.UserID, user.Username, "")
		perms := workflowPermissionInfo(access)
		out = append(out, authUserResponse{
			ID:                      user.UserID,
			Username:                user.Username,
			Provider:                "simple",
			WorkflowAccess:          string(perms.WorkflowAccess),
			CanRunWorkflows:         perms.CanRunWorkflows,
			CanWriteWorkflows:       perms.CanWriteWorkflows,
			CanManageWorkflowAccess: perms.CanManageWorkflowAccess,
		})
	}

	if len(out) == 0 {
		if claims := GetUserFromContext(r.Context()); claims != nil {
			perms := workflowPermissionInfoForClaims(claims)
			out = append(out, authUserResponse{
				ID:                      claims.UserID,
				Username:                claims.Username,
				Email:                   claims.Email,
				Provider:                claims.Provider,
				WorkflowAccess:          string(perms.WorkflowAccess),
				CanRunWorkflows:         perms.CanRunWorkflows,
				CanWriteWorkflows:       perms.CanWriteWorkflows,
				CanManageWorkflowAccess: perms.CanManageWorkflowAccess,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"users": out,
		"total": len(out),
	})
}

// personMayAuthorWorkspace is the website's rule for changing a workflow: the person owns it or may edit it, and the
// account may edit at all. A sign-in that carries Builder consent for every person (owner 2026-10-16) is not by itself
// the right to author; this per-item role is.
func personMayAuthorWorkspace(ctx context.Context, claims *UserClaims, workspace string) bool {
	if claims == nil || !userAccessForClaims(claims).CanEdit {
		return false
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, claims, workspace)
	return manifest != nil && (level == WorkflowAccessOwner || level == WorkflowAccessWrite)
}
