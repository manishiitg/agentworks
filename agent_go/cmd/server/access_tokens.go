package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func openAccessTokens() (*accesstokens.Store, error) {
	path, err := privateAuthorityStatePath(".sqlite")
	if err != nil {
		return nil, err
	}
	return accesstokens.Open(path)
}

// Shared private server state stays outside product documents. Preserve the
// access-token path; guidance uses its own database under the same authority.
func privateAuthorityStatePath(suffix string) (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	docs, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(docs, root)
	if err != nil {
		return "", err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("access token state must be outside workspace documents")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if actualDocs, e := filepath.EvalSymlinks(docs); e == nil {
		docs = actualDocs
	}
	rel, err = filepath.Rel(docs, resolved)
	if err != nil {
		return "", err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("access token state must be outside workspace documents")
	}
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return "", err
	}
	// Bind storage to this server authority even if two local instances use
	// the same default state root. Secret rotation invalidates previous PATs.
	authority := sha256.Sum256(GetAuthSecret())
	return filepath.Join(resolved, "auth", hex.EncodeToString(authority[:16])+suffix), nil
}

// PAT identities are resolved against the current directory, not saved account
// permissions. Unlike legacy browser sessions, directory failures fail closed.
func accessTokenClaims(t accesstokens.Token) (*UserClaims, error) {
	c := &UserClaims{UserID: t.UserID, Username: t.Username, Email: t.Email, Provider: t.Provider, AccessToken: &t}
	if t.NonExpiring && IsMultiUserMode() {
		return nil, accesstokens.ErrInvalid
	}
	if !IsMultiUserMode() {
		if c.UserID != GetDefaultUserID() {
			return nil, accesstokens.ErrInvalid
		}
	} else {
		dir, err := readUserDirectoryFile()
		if err != nil {
			return nil, err
		}
		rec := dir.byID(t.UserID)
		if rec == nil || rec.Disabled {
			return nil, accesstokens.ErrInvalid
		}
		c.Username = rec.Username
		c.Email = rec.Email
	}
	// MCP acts with the person's current role (owner, 2026-10-20: "mcp = role
	// of user"); what was picked when the token was made no longer narrows it.
	// Service accounts, device links, agent-bound and project connections keep
	// their own grant.
	if t.KnowledgebaseIdentityID == "" && t.BoundAgentID == "" && t.ProjectKind == "" && t.ProjectIdentity == "" && !t.Allows("devices:connect") {
		t.Scopes = mcpOAuthPersonScopes(c)
		t.AllWorkflows, t.AllCrews = true, true
		t.WorkflowIDs, t.CrewIDs = nil, nil
		t.FileGuard, t.KnowledgebaseFolders = nil, nil
	}
	if t.KnowledgebaseIdentityID != "" {
		if !knowledgebaseMCPAllowed(c) {
			return nil, accesstokens.ErrInvalid
		}
		service, err := knowledgebaseService()
		if err != nil {
			return nil, err
		}
		// A disabled service identity invalidates all its connections
		// immediately, even if the issuing administrator is still enabled.
		if !service.IdentityActive(context.Background(), t.KnowledgebaseIdentityID) {
			return nil, accesstokens.ErrInvalid
		}
	}
	return c, nil
}

