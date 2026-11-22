package agentworksclient

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
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
const MCPInstructions = `You are connected to an AgentWorks server. Find tools with search_tools, read their schemas with get_api_spec and run them with call_tool; use only tools and actions this connection lists. ` +
	`Find workflow IDs with workflow action=list (IDs are never file paths). Start with help action=context (capabilities and guide version), then load only the guides the task needs with help action=topics or action=skills. ` +
	`To run a workflow: runs action=step, then poll runs action=status with the returned session_id. A published function's call_id gives its status, messages and output files. ` +
	`Messages: send with messages action=send or workspace action=ask, keep the inbox_id and read replies with messages action=read; your normal final text is not sent. If someone asked you a question or for work, answer once in the same inbox when done; don't reply to notices. Nothing is pushed: check the inbox when the person asks or at the start of a conversation. Sending alone does not prove the other agent has it.`

const MCPReadOnlyInstructions = `You are connected to an AgentWorks server with a reading connection: every tool reads; nothing creates, edits or runs. Find tools with search_tools, read their schemas with get_api_spec and run them with call_tool. ` +
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
	byName := make(map[string]Tool, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	s.AddTool(mcp.NewToolWithRawSchema(MCPToolSearch, mcpToolDescriptions[MCPToolSearch], mcpToolSchemas[MCPToolSearch]), func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcpJSONResult(searchMCPTools(definitions, request.GetString("query", ""), request.GetInt("offset", 0), request.GetInt("limit", 10))), nil
	})
	s.AddTool(mcp.NewToolWithRawSchema(MCPToolSpec, mcpToolDescriptions[MCPToolSpec], mcpToolSchemas[MCPToolSpec]), func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		names := request.GetStringSlice("names", nil)
		if name := request.GetString("names", ""); name != "" {
			names = []string{name}
		}
		if len(names) == 0 {
			return mcpJSONResult(searchMCPTools(definitions, "", 0, len(definitions))), nil
		}
		out := map[string]any{}
		tools, unknown := []map[string]any{}, []string{}
		for _, name := range names {
			definition, ok := byName[name]
			if !ok {
				unknown = append(unknown, name)
				continue
			}
			tools = append(tools, map[string]any{"name": name, "description": definition.Description, "inputSchema": withGuidanceSession(definition.InputSchema)})
		}
		out["tools"] = tools
		if len(unknown) > 0 {
			out["unknown"] = unknown
			out["hint"] = "Use search_tools to find the right tool name."
		}
		return mcpJSONResult(out), nil
	})
	s.AddTool(mcp.NewToolWithRawSchema(MCPToolCall, mcpToolDescriptions[MCPToolCall], mcpToolSchemas[MCPToolCall]), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var call struct {
			Name              string         `json:"name"`
			Arguments         map[string]any `json:"arguments"`
			GuidanceSessionID string         `json:"guidance_session_id"`
		}
		if len(request.Params.RawArguments) > 0 {
			// Preserve integers from the wire instead of round-tripping through
			// float64 (for example numeric external IDs greater than 2^53).
			decoder := json.NewDecoder(bytes.NewReader(request.Params.RawArguments))
			decoder.UseNumber()
			if err := decoder.Decode(&call); err != nil {
				return mcp.NewToolResultError("call_tool arguments must be a JSON object with name and arguments"), nil
			}
		} else {
			call.Name = request.GetString("name", "")
			call.Arguments, _ = request.GetArguments()["arguments"].(map[string]any)
			call.GuidanceSessionID = request.GetString("guidance_session_id", "")
		}
		if _, ok := byName[call.Name]; !ok {
			return mcp.NewToolResultError("No tool named " + strconv.Quote(call.Name) + " on this connection. Use search_tools to find the right one."), nil
		}
		if call.Arguments == nil {
			call.Arguments = map[string]any{}
		}
		if id, ok := call.Arguments["guidance_session_id"].(string); ok && call.GuidanceSessionID == "" {
			call.GuidanceSessionID = id
		}
		delete(call.Arguments, "guidance_session_id")
		if call.GuidanceSessionID != "" {
			ctx = WithGuidanceSession(ctx, call.GuidanceSessionID)
		}
		return callMCPBridgeTool(ctx, client, call.Name, call.Arguments, available, request), nil
	})
	return s, nil
}

