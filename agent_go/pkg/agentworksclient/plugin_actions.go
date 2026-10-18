package agentworksclient

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// PluginToolName is the one tool the AgentWorks chat plugins expose. Every capability is an action of it
// (action=send, action=ack, action=attach, ...), so a coding app lists one tool instead of a dozen.
const PluginToolName = "agentworks"

type pluginAction struct {
	name    string
	tool    mcp.Tool
	handler server.ToolHandlerFunc
}

type pluginActions struct {
	mu      sync.Mutex
	actions map[string]pluginAction
}

var pluginActionSets sync.Map // *server.MCPServer -> *pluginActions

// pluginActionName turns the old per-capability tool name into its action: agent_message_send -> send,
// agent_attach_chat -> attach, agent_message_ping_person -> ping_person.
func pluginActionName(tool string) string {
	for _, prefix := range []string{"agent_message_"} {
		if strings.HasPrefix(tool, prefix) {
			return strings.TrimPrefix(tool, prefix)
		}
	}
	if strings.HasPrefix(tool, "agent_") && strings.HasSuffix(tool, "_chat") {
		return strings.TrimSuffix(strings.TrimPrefix(tool, "agent_"), "_chat")
	}
	return tool
}

// AddPluginAction registers one capability of the plugin tool. It takes the same arguments as server.AddTool, so a
// capability is described exactly as before (its own parameters, which of them are required); the server shows the
// single combined tool and routes each call by its action argument.
func AddPluginAction(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	v, _ := pluginActionSets.LoadOrStore(s, &pluginActions{actions: map[string]pluginAction{}})
	set := v.(*pluginActions)
	set.mu.Lock()
	name := pluginActionName(tool.Name)
	set.actions[name] = pluginAction{name: name, tool: tool, handler: handler}
	combined := set.combinedLocked()
	set.mu.Unlock()
	s.AddTool(combined, set.dispatch)
}

func (p *pluginActions) combinedLocked() mcp.Tool {
	names := make([]string, 0, len(p.actions))
	for n := range p.actions {
		names = append(names, n)
	}
	sort.Strings(names)
	props := map[string]any{}
	var desc strings.Builder
	desc.WriteString("AgentWorks messaging for this chat. Set action to one of the following (each lists the other arguments it needs; arguments are ignored by actions that do not use them).")
	for _, n := range names {
		a := p.actions[n]
		desc.WriteString("\n- " + n)
		if len(a.tool.InputSchema.Required) > 0 {
			desc.WriteString(" (needs " + strings.Join(a.tool.InputSchema.Required, ", ") + ")")
		}
		desc.WriteString(": " + a.tool.Description)
		for pn, schema := range a.tool.InputSchema.Properties {
			if _, seen := props[pn]; !seen {
				props[pn] = schema
			}
		}
	}
	enum := make([]any, len(names))
	for i, n := range names {
		enum[i] = n
	}
	props["action"] = map[string]any{"type": "string", "enum": enum, "description": "What to do."}
	return mcp.Tool{
		Name:        PluginToolName,
		Description: desc.String(),
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: props, Required: []string{"action"}},
	}
}

func (p *pluginActions) dispatch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, _ := args["action"].(string)
	p.mu.Lock()
	a, ok := p.actions[name]
	have := make([]string, 0, len(p.actions))
	for n := range p.actions {
		have = append(have, n)
	}
	p.mu.Unlock()
	if !ok {
		sort.Strings(have)
		return mcp.NewToolResultError(fmt.Sprintf("action must be one of: %s", strings.Join(have, ", "))), nil
	}
	var missing []string
	for _, r := range a.tool.InputSchema.Required {
		if v, present := args[r]; !present || v == nil || v == "" {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return mcp.NewToolResultError(fmt.Sprintf("action=%s needs: %s", name, strings.Join(missing, ", "))), nil
	}
	return a.handler(ctx, req)
}
