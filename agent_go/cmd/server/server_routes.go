package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	orchtypes "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/types"
	"github.com/manishiitg/mcpagent/executor"
)

// newServerRouter builds the router with its middleware and the routes registered before
// the bot manager and scheduler start. The order of registration is the order of matching.
func (api *StreamingAPI) newServerRouter() (*mux.Router, *mux.Router) {
	router := mux.NewRouter()

	// CORS middleware
	router.Use(api.corsMiddleware)

	// Auth middleware - applies to all API routes
	// Note: AuthMiddleware handles skipping auth for public endpoints (login, register, health, shared)
	router.Use(AuthMiddleware)
	api.registerVaultOAuthRoutes(router)

	// API routes
	apiRouter := router.PathPrefix("/api").Subrouter()
	apiRouter.Use(api.apiRequestLogMiddleware)
	apiRouter.Use(agentBridgeAPIMiddleware)
	apiRouter.Use(api.crewChatRelayMiddleware)
	apiRouter.HandleFunc("/crews/{project}/panel", api.handleCrewPanel).Methods("GET")
	apiRouter.HandleFunc(strings.TrimPrefix(dashboardAppFramePath, "/api"), api.dashboardAppFrame).Methods("GET")
	apiRouter.HandleFunc("/bullpen/agents", api.handleBullpenAgents).Methods("GET")
	apiRouter.HandleFunc("/agent-directory", api.handleAgentDirectory).Methods("GET")
	apiRouter.HandleFunc("/agent-message-identity", api.handleAgentMessageIdentity).Methods("GET")
	apiRouter.HandleFunc("/bullpen/agents/usage", api.handleBullpenAgentUsage).Methods("GET")
	apiRouter.HandleFunc("/bullpen/usage", api.handleBullpenUsage).Methods("GET", "DELETE")
	apiRouter.HandleFunc("/external/v1/agents/usage", api.handleAgentUsageReport).Methods("POST")
	apiRouter.HandleFunc("/bullpen/shares", api.handleBullpenShares).Methods("GET")
	apiRouter.HandleFunc("/bullpen/shares/{id}", api.handleBullpenShareRevoke).Methods("DELETE")
	apiRouter.HandleFunc("/bullpen/notices", api.handleBullpenNotices).Methods("GET")
	apiRouter.HandleFunc("/bullpen/notices/{id}", api.handleBullpenNoticeDismiss).Methods("DELETE")
	apiRouter.HandleFunc("/bullpen/admin/agents", api.handleBullpenAdmin).Methods("GET")
	apiRouter.HandleFunc("/bullpen/admin/audit", api.handleBullpenAdmin).Methods("GET")
	// A browser connection changed: wake that user's open tabs so the toolbar refetches the status instead of polling it.
	browserrelay.SetChangeHook(func(user, _ string) { livefeed.Default.PublishToUser(livefeed.Browser, user, "") })
	apiRouter.PathPrefix("/caplayer/").HandlerFunc(api.handleCapLayerAdmin)
	apiRouter.PathPrefix("/server-products/").HandlerFunc(api.handleServerProduct)
	common.ScopeAgentMCP = api.scopeAgentMCP
	clisecurity.ResolveWorkflowPolicy = api.resolveWorkflowCLIPolicy
	router.HandleFunc("/internal/vault/mcp", api.handleVaultRuntimeMCP).Methods("GET", "POST", "DELETE")
	router.HandleFunc("/internal/server-vault/mcp/{conn}/{id}", api.handleServerVaultMCP).Methods("GET", "POST", "DELETE")
	router.HandleFunc("/internal/caplayer/oauth-token", api.handleCapLayerOAuthToken).Methods("POST")

	// Authentication API routes (public - no auth required, handled by AuthMiddleware)
	apiRouter.HandleFunc("/auth/register", api.handleRegister).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/login", api.handleLogin).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/logout", api.handleLogout).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/me", api.handleGetCurrentUser).Methods("GET")
	apiRouter.HandleFunc("/account/avatar", api.handleUserAvatar).Methods("GET", "PUT")
	apiRouter.HandleFunc("/account/avatars/{user}", api.handleUserAvatarImage).Methods("GET")
	apiRouter.HandleFunc("/auth/mode", api.handleGetAuthMode).Methods("GET")
	apiRouter.HandleFunc("/client-telemetry/chat-delivery", api.handleClientChatTelemetry).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/users", requireWorkflowOwnerAccess(api.handleListAuthUsers)).Methods("GET", "OPTIONS")
	// Account management (docs/design/user_accounts_and_workflow_sharing.md):
	// the user directory in config/users.json; admins only.
	apiRouter.HandleFunc("/auth/password", api.handleChangeOwnPassword).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/access-tokens", api.handleAccessTokens).Methods("GET", "POST")
	apiRouter.HandleFunc("/auth/access-tokens/{id}", api.handleAccessTokens).Methods("DELETE")
	apiRouter.HandleFunc("/oauth/mcp/register", api.handleMCPOAuthRegister).Methods("POST")
	apiRouter.HandleFunc("/oauth/mcp/authorize", api.handleMCPOAuthAuthorize).Methods("GET")
	apiRouter.HandleFunc("/oauth/mcp/token", api.handleMCPOAuthToken).Methods("POST")
	apiRouter.HandleFunc("/oauth/mcp/consent", api.handleMCPOAuthConsent).Methods("GET", "POST")
	apiRouter.HandleFunc("/oauth/mcp/project", api.handleProjectMCPSetup).Methods("GET")
	apiRouter.HandleFunc("/oauth/mcp/connections", api.handleMCPOAuthConnections).Methods("GET")
	apiRouter.HandleFunc("/oauth/mcp/connections/{id}", api.handleMCPOAuthConnections).Methods("DELETE")
	apiRouter.HandleFunc("/oauth/cli/device", api.handleCLIOAuthDevice).Methods("POST")
	apiRouter.HandleFunc("/oauth/cli/consent", api.handleCLIOAuthConsent).Methods("GET", "POST")
	apiRouter.HandleFunc("/oauth/cli/token", api.handleCLIOAuthToken).Methods("POST")
	apiRouter.HandleFunc("/oauth/cli/revoke", api.handleCLIOAuthRevoke).Methods("POST")
	// Per-workflow ownership and sharing (workflow_access.go).
	apiRouter.HandleFunc("/users/directory", api.handleUserDirectory).Methods("GET", "OPTIONS")
	api.registerCrewTeamRoutes(apiRouter)
	apiRouter.HandleFunc("/me/token-usage", api.handleMyTokenUsage).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/users", requireAdmin(api.handleAdminListUsers)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/crew-storage", requireAdmin(api.handleCrewStorage)).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/admin/agent-processes", requireAdmin(api.handleAgentProcessStats)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/users", requireAdmin(api.handleAdminCreateUser)).Methods("POST")
	apiRouter.HandleFunc("/admin/users/{id}", requireAdmin(api.handleAdminUpdateUser)).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/admin/users/{id}", requireAdmin(api.handleAdminDeleteUser)).Methods("DELETE")
	// Multi-provider OAuth routes
	apiRouter.HandleFunc("/auth/start", api.handleAuthStart).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/callback", api.handleAuthCallback).Methods("GET")
	apiRouter.HandleFunc("/crew-relay/connect", api.handleCrewRelayConnect).Methods("GET")
	log.Printf("[TEAM_ONLINE] event=retired_path_disabled path=mac_dashboard_snapshot")
	log.Printf("[TEAM_ONLINE] event=retired_path_disabled path=standby_copy reason=published_team_agent")
	log.Printf("[TEAM_ONLINE] event=retired_path_disabled path=member_server_shadow_tabs reason=member_mac_pairs")
	apiRouter.HandleFunc("/crew-sync-capabilities", handleCrewSyncCapabilities).Methods("GET")
	apiRouter.HandleFunc("/client-events", handleClientEvent).Methods("POST")
	apiRouter.HandleFunc("/crew-sync/{project}", api.handleCrewSync).Methods("GET", "POST")
	apiRouter.HandleFunc("/work/mac-downloads", api.handleMacDownloadsAccess).Methods("GET", "PUT")
	apiRouter.HandleFunc("/work/remote-links", api.handleRemoteProjectLinks).Methods("GET", "POST")
	loadMacDownloadsAccess()
	apiRouter.HandleFunc("/desktop-connections/{connection}/crew-sync/{project}", api.handleDesktopCrewSync).Methods("GET", "POST")
	apiRouter.HandleFunc("/desktop-connections", api.handleDesktopConnections).Methods("GET", "POST")
	apiRouter.HandleFunc("/desktop-connections/sign-in/{state}", api.handleDesktopSignInStatus).Methods("GET")
	apiRouter.HandleFunc("/desktop-connections/{connection}/inventory", api.handleDesktopConnectionInventory).Methods("GET")
	apiRouter.HandleFunc("/desktop-connections/{connection}/member-crew/{project}", api.handleDesktopMemberCrew).Methods("GET")
	apiRouter.HandleFunc("/desktop-connections/{connection}/provider-accounts", api.handleDesktopProviderAccountCopy).Methods("POST")
	apiRouter.HandleFunc("/desktop-connections/{connection}/provider-accounts", api.handleDesktopProviderAccountStatus).Methods("GET")
	apiRouter.HandleFunc("/desktop-connections/overview", api.handleDesktopOverview).Methods("GET")
	apiRouter.HandleFunc("/desktop-connections/{connection}/token-usage", api.handleDesktopTokenUsage).Methods("GET")
	apiRouter.HandleFunc("/model-proxy/{provider}/v1/{path:.+}", api.handleCompanyModelProxy).Methods("GET", "POST")
	apiRouter.HandleFunc("/model-proxy/{provider}/catalog", api.handleCompanyModelCatalog).Methods("GET")
	apiRouter.HandleFunc("/model-proxy/{provider}/mac-app", api.handleModelProxyMacApp).Methods("GET", "POST")
	apiRouter.HandleFunc("/model-proxy/cursor-cli/relay/{path:.+}", api.handleCursorRelay)
	apiRouter.HandleFunc("/desktop-model-proxy/{account}/cursor/{path:.+}", api.handleDesktopCursorRelay)
	for provider, path := range companyProxyCatalogs {
		router.HandleFunc(path, api.handleDesktopModelCatalog(provider)).Methods("GET")
	}
	apiRouter.HandleFunc("/desktop-model-proxy/{account}/v1/{path:.+}", api.handleDesktopModelProxy).Methods("GET", "POST")
	apiRouter.HandleFunc("/personal-model-proxy/{account}/v1/{path:.+}", api.handlePersonalModelProxy).Methods("GET", "POST")
	apiRouter.HandleFunc("/desktop-connections/{connection}", api.handleDesktopConnections).Methods("DELETE")
	apiRouter.PathPrefix("/connections/{connection}/{path:.*}").HandlerFunc(api.handleDesktopConnectionProxy)
	apiRouter.HandleFunc("/auth/desktop/connect", api.handleDesktopConnect).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/desktop/exchange", api.handleDesktopConnectExchange).Methods("POST", "OPTIONS")

	apiRouter.HandleFunc("/query", api.handleQuery).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/product-tips", api.handleProductTips).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/product-tips/next", api.handleProductTipNext).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/product-tips/preference", api.handleProductTipPreference).Methods("GET", "PUT", "OPTIONS")
	AgentProfileRoutes(apiRouter, api.agentProfiles)
	apiRouter.HandleFunc("/agent-profiles/{id}/query", api.handleAgentProfileChatQuery).Methods("POST", "OPTIONS")
	for _, endpoint := range []string{"bootstrap", "folders", "entries", "read", "search", "access", "backup", "changes", "diff"} {
		apiRouter.HandleFunc("/knowledgebase/"+endpoint, api.handleKnowledgebaseViewer).Methods("GET", "OPTIONS")
	}
	apiRouter.HandleFunc("/knowledgebase/project", api.handleKnowledgebaseProject).Methods("GET", "POST", "OPTIONS")
	brainSecretsAPI = api
	apiRouter.HandleFunc("/knowledgebase/seen", handleBrainSeen).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/knowledgebase/secrets", api.handleBrainSecrets).Methods("GET", "PUT", "OPTIONS")
	apiRouter.HandleFunc("/knowledgebase/secrets/{name}", api.handleBrainSecrets).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/knowledgebase/access-proposals", api.handleKnowledgebaseAccessProposals).Methods("GET", "POST")
	apiRouter.HandleFunc("/knowledgebase/manage", api.handleKnowledgebaseManagement).Methods("POST")
	apiRouter.PathPrefix("/knowledgebase/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalError(w, http.StatusNotFound, "NOT_FOUND", "Brain endpoint not found.")
	})
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation", api.handleResolveAgentProfileConversation).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation/new", api.handleRotateAgentProfileConversation).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation/switch", api.handleSwitchAgentProfileConversation).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation/close", api.handleCloseAgentProfileSideChat).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/side-chats", api.handleListAgentProfileSideChats).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/welcome", api.handleProjectWelcome).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation/fork", api.handleForkCrewChat).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation/rewind", api.handleRewindCrewChat).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/side-chats/order", api.handleOrderAgentProfileSideChats).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/side-chats/pin", api.handlePinAgentProfileSideChat).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/side-chats/fork-parent", api.handleAgentForkParent).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/side-chats/groups", api.handleAgentGroups).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/chat-models", api.handleAgentProfileChatModels).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/chat-context", api.handleChatContext).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/chat-context/summary", api.handleChatCompactionSummary).Methods("GET")
	apiRouter.HandleFunc("/chat-context/part", api.handleChatContextPart).Methods("GET")
	apiRouter.HandleFunc("/agent-profiles/work/local-project", api.handleLocalProject).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversations", api.handleListAgentProfileConversations).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversations/{session_id}", api.handleDeleteAgentProfileConversation).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}", api.handleDeleteAgentProfileProject).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/uploads", api.handleCrewChatUpload).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/chat-context", api.handleCrewChatContext).Methods("POST")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/uploads/{step:plan|chunks}", api.handleCrewChatUpload).Methods("POST", "PUT", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/shared-projects", api.handleListSharedProjects).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/reserve", api.handleReserveAgentProfileProject).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/location", api.handleProjectCreationLocation).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects", api.handleCreateAgentProfileProject).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/own-shared-projects", api.handleListOwnSharedProjects).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/project-sharing", api.handleGetProjectSharing).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/sharing", api.handlePutProjectSharing).Methods("PUT", "OPTIONS")
	// Admin inspection of Code (read-only, audited; admin checked in-handler).
	apiRouter.HandleFunc("/admin/code/workspaces", api.handleAdminListCodeWorkspaces).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/files", api.handleAdminCodeFiles).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/file", api.handleAdminCodeFile).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/audit", api.handleAdminCodeAudit).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/mcp", api.handleAdminCodeMCP).Methods("GET", "OPTIONS")
	// A Code workspace's plain shell: sandboxed, one per person with editor access.
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/shell/stream", api.handleCodeShellStream).Methods("GET")
	codeShellReaperOnce.Do(func() { go codeShellReaper() })
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/shell/stop", api.handleCodeShellStop).Methods("POST", "OPTIONS")
	// Pins of Code chats whose Code is gone, once the workspace is up.
	go func() {
		time.Sleep(3 * time.Minute)
		api.sweepOrphanCodeSessionPins(context.Background())
	}()
	// The catalog of servers that can be connected, and connections of a place.
	apiRouter.HandleFunc("/mcp/catalog", api.handleMCPCatalog).Methods("GET")
	apiRouter.HandleFunc("/vault/connections", api.handleMyVaultServers).Methods("GET")
	apiRouter.HandleFunc("/vault/local-mcp", api.handleLocalMCPImport).Methods("GET", "POST")
	api.registerVaultMeRoutes(apiRouter)
	apiRouter.HandleFunc("/computer-use/status", api.handleComputerUseSetupStatus).Methods("GET")
	apiRouter.HandleFunc("/coding-agent-setup", api.handleCodingAgentSetup).Methods("GET", "POST")
	apiRouter.HandleFunc("/vault/github", api.handleGitHubVault).Methods("GET", "DELETE")
	apiRouter.HandleFunc("/vault/github/connect", api.handleGitHubVault).Methods("POST")
	apiRouter.HandleFunc("/vault/github/complete", api.handleGitHubVault).Methods("POST")
	apiRouter.HandleFunc("/vault/migration", requireAdmin(api.handleVaultMigration)).Methods("POST")
	// The old path, kept for a browser tab still running the previous frontend.
	// Sign-in apps (Google, GitHub, ...): set up once by an admin.
	apiRouter.HandleFunc("/admin/mcp-apps", requireAdmin(api.handleListMCPApps)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/mcp-apps/{key}", requireAdmin(api.handlePutMCPApp)).Methods("PUT", "DELETE", "OPTIONS")
	apiRouter.HandleFunc("/integrations/slack/accounts", api.handlePersonalSlackAccounts).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/integrations/slack/bots", api.handleSlackVaultBots).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/shared-projects/{project_id}/files", api.handleListSharedProjectFiles).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/shared-projects/{project_id}/file", api.handleGetSharedProjectFile).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/work/folders", api.handleListWorkFolders).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/work/folders", api.handleAddWorkFolder).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/work/folders/{id}", api.handleDeleteWorkFolder).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/health", api.handleHealth).Methods("GET")
	apiRouter.HandleFunc("/capabilities", api.handleCapabilities).Methods("GET")
	apiRouter.HandleFunc("/cdp-check", api.handleCdpCheck).Methods("GET")
	apiRouter.HandleFunc("/downloads/cli/{file}", api.handleCliDownload).Methods("GET")
	apiRouter.HandleFunc("/llm-config/defaults", api.handleGetLLMDefaults).Methods("GET")
	apiRouter.HandleFunc("/llm-config/discovery", api.handleDiscoverLLMSetup).Methods("GET")
	apiRouter.HandleFunc("/llm-config/models/metadata", api.handleGetModelMetadata).Methods("GET")
	apiRouter.HandleFunc("/llm-config/azure/deployments", api.handleGetAzureDeployedModels).Methods("POST")
	apiRouter.HandleFunc("/llm-config/validate-key", api.handleValidateAPIKey).Methods("POST")
	apiRouter.HandleFunc("/llm-config/providers", api.handleGetProviderManifest).Methods("GET")
	apiRouter.HandleFunc("/llm-config/providers/{provider}/models", api.handleGetProviderModels).Methods("GET")
	apiRouter.HandleFunc("/provider-connections", api.handleProviderConnections).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/share-targets", api.handleProviderShareTargets).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/{connectionID}/status", api.handleProviderAccountStatus).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/{connectionID}/sign-out", api.handleProviderAccountSignOut).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/{connectionID}/server-login", api.handleServerLogin).Methods("GET", "POST")
	apiRouter.HandleFunc("/provider-connections/{connectionID}/move-to-server", api.handleMoveLoginToServer).Methods("POST")
	apiRouter.HandleFunc("/server-login/move-in", api.handleServerLoginMoveIn).Methods("POST")
	apiRouter.HandleFunc("/provider-accounts/product-defaults", api.handleProductDefaults).Methods("GET", "PUT", "OPTIONS")
	apiRouter.HandleFunc("/provider-accounts/costs", api.handleProviderAccountCosts).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-accounts/limits", api.handleAccountLimits).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-accounts/sign-in-needed", api.handleAccountSignInNeeded).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/{connectionID}", api.handleProviderConnection).Methods("PATCH", "DELETE", "OPTIONS")
	apiRouter.HandleFunc("/byok/{action}", api.handleByok).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/provider-setup/sessions", api.handleStartProviderSetup).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/provider-setup/sessions/{id}", api.handleProviderSetupSession).Methods("GET", "DELETE", "OPTIONS")
	apiRouter.HandleFunc("/provider-setup/sessions/{id}/stream", api.handleProviderSetupStream).Methods("GET")
	apiRouter.HandleFunc("/session/cancel-turn", api.handleCancelCurrentTurn).Methods("POST")
	apiRouter.HandleFunc("/session/stop", api.handleStopSession).Methods("POST")
	apiRouter.HandleFunc("/session/clear", api.handleClearSession).Methods("POST")

	// Tool management routes (from tools.go)
	apiRouter.HandleFunc("/tools", api.handleGetTools).Methods("GET")
	apiRouter.HandleFunc("/tools/detail", api.handleGetToolDetail).Methods("GET")
	apiRouter.HandleFunc("/tools/add", requireAdmin(api.handleAddServer)).Methods("POST")
	apiRouter.HandleFunc("/tools/edit", requireAdmin(api.handleEditServer)).Methods("POST")
	apiRouter.HandleFunc("/tools/remove", requireAdmin(api.handleRemoveServer)).Methods("POST")

	// Tool execution APIs - handlers provided by mcpagent/executor library
	// Pass server logger for proper debugging of session registry usage
	executorHandlers := executor.NewExecutorHandlers(api.mcpConfigPath, api.logger)
	executorHandlers.SetMCPServerResolver(api.resolveMCPServer)

	apiRouter.HandleFunc("/mcp/execute", api.requireOwnBodySession(executorHandlers.HandleMCPExecute)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/custom/execute", api.requireOwnBodySessionAlways(executorHandlers.HandleCustomExecute)).Methods("POST", "OPTIONS")

	// Per-tool endpoints for code execution mode (bearer token auth, bypasses JWT)
	// LLM-generated code calls these directly, so they use API token auth instead of JWT.
	//
	// NOTE: The system prompt tool index lists custom tool categories (e.g. workspace_advanced)
	// and virtual tool categories (e.g. workflow) alongside real MCP servers. Claude Code agents
	// call them all via /tools/mcp/{server}/{tool}. The routeMCPRequest helper detects these
	// categories and redirects to the correct handler (custom or virtual).
	routeMCPRequest := func(w http.ResponseWriter, r *http.Request, server, tool string) {
		// Global bridge URLs carry the session in X-Session-ID. Preserve it in
		// context as well as the request header so workspace tools can resolve
		// session-scoped folder guards, working directories, and read-only host
		// grants (for example the native Chrome Downloads directory).
		if sid := strings.TrimSpace(r.Header.Get("X-Session-ID")); sid != "" {
			r = r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, sid))
		}
		if isMCPBridgeCustomToolCategory(server) {
			if r.Method == http.MethodPost && refuseReaderDeniedToolRequest(w, r, r.Context(), tool) {
				return
			}
			log.Printf("[ROUTE] Redirecting /tools/mcp/%s/%s → custom tool handler", server, tool)
			executorHandlers.HandlePerToolCustomRequest(w, r, tool)
			return
		}
		if isMCPBridgeVirtualToolCategory(server) {
			log.Printf("[ROUTE] Redirecting /tools/mcp/%s/%s → virtual tool handler", server, tool)
			executorHandlers.HandlePerToolVirtualRequest(w, r, tool)
			return
		}
		// A guest's free-text ask uses no MCP server, the platform's Vault ones included (856-59).
		if cfg := common.GetSessionShellConfig(strings.TrimSpace(r.Header.Get("X-Session-ID"))); cfg != nil && cfg.CrewAskReadOnly {
			writeUsersJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "MCP tools are not available in a read-only ask: call one of the project's functions instead."})
			return
		}
		ctx, realServer, realTool, err := api.vaultBridgeToolName(r.Context(), strings.TrimSpace(r.Header.Get("X-Session-ID")), server, tool)
		if err != nil {
			writeUsersJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": err.Error()})
			return
		}
		executorHandlers.HandlePerToolMCPRequest(w, r.WithContext(ctx), realServer, realTool)
		api.recordMCPBridgeCall(strings.TrimSpace(r.Header.Get("X-Session-ID")), server, tool)
	}

	// `mcpbridge secret NAME` from a coding CLI's shell: its own read-only token, outside the tool routes (which refuse
	// that token), session_value_read.go.
	router.HandleFunc("/bridge/secret-read", api.handleSessionSecretRead).Methods("POST")
	toolsRouter := router.PathPrefix("/tools").Subrouter()
	toolsRouter.Use(bridgeAuthMiddleware(api.apiToken))
	toolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		routeMCPRequest(w, r, vars["server"], vars["tool"])
	}).Methods("POST", "OPTIONS")
	toolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		log.Printf("[GLOBAL_ROUTE_DEBUG] Global custom tool request: tool=%s url=%s x-session-id=%s", vars["tool"], r.URL.Path, r.Header.Get("X-Session-ID"))
		if sid := strings.TrimSpace(r.Header.Get("X-Session-ID")); sid != "" {
			r = r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, sid))
		}
		if r.Method == http.MethodPost && refuseReaderDeniedToolRequest(w, r, r.Context(), vars["tool"]) {
			return
		}
		logCustomToolDuration(vars["tool"], strings.TrimSpace(r.Header.Get("X-Session-ID")), func() {
			withReadOnlyShellHint(w, r.Context(), vars["tool"], func(out http.ResponseWriter) {
				executorHandlers.HandlePerToolCustomRequest(out, r, vars["tool"])
			})
		})
	}).Methods("POST", "OPTIONS")
	toolsRouter.HandleFunc("/virtual/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		executorHandlers.HandlePerToolVirtualRequest(w, r, vars["tool"])
	}).Methods("POST", "OPTIONS")

	// Session-scoped per-tool endpoints: /s/{session_id}/tools/...
	// These routes bake the session_id into the URL path, so the LLM-generated code
	// doesn't need to explicitly include session_id in request bodies.
	// The session_id is extracted from the path and injected as X-Session-ID header,
	// which the per-tool handler reads as a fallback when body session_id is empty.
	sessionToolsRouter := router.PathPrefix("/s/{session_id}/tools").Subrouter()
	sessionToolsRouter.Use(bridgeAuthMiddleware(api.apiToken))
	// A coding CLI that does not stream (AGY) shows its work only through the
	// bridge calls it makes. Count each one, at start and at end, as session
	// progress so the turn-inactivity wait does not cancel a turn that is
	// busy running tools (a bot turn cancelled after 10 min, PLAT-861-206).
	sessionToolsRouter.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sid := mux.Vars(r)["session_id"]
			if r.Method == http.MethodPost && sid != "" {
				api.updateSessionActivity(sid)
				defer api.updateSessionActivity(sid)
			}
			next.ServeHTTP(w, r)
		})
	})
	sessionToolsRouter.HandleFunc("/browser/packages/{package}", api.handlePlaywrightPackage).Methods("GET")
	sessionToolsRouter.HandleFunc("/browser/live", api.handlePlaywrightPublisher).Methods("GET")
	sessionToolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		sid := vars["session_id"]
		r.Header.Set("X-Session-ID", sid)
		// A Watcher check reads Google through gog, read-only, and a page in Chrome as a backup (watcher_reads.go), and never
		// reaches platform tools through an MCP-style URL.
		if r.Method == http.MethodPost && (api.serveWatcherGoogle(w, r, sid, vars["server"]) || api.serveWatcherBrowser(w, r, sid, vars["server"])) {
			return
		}
		if (isMCPBridgeCustomToolCategory(vars["server"]) || isMCPBridgeVirtualToolCategory(vars["server"])) && watcherBridgeRefusesPlatformTools(w, sid) {
			return
		}
		// MCP-style URLs can be redirected to custom workspace tools. Those
		// tools resolve folder guards from context, so mirror /tools/custom.
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, sid)
		routeMCPRequest(w, r.WithContext(ctx), vars["server"], vars["tool"])
	}).Methods("POST", "OPTIONS")
	sessionToolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		sid := vars["session_id"]
		tool := vars["tool"]
		if watcherBridgeRefusesPlatformTools(w, sid) {
			return
		}
		log.Printf("[SESSION_ROUTE_DEBUG] Session-scoped custom tool request: session=%s tool=%s url=%s", sid, tool, r.URL.Path)
		r.Header.Set("X-Session-ID", sid)
		// Inject ChatSessionIDKey so execute_shell_command can look up
		// the session's working directory and folder guard from the global map.
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, sid)
		if r.Method == http.MethodPost && refuseReaderDeniedToolRequest(w, r, ctx, tool) {
			return
		}
		logCustomToolDuration(tool, sid, func() {
			withReadOnlyShellHint(w, ctx, tool, func(out http.ResponseWriter) {
				executorHandlers.HandlePerToolCustomRequest(out, r.WithContext(ctx), tool)
			})
		})
	}).Methods("POST", "OPTIONS")
	sessionToolsRouter.HandleFunc("/virtual/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if watcherBridgeRefusesPlatformTools(w, vars["session_id"]) {
			return
		}
		r.Header.Set("X-Session-ID", vars["session_id"])
		executorHandlers.HandlePerToolVirtualRequest(w, r, vars["tool"])
	}).Methods("POST", "OPTIONS")

	// MCP Config API routes (from mcp_config_routes.go)
	apiRouter.HandleFunc("/mcp-config", requireAdmin(api.handleGetMCPConfig)).Methods("GET")
	apiRouter.HandleFunc("/mcp-config", requireAdmin(api.handleSaveMCPConfig)).Methods("POST")
	apiRouter.HandleFunc("/mcp-config/discover", requireAdmin(api.handleDiscoverServers)).Methods("POST")
	apiRouter.HandleFunc("/mcp-config/status", requireAdmin(api.handleGetMCPConfigStatus)).Methods("GET")
	apiRouter.HandleFunc("/mcp-config/logs", requireAdmin(api.handleGetServerLogs)).Methods("GET")

	// Connector connection state (from oauth_routes.go)

	// Secrets encryption API routes (from secrets_routes.go)
	apiRouter.HandleFunc("/secrets/encrypt", api.handleEncryptSecret).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/secrets/decrypt", api.handleDecryptSecret).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/vault/company-keys", api.handleCompanyKeys).Methods("GET", "POST")
	apiRouter.HandleFunc("/secrets/global", api.handleGetGlobalSecrets).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/my-vaults/op", api.handleMyVaultsOp).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/secrets/global", api.handleManageGlobalSecret).Methods("PUT", "DELETE")
	apiRouter.HandleFunc("/secrets/global/reveal", api.handleRevealGlobalSecret).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/secrets/workflow/store", api.handleStoreWorkflowSecret).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/secrets/workflow/store/{name}", api.handleDeleteWorkflowSecret).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/secrets/workflow/sources", api.handleSecretSources).Methods("GET", "OPTIONS")

	// Provider API keys (encrypted file storage for scheduled runs)
	apiRouter.HandleFunc("/provider-keys", api.handleSaveProviderKeys).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/provider-keys", api.handleLoadProviderKeys).Methods("GET", "OPTIONS")

	// Published LLMs (workspace-backed JSON storage)
	apiRouter.HandleFunc("/published-llms", api.handleSavePublishedLLMs).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/published-llms", api.handleLoadPublishedLLMs).Methods("GET", "OPTIONS")

	// OAuth API routes (from oauth_routes.go)
	apiRouter.HandleFunc("/oauth/start", api.handleOAuthStart).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/oauth/callback", api.handleOAuthCallback).Methods("GET")
	apiRouter.HandleFunc("/oauth/complete", api.handleOAuthReturnComplete).Methods("POST")
	apiRouter.HandleFunc("/oauth/status", api.handleOAuthStatus).Methods("GET")
	apiRouter.HandleFunc("/oauth/logout", api.handleOAuthLogout).Methods("POST", "OPTIONS")

	// Observer APIs removed - events are now stored by sessionID, no observers needed

	// Browser APIs
	apiRouter.HandleFunc("/browser/workspace", api.handleWorkspaceBrowser).Methods("GET", "POST")
	apiRouter.HandleFunc("/browser/extension", api.handleBrowserExtension).Methods("GET", "POST")
	apiRouter.HandleFunc("/browser/extension/connect", api.handleBrowserExtensionConnect).Methods("GET")
	apiRouter.HandleFunc("/browser/extension/setup", api.handleBrowserPairingSetup).Methods("GET")
	apiRouter.HandleFunc("/browser/extension/pairing/peek", api.handleBrowserPairingExchange).Methods("POST")
	apiRouter.HandleFunc("/browser/extension/pairing/redeem", api.handleBrowserPairingExchange).Methods("POST")
	apiRouter.HandleFunc("/downloads/chrome-extension.zip", api.handleBrowserExtensionDownload).Methods("GET")
	apiRouter.HandleFunc("/browser/live/sessions", api.handleLiveBrowserSessions).Methods("GET")
	apiRouter.HandleFunc("/browser/live/{session}/stream", api.handleLiveBrowserStream).Methods("GET")
	apiRouter.HandleFunc("/browser/live/{session}/recording", api.handleBrowserRecording).Methods("GET", "POST")

	// Active Session API routes (from polling.go)
	apiRouter.HandleFunc("/sessions/active", api.handleGetActiveSessions).Methods("GET")
	// Combined poll for the app header (ModePresetBar + GlobalActivityMonitor):
	// active sessions + workflow schedule counts in one round trip.
	apiRouter.HandleFunc("/header-summary", api.handleGetHeaderSummary).Methods("GET")
	apiRouter.HandleFunc("/home", api.handleHome).Methods("GET")
	apiRouter.HandleFunc("/workspace-access", api.handleWorkspaceAccess).Methods("GET")
	apiRouter.HandleFunc("/workspace-state", api.handleWorkspaceReturnState).Methods("GET", "PUT")
	apiRouter.HandleFunc("/appearance", api.handleAppearance).Methods("GET", "PUT")
	apiRouter.HandleFunc("/appearance/videos/{id}", api.handleAppearanceVideo).Methods("GET", "HEAD")
	apiRouter.HandleFunc("/appearance/images/{id}", api.handleAppearanceImage).Methods("GET", "HEAD")
	apiRouter.HandleFunc("/menu-filter", api.handleMenuFilter).Methods("GET", "PUT")
	api.registerNotificationRoutes(apiRouter)
	apiRouter.HandleFunc("/crew-run-problems/{project}", api.handleCrewRunProblems).Methods("GET")       // the owner's missed/failed workflow run cards (#3617)
	apiRouter.HandleFunc("/crew-run-problems/{project}/{id}", api.handleCrewRunProblems).Methods("POST") // one card's button
	apiRouter.HandleFunc("/desktop/notify-stream", api.handleDesktopNotifyStream).Methods("GET")         // the Mac app's own process: its person's sign-in and start secret
	apiRouter.HandleFunc("/sessions/{session_id}/events", api.handleGetSessionEvents).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/chat-artifacts/{artifact_id}", api.handleGetChatArtifact).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/helper-events", api.handleGetSessionHelperEvents).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/linked-helpers", api.handleGetSessionLinkedHelpers).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/ui-control", api.handleUIControl).Methods("POST")
	apiRouter.HandleFunc("/sessions/{session_id}/events/stream", api.handleSSEStream).Methods("GET")
	// Header + right-pane live stream (change notices only; chat keeps its own streams).
	apiRouter.HandleFunc("/live", api.handleLiveFeed).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/reconnect", api.handleReconnectSession).Methods("POST")
	apiRouter.HandleFunc("/sessions/{session_id}/status", api.handleGetSessionStatus).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/instructions", api.handleGetSessionInstructions).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/tool-changes", api.handleSessionToolChanges).Methods("GET", "POST")
	apiRouter.HandleFunc("/sessions/{session_id}/usage-messages", api.handleUsageTurnMessages).Methods("GET")
	// The product raw view receives only its owning chat's main terminal. All
	// child-pane enumeration and controls remain behind runtime diagnostics.
	apiRouter.HandleFunc("/sessions/{session_id}/main-terminal", api.handleGetMainTerminal).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/sessions/{session_id}/main-terminal/stream", api.handleMainTerminalStream).Methods("GET")
	// The chat's own background work and a per-item Stop (PLAT-705); owner-scoped, not diagnostics-only.
	apiRouter.HandleFunc("/sessions/{session_id}/queued-messages", api.handleQueuedConversationMessages).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/queued-messages/{message_id}", api.handleRemoveQueuedConversationMessage).Methods("DELETE")
	apiRouter.HandleFunc("/sessions/{session_id}/queued-messages/{message_id}/steer", api.handleSteerQueuedConversationMessage).Methods("POST")
	apiRouter.HandleFunc("/sessions/{session_id}/queued-messages/{message_id}/retry", api.handleRetryQueuedConversationMessage).Methods("POST")
	apiRouter.HandleFunc("/sessions/{session_id}/background-work", api.handleGetSessionBackgroundWork).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/background-work/{execution_id}/inspect", api.handleInspectSessionExecution).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/background-work/{execution_id}/stop", api.handleStopSessionBackgroundWork).Methods("POST")
	apiRouter.HandleFunc("/sessions/{session_id}/subagents/{tool_call_id}", api.handleGetSessionSubagent).Methods("GET")
	apiRouter.HandleFunc("/workflow/step-inspections", api.handleWorkspaceStepInspections).Methods("GET")
	apiRouter.HandleFunc("/workspace/run-evidence", api.handleWorkspaceRunEvidence).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/execution-tree", api.runtimeDiagnosticsHandler(api.handleGetSessionExecutionTree)).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/activity-tree", api.runtimeDiagnosticsHandler(api.handleGetSessionActivityTree)).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/dismiss", api.handleDismissSession).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals", api.runtimeDiagnosticsHandler(api.handleListTerminals)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/events", api.runtimeDiagnosticsHandler(api.handleGetTerminalEvents)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}", api.runtimeDiagnosticsHandler(api.handleGetTerminal)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}", api.runtimeDiagnosticsHandler(api.handleDismissTerminal)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/complete", api.runtimeDiagnosticsHandler(api.handleCompleteTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/fail", api.runtimeDiagnosticsHandler(api.handleFailTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/refresh", api.runtimeDiagnosticsHandler(api.handleRefreshTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/kill", api.runtimeDiagnosticsHandler(api.handleKillTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/input", api.runtimeDiagnosticsHandler(api.handleSendTerminalInput)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/key", api.runtimeDiagnosticsHandler(api.handleSendTerminalKey)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/resize", api.runtimeDiagnosticsHandler(api.handleResizeTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/size-hint", api.runtimeDiagnosticsHandler(api.handleTerminalSizeHint)).Methods("POST", "OPTIONS")
	// Live-attach (control-mode) WebSocket transport for the selected live tmux
	// terminal. Inert (404) only if tmux is too old; see terminal_live_attach.go.
	apiRouter.HandleFunc("/terminals/{terminal_id}/stream", api.runtimeDiagnosticsHandler(api.handleTerminalStream)).Methods("GET")

	// Streaming speech-to-text (agentprofiles.RuntimeCapabilities.Voice). Gated
	// per-request by profile_id inside the handler, not by whether this route is
	// registered — mirroring how Browser/Secrets stay one shared implementation
	// that products opt into via product.yaml rather than owning a copy.
	apiRouter.HandleFunc("/voice/stream", api.handleVoiceStream).Methods("GET")
	// First-run setup for the mic: the model is a one-time ~690MB download
	// that the composer asks about explicitly instead of the first click
	// silently waiting on it. status is what the progress bar polls.
	apiRouter.HandleFunc("/voice/status", api.handleVoiceStatus).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/voice/warm", api.handleVoiceWarm).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/voice/unload", api.handleVoiceUnload).Methods("POST", "OPTIONS")
	// Load the engine at server startup, not on the first mic click: loading
	// takes ~1-2s, and a user clicking before it finished used to sit looking
	// at a silent button. Only when the model is already on disk, though — a
	// machine that never used voice is not made to download 690MB just
	// because the server started; the composer offers that as an explicit
	// first-time step (/voice/warm above).
	if svc := currentVoiceService(); svc.Status().Installed {
		svc.Warm()
	}

	// LLM Guidance API routes
	apiRouter.HandleFunc("/sessions/{session_id}/llm-guidance", api.handleSetLLMGuidance).Methods("POST", "OPTIONS")

	apiRouter.HandleFunc("/sessions/{session_id}/live-input", api.handleLiveInputMessage).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/sessions/{session_id}/coding-agent-question/answer", api.handleCodingAgentQuestionAnswer).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/sessions/{session_id}/muse-question/answer", api.handleCodingAgentQuestionAnswer).Methods("POST", "OPTIONS") // Existing clients.
	apiRouter.HandleFunc("/sessions/{session_id}/control", api.handleControlKey).Methods("POST", "OPTIONS")

	// Human Feedback API
	apiRouter.HandleFunc("/human-feedback/submit", api.handleSubmitHumanFeedback).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/human-feedback/pending", api.handleListPendingHumanFeedback).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/team", api.handleWorkflowTeam).Methods("GET", "PUT")

	// Workflow running-session API (decoupled from chat session storage).
	apiRouter.HandleFunc("/workflow/running", api.handleListRunningWorkflows).Methods("GET")
	apiRouter.HandleFunc("/workflow/running/{session_id}", api.handleUpdateRunningWorkflow).Methods("PATCH", "OPTIONS")

	// Global cost ledger summary.
	apiRouter.HandleFunc("/cost/summary", api.handleCostSummary).Methods("GET")
	apiRouter.HandleFunc("/cost/overview", api.handleCostOverview).Methods("GET")

	// Inspector debug panel — opt-in per-session timeline of
	// structured InspectorEvents emitted by the LLM adapters. See
	// inspector_routes.go and internal/inspector/store.go.
	apiRouter.HandleFunc("/inspector", requireAdmin(api.handleInspectorSessions)).Methods("GET")
	apiRouter.HandleFunc("/inspector/{session_id}", api.handleInspectorEvents).Methods("GET")
	apiRouter.HandleFunc("/inspector/{session_id}", api.handleInspectorClear).Methods("DELETE")

	// Chat history (read-only, persisted to workspace)
	ChatHistoryRoutes(apiRouter, api)

	// Slack Feedback API routes
	SlackFeedbackRoutes(apiRouter, api)

	// Slack connection registry (per-workflow Slack apps)
	SlackConnectionRoutes(apiRouter, api)

	// Gmail (outbound-only) config/status/test API routes
	GmailFeedbackRoutes(apiRouter, api)
	GmailConnectionRoutes(apiRouter, api)
	GmailOAuthRoutes(apiRouter, api)
	GoogleAppRoutes(apiRouter, api)
	GmailOAuthClientRoutes(apiRouter, api)

	// Per-user notification preferences (Slack channel, WhatsApp number)
	NotificationPreferencesRoutes(apiRouter)
	return router, apiRouter
}

