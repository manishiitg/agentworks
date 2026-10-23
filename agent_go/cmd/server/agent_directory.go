package server

import (
	"context"
	"net/http"
	"strings"
)

// The browser and Builder tools use the same caller-authorized directory.
func (api *StreamingAPI) handleAgentDirectory(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		externalError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to see the agents you can reach.")
		return
	}
	out, err := listAgentDirectory(r.Context(), claims)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "directory_unavailable", "Could not load your agents. Try again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}

// listAgentDirectory is the one list of agents a person can use (PLAT-861-297, owner 2026-10-20: "all should be a
// group of agents users can easily.. once shared"): every Crew, Goal and Relay they own or that is shared with them,
// and their agents connected through AgentBridge (local Claude Code and others). Nothing has to be linked first.
// Rows come only from the existing access paths, and the list is built only when asked, never at message start.
func listAgentDirectory(ctx context.Context, claims *UserClaims) (map[string]interface{}, error) {
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return map[string]interface{}{"agents": []map[string]interface{}{}}, nil
	}
	agents := []map[string]interface{}{}
	// The same rows on the website and over MCP: the person's live access decides, not connection scopes.
	crews, err := listAccessibleWorkspaceProjects(ctx, claims, "")
	if err != nil {
		return nil, err
	}
	for _, crew := range crews {
		id, _ := crew["id"].(string)
		profileID, _ := crew["profile_id"].(string)
		crewPath, _ := crew["workspace_path"].(string)
		name, _ := crew["name"].(string)
		if identity, ok := crew["identity"].(accessibleProjectIdentity); ok && strings.TrimSpace(identity.Name) != "" {
			name = identity.Name
		}
		owner := claims.UserID
		if crewOwner, ok := crewProjectOwnerID(crewPath); ok {
			owner = crewOwner
		}
		target := triggerTarget{Kind: triggerCallerCrew, Path: crewPath, Label: name, CrewID: id, CrewProfile: profileID, CrewOwner: owner}
		functions, _ := readCrewFunctions(ctx, target)
		kind := "crew"

		row := map[string]interface{}{
			"kind": kind, "type": "Crew", "presence": "Runs when called", "delivery_mode": "on_request", "id": "workspace:" + profileID + ":" + id, "crew_id": id, "name": name, "owner": crew["owner"],
			"your_access": crew["access"], "description": crew["description"], "functions": agentDirectoryFunctions(functions),
			"reach": "call_function(target=<id>, function=<name>) for a function; send_message(target=<id>) to ask it",
		}
		agents = append(agents, row)
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return nil, err
	}
	for _, workflow := range filterWorkflowManifestsForUser(claims, discovered) {
		manifest := workflow.Manifest
		if manifest == nil {
			continue
		}
		kind := "goal"
		if manifestIsRelay(manifest) {
			kind = "relay"
		}
		access := "run"
		if workflow.MyAccess == WorkflowAccessOwner {
			access = "owner"
		} else if workflow.MyAccess == WorkflowAccessWrite {
			access = "editor"
		}
		agents = append(agents, map[string]interface{}{
			"kind": kind, "type": map[string]string{"goal": "Goal", "relay": "Relay"}[kind], "presence": "Runs when called", "delivery_mode": "on_request", "id": "workflow:" + manifest.ID, "workflow_id": manifest.ID, "name": firstNonEmptyTrimmed(manifest.Label, workflow.WorkspacePath),
			"your_access": access, "description": strings.TrimSpace(manifest.Description), "functions": agentDirectoryFunctions(workflowFunctions(manifest)),
			"reach": "call_function(target=<id>, function=<name>)",
		})
	}
	connected, err := listNamedMessageAgents(ctx, claims.UserID)
	if err == nil {
		list, _ := connected["agents"].([]map[string]interface{})
		for _, item := range list {
			row := map[string]interface{}{"kind": "connected_agent", "reach": "send_message(target=<id>)"}
			for key, value := range item {
				row[key] = value
			}
			row["id"] = item["target"]
			agents = append(agents, row)
		}
	}
	return map[string]interface{}{
		"agents": agents,
		"note":   "Every agent you own or that is shared with you. Show name, type, provider, presence and last_seen_at; include last_received_at and last_handled_at when supplied. Builder/Run describes access, not connection or delivery. Crews, Goals and Relays run when called and have no laptop heartbeat. Last seen is connection activity, not proof that the model read a message. Missing timestamps are unknown, never zero activity. Use the id; a name works only when unique.",
	}, nil
}

func agentDirectoryFunctions(functions []crewFunction) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(functions))
	for _, fn := range functions {
		out = append(out, map[string]interface{}{"name": fn.Name, "description": fn.Description})
	}
	return out
}
