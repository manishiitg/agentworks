package server

import (
	"encoding/json"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const externalCrewCommandsTool = "manage_crew_commands"

// The Crew factory owns both the schema and executor; the external transport
// adds only the project selector and authenticates its role on each call.
func externalCrewCommandDefinition(add func(string, string, bool, bool, map[string]any, ...string)) error {
	tool, err := workproduct.CustomCommandsFactory("", "Crew project")(agentprofiles.ToolRuntimeContext{}, nil)
	if err != nil {
		return err
	}
	// Normalize Go slices to JSON values for the external schema compiler.
	raw, err := json.Marshal(tool.Parameters)
	if err != nil {
		return err
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	props := schema["properties"].(map[string]any)
	props["crew_id"] = externalString("Crew ID returned by crew action=list. Never a filesystem path.")
	add(externalCrewCommandsTool, tool.Description+" Requires Crew read access to list; only its owner can create, update or delete, with crews:write consent. Runs directly without a Builder model turn.", true, false, props, "crew_id", "action")
	return nil
}

func (api *StreamingAPI) externalCrewCommandsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	crewID, action := externalArg(args, "crew_id"), externalArg(args, "action")
	write := action != "list"
	if t := claims.AccessToken; t != nil {
		scope := "crews:read"
		if write {
			scope = "crews:write"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "This connection does not allow "+action+" on this Crew.")
			return
		}
	}
	if write {
		if status, message := externalCrewAuthorGate(claims); status != 0 {
			externalError(w, status, "forbidden", message)
			return
		}
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, crewID)
	if !ok {
		externalError(w, http.StatusNotFound, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	if write {
		if !crew.OwnedByCaller {
			externalError(w, http.StatusForbidden, "forbidden", "Only the Crew's owner can change its custom commands.")
			return
		}
	}
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	// Resolve only the declared command factory. Never accept a workspace or
	// an arbitrary factory/executor name from the caller.
	for _, binding := range profile.Tools {
		if binding.ID != "work.custom-commands" {
			continue
		}
		tool, err := api.agentProfiles.BuildTool(binding, agentprofiles.ToolRuntimeContext{
			UserID: claims.UserID, Product: profile.Product,
			WorkspacePath: agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath),
		})
		if err != nil {
			externalFailure(w, err)
			return
		}
		params := make(map[string]any, len(args)-1)
		for key, value := range args {
			if key != "crew_id" {
				params[key] = value
			}
		}
		result, err := tool.Execute(r.Context(), params)
		if err != nil {
			externalError(w, http.StatusBadRequest, "command_rejected", err.Error())
			return
		}
		var value any
		if json.Unmarshal([]byte(result), &value) != nil {
			value = result
		}
		externalJSON(w, map[string]any{"crew_id": crewID, "action": action, "result": value})
		return
	}
	externalError(w, http.StatusNotFound, "tool_unavailable", "This Crew profile does not declare custom commands.")
}
