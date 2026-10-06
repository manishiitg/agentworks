package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func knowledgebaseExecute(ctx context.Context, userID string, accessOnly bool, tool string, args map[string]any) (string, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil || strings.TrimSpace(userID) == "" || claims.UserID != userID || !knowledgebaseMCPAllowed(claims) {
		// Agents and connections act within the person's folder roles and a bound folder; the app product is not needed.
		return "", fmt.Errorf("Brain is unavailable to this principal")
	}
	if caller, _ := ctx.Value(common.UserIDKey).(string); caller != "" && caller != claims.UserID {
		return "", fmt.Errorf("Brain caller identity conflicts with authenticated claims")
	}
	userID = claims.UserID
	if claims.AccessToken != nil {
		// Local tokens and OAuth MCP connections ("oauth-" IDs) are verified in their own stores.
		live, err := activeExternalGrantClaims(ctx, claims.AccessToken.ID)
		if err != nil || live == nil || live.AccessToken == nil || live.UserID != claims.UserID {
			return "", fmt.Errorf("connection is expired or revoked")
		}
		copy := *claims
		copy.AccessToken = live.AccessToken
		claims = &copy
		if accessOnly || !externalTokenAllows(claims, externalTool{Name: tool}) || !knowledgebaseConnectionAllowsAction(claims, tool, args) {
			return "", fmt.Errorf("connection does not allow this operation")
		}
	}
	service, err := knowledgebaseService()
	if err != nil {
		return "", fmt.Errorf("Brain service unavailable")
	}
	if err = knowledgebaseSyncIdentities(ctx, service); err != nil {
		return "", err
	}
	r := (&http.Request{}).WithContext(context.WithValue(ctx, UserContextKey, claims))
	p := knowledgebasePrincipal(r, claims)
	p.AccessOnly = accessOnly
	result, err := knowledgebaseDispatch(r.Context(), service, p, userID, tool, args)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func knowledgebaseAccessExecutor(ctx context.Context, runtime agentprofiles.ToolRuntimeContext, args map[string]any) (string, error) {
	if runtime.Product != "knowledgebase" {
		return "", fmt.Errorf("access tool requires the Brain profile")
	}
	action, _ := args["action"].(string)
	if knowledgebase.ToolActionMutates("manage_knowledgebase_access", action) {
		if knowledgebaseChatActsDirectly(action, args) {
			if pat, _ := args["pat"].(string); pat != "" {
				return "", fmt.Errorf("a token must not be sent through chat: ask the person to add it under Secrets and pass its name as pat_secret")
			}
			if err := knowledgebase.ValidateToolArguments("manage_knowledgebase_access", args); err != nil {
				return "", err
			}
			if claims := GetUserFromContext(ctx); claims == nil || claims.UserID != runtime.UserID || !knowledgeInteractiveAccess(claims) || !knowledgebaseProductAllowed(claims) {
				return "", fmt.Errorf("interactive Brain access required")
			}
			return knowledgebaseExecute(ctx, runtime.UserID, true, "manage_knowledgebase_access", args)
		}
		return knowledgeProposeAccess(ctx, runtime.UserID, args)
	}
	return knowledgebaseExecute(ctx, runtime.UserID, true, "manage_knowledgebase_access", args)
}

// knowledgebaseChatActsDirectly says which Brain-chat changes run when the person asks for them, like any other
// product's chat, instead of becoming a proposal they approve in a card (owner, 2026-10-06: the chat should be agentic).
// The card stays for granting or revoking access and service accounts: note text is untrusted and must not be able to
// widen access on its own. A Git token never passes through the chat: the person adds it under Secrets and backup
// setup names that secret (pat_secret).
func knowledgebaseChatActsDirectly(action string, args map[string]any) bool {
	switch action {
	case "bind_project", "unbind_project", "set_project_access":
		return true
	case "configure_backup":
		return true
	}
	return false
}

// The Files AI actions use the existing product chat and a narrow Git tool.
func knowledgebaseBackupExecutor(ctx context.Context, runtime agentprofiles.ToolRuntimeContext, args map[string]any) (string, error) {
	if runtime.Product != "knowledgebase" {
		return "", fmt.Errorf("Git tool requires the Brain profile")
	}
	action, _ := args["action"].(string)
	if action != "git" && action != "status" {
		return "", fmt.Errorf("This chat supports only Files Git operations and backup status")
	}
	return knowledgebaseExecute(ctx, runtime.UserID, false, "backup_knowledgebase", args)
}

// Bound workflow/Crew operations use the same domain boundary as external agents.
// The dedicated access builder's allowlist excludes every one of these tools.
func createKnowledgebaseTools(userID string, sessionIDs ...string) ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	var tools []llmtypes.Tool
	executors := map[string]interface{}{}
	categories := map[string]string{}
	if !productEnabled("knowledgebase") {
		return tools, executors, categories
	}
	if len(sessionIDs) == 0 || strings.TrimSpace(sessionIDs[0]) == "" {
		return tools, executors, categories
	}
	workspace := ""
	if cfg := common.GetSessionShellConfig(sessionIDs[0]); cfg != nil {
		workspace = cfg.WorkflowPath
		if workspace == "" {
			workspace = cfg.WorkingDir
		}
	}
	// Initial registration precedes shell configuration in the workflow builder.
	// This path comes from the authorized request, never a tool argument.
	if len(sessionIDs) > 1 && sessionIDs[1] != "" {
		workspace = sessionIDs[1]
	}
	project, err := knowledgeProjectLoad(context.Background(), userID, workspace, false)
	mode := ""
	if err == nil {
		mode = project.BrainMode()
	}
	// An owner's tool pool exists while Brain is off so a Builder session can turn it on or bind a folder and use it
	// at once; the runtime policy refuses every call while it is off, and it is what limits "read" to reading.
	if err != nil || mode == "off" && !containsID(project.Owners, userID) {
		return tools, executors, categories
	}
	canWrite := mode == "write" || mode == "off" && containsID(project.Owners, userID)
	if mode == "folders" {
		for _, binding := range project.Bindings {
			if binding.Access == "write" {
				canWrite = true
			}
		}
	}
	if cfg := common.GetSessionShellConfig(sessionIDs[0]); cfg != nil && (cfg.ReadOnlyAccess || cfg.CrewReader || cfg.Env["SHARED_KB_STEP_ACCESS"] == "read" || cfg.Env["SHARED_KB_STEP_ACCESS"] == "none" || cfg.Env["WORKFLOW_KB_ACCESS"] == "none") {
		canWrite = false
	}
	for _, def := range knowledgebase.ConnectionToolDefinitions(canWrite) {
		def := def
		encoded, _ := json.Marshal(def.InputSchema)
		params := new(llmtypes.Parameters)
		if err := json.Unmarshal(encoded, params); err != nil {
			continue
		}
		tools = append(tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: def.Name, Description: def.Description, Parameters: params}})
		executors[def.Name] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			// Registration identity is only a consistency check. A reused tool
			// table must never supply the authority for an unauthenticated caller.
			claims := GetUserFromContext(ctx)
			if claims == nil || claims.UserID != userID {
				return "", fmt.Errorf("Brain tool requires its authenticated caller")
			}
			// A step must retain its own policy, rather than the parent's write policy.
			session, _ := ctx.Value(common.ChatSessionIDKey).(string)
			if session == "" {
				session = executor.SessionIDFromContext(ctx)
			}
			if session == "" {
				return "", fmt.Errorf("Brain tool requires an authenticated tool session")
			}
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, session)
			return knowledgebaseExecute(ctx, claims.UserID, false, def.Name, args)
		}
		categories[def.Name] = "knowledgebase"
	}
	return tools, executors, categories
}

