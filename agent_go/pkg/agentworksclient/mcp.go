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
const MCPInstructions = `You are connected to an AgentWorks server; use only the tools and actions listed for this connection. ` +
	`Find workflow IDs with workflow action=list (IDs are never file paths). Start with help action=context (capabilities and guide version), then load only the guides the task needs with help action=topics or action=skills. ` +
	`To run a workflow: runs action=step, then poll runs action=status with the returned session_id. A published function's call_id gives its status, messages and output files. ` +
	`Messages: send with messages action=send or workspace action=ask, keep the inbox_id and read replies with messages action=read; your normal final text is not sent. If someone asked you a question or for work, answer once in the same inbox when done; don't reply to notices. Nothing is pushed: check the inbox when the person asks or at the start of a conversation. Sending alone does not prove the other agent has it.`

const MCPReadOnlyInstructions = `You are connected to an AgentWorks server with a reading connection: every tool reads; nothing creates, edits or runs. ` +
	`Find workflow IDs with workflow action=list (IDs are never file paths). help action=context, action=topics and action=skills give current guides. ` +
	`If your connection may use a registered agent_name, read messages action=read with that name at the start and end of work. ` +
	`If the task needs a change, say so instead of attempting one.`

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
		// runs:execute, so a runs, functions or sessions tool proves this bridge runs.
		if definition.Name == "runs" || definition.Name == "functions" || definition.Name == "sessions" || definition.Name == "vault" {
			instructions = MCPInstructions
			break
		}
	}
	instructions += " Keep the guidance_session_id that help action=skills returns and pass it on later calls in this conversation; after losing context, load the guides again."
	opts := []server.ServerOption{server.WithToolCapabilities(false), server.WithElicitation(), server.WithInstructions(instructions)}
	if _, ok := client.(MCPTaskService); ok {
		opts = append(opts, server.WithExtensions(map[string]any{MCPTasksExtension: map[string]any{}}))
	}
	s := server.NewMCPServer("AgentWorks", "1.0.0", append(opts, extra...)...)
	available := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		available[definition.Name] = true
		var shape struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if json.Unmarshal(definition.InputSchema, &shape) == nil {
			for _, action := range shape.Properties["action"].Enum {
				available[definition.Name+"."+action] = true
			}
		}
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
