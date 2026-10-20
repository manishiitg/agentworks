package server

import (
	"context"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// listAgentDirectory is the one list of agents a person can use (PLAT-861-297, owner 2026-10-20: "all should be a
// group of agents users can easily.. once shared"): every Crew, Goal and Relay they own or that is shared with them,
// and their agents connected through AgentBridge (local Claude Code and others). Nothing has to be linked first.
// Rows come only from the existing access paths, and the list is built only when asked, never at message start.
func listAgentDirectory(ctx context.Context, claims *UserClaims) (map[string]interface{}, error) {
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return map[string]interface{}{"agents": []map[string]interface{}{}}, nil
	}
	agents := []map[string]interface{}{}
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
		if strings.EqualFold(profileID, codeproduct.ProfileID) {
			kind = "code"
		}
		row := map[string]interface{}{
			"kind": kind, "id": "workspace:" + profileID + ":" + id, "name": name, "owner": crew["owner"],
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
			"kind": kind, "id": "workflow:" + manifest.ID, "name": firstNonEmptyTrimmed(manifest.Label, workflow.WorkspacePath),
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
		"note":   "Every agent you own or that is shared with you. Use its id; a name works only when it matches one agent.",
	}, nil
}

func agentDirectoryFunctions(functions []crewFunction) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(functions))
	for _, fn := range functions {
		out = append(out, map[string]interface{}{"name": fn.Name, "description": fn.Description})
	}
	return out
}