// registerProductRoutes registers the workflow, dashboard, skill and command routes once the
// scheduler and webhook routes are in place.
func (api *StreamingAPI) registerProductRoutes(router, apiRouter *mux.Router) {
	// Workflow API routes
	apiRouter.HandleFunc("/workflow/create", requireWorkflowCreateAccess(api.handleCreateWorkflow)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/update", api.handleUpdateWorkflow).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/constants", orchtypes.HandleWorkflowConstants).Methods("GET")
	apiRouter.HandleFunc("/dashboards", api.handleDashboards).Methods("POST")
	api.registerDashboardQuestionRoutes(apiRouter)
	apiRouter.HandleFunc("/project-ask/conversation", api.handleProjectAskConversation).Methods(http.MethodPost)
	apiRouter.HandleFunc("/chat-search", api.handleChatMessageSearch).Methods(http.MethodPost)
	apiRouter.HandleFunc("/chat-search/message", api.handleChatMessageOpen).Methods(http.MethodPost)
	apiRouter.HandleFunc("/project-chat-instructions", api.handleProjectChatInstructions).Methods(http.MethodGet, http.MethodPut)
	api.registerDashboardFunctionRoutes(apiRouter)
	api.registerDefaultDashboardRoutes(apiRouter)
	api.registerDashboardShortLinkRoutes(apiRouter)
	// Headless report preview data (preview_report tool). The only API paths a
	// report-preview scoped token can reach; see report_preview_routes.go.
	apiRouter.HandleFunc("/workflow/report-preview/query", api.handleReportPreviewQuery).Methods("POST")
	apiRouter.HandleFunc("/workflow/report-preview/media-url", api.handleReportMediaURL).Methods("POST")
	apiRouter.HandleFunc("/workflow/report-preview/upload", api.handleDashboardUpload).Methods("POST")
	apiRouter.HandleFunc("/workflow/report-preview/run", api.handleReportRun).Methods("POST")
	apiRouter.HandleFunc("/workflow/report-media", api.handleReportMediaStream).Methods("GET", "HEAD")

	// Generic AgentWorks chat defaults (skills, servers, secrets, browser).
	MultiAgentConfigRoutes(apiRouter)

	// Workspace API reverse proxy (auth-protected) — frontend calls /api/wp/* instead of /workspace/*
	apiRouter.PathPrefix("/wp/").Handler(workspaceProxyHandler())

	// Focused workflow endpoints used for mutations and incremental refreshes.
	apiRouter.HandleFunc("/workflow/run", requireWorkflowWriteAccess(api.handleWorkflowRun)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/run-folder", api.handleCreateRunFolder).Methods("POST", "OPTIONS")
	// /workflow/progress endpoint removed — steps_done.json progress tracking no longer consumed by frontend
	apiRouter.HandleFunc("/workflow/run-folder", requireWorkflowWriteAccess(api.handleDeleteRunFolder)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflow/variable-groups", requireWorkflowWriteAccess(api.handleUpdateVariableGroups)).Methods("POST", "PUT", "OPTIONS")
	// Workflow data reads, each behind a per-workflow read check.
	registerWorkflowReadRoutes(apiRouter, api)
	apiRouter.HandleFunc("/workflow/logs/webhook-payload", api.handleGetExecutionWebhookPayload).Methods("GET", "OPTIONS")

	// Auto-improvement framework — see docs/workflow/auto_improvement_framework.md

	apiRouter.HandleFunc("/external/v1/devices/connect", api.handleLocalDeviceConnect).Methods("GET")
	apiRouter.HandleFunc("/external/v1/agents/events", api.handleAgentEvents).Methods("GET")
	apiRouter.HandleFunc("/external/v1/agents/attachment", api.handleAgentAttachment).Methods("POST")
	apiRouter.HandleFunc("/external/v1/agents/app", api.handleAppAgentRegistration).Methods("POST")
	apiRouter.HandleFunc("/external/v1/agents/sharing", api.handleAgentSharing).Methods("POST")
	apiRouter.HandleFunc("/external/v1/brain/secret", api.handleBrainSecretSet).Methods("POST")
	apiRouter.HandleFunc("/external/v1/secrets", api.handleSecretsImport).Methods("POST")
	apiRouter.HandleFunc("/devices", api.handleLocalDevices).Methods("GET")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/laptop", api.handleCrewLaptopSettings).Methods("GET", "PUT")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/computer-use", api.handleCrewComputerUse).Methods("GET", "POST")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}/laptop/folders", api.handleLaptopBrowse).Methods("GET")
	apiRouter.HandleFunc("/external/v1/tools", api.handleExternalTools).Methods("GET")
	apiRouter.HandleFunc("/external/v1/chats/terminal", api.handleChatTerminal).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/external/v1/call", api.handleExternalCall).Methods("POST")
	apiRouter.HandleFunc("/external/v1/mcp/tasks", api.handleExternalMCPTasks).Methods("POST")
	apiRouter.HandleFunc("/external/v1/mcp/elicitation", api.handleExternalMCPInput).Methods("POST")
	apiRouter.HandleFunc("/mcp/elicitation/{request_id}/open", api.handleMCPURLSignInOpen).Methods("POST")
	apiRouter.HandleFunc("/workspaces", api.handleWorkspaces).Methods("POST")
	apiRouter.HandleFunc("/external/v1/publication-history", api.handlePublicationHistory).Methods("GET")
	apiRouter.HandleFunc("/external/v1/publications", api.handlePublicationUpload).Methods("POST")
	apiRouter.HandleFunc("/external/v1/publications/{operation}", api.handlePublicationUpload).Methods("GET", "POST")
	apiRouter.HandleFunc("/external/v1/publications/{operation}/{step:plan|chunks}", api.handlePublicationUpload).Methods("POST", "PUT")
	apiRouter.HandleFunc("/external/v1/files/content", api.handleExternalAssetContent).Methods("GET", "HEAD")
	apiRouter.HandleFunc("/external/v1/mcp/project/{kind}/{project}/{access}", api.handleProjectMCP).Methods("POST", "GET", "DELETE")
	apiRouter.HandleFunc("/external/v1/mcp", api.handleExternalMCP).Methods("POST", "GET", "DELETE")
	apiRouter.HandleFunc("/external/v1/skill.md", api.handleExternalSkillMD).Methods("GET")
	apiRouter.HandleFunc("/external/v1/skill.zip", api.handleExternalSkillZIP).Methods("GET")
	apiRouter.HandleFunc("/external/v1/agentworks.plugin", api.handleExternalPlugin).Methods("GET")
	// Dynamic report system. The frontend ReportViewer loads db/reports/index.html
	// directly; HTML pages read durable data through window.report.

	apiRouter.HandleFunc("/crew-executables", api.handleCrewExecutables).Methods("GET", "POST", "PATCH", "DELETE")
	apiRouter.HandleFunc("/crew-dashboards", api.handleCrewDashboards).Methods("GET")
	apiRouter.HandleFunc("/crew-script-file", api.handleCrewScriptFile).Methods("GET", "PUT")
	apiRouter.HandleFunc("/crew-script-graph", api.handleCrewScriptGraph).Methods("GET")
	apiRouter.HandleFunc("/crew-workflow-uses", api.handleCrewWorkflowUses).Methods("GET")
	apiRouter.HandleFunc("/crew-run-files", api.handleCrewRunFiles).Methods("GET")
	apiRouter.HandleFunc("/crew-task-steps", api.handleCrewTaskSteps).Methods("GET")
	apiRouter.HandleFunc("/crew-code-files", api.handleCrewCodeFiles).Methods("GET")
	// Manifest-backed workflow API routes (file-backed workflow definitions)
	apiRouter.HandleFunc("/workflows/manifests", api.handleListWorkflowManifests).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/knowledgebase-sources", api.handleWorkflowKnowledgebaseSources).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest", api.handleGetWorkflowManifest).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest", requireWorkflowCreateAccess(api.handleCreateWorkflowManifest)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest", requireWorkflowWriteAccess(api.handleUpdateWorkflowManifest)).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest", requireWorkflowWriteAccess(api.handleDeleteWorkflowManifest)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflows/folder", requireWorkflowWriteAccess(api.handleDeleteWorkflowFolder)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest/duplicate", requireWorkflowCreateAccess(api.handleDuplicateWorkflowManifest)).Methods("POST", "OPTIONS")

	// Skills API routes (from skill_routes.go)
	RegisterSkillRoutes(apiRouter, api)

	// Note: System skills sync runs inside the workspace Docker container (workspace/skill_sync.go)
	// The backend server only proxies skill API calls to the workspace.

	// Sub-agent template API routes (from subagent_routes.go)
	RegisterSubAgentRoutes(apiRouter, api)

	// User-defined command routes (from command_routes.go)
	RegisterCommandRoutes(apiRouter, api)

	// Public file sharing routes — filepath passed as base64 query param
	apiRouter.HandleFunc("/public/file", api.handlePublicFile).Methods("GET")
	apiRouter.HandleFunc("/public/folder", api.handlePublicFolder).Methods("GET")
	apiRouter.HandleFunc("/public/folder/download", api.handlePublicFolderDownload).Methods("GET")

	// pprof routes for profiling (must be before static file serving). Heap,
	// goroutine and cmdline dumps carry secrets and the server's signing key:
	// administrators only.
	router.PathPrefix("/debug/pprof/").Handler(requireAdmin(http.DefaultServeMux.ServeHTTP))
}

