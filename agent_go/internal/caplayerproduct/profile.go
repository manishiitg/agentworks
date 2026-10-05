package caplayerproduct

import (
	"context"
	"embed"
	"encoding/json"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const ProfileID = "caplayer"
const WorkspaceRoot = "Chats/CapLayer"

//go:embed system-prompt.md skills/vault-access/SKILL.md
var files embed.FS

// Vault uses the same provider catalog, conversation runtime and composer
// as Crew. Its allowlist also exposes the shared, permission-checked MCP bridge.
func BuiltinAgentProfile() agentprofiles.Profile {
	runtime := workproduct.BuiltinAgentProfile().Runtime
	// Use the shared full native CLI setup, including skill/file/shell tools.
	// The server applies the same confinement policy as other product builders.
	runtime.AgentTools = agentprofiles.AgentToolsPolicy{Mode: "full"}
	runtime.APITransport = agentprofiles.APITransportPolicy{}
	runtime.BridgeTools = []string{"manage_vault_access", "query_workflow_db", "mutate_workflow_db", "manage_vault_secret_access", "list_mcp_servers", "call_mcp_tool"}
	runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: "fixed", Root: WorkspaceRoot}
	runtime.Conversation = agentprofiles.ConversationPolicy{Mode: "singleton"}
	runtime.Capabilities = agentprofiles.RuntimeCapabilities{
		LiveInput:         agentprofiles.CapabilityPreferred,
		WarmSession:       agentprofiles.CapabilityPreferred,
		RawTerminal:       agentprofiles.CapabilityPreferred,
		Voice:             agentprofiles.CapabilityPreferred,
		NewConversation:   agentprofiles.CapabilityPreferred,
		WorkflowExecution: agentprofiles.CapabilityDisabled,
		Secrets:           agentprofiles.CapabilityDisabled,
	}
	prompt, _ := files.ReadFile("system-prompt.md")
	return agentprofiles.Profile{
		ID: ProfileID, Name: "Vault", Version: 10, BuiltIn: true,
		Scope:                agentprofiles.ProfileScopeProject,
		SystemPromptTemplate: string(prompt), Skills: []string{"vault-access"},
		Tools:      []agentprofiles.ToolBinding{{ID: "caplayer.access"}, {ID: "caplayer.database.query"}, {ID: "caplayer.database.mutate"}, {ID: "caplayer.secrets"}},
		ToolPolicy: agentprofiles.ToolPolicy{Mode: "allowlist", Enabled: []string{"manage_vault_access", "query_workflow_db", "mutate_workflow_db", "manage_vault_secret_access", "list_mcp_servers", "call_mcp_tool"}}, Runtime: runtime,
	}
}

// AccessExecutor is provided by the product server. It rechecks the current
// administrator role and calls the fixed gateway service using server secrets.
type AccessExecutor func(context.Context, string, string, json.RawMessage) (string, error)

func RegisterRuntime(registry *agentprofiles.Registry, execute AccessExecutor) error {
	if err := agentprofiles.RegisterEmbeddedSkills(files, []agentprofiles.SkillFileBinding{{
		Name: "vault-access", Description: "Connect MCPs, inspect schemas, look up permitted resources and apply validated deterministic access permissions.", Path: "skills/vault-access/SKILL.md",
	}}); err != nil {
		return err
	}
	return registry.RegisterToolFactory("caplayer.access", func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		return agentprofiles.ToolSpec{
			Name: "manage_vault_access", Category: "vault",
			Description: AccessToolDescription,
			Parameters:  AccessToolParameters(),
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				operation, _ := args["operation"].(string)
				payload, err := json.Marshal(args["arguments"])
				if err != nil {
					return "", err
				}
				return execute(ctx, runtime.UserID, operation, payload)
			},
		}, nil
	})
}

// AccessToolDescription and AccessToolParameters describe manage_vault_access. The Vault chat and the other chats an
// administrator works in (Crew, Builder, Code) offer the same tool; the server rechecks the administrator role on every call.
const AccessToolDescription = "Create named catalog MCP connections with separate OAuth accounts, start sign-in for a connection, check connection status, sync tools, disconnect an explicitly requested connection, connect a custom MCP server by name and URL, list active platform users by email, username and ID, inspect a group's effective tool access, remove all access to one MCP from one group, inspect connected MCP tools and groups, inspect an exact tool schema, or save and immediately apply validated access permissions. Connection approves initial tool definitions but assigns no group access. Regex conditions require a human-readable description. Cannot handle credentials, add group members or execute upstream tools. Read vault-access first."

func AccessToolParameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{"type": "string", "enum": []string{"inspect_environment", "list_users", "inspect_group", "remove_group_mcp", "inspect_tool", "save_permissions", "connect_server", "sign_in_connection", "connection_status", "sync_connection", "disconnect_connection"}},
			"arguments": map[string]interface{}{"type": "object", "description": "connect_server: {provider, label, instance?} for catalog or {name, url, instance?} for custom (no credentials); sign_in_connection/connection_status/sync_connection/disconnect_connection: {connection_id}; inspect_environment/list_users: {}; inspect_group: {group_id}; remove_group_mcp: {group_id,connector_id} removes whole-server, individual tool and saved policy grants for this group/connector, leaving the connection and other groups intact; inspect_tool: {public_name}; save_permissions: {id?, version?, name, group_id, rules:[{public_name,fingerprint,conditions:[{path,op:equals|matches,value,description}]}]}; description is required for matches and explains the allowed resources or values in plain language (maximum 500 characters)."},
		}, "required": []string{"operation", "arguments"},
	}
}