func authenticateAccessToken(w http.ResponseWriter, r *http.Request, raw string) (*UserClaims, bool) {
	// Hosted MCP clients (ChatGPT connectors) cannot set request headers, so
	// the MCP endpoint also accepts the middleware's ?token= fallback. Every
	// other PAT path still requires the Authorization header: credentials in
	// URLs leak into proxy logs and history.
	if r.Header.Get("Authorization") == "" && r.URL.Path != externalMCPPath {
		externalError(w, 401, "unauthorized", "Access tokens must use the Authorization header.")
		return nil, false
	}
	if !(publicationTokenPath(r) || serverProductTokenPath(r) || (r.Method == "GET" && r.URL.Path == "/api/external/v1/agents/events") || (r.Method == "GET" && r.URL.Path == "/api/external/v1/devices/connect") || (r.Method == "GET" && r.URL.Path == "/api/external/v1/tools") || (r.Method == "POST" && (r.URL.Path == "/api/external/v1/call" || r.URL.Path == "/api/external/v1/mcp/tasks" || r.URL.Path == "/api/external/v1/mcp/elicitation")) || ((r.Method == "GET" || r.Method == "HEAD") && r.URL.Path == "/api/external/v1/files/content") || (r.URL.Path == externalMCPPath && (r.Method == "POST" || r.Method == "GET" || r.Method == "DELETE")) || (r.Method == "GET" && (r.URL.Path == "/api/external/v1/skill.md" || r.URL.Path == "/api/external/v1/skill.zip" || r.URL.Path == "/api/external/v1/agentworks.plugin"))) {
		externalError(w, 403, "forbidden", "Access tokens are valid only for the external tools API.")
		return nil, false
	}
	store, err := openAccessTokens()
	if err != nil {
		externalError(w, 503, "auth_unavailable", "Access token storage is unavailable.")
		return nil, false
	}
	defer store.Close()
	t, err := store.Authenticate(r.Context(), raw, time.Now())
	if err != nil {
		if errors.Is(err, accesstokens.ErrInvalid) {
			externalError(w, 401, "invalid_token", "Legacy access token is invalid, expired, or revoked. Sign in again with agentworks login.")
		} else {
			externalError(w, 503, "auth_unavailable", "Access token validation is unavailable.")
		}
		return nil, false
	}
	c, err := accessTokenClaims(t)
	if err != nil {
		externalError(w, 401, "invalid_token", "The access token's account is unavailable or disabled.")
		return nil, false
	}
	return c, true
}

