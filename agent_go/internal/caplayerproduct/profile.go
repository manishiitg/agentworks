package caplayerproduct

import (
	"context"
	"embed"
	"encoding/json"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const ProfileID = "caplayer"
const WorkspaceRoot = "Chats/CapLayer"

//go:embed product.yaml system-prompt.md skills/vault-access/SKILL.md
var files embed.FS

// Vault uses the same provider catalog, conversation runtime and composer
// as Crew. Its allowlist also exposes the shared, permission-checked MCP bridge.
func BuiltinAgentProfile() agentprofiles.Profile {
	manifest := Manifest()
	profile := manifest.Profile
	// Provider choices and confinement remain shared with Crew. Vault declares
	// its management tools and product runtime controls in its own manifest.
	runtime := workproduct.BuiltinAgentProfile().Runtime
	runtime.AgentTools = profile.Runtime.AgentTools
	runtime.APITransport = profile.Runtime.APITransport
	runtime.BridgeTools = profile.Runtime.BridgeTools
	runtime.Workspace = profile.Runtime.Workspace
	runtime.Conversation = profile.Runtime.Conversation
	runtime.Capabilities = profile.Runtime.Capabilities
	profile.Runtime = runtime
	profile.BuiltIn = true
	prompt, err := manifest.RenderPrompt(files, profile, nil)
	if err != nil {
		panic(err)
	}
	profile.SystemPromptTemplate = prompt
	return profile
}

// AccessExecutor is provided by the product server. It rechecks the current
// administrator role and calls the fixed gateway service using server secrets.
type AccessExecutor func(context.Context, string, string, json.RawMessage) (string, error)

var registerSkillsOnce sync.Once
var registerSkillsErr error

func RegisterRuntime(registry *agentprofiles.Registry, execute AccessExecutor) error {
	registerSkillsOnce.Do(func() {
		registerSkillsErr = agentprofiles.RegisterEmbeddedSkills(files, []agentprofiles.SkillFileBinding{{
			Name: "vault-access", Description: "Connect MCPs, inspect schemas, look up permitted resources and apply validated deterministic access permissions.", Path: "skills/vault-access/SKILL.md",
		}})
	})
	if registerSkillsErr != nil {
		return registerSkillsErr
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
const AccessToolDescription = "Create named catalog MCP connections with separate OAuth accounts, start sign-in for a connection, check connection status, sync tools, disconnect an explicitly requested connection, connect a custom MCP server by name and URL, list active platform users by email, username and ID, inspect a group's effective tool access, inspect what one person can reach and through which group, remove all access to one MCP from one group, inspect connected MCP tools and groups, inspect an exact tool schema, or save and immediately apply validated access permissions. Connection approves initial tool definitions but assigns no group access. Regex conditions require a human-readable description. Cannot handle credentials, add group members or execute upstream tools. Inspect the environment and exact tool schemas before changing permissions."

func AccessToolParameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{"type": "string", "enum": []string{"inspect_environment", "list_users", "inspect_group", "inspect_user", "remove_group_mcp", "inspect_tool", "save_permissions", "connect_server", "sign_in_connection", "connection_status", "sync_connection", "disconnect_connection"}},
			"arguments": map[string]interface{}{"type": "object", "description": "connect_server: {provider, label, instance?} for catalog or {name, url, instance?} for custom (no credentials); sign_in_connection/connection_status/sync_connection/disconnect_connection: {connection_id}; inspect_environment/list_users: {}; inspect_group: {group_id}; inspect_user: {user_id} (what one person can reach and which group gives it); remove_group_mcp: {group_id,connector_id} removes whole-server, individual tool and saved policy grants for this group/connector, leaving the connection and other groups intact; inspect_tool: {public_name}; save_permissions: {id?, version?, name, group_id, rules:[{public_name,fingerprint,conditions:[{path,op:equals|matches,value,description}]}]}; description is required for matches and explains the allowed resources or values in plain language (maximum 500 characters)."},
		}, "required": []string{"operation", "arguments"},
	}
}
