package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

type WorkflowAccessLevel string

// A read tier was defined here once, removed 2026-08-17 (see below), and
// restored in PLAT-262 with a materially different enforcement mechanism —
// read the two paragraphs below in order, they explain both why it was safe
// to bring back and what NOT to repeat.
//
// First attempt (removed 2026-08-17): none of WORKFLOW_READ_USERS /
// WORKFLOW_WRITE_USERS / WORKFLOW_OWNER_USERS / WORKFLOW_USER_PERMISSIONS was
// ever set in any deployment, so every caller resolved to owner and the tier
// never took effect. What it did do was leave a query-access gate full of
// unreachable checks against workshop modes that no longer exist, which cost
// real debugging time while diagnosing PLAT-125. That gate checked HTTP
// query-endpoint access against workshop-mode strings that had already gone
// stale by the time anyone read the code.
//
// PLAT-262 (this restoration): a read-access identity is forced into Run
// mode server-side at session-creation time
// (installWorkflowPhaseTools/workflow_phase_tools.go), which in turn gates
// real MCP tool registration (registerRunModeAgentTools,
// interactive_workshop_manager.go) and the chat session's own shell
// folder-guard write paths (createInteractiveWorkshopAgent) — not an
// HTTP-query-level check against a mode string. If this tier ever again
// resolves to owner for everyone because nothing sets
// WORKFLOW_READ_USERS/WORKFLOW_USER_PERMISSIONS, that is still safe (no
// regression, matches today's default), but unlike the removed gate, every
// piece this tier feeds is exercised by a real caller whenever it IS
// configured — there is no dead branch left behind if it goes unused again.
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
	WorkflowPermissionSource   string              `json:"workflow_permission_source,omitempty"`
	WorkflowPermissionsEnabled bool                `json:"workflow_permissions_enabled"`
}

type workflowPermissionConfig struct {
	configured bool
	entries    map[string]WorkflowAccessLevel
}

func loadWorkflowPermissionConfig() workflowPermissionConfig {
	cfg := workflowPermissionConfig{entries: make(map[string]WorkflowAccessLevel)}

	parseList := func(envName string, level WorkflowAccessLevel) {
		raw := os.Getenv(envName)
		if strings.TrimSpace(raw) == "" {
			return
		}
		cfg.configured = true
		for _, item := range splitPermissionList(raw) {
			cfg.entries[normalizeWorkflowPermissionKey(item)] = level
		}
	}

	parseList("WORKFLOW_WRITE_USERS", WorkflowAccessWrite)
	parseList("WORKFLOW_OWNER_USERS", WorkflowAccessOwner)

	if raw := strings.TrimSpace(os.Getenv("WORKFLOW_USER_PERMISSIONS")); raw != "" {
		cfg.configured = true
		for _, item := range splitPermissionList(raw) {
			key, level, ok := parseWorkflowPermissionEntry(item)
			if ok {
				cfg.entries[key] = level
			}
		}
	}

	// File-backed grants override env. Failing to read the file is non-fatal —
	// we fall back to env-only behavior (logged elsewhere if needed).
	if persisted, err := readWorkflowUserPermissionsFile(); err == nil {
		for k, v := range persisted {
			cfg.configured = true
			cfg.entries[k] = v
		}
	}

	return cfg
}

func splitPermissionList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	items := make([]string, 0, len(fields))
	for _, field := range fields {
		if item := strings.TrimSpace(field); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseWorkflowPermissionEntry(entry string) (string, WorkflowAccessLevel, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", "", false
	}

	separator := strings.LastIndex(entry, "=")
	if separator < 0 {
		separator = strings.LastIndex(entry, ":")
	}
	if separator <= 0 || separator >= len(entry)-1 {
		return "", "", false
	}

	key := normalizeWorkflowPermissionKey(entry[:separator])
	level, ok := parseWorkflowAccessLevel(entry[separator+1:])
	return key, level, key != "" && ok
}

func parseWorkflowAccessLevel(raw string) (WorkflowAccessLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "owner", "admin", "manage", "manager":
		return WorkflowAccessOwner, true
	case "write", "writer", "edit", "editor":
		return WorkflowAccessWrite, true
	// PLAT-262: these resolve to the real read tier again (previously
	// collapsed into write while the tier was removed) — see the
	// WorkflowAccessLevel doc comment above for why this is safe now.
	case "read", "reader", "run", "runner", "view", "viewer":
		return WorkflowAccessRead, true
	default:
		return "", false
	}
}

func normalizeWorkflowPermissionKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func workflowAccessForClaims(claims *UserClaims) WorkflowAccessLevel {
	if level, ok := workflowAccessForBotRouteClaims(claims, nil); ok {
		return level
	}
	if claims == nil {
		if loadWorkflowPermissionConfig().configured {
			return WorkflowAccessWrite
		}
		return WorkflowAccessOwner
	}
	return workflowAccessForIdentity(claims.UserID, claims.Username, claims.Email)
}

func workflowAccessForIdentity(userID, username, email string) WorkflowAccessLevel {
	// The user directory (config/users.json) is authoritative for anyone it
	// knows: an admin is owner, a creator or editor is write, and a viewer
	// is read. Creation is intentionally separate so an editor can own an
	// assigned workflow without creating new ones.
	// Identities it does not know fall through
	// to the legacy env/file tiers below, unchanged.
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
	cfg := loadWorkflowPermissionConfig()
	if !cfg.configured {
		return WorkflowAccessOwner
	}

	for _, key := range []string{userID, username, email} {
		normalized := normalizeWorkflowPermissionKey(key)
		if normalized == "" {
			continue
		}
		if access, ok := cfg.entries[normalized]; ok {
			return access
		}
	}

	return WorkflowAccessWrite
}

func workflowPermissionInfoForClaims(claims *UserClaims) WorkflowPermissionInfo {
	return workflowPermissionInfo(workflowAccessForClaims(claims))
}

func workflowPermissionInfo(access WorkflowAccessLevel) WorkflowPermissionInfo {
	cfg := loadWorkflowPermissionConfig()
	canWrite := access == WorkflowAccessWrite || access == WorkflowAccessOwner
	canManage := access == WorkflowAccessOwner
	return WorkflowPermissionInfo{
		WorkflowAccess:             access,
		CanRunWorkflows:            true,
		CanWriteWorkflows:          canWrite,
		CanManageWorkflowAccess:    canManage,
		WorkflowPermissionsEnabled: cfg.configured || userDirectoryHasPasswordUsers(),
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

func currentUserCanCreateWorkflows(r *http.Request) bool {
	return userAccessForClaims(GetUserFromContext(r.Context())).CanCreate
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

type workflowUserPermissionResponse struct {
	UserKey        string `json:"user_key"`
	WorkflowAccess string `json:"workflow_access"`
}

func (api *StreamingAPI) handleListWorkflowUserPermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	entries, err := listWorkflowUserPermissions()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read permissions: %v", err), http.StatusInternalServerError)
		return
	}
	out := make([]workflowUserPermissionResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, workflowUserPermissionResponse{
			UserKey:        e.UserKey,
			WorkflowAccess: string(e.Access),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"permissions": out,
		"total":       len(out),
	})
}

type workflowUserPermissionUpsertRequest struct {
	UserKey        string `json:"user_key"`
	WorkflowAccess string `json:"workflow_access"`
}

func (api *StreamingAPI) handleUpsertWorkflowUserPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req workflowUserPermissionUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	key := normalizeWorkflowPermissionKey(req.UserKey)
	if key == "" {
		http.Error(w, "user_key is required", http.StatusBadRequest)
		return
	}
	level, ok := parseWorkflowAccessLevel(req.WorkflowAccess)
	if !ok {
		http.Error(w, "workflow_access must be one of read|write|owner", http.StatusBadRequest)
		return
	}
	if err := upsertWorkflowUserPermission(key, level); err != nil {
		http.Error(w, fmt.Sprintf("failed to save permission: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(workflowUserPermissionResponse{
		UserKey:        key,
		WorkflowAccess: string(level),
	})
}

func (api *StreamingAPI) handleDeleteWorkflowUserPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	key := normalizeWorkflowPermissionKey(r.URL.Query().Get("user_key"))
	if key == "" {
		http.Error(w, "user_key query param is required", http.StatusBadRequest)
		return
	}
	if err := deleteWorkflowUserPermission(key); err != nil {
		http.Error(w, fmt.Sprintf("failed to delete permission: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
