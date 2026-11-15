package main

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The native resolver supplies identity; the model supplies only a Team selector
// and an operation. No native session, owner, attachment or path arguments.
func addTerminalTeamTools(s *server.MCPServer, root, provider, host string, resolve chatResolver) {
	if provider != "claude" {
		return
	}
	s.AddTool(mcp.NewTool("team_sync", mcp.WithDescription("Join or leave a Team from THIS attached Claude Code session, only when the person asks. First join the Team in the running AgentWorks Mac app. join returns your existing local Team copy path; work in its selected folders. New sessions start Only me: choose In team in AgentWorks to let teammates ask. Answers only is the default; Live is explicit. AgentWorks must keep running for file sync. Use normal AgentBridge discovery and messages to find and ask teammates. Clashes and conversation copies stay in AgentWorks. Team keys stay on the server; hand work needing them to the Team agent."), mcp.WithString("action", mcp.Required(), mcp.Enum("join", "leave", "doing", "decision")), mcp.WithString("project", mcp.Required(), mcp.Description("Team Crew ID from AgentWorks, never a local path.")), mcp.WithString("line"), mcp.WithString("why")), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		session, err := resolve(ctx, req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		args := req.GetArguments()
		payload := map[string]any{"project": args["project"], "team_action": args["action"]}
		for _, key := range []string{"line", "why"} {
			if value, ok := args[key]; ok {
				payload[key] = value
			}
		}
		out, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "team_sync", Provider: provider, SessionID: session, HostKey: host, Arguments: payload})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if out.Error != "" {
			return mcp.NewToolResultError(out.Error), nil
		}
		if !out.Attached || len(out.Result) == 0 {
			return mcp.NewToolResultError("Connect this session to AgentWorks first, then try again."), nil
		}
		var pretty any
		if json.Unmarshal(out.Result, &pretty) != nil {
			return mcp.NewToolResultError("The Team returned an invalid reply."), nil
		}
		raw, _ := json.MarshalIndent(pretty, "", "  ")
		return mcp.NewToolResultText(string(raw)), nil
	})
}
