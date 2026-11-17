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
				for _, key := range []string{"team_action", "task", "label", "title", "link", "part_of", "for", "line", "why", "replaces", "query", "buttons", "need", "button", "helper"} {
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
	agentworksclient.AddPluginAction(s, mcp.NewTool("team_sync", mcp.WithDescription("The Crew's work log, from this exact joined session. work_brief at the start of a session shows your tasks, open tasks and current decisions; work_add (title, label, link, part_of, for) adds a task; work_start takes one (someone else's started task is refused); work_update (task, line) says where it is; work_decide (line, why, optional task, replaces) records a choice; work_done (task, line=outcome, link) finishes; work_release gives it back; work_find (query) searches everything. Blocked on a person? work_need (for = their name, line, buttons = up to three such as Approve, Not now, Ask my agent) pins a Needs-you card; their click comes back to you as a message. work_withdraw (need) takes it back; work_answer (need, button) answers one for your person. An outside tracker (GitHub, Jira, files) stays the source of truth: use its label and link. Join the Crew with action=join; discovery and messages use AgentBridge."), mcp.WithString("project", mcp.Required()), mcp.WithString("team_action", mcp.Required(), mcp.Enum("work_brief", "work_add", "work_start", "work_update", "work_decide", "work_done", "work_release", "work_find", "work_need", "work_withdraw", "work_answer")), mcp.WithString("task", mcp.Description("Task id or the label of an open task.")), mcp.WithString("label"), mcp.WithString("title"), mcp.WithString("link"), mcp.WithString("part_of"), mcp.WithString("for"), mcp.WithString("line"), mcp.WithString("why"), mcp.WithString("replaces"), mcp.WithString("query"), mcp.WithArray("buttons", mcp.Items(map[string]any{"type": "string"})), mcp.WithString("need"), mcp.WithString("button"), mcp.WithString("helper")), handler("team_sync"))
}
