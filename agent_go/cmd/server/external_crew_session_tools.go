package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// A session comes only from this caller's live Crew conversation registry.
// Neither a supplied session ID nor a Crew owner's chat establishes authority.
func (api *StreamingAPI) externalCrewSessionToolsCall(w http.ResponseWriter, r *http.Request, args map[string]any, crew crewProjectBinding) {
	claims := GetUserFromContext(r.Context())
	id, action := externalArg(args, "crew_id"), externalArg(args, "action")
	chat, found := findCrewChat(api.crewChats(r, claims.UserID, id, crew.Binding.WorkspacePath), externalArg(args, "chat_id"))
	if !found || !chat.CanWrite {
		externalError(w, 404, "chat_not_found", "Pass an explicit chat_id from your own live chat list.")
		return
	}
	api.activeSessionsMux.RLock()
	active := api.activeSessions[chat.SessionID]
	wrongOwner := active != nil && active.UserID != claims.UserID
	api.activeSessionsMux.RUnlock()
	if wrongOwner {
		externalError(w, 404, "chat_not_found", "This session is not owned by the caller.")
		return
	}
	if claims.AccessToken != nil && claims.AccessToken.FileGuard != nil {
		externalError(w, 403, "insufficient_scope", "Session tools require unrestricted Crew file consent.")
		return
	}
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	reg := crewCapabilityRegistrar{}
	if agentprofiles.HasFeature(profile, "workspace-ui") {
		if err = api.registerOpenWorkWorkspaceViewTool(reg, claims.UserID, chat.SessionID, crew.Binding.WorkspacePath, crew.OwnedByCaller); err != nil {
			externalFailure(w, err)
			return
		}
	}
	api.lastQueryMu.RLock()
	parent, initialized := api.lastQueryRequests[chat.SessionID]
	api.lastQueryMu.RUnlock()
	root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
	initialized = initialized && registerWorkUIAllowed(parent) && canonicalChatHistoryWorkspacePath(claims.UserID, parent.SelectedFolder) == canonicalChatHistoryWorkspacePath(claims.UserID, root)
	if agentprofiles.HasFeature(profile, "background-work") && api.bgAgentRegistry != nil {
		api.sessionAgentsMux.RLock()
		wrapper := api.sessionAgents[chat.SessionID]
		api.sessionAgentsMux.RUnlock()
		if wrapper != nil {
			err = api.registerWorkBackgroundToolSet(reg, parent, chat.SessionID, claims.UserID, wrapper.AttachedSkills())
		} else {
			err = api.registerWorkBackgroundToolSet(reg, parent, chat.SessionID, claims.UserID, nil)
		}
		if err != nil {
			externalFailure(w, err)
			return
		}
	}
	canStart := crew.OwnedByCaller && initialized
	if token := claims.AccessToken; token != nil {
		canStart = canStart && token.Allows("crews:write")
	}
	if status, _ := externalCrewAuthorGate(claims); status != 0 {
		canStart = false
	}
	if !canStart {
		delete(reg, "run_in_background")
	}
	definitions := []mcpagent.ToolDefinition{}
	for name, tool := range reg {
		definitions = append(definitions, mcpagent.ToolDefinition{Name: name, Description: tool.description, InputSchema: tool.schema, Execute: tool.execute})
	}
	projected, err := platformtools.Project(definitions)
	if err != nil {
		externalFailure(w, err)
		return
	}
	reg = crewCapabilityRegistrar{}
	for _, tool := range projected {
		reg[tool.Name] = crewCapabilityTool{description: tool.Description, schema: tool.InputSchema, execute: tool.Execute}
	}
	if action == "tools" {
		inventory := []map[string]any{}
		for name, tool := range reg {
			inventory = append(inventory, map[string]any{"name": name, "description": tool.description, "input_schema": tool.schema})
		}
		externalJSON(w, map[string]any{"crew_id": id, "chat_id": chat.ChatID, "tools": inventory, "background_initialized": initialized, "next": "Background starts require current editable ownership, crews:write and an initialized manual chat. Results use the same native chat notifications. Browser actions require an attached browser and return its receipt; unavailable is not success."})
		return
	}
	name := externalArg(args, "tool_name")
	tool, ok := reg[name]
	if !ok {
		externalError(w, 403, "tool_unavailable", "This native session tool is unavailable for the current role or chat; inspect action=tools.")
		return
	}
	params, _ := args["arguments"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	validator, err := jsonschemaCompiler(tool.schema)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if err = validator.Validate(params); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	if crewSessionToolIdentity(name, params) == platformtools.Identity("run_in_background") {
		// Never inherit a retained website chat's broader filesystem grants.
		// Every non-root grant must still be approved AND covered by this token.
		cfg := common.GetSessionShellConfig(chat.SessionID)
		if cfg == nil || cfg.CrewReader || cfg.ReadOnlyAccess || cfg.WorkflowReadOnly {
			externalError(w, 409, "chat_uninitialized", "Open this own Crew chat first to initialize its native sandbox.")
			return
		}
		folderReads, folderWrites, _, _ := workFolderGuardInputs(r.Context())
		check := func(paths, folders []string, write bool) bool {
			for _, p := range paths {
				p = strings.TrimSuffix(p, "/")
				if p == root || strings.HasPrefix(p, root+"/") {
					continue
				}
				approved := false
				if claims.AccessToken == nil || claims.AccessToken.AllCrews {
					for _, f := range folders {
						approved = approved || p == strings.TrimSuffix(f, "/") || strings.HasPrefix(p, strings.TrimSuffix(f, "/")+"/")
					}
				}
				if !approved && !api.externalCrewReferenceAllowed(r.Context(), claims, p, write) {
					return false
				}
			}
			return true
		}
		if !check(cfg.ReadPaths, folderReads, false) || !check(cfg.WritePaths, folderWrites, true) {
			externalError(w, 403, "insufficient_scope", "This chat has attached grants outside this connection's consent. Use a connection covering those grants.")
			return
		}
	}
	connection := "interactive"
	if claims.AccessToken != nil {
		connection = claims.AccessToken.ID
	}
	audit, auditID, err := startCrewExecutionAudit(r.Context(), claims.UserID, id, connection, "crew_session", crewSessionToolIdentity(name, params), params)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer audit.Close()
	result, err := tool.execute(r.Context(), params)
	if finish := finishCrewExecutionAudit(audit, auditID, result, err); finish != nil {
		externalError(w, 503, "audit_completion_unavailable", "Session action may have completed; inspect your chat and owner history before retrying.")
		return
	}
	if err != nil {
		externalError(w, 400, "execution_rejected", err.Error())
		return
	}
	externalJSON(w, map[string]any{"crew_id": id, "chat_id": chat.ChatID, "result": externalCrewSessionResult(result)})
}

func jsonschemaCompiler(schema map[string]any) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("crew-session.json", schema); err != nil {
		return nil, err
	}
	return compiler.Compile("crew-session.json")
}

func externalCrewSessionResult(result string) any {
	var value any
	if json.Unmarshal([]byte(result), &value) != nil {
		return result
	}
	return value
}