func (api *StreamingAPI) handleAccessTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	c := GetUserFromContext(r.Context())
	if c == nil || c.AccessToken != nil || c.Scope != "" {
		externalError(w, 403, "forbidden", "Use your app login to manage access tokens.")
		return
	}
	store, err := openAccessTokens()
	if err != nil {
		externalError(w, 503, "auth_unavailable", "Access token storage is unavailable.")
		return
	}
	defer store.Close()
	switch r.Method {
	case "GET":
		tokens, err := store.List(r.Context(), c.UserID)
		if err != nil {
			externalError(w, 503, "auth_unavailable", "Could not load access tokens.")
			return
		}
		externalJSON(w, map[string]any{"tokens": tokens})
	case "POST":
		var req struct {
			Name                    string               `json:"name"`
			FileGuard               *wf.FolderGuard      `json:"file_guard"`
			LocalFullAccess         bool                 `json:"local_full_access"`
			Scopes                  []string             `json:"scopes"`
			WorkflowIDs             []string             `json:"workflow_ids"`
			AllWorkflows            bool                 `json:"all_workflows"`
			CrewIDs                 []string             `json:"crew_ids"`
			AllCrews                bool                 `json:"all_crews"`
			KnowledgebaseFolders    *[]knowledgebase.Cap `json:"knowledgebase_folders"`
			KnowledgebaseIdentityID string               `json:"knowledgebase_identity_id"`
			ExpiresInDays           int                  `json:"expires_in_days"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		d.DisallowUnknownFields()
		if err := d.Decode(&req); err != nil {
			externalError(w, 400, "invalid_arguments", "Invalid access token request.")
			return
		}
		if d.Decode(new(any)) != io.EOF {
			externalError(w, 400, "invalid_arguments", "Expected one JSON object.")
			return
		}
		localPreset := req.LocalFullAccess
		if localPreset {
			if IsMultiUserMode() || c.UserID != GetDefaultUserID() {
				externalError(w, 403, "forbidden", "Full local tokens are available only to the local single-user account.")
				return
			}
			// An MCP connection acts with its person's role; on a local app that
			// is the owner. Permissions come from the current account and enabled
			// products; a client cannot choose a different identity.
			req.Name = "agentworks-local"
			req.Scopes = mcpOAuthScopesFor(c, append([]string(nil), mcpOAuthScopes...))
			req.AllWorkflows, req.AllCrews = true, true
			req.WorkflowIDs, req.CrewIDs = nil, nil
			req.KnowledgebaseFolders, req.KnowledgebaseIdentityID = nil, ""
		}
		if req.ExpiresInDays < 0 || req.ExpiresInDays > 90 || !localPreset && req.ExpiresInDays < 1 {
			externalError(w, 400, "invalid_arguments", "Choose an expiry between 1 and 90 days.")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			req.Name = "Access token"
		}
		now := time.Now()
		t := accesstokens.Token{LocalPreset: localPreset, Name: req.Name, UserID: c.UserID, Username: c.Username, Email: c.Email, Provider: c.Provider, Scopes: req.Scopes, FileGuard: req.FileGuard, WorkflowIDs: req.WorkflowIDs, AllWorkflows: req.AllWorkflows, CrewIDs: req.CrewIDs, AllCrews: req.AllCrews, KnowledgebaseFolders: req.KnowledgebaseFolders, KnowledgebaseIdentityID: req.KnowledgebaseIdentityID, ExpiresAt: now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)}
		if localPreset && req.ExpiresInDays == 0 {
			t.NonExpiring = true
			t.ExpiresAt = accesstokens.PermanentExpiry()
		}
		if err := accesstokens.Validate(t, now); err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			return
		}
		if t.KnowledgebaseAccess() {
			if !knowledgebaseMCPAllowed(c) {
				externalError(w, 403, "forbidden", "Brain is not available for this account.")
				return
			}
			if t.KnowledgebaseIdentityID != "" && !currentUserIsAdmin(r) {
				externalError(w, 403, "forbidden", "Only an administrator can issue service account connections.")
				return
			}
			service, err := knowledgebaseService()
			if err != nil {
				externalError(w, 503, "knowledgebase_unavailable", "Could not check knowledge base access.")
				return
			}
			if err := knowledgebaseSyncIdentities(r.Context(), service); err != nil {
				externalError(w, 503, "knowledgebase_unavailable", "Could not check current knowledge base identities.")
				return
			}
			principal := knowledgebasePrincipal(r, c)
			identityID := c.UserID
			if t.KnowledgebaseIdentityID != "" {
				identityID = t.KnowledgebaseIdentityID
				if err := service.ValidateServiceTokenIdentity(r.Context(), principal, identityID); err != nil {
					externalError(w, 403, "forbidden", "The selected service account is unavailable.")
					return
				}
			}
			if err := service.ValidateCaps(r.Context(), principal, identityID, t.KnowledgebaseFolders); err != nil {
				externalError(w, 403, "forbidden", "Selected folder scopes exceed the identity's current knowledge base access.")
				return
			}
		}
		if t.Allows("vault:manage") && !vaultAdminActive(c.UserID) {
			externalError(w, 403, "forbidden", "vault:manage requires an active Vault administrator.")
			return
		}
		if t.Allows("vault:read") && vaultPersonLevel(c.UserID) == vaultNone {
			externalError(w, 403, "forbidden", "vault:read requires a Vault manager or Vault reader.")
			return
		}
		if t.Allows("crews:review") && !currentUserCanReviewCode(r) {
			externalError(w, 403, "forbidden", "crews:review is for admins and Code reviewers.")
			return
		}
		if t.Allows("users:manage") && !currentUserIsAdmin(r) {
			externalError(w, 403, "forbidden", "users:manage is for admins.")
			return
		}
		if !t.AllCrews && len(t.CrewIDs) > 0 {
			crews, err := listAccessibleCrewProjects(r.Context(), c.UserID, "")
			if err != nil {
				externalError(w, 502, "workspace_unavailable", "Cannot check Crew access.")
				return
			}
			allowed := map[string]bool{}
			for _, crew := range crews {
				allowed[fmt.Sprint(crew["id"])] = true
			}
			for _, id := range t.CrewIDs {
				if !allowed[id] {
					externalError(w, 403, "forbidden", "One or more selected Crews are not accessible.")
					return
				}
			}
		}
		if !t.AllWorkflows && len(t.WorkflowIDs) > 0 {
			workflows, err := DiscoverWorkflowManifests(r.Context())
			if err != nil {
				externalError(w, 502, "workspace_unavailable", "Cannot check workflow access.")
				return
			}
			visible := filterWorkflowManifestsForUser(c, workflows)
			allowed := map[string]bool{}
			for _, wf := range visible {
				if wf.Manifest != nil {
					role := workflowAccessForManifest(c, wf.Manifest)
					allowed[wf.Manifest.ID] = !(t.Allows("builder:chat") || t.Allows("relays:write")) || role == WorkflowAccessOwner || role == WorkflowAccessWrite
				}
			}
			for _, id := range t.WorkflowIDs {
				if !allowed[id] {
					externalError(w, 403, "forbidden", "One or more selected workflows are not accessible.")
					return
				}
			}
		}
		previous, err := store.List(r.Context(), c.UserID)
		if err != nil {
			externalError(w, 503, "auth_unavailable", "Could not load access tokens.")
			return
		}
		token, raw, err := store.Issue(r.Context(), t, now)
		if err != nil {
			if errors.Is(err, accesstokens.ErrLocalPresetLimit) {
				externalError(w, http.StatusConflict, "local_token_limit", err.Error())
				return
			}
			externalError(w, 503, "token_creation_failed", "Could not create token; at most 100 active tokens are allowed.")
			return
		}
		// Local presets and knowledge-base connections remain independent;
		// cancel sessions only for grants actually replaced in the store.
		for _, old := range previous {
			if !t.LocalPreset && !old.LocalPreset && !t.KnowledgebaseAccess() && !t.Allows("devices:connect") && !old.KnowledgebaseAccess() && !old.Allows("devices:connect") && old.RevokedAt == nil && old.ExpiresAt.After(now) {
				api.cancelAccessTokenSessions(old.ID)
			}
		}
		w.WriteHeader(http.StatusCreated)
		externalJSON(w, map[string]any{"token": raw, "access_token": token})
	case "DELETE":
		id := mux.Vars(r)["id"]
		if err := store.Revoke(r.Context(), id, c.UserID, time.Now()); err != nil {
			externalError(w, 404, "token_not_found", "Access token not found.")
			return
		}
		api.cancelAccessTokenSessions(id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// tokenSessionWorkflowReadRoot scopes a workflow chat's workflow reads to
// its own workflow folder, for app and token sessions alike: other workflows
// are readable only when attached (PLAT-395; they arrive as read-only
// folders). An unresolved folder grants nothing ("" — the caller skips it),
// never the whole Workflow/ tree.
func tokenSessionWorkflowReadRoot(_ *UserClaims, workflowPhaseFolder string) string {
	folder := strings.Trim(strings.TrimSpace(workflowPhaseFolder), "/")
	if folder == "" {
		return ""
	}
	return folder + "/"
}

func externalTokenAllows(c *UserClaims, tool externalTool) bool {
	if !agentSourceConsentAllowed(c, tool) {
		return false
	}
	if source, operation := dataSourceAction(tool.Name); source == "crew_folders" && c != nil && c.AccessToken != nil {
		scope := "crews:write"
		if crewCapabilityRead(operation) {
			scope = "crews:read"
		}
		if !c.AccessToken.Allows(scope) {
			return false
		}
	}
	// These narrower catalog members use the existing chat action consent as
	// well as the source tool's consent; grouping must not admit write-only
	// connections that the source tool refused (PLAT-825).
	if action := tabModelMemberAction(tool.Name); action != "" && c != nil && c.AccessToken != nil && !c.AccessToken.Allows(crewChatActionScope(action, "")) {
		return false
	}
	tool.Name = configurationConsentMember(tool.Name)
	tool.Name = dataConsentMember(tool.Name)
	tool.Name = agentConsentMember(tool.Name)
	if agentBridgeOnlyDeployment() && tool.Name != "messages" && tool.Name != "help" && tool.actions == nil {
		return false
	}
	if c == nil || !(productpolicy.Selection{}).AllowsTool(tool.Name) {
		return false
	}
	if c.AccessToken != nil && c.AccessToken.ProjectKind != "" && tool.actions == nil && !projectMCPToolAllowed(c.AccessToken.ProjectKind, tool.Name) {
		return false
	}
	if c.AccessToken != nil && c.AccessToken.BoundAgentID != "" {
		return tool.Name == "messages" && namedAgentOperationAllowed(c, false)
	}
	if crewIntegrationMembers[tool.Name] {
		status, _ := externalCrewAuthorGate(c)
		return productEnabled(crewProfileID) && status == 0 && (c.AccessToken == nil || c.AccessToken.Allows("crews:write"))
	}
	if (isExternalCrewTool(tool.Name) || isExternalCodeReviewTool(tool.Name)) && !productEnabled(crewProfileID) {
		return false
	}
	if tool.Name == "manage_my_vaults" {
		return personalLaptopSettingsClaims(c) && personVaultActive(c.UserID)
	}
	if tool.Name == "computer_use" {
		return computerUseAllowed(c) && externalWorkspaceAllowed(c) && (c.AccessToken == nil || c.AccessToken.Allows("crews:write"))
	}
	if tool.Name == "manage_laptop_access" || isExternalLaptopFileTool(tool.Name) {
		return personalLaptopSettingsClaims(c) && (externalWorkspaceAllowed(c) || externalFileAuthoringAllowed(c, nil))
	}
	if tool.Name == "workspace" {
		return externalWorkspaceAllowed(c) || projectCreationAvailable(c)
	}
	if tool.Name == "open_dashboard" {
		return userAllowedProduct(c, crewProfileID) && dashboardScopeAllowed(c, "get")
	}
	if tool.Name == "browser_connections" {
		return externalBrowserConnectionsAllowed(c)
	}
	if tool.Name == "costs" && tool.actions == nil {
		return externalCostsAllowed(c)
	}
	if tool.Name == "agent_inbox" {
		return !IsLocalMode() && c != nil
	}

	if tool.Name == externalUsersTool || tool.Name == externalSignInAppsTool {
		return externalUsersAllowed(c)
	}
	if tool.actions != nil {
		return externalMergedAllows(c, tool)
	}
	if projectFunctionTool(tool.Name) {
		if c.AccessToken == nil {
			return true
		}
		t := c.AccessToken
		switch tool.Name {
		case "project_functions_define", "project_functions_delete":
			return t.Allows("crews:write") || t.BuilderAccess() || t.RelayBuilderAccess()
		case "project_functions_call", "project_functions_reply":
			return t.Allows("crews:run") || t.Allows("runs:execute")
		default:
			return t.Allows("crews:read") || t.Allows("crews:run") || t.Allows("workflows:read") || t.Allows("runs:execute")
		}
	}
	if tool.Name == "messages" {
		if c.AccessToken != nil && c.AccessToken.ProjectKind != "" {
			return false
		}
		if c.AccessToken == nil {
			return true
		}
		t := c.AccessToken
		return t.Allows("messages:read") || t.Allows("messages:send") || t.Allows("crews:run") || t.Allows("crews:read") || t.Allows("runs:execute") || t.Allows("workflows:read") || externalCodeRunAllowed(c)
	}
	if isDashboardTool(tool.Name) {
		return dashboardScopeAllowed(c, dashboardActions[tool.Name])
	}
	if knowledgebase.IsMCPTool(tool.Name) {
		if !knowledgebaseMCPAllowed(c) {
			return false
		}
		if c.AccessToken == nil {
			return true
		}
		// Backup/access include read actions. Dispatch checks each action;
		// only the all-write update tool is hidden from read-only connections.
		if tool.Name == knowledgebase.ToolUpdate {
			return c.AccessToken.Allows("knowledgebase:read") && c.AccessToken.Allows("knowledgebase:write")
		}
		return c.AccessToken.Allows("knowledgebase:read")
	}
	if isExternalVaultTool(tool.Name) {
		level := vaultClaimsLevel(c)
		return level == vaultManage || (level == vaultRead && vaultToolHasReads(tool.Name))
	}
	// Code review tools exist only for admins and Code reviewers, and a token
	// also needs crews:review; both are re-checked on every call.
	if isExternalCodeReviewTool(tool.Name) {
		return claimsCanReviewCode(c) && (c.AccessToken == nil || c.AccessToken.Allows("crews:review"))
	}
	if tool.Name == "manage_condition_triggers" {
		return externalWatchersAllowed(c)
	}

	if isExternalTokenLimitTool(tool.Name) {
		return externalTokenLimitAllowed(c, tool.Name)
	}
	if tool.Name == serverProductManagementTool {
		return serverProductManagementAllowed(c)
	}
	if tool.Name == bullpenAdminTool {
		return bullpenAdminToolAllowed(c)
	}
	if tool.Name == brainAdminTool {
		return brainAdminToolAllowed(c)
	}
	if tool.Name == "create_workflow" {
		return externalWorkflowCreationAllowed(c)
	}
	if tool.Name == "copy_project_files" {
		return projectCopyAvailable(c)
	}
	if tool.Name == "workflow_team" {
		// Readers can discover get. Dispatch separately enforces current Builder
		// consent, workflow authority and file access for either mutation.
		return c.AccessToken == nil || c.AccessToken.Allows("workflows:read") || c.AccessToken.Allows("runs:execute")
	}
	if strings.HasPrefix(tool.Name, "builder_") || tool.Name == "plan" || tool.Name == "workflow_upgrade" || tool.Name == "workflow_crews" || isExternalWorkflowAccountTool(tool.Name) {
		return c.AccessToken != nil && (c.AccessToken.BuilderAccess() || c.AccessToken.RelayBuilderAccess())
	}
	if isExternalRelayAuthoringTool(tool.Name) {
		return c.AccessToken != nil && (c.AccessToken.RelayBuilderAccess() || tool.Name != "create_relay" && c.AccessToken.BuilderAccess())
	}
	if tool.Name == "write_file" || tool.Name == "patch_file" {
		return externalFileAuthoringAllowed(c, nil)
	}
	if c.AccessToken == nil {
		return true
	}
	t := c.AccessToken
	if isExternalCrewTool(tool.Name) {
		if externalCrewCapabilities[tool.Name] != nil {
			return t.Allows("crews:read") || t.Allows("crews:write")
		}
		if tool.Name == "crew_files" {
			return t.Allows("crews:write")
		}
		if tool.Name == "crew_execution" || tool.Name == "crew_integrations" {
			status, _ := externalCrewAuthorGate(c)
			return t.Allows("crews:write") && status == 0
		}
		switch tool.Name {
		case externalCrewCommandsTool:
			// Discovery includes list for readers; dispatch checks the action,
			// live ownership and authoring consent before any mutation.
			return t.Allows("crews:read") || t.Allows("crews:write")
		case "ask_crew", "suggest_crew_change", "review_crew_snapshot":
			return t.Allows("crews:run")
		case "undo_crew_reply":
			return t.Allows("crews:write")
		case "write_crew_file":
			// Anyone who runs a Crew may drop a file in their own shared/<id>/ folder (crews:run); writing anywhere else
			// in the Crew is the owner's and needs crews:write (checked with the path).
			return t.Allows("crews:write") || t.Allows("crews:run")
		case "create_crew", "update_crew", "import_crew":
			return t.Allows("crews:write")
		}
		return t.Allows("crews:read")
	}
	// Execution implies workflow visibility: a token that may run must be
	// able to discover workflows, read plans, and poll run evidence.
	// File content stays behind files:read; the run session reads files
	// itself, server-side.
	reads := t.Allows("workflows:read") || t.Allows("runs:execute")
	if tool.executes {
		return t.Allows("runs:execute")
	}
	switch tool.Name {
	case "chat_preferences":
		return true // Only the authenticated caller's display is read or changed.
	case "get_file_link":
		return t.Allows("files:read") || t.Allows("crews:read")
	case "list_files", "read_file", "search_files", "list_step_code":
		return t.Allows("files:read")
	case "run_relay", "get_relay_run":
		return t.Allows("runs:execute")
	case "list_relay_runs":
		return t.Allows("runs:execute") || t.Allows("workflows:read")
	case "get_relay_releases":
		return t.Allows("workflows:read")
	case "query_database", "manage_messaging":
		return reads || t.Allows("crews:read")
	case "manage_crew_chats":
		return t.Allows("crews:read") || t.Allows("crews:run")
	case "get_secret_sources":
		return reads || t.Allows("crews:read") || externalCodeRunAllowed(c)
	case "get_settings", "update_settings", "manage_schedules", "manage_triggers", "list_needs_you", "answer_needs_you", "dismiss_needs_you", "manage_project":
		// Workflow or Crew settings: each target rechecks its own scope and role.
		return reads || t.Allows("crews:read") || t.Allows("crews:write")
	case "get_agent_context", "list_guidance_topics", "get_guidance_topic", "get_guidance_skills", "get_skill":
		// Canonical server-owned guidance carries no workflow content.
		return reads || t.Allows("dashboards:read") || t.Allows("vault:manage") || t.Allows("vault:read") || t.Allows("crews:read") || t.Allows("crews:run") || t.Allows("crews:write") || t.Allows("knowledgebase:read") || externalCodeRunAllowed(c)
	case "list_workflow_knowledge", "read_workflow_knowledge":
		// Workflow-authored learnings, notes, and skills are workflow content.
		return t.Allows("files:read")
	default:
		return reads
	}
}

// Only PAT-created conversations are controlled by a PAT. A full-access PAT
// cannot inherit a browser conversation (or one owned by another token).
func accessTokenSessionPrefix(c *UserClaims) string { return "pat-" + c.AccessToken.ID + "-" }

// The external catalog and token-backed Run assistant must enforce the same
// denied tool set. A catalog-only check leaves the tool callable via chat.
func accessTokenRunToolDenied(claims *UserClaims, name string) bool {
	if claims == nil || claims.AccessToken == nil {
		return false
	}
	// An owner's or editor's Builder operation is Builder, not Run.
	if claims.ExternalBuilderOperationID != "" {
		return externalBuilderToolDenied(claims, name)
	}
	for _, denied := range agentworksproduct.RunExternalDenylist() {
		if denied == name {
			return true
		}
	}
	return false
}

type accessTokenSession struct {
	tokenID, sessionID, workspace string
	access                        WorkflowAccessLevel
}
type accessTokenSessionRegistry struct {
	sync.Mutex
	sessions map[string]accessTokenSession
}

func (api *StreamingAPI) cancelAccessTokenSessions(id string) {
	api.accessTokenSessions.Lock()
	sessions := []string{}
	for sid, s := range api.accessTokenSessions.sessions {
		if s.tokenID == id {
			sessions = append(sessions, sid)
			delete(api.accessTokenSessions.sessions, sid)
		}
	}
	api.accessTokenSessions.Unlock()
	for _, sid := range sessions {
		api.cancelSessionRuntimeWork(sid, "access token revoked or expired", runtimePhaseCanceled)
	}
}

func (api *StreamingAPI) watchAccessTokenSession(c *UserClaims, sessionID, workspace string, access WorkflowAccessLevel) {
	api.accessTokenSessions.Lock()
	if api.accessTokenSessions.sessions == nil {
		api.accessTokenSessions.sessions = map[string]accessTokenSession{}
	}
	if _, ok := api.accessTokenSessions.sessions[sessionID]; ok {
		api.accessTokenSessions.Unlock()
		return
	}
	entry := accessTokenSession{c.AccessToken.ID, sessionID, workspace, access}
	api.accessTokenSessions.sessions[sessionID] = entry
	api.accessTokenSessions.Unlock()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			api.accessTokenSessions.Lock()
			_, exists := api.accessTokenSessions.sessions[sessionID]
			api.accessTokenSessions.Unlock()
			if !exists {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			allowed := accessTokenSessionAllowed(ctx, entry)
			cancel()
			if !allowed {
				api.cancelAccessTokenSessions(entry.tokenID)
				return
			}
		}
	}()
}

func accessTokenSessionAllowed(ctx context.Context, entry accessTokenSession) bool {
	if strings.HasPrefix(entry.tokenID, "oauth-") {
		grant, err := mcpOAuthFamilyActive(ctx, strings.TrimPrefix(entry.tokenID, "oauth-"))
		if err != nil {
			return false
		}
		claims, err := accessTokenClaims(mcpOAuthTokenForGrant(grant))
		if err != nil {
			return false
		}
		level, manifest := workflowAccessForWorkspacePath(ctx, claims, entry.workspace)
		return manifest != nil && level == entry.access
	}
	store, err := openAccessTokens()
	if err != nil {
		return false
	}
	defer store.Close()
	token, err := store.Active(ctx, entry.tokenID, time.Now())
	if err != nil {
		return false
	}
	claims, err := accessTokenClaims(token)
	if err != nil {
		return false
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, claims, entry.workspace)
	return manifest != nil && level == entry.access
}
