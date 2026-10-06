package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Like Vault setup authority, this comes only from the authenticated root
// Builder turn. Child agents and workflow steps never inherit it.
type knowledgeProjectBuilderKey struct{}
type knowledgeProjectBuilderAuthority struct{ UserID, Workspace, Session string }

func knowledgeProjectBuilderQuery(req QueryRequest, claims *UserClaims, authoritySession, toolSession string, readOnly bool) bool {
	// The admission decision, not AgentMode: handleQuery rewrites workflow_phase to multi-agent before tools run
	// (PLAT-600, PLAT-608).
	return req.admittedWorkflowPhase && req.PhaseID == "workflow-builder" &&
		!readOnly && authoritySession == toolSession && req.BotPlatform == "" &&
		(req.TriggeredBy == "" || req.TriggeredBy == "external") && claims != nil &&
		claims.ExecutionPrincipal == nil && (knowledgeInteractiveAccess(claims) ||
		claims.AccessToken != nil && claims.AccessToken.BuilderAccess())
}

func knowledgeProjectExecute(ctx context.Context, userID, workspace, tool string, args map[string]any) (string, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID != userID || !knowledgebaseProductAllowed(claims) ||
		!knowledgebaseConnectionAllowsAction(claims, knowledgebase.ToolAccess, map[string]any{"action": "bind_project"}) ||
		workflowAccessForClaims(claims) == WorkflowAccessRead {
		return "", &knowledgebase.Error{Code: "FORBIDDEN", Message: "Project setup authority is required"}
	}
	project, err := knowledgeProjectLoad(ctx, userID, workspace, true)
	if err != nil {
		return "", err
	}
	if err := knowledgeProjectConnectionCheck(ctx, project); err != nil {
		return "", err
	}
	copy := make(map[string]any, len(args)+1)
	for k, v := range args {
		copy[k] = v
	}
	if tool == knowledgebase.ToolAccess && knowledgebase.IsProjectAction(fmt.Sprint(copy["action"])) {
		if value, ok := copy["workspace_path"]; ok && value != workspace {
			return "", &knowledgebase.Error{Code: "FORBIDDEN", Message: "Use the current workflow workspace"}
		}
		copy["workspace_path"] = workspace
	} else if tool != knowledgebase.ToolBrowse || copy["action"] != "folders" || copy["binding_alias"] != nil {
		return knowledgebaseExecute(ctx, userID, false, tool, copy)
	}
	service, err := knowledgebaseService()
	if err != nil {
		return "", err
	}
	if err := knowledgebaseSyncIdentities(ctx, service); err != nil {
		return "", err
	}
	r := (&http.Request{}).WithContext(ctx)
	p := knowledgebasePrincipal(r, claims)
	var result any
	if tool == knowledgebase.ToolBrowse {
		// Only caller-authorized folder metadata is available before attachment.
		result, err = service.CallTool(ctx, p, tool, copy)
	} else {
		p.AccessOnly = true
		result, err = knowledgebaseDispatch(ctx, service, p, userID, tool, copy)
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}

func knowledgeProjectBuilderExecute(ctx context.Context, tool string, args map[string]any) (string, error) {
	authority, ok := ctx.Value(knowledgeProjectBuilderKey{}).(knowledgeProjectBuilderAuthority)
	if !ok || executor.SessionIDFromContext(ctx) != authority.Session {
		chat, _ := ctx.Value(common.ChatSessionIDKey).(string)
		log.Printf("[KB_PROJECT] refused %s: authority=%v authority_session=%q executor_session=%q chat_session=%q", tool, ok, authority.Session, executor.SessionIDFromContext(ctx), chat)
		return "", fmt.Errorf("Only the current workflow Builder can configure knowledge bindings")
	}
	// A step may invoke a parent's registered HTTP bridge; the actual caller
	// remains in ChatSessionIDKey and must not acquire root Builder authority.
	if session, _ := ctx.Value(common.ChatSessionIDKey).(string); session != "" && session != authority.Session {
		return "", fmt.Errorf("Steps cannot configure knowledge bindings")
	}
	return knowledgeProjectExecute(ctx, authority.UserID, authority.Workspace, tool, args)
}

func createKnowledgeProjectBuilderTools() ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	tools := []llmtypes.Tool{}
	execs := map[string]interface{}{}
	categories := map[string]string{}
	if !productEnabled("knowledgebase") {
		return tools, execs, categories
	}
	for _, def := range knowledgebase.ProjectToolDefinitions() {
		def := def
		data, _ := json.Marshal(def.InputSchema)
		params := new(llmtypes.Parameters)
		if json.Unmarshal(data, params) != nil {
			continue
		}
		tools = append(tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: def.Name, Description: def.Description, Parameters: params}})
		execs[def.Name] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			return knowledgeProjectBuilderExecute(ctx, def.Name, args)
		}
		categories[def.Name] = "knowledgebase"
	}
	return tools, execs, categories
}

// UI project selection shares the same domain action as MCP and Builder.
// It never writes content or grants folder access.
func (api *StreamingAPI) handleKnowledgebaseProject(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) || !knowledgeInteractiveAccess(claims) {
		externalError(w, http.StatusForbidden, "FORBIDDEN", "Interactive project access is required.")
		return
	}
	if r.Method == http.MethodGet {
		project, err := knowledgeProjectLoad(r.Context(), claims.UserID, r.URL.Query().Get("workspace_path"), false)
		if err != nil {
			knowledgebaseHTTPError(w, err)
			return
		}
		if !containsID(project.Audience, claims.UserID) {
			externalError(w, http.StatusForbidden, "FORBIDDEN", "Project access is required.")
			return
		}
		knowledgebaseWriteJSON(w, map[string]any{"manifest_version": project.Version, "brain_access": project.BrainMode(), "shared_knowledgebase": project.Bindings, "can_manage": containsID(project.Owners, claims.UserID) && workflowAccessForClaims(claims) != WorkflowAccessRead})
		return
	}
	args := map[string]any{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&args); err != nil {
		externalError(w, 400, "INVALID_ARGUMENT", "Invalid project request.")
		return
	}
	action, _ := args["action"].(string)
	if action != "bind_project" && action != "unbind_project" && action != "set_project_access" {
		externalError(w, 400, "INVALID_ARGUMENT", "Use bind_project, unbind_project or set_project_access.")
		return
	}
	workspace, _ := args["workspace_path"].(string)
	result, err := knowledgeProjectExecute(r.Context(), claims.UserID, workspace, knowledgebase.ToolAccess, args)
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(result))
}

// isKnowledgeProjectTool names the Builder's project-scoped Brain tools.
func isKnowledgeProjectTool(name string) bool {
	return name == knowledgebase.ToolBrowse || name == knowledgebase.ToolAccess
}
