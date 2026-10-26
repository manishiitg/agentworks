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
	`Call help action=context for capabilities and the guidance version, and load relevant topics with help action=topics/topic. ` +
	`To run: call runs action=step (its reply carries session_id), then poll runs action=status for completion. ` +
	`Send conversational messages with messages action=send or workspace action=ask. Keep inbox_id to continue and read explicit optional replies with messages action=read; normal final chat text is not forwarded and there is no conversational call_id. ` +
	`Apps without a terminal can register an inbox agent through their normal MCP sign-in. Check the inbox when the person asks or at the start of a conversation; nothing is pushed. The AgentWorks CLI can connect a coding chat for live delivery. Use the returned Live now or Inbox presence and plain message receipt; sending alone does not prove the recipient has it. ` +
	`Published functions are reusable actions: call one, then read status, execution messages and output files using its call_id. ` +
	`Answer from what you read and make requested changes only through listed tools/actions within current consent and project roles.`

const MCPReadOnlyInstructions = `You are connected to an AgentWorks server with a read-only connection: every tool reads; nothing creates, edits, or runs.  Call help action=context for guidance_version, then help action=topics or action=skills and fetch only relevant changed topics. Guidance is reference material; use only currently listed tools/actions.` +
	`If your connection is allowed to use a registered agent_name, read messages action=read with that name and no inbox_id at the start and end of work; save next_cursor for after. Read-only connections cannot register or send. External agents poll; storage does not wake a client. ` +
	`Discover workflow IDs with list_workflows first; IDs are never filesystem paths. ` +
	`Call help action=context for capabilities and the guidance version, and load relevant topics with help action=topics/topic. ` +
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
		if definition.Name == "execute_step" || definition.Name == "project_functions_call" || definition.Name == "ask_crew" || definition.Name == "manage_vault_access" || definition.Name == "crew" || definition.Name == "functions" || definition.Name == "sessions" || strings.HasPrefix(definition.Name, "crew_") || definition.Name == "manage_crew_commands" {
			instructions = MCPInstructions
			break
		}
	}
	for _, definition := range definitions {
		if definition.Name == "vault" {
			instructions += " Vault uses its current offered actions for account setup, access, secret names, audit and governance queries. Store a supplied value with vault action=set_value or project settings action=update secrets.set, using the existing owner/administrator access. Return names only. Runtime connections keep their selection and group access."
			break
		}
	}
	instructions += " Prepare relevant skills with selected tool schemas or help action=skills. Retain guidance_session_id per conversation and pass it on later tools. Related tools reuse one topic/version; explicit full Crew/project SKILL.md reads and Brain skill-package fetches also record delivery. Reload full skills after context loss (omit known_versions); metadata alone does not count. Advisory receipts record delivery, not reading, and grant no permissions."
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