// The stdio bridge lists the same three discovery tools as the remote MCP,
// not every full schema: listing all of them cost each chat about 50K tokens
// before any work started (#3563).
const (
	MCPToolSearch = "search_tools"
	MCPToolSpec   = "get_api_spec"
	MCPToolCall   = "call_tool"
)

var mcpToolDescriptions = map[string]string{
	MCPToolSearch: "Searches this connection's AgentWorks tools by intent or name. Returns names and short descriptions, ten per page, with next_offset when there are more. Read a tool's schema with get_api_spec and run it with call_tool.",
	MCPToolSpec:   "Returns full JSON schemas for the named AgentWorks tools (names: one name or a list). With names omitted, lists every tool with a short description. Does not run anything.",
	MCPToolCall:   "Runs one AgentWorks tool by name with arguments matching its schema from get_api_spec. It runs with this connection's current access.",
}

var mcpToolSchemas = map[string]json.RawMessage{
	MCPToolSearch: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","maxLength":512,"description":"Intent or tool-name keywords; omit to list every tool."},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":20,"default":10}},"additionalProperties":false}`),
	MCPToolSpec:   json.RawMessage(`{"type":"object","properties":{"names":{"description":"Tool name or names to get schemas for. Omit to list every tool.","anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}}]}},"additionalProperties":false}`),
	MCPToolCall:   json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"description":"Tool name from search_tools or get_api_spec."},"arguments":{"type":"object","additionalProperties":true,"description":"Arguments matching the tool's schema. Defaults to {}."},"guidance_session_id":{"type":"string","maxLength":256,"description":"Optional guide receipt for this conversation, returned by help action=skills."}},"required":["name"],"additionalProperties":false}`),
}

// searchMCPTools ranks tools by how many query words appear in the name
// (weighted) and description; an empty query lists every tool by name.
func searchMCPTools(definitions []Tool, query string, offset, limit int) map[string]any {
	if limit < 1 {
		limit = 10
	}
	if limit > 20 && query != "" {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	words := strings.Fields(strings.ToLower(query))
	type hit struct {
		tool  Tool
		score int
	}
	hits := []hit{}
	for _, definition := range definitions {
		name, description := strings.ToLower(definition.Name), strings.ToLower(definition.Description)
		score := 0
		for _, word := range words {
			if strings.Contains(name, word) {
				score += 3
			}
			if strings.Contains(description, word) {
				score++
			}
		}
		if len(words) == 0 || score > 0 {
			hits = append(hits, hit{definition, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].tool.Name < hits[j].tool.Name
	})
	page := []map[string]string{}
	for i := offset; i < len(hits) && len(page) < limit; i++ {
		page = append(page, map[string]string{"name": hits[i].tool.Name, "description": shortMCPDescription(hits[i].tool.Description)})
	}
	out := map[string]any{"tools": page, "total": len(hits)}
	if offset+len(page) < len(hits) {
		out["next_offset"] = offset + len(page)
	}
	return out
}

func shortMCPDescription(description string) string {
	description = strings.TrimSpace(description)
	if i := strings.Index(description, ". "); i > 0 {
		description = description[:i+1]
	}
	if len(description) > 200 {
		description = strings.TrimSpace(description[:197]) + "..."
	}
	return description
}

// withGuidanceSession adds the shared optional guide receipt to a tool schema.
func withGuidanceSession(schema json.RawMessage) json.RawMessage {
	var common map[string]any
	if json.Unmarshal(schema, &common) != nil {
		return schema
	}
	properties, _ := common["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		common["properties"] = properties
	}
	properties["guidance_session_id"] = map[string]any{"type": "string", "maxLength": 256, "description": "Optional guide receipt for this conversation, returned by help action=skills. Load the guides again after losing context."}
	out, err := json.Marshal(common)
	if err != nil {
		return schema
	}
	return out
}

func mcpJSONResult(value any) *mcp.CallToolResult {
	raw, err := json.Marshal(value)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultStructured(value, string(raw))
}
