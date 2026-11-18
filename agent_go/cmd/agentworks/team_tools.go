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
				for _, key := range []string{"team_action", "task", "label", "title", "link", "part_of", "for", "line", "why", "replaces", "query", "buttons", "need", "button", "helper", "status", "category", "tags", "decision", "file_name", "file_text", "state", "tag", "issue", "who", "mine", "limit"} {
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
	agentworksclient.AddPluginAction(s, mcp.NewTool("team_sync", mcp.WithDescription("The Crew's Team tasks, from this exact joined session. tasks lists the Team's tasks with filters that combine: state (active = not done, the default; all, open, in_progress, blocked, needs_you, stalled, done), category (none = not categorised yet), tag, issue (#2965), who (an agent or person name), mine=true, query, limit (default 30, up to 100). task (task = its id) reads one task with its steps, decisions with why, links, files and notes. task_update (task = its id) sets status (in_progress, blocked, needs_you, done), category, tags (replace the old ones), line (a short note), decision=true with line and why, or a note file (file_name such as plan.md, file_text); the agent that asked, the agent doing it and the Team owner's agents may update a task. work_brief at the start of a session shows the tasks you are doing (my_tasks), open Team tasks any agent may take (team_requests) and requests waiting on your person; work_answer (need, button, optional helper) answers one for your person. New work, hand-offs and requests to a person are AgentBridge messages (target=self, an agent, team or person:<name> with buttons). An outside tracker (GitHub, Jira, files) stays the source of truth: use its label and link. Join the Crew with action=join; discovery and messages use AgentBridge."), mcp.WithString("project", mcp.Required()), mcp.WithString("team_action", mcp.Required(), mcp.Enum("tasks", "task", "task_update", "work_brief", "work_answer")), mcp.WithString("task", mcp.Description("Task id from tasks.")), mcp.WithString("status", mcp.Enum("in_progress", "blocked", "needs_you", "done")), mcp.WithString("category"), mcp.WithArray("tags", mcp.Items(map[string]any{"type": "string"})), mcp.WithBoolean("decision"), mcp.WithString("file_name"), mcp.WithString("file_text"), mcp.WithString("state", mcp.Enum("active", "all", "open", "in_progress", "blocked", "needs_you", "stalled", "done")), mcp.WithString("tag"), mcp.WithString("issue"), mcp.WithString("who"), mcp.WithBoolean("mine"), mcp.WithNumber("limit"), mcp.WithString("line"), mcp.WithString("why"), mcp.WithString("query"), mcp.WithString("need"), mcp.WithString("button"), mcp.WithString("helper")), handler("team_sync"))
}