func knowledgebaseConnectionAllowsAction(claims *UserClaims, tool string, args map[string]any) bool {
	if claims == nil || claims.AccessToken != nil && !claims.AccessToken.Allows("knowledgebase:read") {
		return false
	}
	action, _ := args["action"].(string)
	if tool == "manage_knowledgebase_access" && action != "inspect" {
		if claims.ExecutionPrincipal != nil || !knowledgeInteractiveAccess(claims) && claims.AccessToken == nil {
			return false
		}
		// Content-scoped connections and service execution cannot acquire access
		// administration by supplying an action that discovery omitted.
		if claims.AccessToken != nil && (!claims.AccessToken.Allows("knowledgebase:write") || claims.AccessToken.KnowledgebaseFolders != nil) {
			return false
		}
		switch action {
		case "inspect_project", "bind_project", "unbind_project", "set_project_access":
			return claims.AccessToken == nil || claims.AccessToken.BuilderAccess() ||
				claims.AccessToken.Allows("crews:read") && claims.AccessToken.Allows("crews:write")
		case "list", "grant", "revoke", "create_service_account", "disable_service_account", "configure_backup":
			return true
		default:
			return false
		}
	}
	return claims.AccessToken == nil || !knowledgebase.ToolActionMutates(tool, action) || claims.AccessToken.Allows("knowledgebase:write")
}

// Mixed-action tools remain discoverable by readers, with read-only schemas.
func knowledgebaseToolForClaims(claims *UserClaims, tool externalTool) externalTool {
	if !isExternalKnowledgebaseTool(tool.Name) {
		return tool
	}
	canWrite := claims != nil && (claims.AccessToken == nil || claims.AccessToken.Allows("knowledgebase:write"))
	canManage := knowledgebaseConnectionAllowsAction(claims, "manage_knowledgebase_access", map[string]any{"action": "grant"})
	canMigrate := claims != nil && claims.AccessToken != nil && (claims.AccessToken.BuilderAccess() ||
		claims.AccessToken.Allows("crews:read") && claims.AccessToken.Allows("crews:write"))
	defs := knowledgebase.ExternalConnectionToolDefinitions(canWrite, canManage, canMigrate)
	for _, def := range defs {
		if def.Name == tool.Name {
			tool.InputSchema, tool.Description, tool.mutates = def.InputSchema, def.Description, def.Mutates
			break
		}
	}
	return tool
}

// knowledgebaseCallerLine states who is chatting and whether they are an administrator, for the Brain chat's prompt.
// The server still decides every request; this only stops the model asking a person to confirm what it can be told.
func knowledgebaseCallerLine(userID string) string {
	rec := directoryUserFor(userID, "", "")
	if rec == nil {
		if !IsMultiUserMode() {
			return "The person you are talking with is an administrator (single-user install)."
		}
		return "The person you are talking with has no account record, so treat them as a non-administrator."
	}
	if acc := accessForRecord(rec); acc.Admin && !acc.Disabled {
		return "The person you are talking with is an administrator: do not ask them to confirm it, and go straight to the details you need."
	}
	return "The person you are talking with is not an administrator: tell them administrator-only steps (backup setup, service accounts) need an administrator."
}
