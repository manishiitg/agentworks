package agentworksclient

import (
	"bytes"
	"context"
	"encoding/json"

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
const MCPInstructions = `You are connected to an AgentWorks server: tools read, and run-mode tools execute in pinned Run-mode sessions; nothing creates, edits, or authors. ` +
	`Discover workflow IDs with list_workflows first; IDs are never filesystem paths. ` +
	`Call get_agent_context for token capabilities and the guidance version, and load only the guidance topics relevant to the task via list_guidance_topics/get_guidance_topic. ` +
	`To run: call a run-mode tool such as execute_step (its reply carries session_id), then poll run_status for completion. ` +
	`Chat with the chat tool for questions and analysis; pass session_id to continue a conversation. ` +
	`Answer from what you read; if the task needs a change, say so instead of attempting one.`

const MCPReadOnlyInstructions = `You are connected to an AgentWorks server with a read-only connection: every tool reads; nothing creates, edits, or runs. ` +
	`Discover workflow IDs with list_workflows first; IDs are never filesystem paths. ` +
	`Call get_agent_context for token capabilities and the guidance version, and load only the guidance topics relevant to the task via list_guidance_topics/get_guidance_topic. ` +
	`Answer from what you read; if the task needs a change, say so instead of attempting one.`

// NewMCPServer discovers schemas from the hosted server. It contains no local
// workflow logic or copied plan schemas. Restart the bridge to refresh tools
// after a server upgrade.
func NewMCPServer(ctx context.Context, client ToolCaller) (*server.MCPServer, error) {
	definitions, err := client.Tools(ctx)
	if err != nil {
		return nil, err
	}
	instructions := MCPReadOnlyInstructions
	for _, definition := range definitions {
		// The server omits run tools from catalogs whose token lacks
		// runs:execute, so execute_step's presence proves this bridge runs.
		if definition.Name == "execute_step" || definition.Name == "manage_vault_access" {
			instructions = MCPInstructions
			break
		}
	}
	for _, definition := range definitions {
		if definition.Name == "manage_vault_access" {
			instructions += " Exception: authorized Vault management may make changes. Use manage_vault_access for connections/tool/regex permissions, manage_vault_groups for membership, and manage_vault_secret_access for secret names/group grants. No workflow_id is needed. Secret values are never returned. The same Vault management tools are declared in Vault product.yaml for builder and MCP. Use query_vault_db/mutate_vault_db for guarded governance SQL, list_vault_mcp_servers for setup inventory and call_vault_mcp_tool for approved upstream setup calls as this administrator. Ordinary Vault MCP runtime remains group scoped."
			break
		}
	}
	s := server.NewMCPServer("AgentWorks", "1.0.0", server.WithToolCapabilities(false), server.WithElicitation(), server.WithInstructions(instructions))
	available := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		available[definition.Name] = true
	}
	for _, definition := range definitions {
		name := definition.Name
		s.AddTool(mcp.NewToolWithRawSchema(name, definition.Description, definition.InputSchema), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			return callMCPBridgeTool(ctx, client, name, arguments, available, request), nil
		})
	}
	return s, nil
}
