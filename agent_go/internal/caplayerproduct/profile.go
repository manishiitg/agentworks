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

//go:embed system-prompt.md skills/caplayer-access/SKILL.md
var files embed.FS

// Vault uses the same provider catalog, conversation runtime and composer
// as Crew. Its own tool allowlist contains only governance operations.
func BuiltinAgentProfile() agentprofiles.Profile {
	runtime := workproduct.BuiltinAgentProfile().Runtime
	// Provider choices are shared, authority is not inherited from Crew. Full
	// CLI rollout must not turn this draft assistant into a general executor.
	runtime.AgentTools = agentprofiles.AgentToolsPolicy{Mode: "mcp_only"}
	runtime.APITransport = agentprofiles.APITransportPolicy{}
	runtime.BridgeTools = []string{"manage_caplayer_access", "query_workflow_db", "mutate_workflow_db", "manage_vault_secret_access"}
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
		ID: ProfileID, Name: "Vault", Version: 1, BuiltIn: true,
		Scope:                agentprofiles.ProfileScopeProject,
		SystemPromptTemplate: string(prompt), Skills: []string{"caplayer-access"},
		Tools:      []agentprofiles.ToolBinding{{ID: "caplayer.access"}, {ID: "caplayer.database.query"}, {ID: "caplayer.database.mutate"}, {ID: "caplayer.secrets"}},
		ToolPolicy: agentprofiles.ToolPolicy{Mode: "allowlist", Enabled: []string{"manage_caplayer_access", "query_workflow_db", "mutate_workflow_db", "manage_vault_secret_access"}}, Runtime: runtime,
	}
}

// AccessExecutor is provided by the product server. It rechecks the current
// administrator role and calls the fixed gateway service using server secrets.
type AccessExecutor func(context.Context, string, string, json.RawMessage) (string, error)

func RegisterRuntime(registry *agentprofiles.Registry, execute AccessExecutor) error {
	if err := agentprofiles.RegisterEmbeddedSkills(files, []agentprofiles.SkillFileBinding{{
		Name: "caplayer-access", Description: "Inspect MCP schemas and prepare deterministic access drafts for administrator review.", Path: "skills/caplayer-access/SKILL.md",
	}}); err != nil {
		return err
	}
	return registry.RegisterToolFactory("caplayer.access", func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		return agentprofiles.ToolSpec{
			Name: "manage_caplayer_access", Category: "caplayer",
			Description: "Connect a custom MCP server by name and URL, inspect connected MCP tools and groups, inspect an exact tool schema, or save a validated access DRAFT. Connection approves initial tool definitions but assigns no group access. Cannot handle credentials, publish, revoke, add group members, execute upstream tools, or change live permissions. Read caplayer-access first.",
			Parameters: map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{"type": "string", "enum": []string{"inspect_environment", "inspect_tool", "save_draft", "connect_server"}},
					"arguments": map[string]interface{}{"type": "object", "description": "connect_server: {name, url, instance?} (no credentials); inspect_environment: {}; inspect_tool: {public_name}; save_draft: {id?, version?, name, group_id, rules:[{public_name,fingerprint,conditions:[{path,op:equals|matches,value}]}]}."},
				}, "required": []string{"operation", "arguments"},
			},
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
