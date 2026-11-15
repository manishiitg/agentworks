package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
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
			if action == "put_online" {
				payload["audience"] = "only_me"
				for _, key := range []string{"audience", "chosen_people"} {
					if value, ok := args[key]; ok {
						payload[key] = value
					}
				}
			}
			if action == "team_sync" {
				for _, key := range []string{"team_action", "line", "why"} {
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
	putOnline := mcp.NewTool("put_online", mcp.WithDescription("Put this exact joined native agent online only when the person asks. Default audience is only_me; chosen or whole_team is explicit. The server derives the source and accounts; never supply a path, session or other agent. Requested/preparing is not ready or live proof."), mcp.WithString("project"), mcp.WithString("audience", mcp.Enum("only_me", "chosen", "whole_team")))
	putOnline.InputSchema.Properties["chosen_people"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "User IDs from the Crew, when audience is chosen."}
	agentworksclient.AddPluginAction(s, putOnline, handler("put_online"))
	agentworksclient.AddPluginAction(s, mcp.NewTool("team_sync", mcp.WithDescription("Post explicit doing or decision activity from this exact session in its Crew's Team. Join the Crew with action=join; discovery and messages use AgentBridge."), mcp.WithString("project", mcp.Required()), mcp.WithString("team_action", mcp.Required(), mcp.Enum("doing", "decision")), mcp.WithString("line"), mcp.WithString("why")), handler("team_sync"))
}

// No provider/session/attachment overrides: this command can select only its
// own detected native process and sends no generic desktop credential.
func terminalPutOnlineCommand(o *options) *cobra.Command {
	var project, audience string
	var people []string
	cmd := &cobra.Command{Use: "put-online", Short: "Put this exact joined Claude Code or Codex session online", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return err
		}
		own, ok, err := ownAgentSession(cmd.Context(), root, o)
		if err != nil {
			return err
		}
		if !ok || (own.provider != "claude" && own.provider != "codex") || own.session == "" || own.host == "" {
			return fmt.Errorf("run Put online from your own connected Claude Code or Codex session")
		}
		out, err := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "put_online", Provider: own.provider, SessionID: own.session, HostKey: own.host, Arguments: map[string]any{"project": project, "audience": audience, "chosen_people": people}})
		if err != nil {
			return err
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		if !out.Attached || !json.Valid(out.Result) {
			return fmt.Errorf("connect this exact session to AgentWorks first")
		}
		_, err = fmt.Fprintln(o.stdout, string(out.Result))
		return err
	}}
	cmd.Flags().StringVar(&project, "project", "", "Optional Crew ID; must match this session's joined Crew")
	cmd.Flags().StringVar(&audience, "audience", "only_me", "only_me, chosen, or whole_team")
	cmd.Flags().StringSliceVar(&people, "chosen-people", nil, "Crew person IDs when audience is chosen")
	return cmd
}
