package server

import (
	"context"
	"encoding/json"
	"fmt"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Reuse the real workflow SQL tool definitions. Execution goes through the
// gateway's storage owner so its in-memory authorization and SQLite commit agree.
func registerCapLayerDatabaseTools(registry *agentprofiles.Registry) error {
	for _, definition := range virtualtools.WorkflowDBSQLToolDefinitions() {
		name := definition.Function.Name
		operation := "query"
		if name == "mutate_workflow_db" {
			operation = "mutate"
		}
		encoded, err := json.Marshal(definition.Function.Parameters)
		if err != nil {
			return err
		}
		var params map[string]any
		if err = json.Unmarshal(encoded, &params); err != nil {
			return err
		}
		description := definition.Function.Description + " In CapLayer this targets Chats/CapLayer/db/gateway.sqlite. Use action=describe first. Mutable tables: groups, group_members, user_tool_grants, group_tool_grants, permission_drafts. Draft versions increment automatically after validated edits; include the current version in WHERE predicates and inserts based on existing policies. Users, connector metadata, tool approvals, live policies and history are read-only. No credentials are exposed."
		if err = registry.RegisterToolFactory("caplayer.database."+operation, func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
			return agentprofiles.ToolSpec{Name: name, Description: description, Parameters: params, Category: virtualtools.WorkflowDBToolCategory, Execute: func(ctx context.Context, args map[string]any) (string, error) {
				if canonicalChatHistoryWorkspacePath(runtime.UserID, runtime.WorkspacePath) != "Chats/CapLayer" {
					return "", fmt.Errorf("CapLayer SQL requires its own chat project")
				}
				data, err := json.Marshal(args)
				if err != nil {
					return "", err
				}
				return capLayerAgentRequest(ctx, runtime.UserID, "/api/admin/database/"+operation, data)
			}}, nil
		}); err != nil {
			return err
		}
	}
	return nil
}