// registerPublicRoutes registers the runtime config, report, dashboard asset and public
// dashboard routes, then the frontend fallback, which must stay last.
func (api *StreamingAPI) registerPublicRoutes(router *mux.Router, actualPort int) {
	// Dynamically serve runtime-config.js so the frontend learns the real ports.
	// In packaged/desktop mode ports are dynamic (--port 0), so the static file's
	// hardcoded values are wrong. Serve same-origin URLs for the agent API and
	// the workspace URL passed via WORKSPACE_API_URL env var.
	router.HandleFunc("/runtime-config.js", func(w http.ResponseWriter, r *http.Request) {
		workspaceURL := os.Getenv("WORKSPACE_API_URL")
		if workspaceURL == "" {
			workspaceURL = fmt.Sprintf("http://localhost:%d", actualPort)
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, runtimeFrontendConfigJS(actualPort, workspaceURL))
	}).Methods("GET")

	// OAuth discovery must resolve before the SPA fallback.
	router.HandleFunc("/.well-known/oauth-protected-resource", api.handleMCPOAuthProtectedResource).Methods("GET")
	router.HandleFunc(mcpOAuthProtectedResourcePath+"/project/{kind}/{project}/{access}", api.handleMCPOAuthProtectedResource).Methods("GET")
	router.HandleFunc(mcpOAuthProtectedResourcePath, api.handleMCPOAuthProtectedResource).Methods("GET")
	router.HandleFunc(mcpOAuthMetadataPath, api.handleMCPOAuthMetadata).Methods("GET")

	// Headless report preview page (preview_report tool): embedded in the
	// binary, so it works wherever the server runs regardless of how the SPA
	// is served. Registered before the SPA catch-all below.
	router.HandleFunc(reportPreviewPagePath, api.reportPreviewPageHandler).Methods("GET")
	router.HandleFunc(reportPreviewPagePath+"report-preview.js", api.reportPreviewScriptHandler).Methods("GET")
	router.HandleFunc("/dashboard-react.js", api.dashboardRuntimeAssetHandler).Methods("GET")
	router.HandleFunc("/dashboard.css", api.dashboardRuntimeAssetHandler).Methods("GET")
	api.registerDashboardPublicRoutes(router)

	// Static file serving (for frontend). Unknown GET/HEAD routes fall back to
	// index.html so dedicated SPA URLs like /report, /file, and /folder work
	// when opened directly in a browser. Defaults to cwd-relative "./static/"
	// STATIC_DIR overrides it, see staticFrontendDir. Source development sends
	// app navigation to the launcher's current frontend instead of old assets.
	log.Printf("[FRONTEND] Bundled static directory: %s", staticFrontendDir())
	router.PathPrefix("/").Handler(spaStaticFileHandler(staticFrontendDir()))
}
