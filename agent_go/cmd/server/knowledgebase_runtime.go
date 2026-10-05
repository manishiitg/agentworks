package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func knowledgebaseExecute(ctx context.Context, userID string, accessOnly bool, tool string, args map[string]any) (string, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil {
		claims = principalClaims(userID)
	}
	if strings.TrimSpace(userID) == "" || claims.UserID != userID || !knowledgebaseProductAllowed(claims) {
		return "", fmt.Errorf("Knowledge Base is unavailable to this principal")
	}
	if claims.AccessToken != nil {
		store, err := openAccessTokens()
		if err != nil {
			return "", fmt.Errorf("token authority unavailable")
		}
		fresh, err := store.Active(ctx, claims.AccessToken.ID, time.Now())
		_ = store.Close()
		if err != nil {
			return "", fmt.Errorf("connection is expired or revoked")
		}
		copy := *claims
		copy.AccessToken = &fresh
		claims = &copy
		if accessOnly || !externalTokenAllows(claims, externalTool{Name: tool}) || !knowledgebaseConnectionAllowsAction(claims, tool, args) {
			return "", fmt.Errorf("connection does not allow this operation")
		}
	}
	service, err := knowledgebaseService()
	if err != nil {
		return "", fmt.Errorf("Knowledge Base service unavailable")
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
		return "", fmt.Errorf("access tool requires the Knowledge Base profile")
	}
	action, _ := args["action"].(string)
	if knowledgebase.ToolActionMutates("manage_knowledgebase_access", action) {
		return knowledgeProposeAccess(ctx, runtime.UserID, args)
	}
	return knowledgebaseExecute(ctx, runtime.UserID, true, "manage_knowledgebase_access", args)
}

// The Files AI actions use the existing product chat and a narrow Git tool.
func knowledgebaseBackupExecutor(ctx context.Context, runtime agentprofiles.ToolRuntimeContext, args map[string]any) (string, error) {
	if runtime.Product != "knowledgebase" {
		return "", fmt.Errorf("Git tool requires the Knowledge Base profile")
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
	if err != nil || len(project.Bindings) == 0 {
		return tools, executors, categories
	}
	canWrite := false
	for _, binding := range project.Bindings {
		if binding.Access == "write" {
			canWrite = true
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
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, sessionIDs[0])
			return knowledgebaseExecute(ctx, userID, false, def.Name, args)
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
	canMigrate := claims != nil && claims.AccessToken != nil && (claims.AccessToken.BuilderAccess() || claims.AccessToken.Allows("crews:write"))
	defs := knowledgebase.ExternalConnectionToolDefinitions(canWrite, canManage, canMigrate)
	for _, def := range defs {
		if def.Name == tool.Name {
			tool.InputSchema, tool.Description, tool.mutates = def.InputSchema, def.Description, def.Mutates
			break
		}
	}
	return tool
}
