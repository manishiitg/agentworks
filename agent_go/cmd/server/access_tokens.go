package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	root, err := workflowCLIStateRoot()
	if err != nil {
		return nil, err
	}
	docs, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(docs, root)
	if err != nil {
		return nil, err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("access token state must be outside workspace documents")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if actualDocs, e := filepath.EvalSymlinks(docs); e == nil {
		docs = actualDocs
	}
	rel, err = filepath.Rel(docs, resolved)
	if err != nil {
		return nil, err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("access token state must be outside workspace documents")
	}
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return nil, err
	}
	// Bind storage to this server authority even if two local instances use
	// the same default state root. Secret rotation invalidates previous PATs.
	authority := sha256.Sum256(GetAuthSecret())
	return accesstokens.Open(filepath.Join(resolved, "auth", hex.EncodeToString(authority[:16])+".sqlite"))
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
	if !((r.Method == "GET" && r.URL.Path == "/api/external/v1/devices/connect") || (r.Method == "GET" && r.URL.Path == "/api/external/v1/tools") || (r.Method == "POST" && r.URL.Path == "/api/external/v1/call") || ((r.Method == "GET" || r.Method == "HEAD") && r.URL.Path == "/api/external/v1/files/content") || (r.URL.Path == externalMCPPath && (r.Method == "POST" || r.Method == "GET" || r.Method == "DELETE")) || (r.Method == "GET" && (r.URL.Path == "/api/external/v1/skill.md" || r.URL.Path == "/api/external/v1/skill.zip" || r.URL.Path == "/api/external/v1/agentworks.plugin"))) {
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
		if req.LocalFullAccess {
			if IsMultiUserMode() || c.UserID != GetDefaultUserID() {
				externalError(w, 403, "forbidden", "Full local tokens are available only to the local single-user account.")
				return
			}
			// Derive permissions from the current account and enabled products;
			// a client cannot choose a different identity or broaden folder grants.
			req.Name = "agentworks-local"
			req.Scopes = mcpOAuthScopesFor(c, mcpOAuthScopes)
			req.AllWorkflows, req.AllCrews = true, true
			req.WorkflowIDs, req.CrewIDs = nil, nil
			req.KnowledgebaseFolders, req.KnowledgebaseIdentityID = nil, ""
		}
		if !req.LocalFullAccess && (req.ExpiresInDays < 1 || req.ExpiresInDays > 90) {
			externalError(w, 400, "invalid_arguments", "Choose an expiry between 1 and 90 days.")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			req.Name = "Access token"
		}
		now := time.Now()
		t := accesstokens.Token{Name: req.Name, UserID: c.UserID, Username: c.Username, Email: c.Email, Provider: c.Provider, Scopes: req.Scopes, FileGuard: req.FileGuard, WorkflowIDs: req.WorkflowIDs, AllWorkflows: req.AllWorkflows, CrewIDs: req.CrewIDs, AllCrews: req.AllCrews, KnowledgebaseFolders: req.KnowledgebaseFolders, KnowledgebaseIdentityID: req.KnowledgebaseIdentityID, ExpiresAt: now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)}
		if req.LocalFullAccess {
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
		if t.Allows("code:review") && !currentUserCanReviewCode(r) {
			externalError(w, 403, "forbidden", "code:review is for admins and Code reviewers.")
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
					allowed[wf.Manifest.ID] = !(t.Allows("builder:chat") || t.Allows("relays:write") || t.Allows("files:write")) || role == WorkflowAccessOwner || role == WorkflowAccessWrite
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
			externalError(w, 503, "token_creation_failed", "Could not create token; at most 100 active tokens are allowed.")
			return
		}
		// Legacy connections replace only other legacy connections. Knowledge
		// base tokens coexist and are revoked explicitly by their owner.
		for _, old := range previous {
			if !t.KnowledgebaseAccess() && !t.Allows("devices:connect") && !old.KnowledgebaseAccess() && !old.Allows("devices:connect") && old.RevokedAt == nil && old.ExpiresAt.After(now) {
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
	if c == nil {
		return false
	}
	if tool.actions != nil {
		return externalMergedAllows(c, tool)
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
	// also needs code:review; both are re-checked on every call.
	if isExternalCodeReviewTool(tool.Name) {
		return claimsCanReviewCode(c) && (c.AccessToken == nil || c.AccessToken.Allows("code:review"))
	}
	if isExternalCodeRunTool(tool.Name) {
		return externalCodeRunAllowed(c)
	}
	if isExternalTokenLimitTool(tool.Name) {
		return externalTokenLimitAllowed(c, tool.Name)
	}
	if tool.Name == "create_workflow" {
		return externalWorkflowCreationAllowed(c)
	}
	if strings.HasPrefix(tool.Name, "builder_") {
		return externalBuilderEnabled() && c.AccessToken != nil && (c.AccessToken.BuilderAccess() || c.AccessToken.RelayBuilderAccess())
	}
	if isExternalRelayAuthoringTool(tool.Name) {
		return externalBuilderEnabled() && c.AccessToken != nil && (c.AccessToken.RelayBuilderAccess() || tool.Name != "create_relay" && c.AccessToken.BuilderAccess())
	}
	if tool.Name == "write_file" {
		return c.AccessToken != nil && c.AccessToken.Allows("files:write") && c.AccessToken.Allows("files:read") && c.AccessToken.Allows("workflows:read") && userAccessForClaims(c).CanEdit
	}
	if c.AccessToken == nil {
		return true
	}
	t := c.AccessToken
	if isExternalCrewTool(tool.Name) {
		switch tool.Name {
		case "call_crew_function", "ask_crew", "reply_crew_function_call", "suggest_crew_change":
			return t.Allows("crews:run")
		case "get_crew_function_call":
			return t.Allows("crews:read") || t.Allows("crews:run")
		case "write_crew_file":
			// Anyone who runs a Crew may drop a file in their own shared/<id>/ folder (crews:run); writing anywhere else
			// in the Crew is the owner's and needs crews:write (checked with the path).
			return t.Allows("crews:write") || t.Allows("crews:run")
		case "create_crew", "update_crew", "import_crew":
			return t.Allows("crews:write")
		}
		return t.Allows("crews:read")
	}
	if tool.plan {
		return t.Allows("plan:write")
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
	case "list_files", "read_file", "search_files", "list_step_code", "get_file_link":
		return t.Allows("files:read")
	case "write_file", "patch_file":
		return t.Allows("files:write")
	case "run_relay", "get_relay_run":
		return t.Allows("runs:execute")
	case "get_relay_releases":
		return t.Allows("workflows:read")
	case "query_database", "manage_messaging":
		return reads || t.Allows("crews:read")
	case "manage_crew_chats":
		return t.Allows("crews:read") || t.Allows("crews:run")
	case "get_settings", "update_settings", "manage_schedules", "manage_triggers", "list_needs_you", "answer_needs_you", "manage_project":
		// Workflow or Crew settings: each target rechecks its own scope and role.
		return reads || t.Allows("crews:read") || t.Allows("crews:write")
	case "get_agent_context", "list_guidance_topics", "get_guidance_topic", "get_skill":
		// Canonical server-owned guidance carries no workflow content.
		return reads || t.Allows("dashboards:read") || t.Allows("vault:manage") || t.Allows("vault:read")
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
	if externalBuilderToolDenied(claims, name) {
		return true
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
