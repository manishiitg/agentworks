package main

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// All native chat/MCP join calls carry only a Crew selector. The existing
// resolver/helper supplies the exact attached session, never a model-chosen ID.
func addTerminalTeamTools(s *server.MCPServer, root, provider, host string, resolve chatResolver) {
	if provider != "claude" && provider != "codex" {
		return
	}
	handler := func(action string) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			session, err := resolve(ctx, req)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			args := req.GetArguments()
			payload := map[string]any{"project": args["project"]}
			if action == "team_sync" {
				for _, key := range []string{"team_action", "need", "button", "helper"} {
					if value, ok := args[key]; ok {
						payload[key] = value
					}
				}
			}
			out, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: action, Provider: provider, SessionID: session, HostKey: host, Arguments: payload})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if out.Error != "" {
				return mcp.NewToolResultError(out.Error), nil
			}
			if !out.Attached || len(out.Result) == 0 {
				return mcp.NewToolResultError("Connect this exact session to AgentWorks first."), nil
			}
			var result any
			if json.Unmarshal(out.Result, &result) != nil {
				return mcp.NewToolResultError("The Crew returned an invalid reply."), nil
			}
			raw, _ := json.MarshalIndent(result, "", "  ")
			return mcp.NewToolResultText(string(raw)), nil
		}
	}
	for _, operation := range []string{"join", "leave"} {
		description := "Join this exact attached session to a Crew the person owns or is a current member of. Call only when the person asks. The server returns the accepted local folder or copy=null if the files are on the server; never guess a path. Read the returned Crew instructions. Team participation follows the Crew settings automatically: Shared with team, answers only, no changes for a new join. If the Team is off, only the person and their own agents can reach it. No new sync or keys."
		if operation == "leave" {
			description = "Leave this Crew from this exact attached session, only when the person asks. Removes its Crew listing, not files or the AgentBridge attachment."
		}
		agentworksclient.AddPluginAction(s, mcp.NewTool(operation, mcp.WithDescription(description), mcp.WithString("project", mcp.Required(), mcp.Description("Crew ID from AgentWorks, never a filesystem path."))), handler("crew_"+operation))
	}
	agentworksclient.AddPluginAction(s, mcp.NewTool("team_sync", mcp.WithDescription("The Crew's Team, from this exact joined session. work_brief at the start of a session shows the asks sent to you that you have not finished (my_tasks) and requests waiting on your person; work_answer (need, button, optional helper) answers one for your person. Asks to other agents and requests to a person are AgentBridge messages (an agent, or person:<name> with buttons). Track work and decisions in the project's own issue tracker (GitHub, Jira or files) with your own tools. Join the Crew with action=join; discovery and messages use AgentBridge."), mcp.WithString("project", mcp.Required()), mcp.WithString("team_action", mcp.Required(), mcp.Enum("work_brief", "work_answer")), mcp.WithString("need"), mcp.WithString("button"), mcp.WithString("helper")), handler("team_sync"))
}
