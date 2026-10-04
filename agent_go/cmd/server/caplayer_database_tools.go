package server

import (
	"context"
	"encoding/json"
	"fmt"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"net/http"
	"net/url"
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
		description := definition.Function.Description + " In Vault this targets Chats/CapLayer/db/gateway.sqlite. Use action=describe first. Mutable tables: groups, group_members, user_tool_grants, group_tool_grants. Advanced equality/regex permissions use manage_vault_access with operation save_permissions and apply immediately after validation. Users, connector metadata, tool approvals, live policies and history are read-only. No credentials are exposed."
		if err = registry.RegisterToolFactory("caplayer.database."+operation, func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
			return agentprofiles.ToolSpec{Name: name, Description: description, Parameters: params, Category: virtualtools.WorkflowDBToolCategory, Execute: func(ctx context.Context, args map[string]any) (string, error) {
				if canonicalChatHistoryWorkspacePath(runtime.UserID, runtime.WorkspacePath) != "Chats/CapLayer" {
					return "", fmt.Errorf("Vault SQL requires its own chat project")
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
	return registry.RegisterToolFactory("caplayer.secrets", func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		return agentprofiles.ToolSpec{Name: "manage_vault_secret_access", Description: "Grant or revoke use of an existing Vault secret for an existing group. Query vault_secrets and groups with query_workflow_db first. This changes live use permission immediately; it never returns a secret value. Do not ask for values in chat. Projects must separately select the name in Integrations > Plugins > Vault. Secret values are added or rotated through Vault's secure Secrets panel.", Parameters: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"group_id", "name", "allowed"}, "properties": map[string]any{"group_id": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "allowed": map[string]any{"type": "boolean"}}}, Category: "vault_secrets", Execute: func(ctx context.Context, args map[string]any) (string, error) {
			if !canManageGlobalSecrets(runtime.UserID) || !userAllowedProduct(&UserClaims{UserID: runtime.UserID}, "mcp-gateway") || canonicalChatHistoryWorkspacePath(runtime.UserID, runtime.WorkspacePath) != "Chats/CapLayer" {
				return "", errGlobalAdmin
			}
			group, ok := args["group_id"].(string)
			if !ok || group == "" {
				return "", fmt.Errorf("group_id required")
			}
			name, ok := args["name"].(string)
			if !ok || !globalSecretNamePattern.MatchString(name) {
				return "", fmt.Errorf("exact secret name required")
			}
			allow, ok := args["allowed"].(bool)
			if !ok {
				return "", fmt.Errorf("allowed must be boolean")
			}
			if err := syncVaultSecretMetadata(ctx, runtime.UserID); err != nil {
				return "", err
			}
			if err := vaultSecretAdminRequest(ctx, runtime.UserID, http.MethodPost, "/api/admin/groups/"+url.PathEscape(group)+"/secrets", map[string]any{"name": name, "allowed": allow}); err != nil {
				return "", err
			}
			return "Updated group secret use permission. No value returned.", nil
		}}, nil
	})
}
