package agentworksclient

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ToolCaller interface {
	Tools(context.Context) ([]Tool, error)
	Call(context.Context, string, map[string]any) (json.RawMessage, error)
}

// MCPInstructions are delivered to every MCP client in the initialize
// response. Skill loading is host-dependent and not guaranteed, so these
// instructions — not an installable skill file — are the reliable channel
// telling the agent how to obtain AgentWorks guidance. The bridge serves
// the run variant when the scope-filtered catalog carries run tools.
const MCPInstructions = `You are connected to an AgentWorks server: use only currently listed tools/actions. Workflow run tools use pinned model sessions; direct project edits use existing authoring consent, current roles and guarded handlers.  Call help action=context for guidance_version, then help action=topics or action=skills and fetch only relevant changed topics. Guidance is reference material; use only currently listed tools/actions.` +
	`Discover workflow IDs with list_workflows first; IDs are never filesystem paths. ` +
	`Call get_agent_context for token capabilities and the guidance version, and load only the guidance topics relevant to the task via list_guidance_topics/get_guidance_topic. ` +
	`To run: call a run-mode tool such as execute_step (its reply carries session_id), then poll run_status for completion. ` +
	`Send conversational messages with messages action=send (chat and ask_crew are aliases). Keep inbox_id to continue and read explicit optional replies with messages action=read; normal final chat text is not forwarded and there is no conversational call_id. ` +
	`AgentBridge messaging needs the AgentWorks CLI on the person's computer: it works only for a chat attached with agentworks attach; over plain MCP the messages tool is refused. ` +
	`Declared functions are isolated internal triggers: call one, then read status, execution messages and output files using its call_id. ` +
	`Answer from what you read and make requested changes only through listed tools/actions within current consent and project roles.`

const MCPReadOnlyInstructions = `You are connected to an AgentWorks server with a read-only connection: every tool reads; nothing creates, edits, or runs.  Call help action=context for guidance_version, then help action=topics or action=skills and fetch only relevant changed topics. Guidance is reference material; use only currently listed tools/actions.` +
	`If your connection is allowed to use a registered agent_name, read messages action=read with that name and no inbox_id at the start and end of work; save next_cursor for after. Read-only connections cannot register or send. External agents poll; storage does not wake a client. ` +
	`Discover workflow IDs with list_workflows first; IDs are never filesystem paths. ` +
	`Call get_agent_context for token capabilities and the guidance version, and load only the guidance topics relevant to the task via list_guidance_topics/get_guidance_topic. ` +
	`Answer from what you read; if the task needs a change, say so instead of attempting one.`

// NewMCPServer discovers schemas from the hosted server. It contains no local
// workflow logic or copied plan schemas. Restart the bridge to refresh tools
// after a server upgrade.
func NewMCPServer(ctx context.Context, client ToolCaller, extra ...server.ServerOption) (*server.MCPServer, error) {
	definitions, err := client.Tools(ctx)
	if err != nil {
		return nil, err
	}
	instructions := MCPReadOnlyInstructions
	for _, definition := range definitions {
		// The server omits run tools from catalogs whose token lacks
		// runs:execute, so execute_step's presence proves this bridge runs.
		if definition.Name == "execute_step" || definition.Name == "call_crew_function" || definition.Name == "ask_crew" || definition.Name == "manage_vault_access" || definition.Name == "crew" || definition.Name == "functions" || definition.Name == "builder" || strings.HasPrefix(definition.Name, "crew_") || definition.Name == "manage_crew_commands" {
			instructions = MCPInstructions
			break
		}
	}
	for _, definition := range definitions {
		if definition.Name == "manage_vault_access" {
			instructions += " Exception: authorized Vault management may make changes. Use manage_vault_access for connections/tool/regex permissions, manage_vault_groups for membership, and manage_vault_secret_access for secret names/group grants and storing a secret value the person gives you (operation=set_value; never repeat the value). No workflow_id is needed. Secret values are never returned. The same Vault management tools are declared in Vault product.yaml for builder and MCP. Use query_vault_db/mutate_vault_db for guarded governance SQL, list_vault_mcp_servers for setup inventory and call_vault_mcp_tool for approved upstream setup calls as this administrator. Ordinary Vault MCP runtime remains group scoped."
			break
		}
	}
	instructions += " Prepare relevant skills with selected tool schemas or help action=skills. Retain guidance_session_id per conversation and pass it on later tools. Related tools reuse one topic/version; explicit full Crew/project SKILL.md reads and Brain skill-package fetches also record delivery. Reload full skills after context loss (omit known_versions); metadata alone does not count. Advisory receipts record delivery, not reading, and grant no permissions."
	opts := []server.ServerOption{server.WithToolCapabilities(false), server.WithElicitation(), server.WithInstructions(instructions)}
	s := server.NewMCPServer("AgentWorks", "1.0.0", append(opts, extra...)...)
	available := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		available[definition.Name] = true
	}
	for _, definition := range definitions {
		name := definition.Name
		schema := definition.InputSchema
		var common map[string]any
		if json.Unmarshal(schema, &common) == nil {
			properties, _ := common["properties"].(map[string]any)
			if properties == nil {
				properties = map[string]any{}
				common["properties"] = properties
			}
			properties["guidance_session_id"] = map[string]any{"type": "string", "maxLength": 256, "description": "Optional guidance receipt for this conversation, returned by help action=skills. Reload skills after context loss."}
			schema, _ = json.Marshal(common)
		}
		s.AddTool(mcp.NewToolWithRawSchema(name, definition.Description, schema), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			arguments := request.GetArguments()
			// Preserve integers from the wire instead of round-tripping through
			// float64 (for example numeric external IDs greater than 2^53).
			if len(request.Params.RawArguments) > 0 {
				decoder := json.NewDecoder(bytes.NewReader(request.Params.RawArguments))
				decoder.UseNumber()
				if err := decoder.Decode(&arguments); err != nil || arguments == nil {
					return mcp.NewToolResultError("tool arguments must be a JSON object"), nil
				}
			}
			if id, ok := arguments["guidance_session_id"].(string); ok {
				ctx = WithGuidanceSession(ctx, id)
			}
			delete(arguments, "guidance_session_id")
			return callMCPBridgeTool(ctx, client, name, arguments, available, request), nil
		})
	}
	return s, nil
}
