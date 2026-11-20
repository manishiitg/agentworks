package agentworksclient

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The Codex Team pane (#3357): the same read-only Team projection the Claude Code pane shows (team_pane on the
// local bridge, decided by the server for this exact attached chat), as one MCP tool with an MCP Apps view. A host
// without views still gets the plain-text summary. The view is a new URI when its contract changes.
const (
	TeamToolName    = "agentworks_team"
	teamViewURI     = "ui://agentworks/team-v1.html"
	teamViewMIME    = "text/html;profile=mcp-app"
	teamPaneTimeout = 4 * time.Second
	teamOpenTimeout = 18 * time.Second // opening a chat reads it from its owner's Mac
	teamForkTimeout = 100 * time.Second
)

//go:embed team_view.html
var teamViewHTML string

// addTeamPane registers the Team tool and its view on a Codex plugin MCP child. session resolves the exact Codex
// thread of the calling chat; nothing here chooses or attaches another chat.
func addTeamPane(s *server.MCPServer, root, hostKey string, session func(mcp.CallToolRequest) (string, error)) {
	meta := mcp.NewMetaFromMap(map[string]any{"ui": map[string]any{"resourceUri": teamViewURI}, "openai/outputTemplate": teamViewURI})
	tool := mcp.NewTool(TeamToolName,
		mcp.WithDescription("Show this chat's AgentWorks Team: what needs the person, the Team kit, which agents are working now and every Team agent by person. agent (an id from the list) opens that agent's recent chat when it is shared with the person. fork (an id) copies a teammate's agent and its chat to the person's own Mac; only when the person asks. Read-only otherwise; it never answers, sends or changes sharing."),
		mcp.WithString("agent", mcp.Description("Open one agent by its id from the Team list.")),
		mcp.WithString("fork", mcp.Description("Copy this teammate's agent (its id) to the person's own Mac, when the person asks.")),
		mcp.WithTitleAnnotation("AgentWorks Team"),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	)
	tool.Meta = meta
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		thread, err := session(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		args := req.GetArguments()
		agent, _ := args["agent"].(string)
		fork, _ := args["fork"].(string)
		if fork != "" {
			out := teamFork(ctx, root, hostKey, thread, fork)
			text := "Copied. It opens in AgentWorks on your Mac."
			if out["forked"] != true {
				text, _ = out["error"].(string)
			}
			return mcp.NewToolResultStructured(out, text), nil
		}
		pane := readTeamPane(ctx, root, hostKey, thread, agent)
		return mcp.NewToolResultStructured(pane, TeamPaneText(pane)), nil
	})
	resource := mcp.NewResource(teamViewURI, "AgentWorks Team", mcp.WithMIMEType(teamViewMIME), mcp.WithResourceDescription("The AgentWorks Team for this chat."))
	s.AddResource(resource, func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: teamViewURI, MIMEType: teamViewMIME, Text: teamViewHTML, Meta: map[string]any{"ui": map[string]any{"prefersBorder": true}}}}, nil
	})
}

// readTeamPane returns the server's pane JSON for this exact chat, plus attached/failed so the view can say why
// there is nothing to show (not connected, or AgentWorks could not be reached).
func readTeamPane(ctx context.Context, root, hostKey, thread, agent string) map[string]any {
	limit := teamPaneTimeout
	request := LocalBridgeRequest{Action: "team_pane", Provider: "codex", SessionID: thread, HostKey: hostKey}
	if agent != "" {
		limit = teamOpenTimeout
		request.Arguments = map[string]any{"query": agent}
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := CallLocalBridge(ctx, root, request)
	if err != nil || (out.Attached && out.Error != "") || out.Error == "unknown local bridge action" {
		return map[string]any{"attached": out.Attached, "failed": true}
	}
	if !out.Attached {
		return map[string]any{"attached": false}
	}
	pane := map[string]any{}
	if json.Unmarshal(out.Result, &pane) != nil {
		return map[string]any{"attached": true, "failed": true}
	}
	pane["attached"] = true
	return pane
}

func teamFork(ctx context.Context, root, hostKey, thread, agent string) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, teamForkTimeout)
	defer cancel()
	out, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "team_sync", Provider: "codex", SessionID: thread, HostKey: hostKey, Arguments: map[string]any{"team_action": "fork_from", "agent_key": agent}})
	switch {
	case err != nil || out.Error == "unknown local bridge action":
		return map[string]any{"forked": false, "error": "Couldn't reach AgentWorks. Open the AgentWorks app on this Mac and try again."}
	case !out.Attached:
		return map[string]any{"forked": false, "error": "Connect this chat to AgentWorks first: type /agentworks attach."}
	case out.Error != "":
		return map[string]any{"forked": false, "error": out.Error}
	}
	return map[string]any{"forked": true}
}

// TeamPaneText is the plain summary for hosts without views (the Codex CLI) and for the model.
func TeamPaneText(p map[string]any) string {
	if p["failed"] == true {
		return "Couldn't reach AgentWorks. Open the AgentWorks app on this Mac and try again."
	}
	if p["attached"] != true {
		return "This chat isn't connected to AgentWorks. Type /agentworks attach to connect it."
	}
	if s, _ := p["crew_name"].(string); s == "" {
		return "This chat isn't in a Crew's Team yet. Ask your agent to join your Crew's Team."
	}
	if p["team_on"] != true {
		return "The Team for " + teamStr(p["crew_name"]) + " is off. The Crew's owner turns it on in AgentWorks."
	}
	if p["in_team"] != true {
		return "This agent isn't shared with the Team. Share it from the Team page in AgentWorks."
	}
	if a, ok := p["agent"].(map[string]any); ok {
		return teamAgentText(a)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Team · %s\n", teamStr(p["crew_name"]))
	needs := teamList(p["needs"])
	fmt.Fprintf(&b, "\nNeeds you (%d)\n", len(needs))
	for _, n := range needs {
		fmt.Fprintf(&b, "  %s%s · from %s · answer in AgentWorks\n", teamRef(n), teamStr(n["line"]), teamStr(n["agent"]))
	}
	kit := teamList(p["kit"])
	todo, done := 0, 0
	for _, k := range kit {
		if teamStr(k["state"]) == "done" {
			done++
			continue
		}
		todo++
		fmt.Fprintf(&b, "%s", map[bool]string{true: "\nTeam kit\n", false: ""}[todo == 1])
		fmt.Fprintf(&b, "  %s %s · %s\n", teamKitMark[teamStr(k["state"])], teamStr(k["title"]), teamKitShort[teamStr(k["state"])])
	}
	if done > 0 {
		fmt.Fprintf(&b, "%s  ✓ %d done\n", map[bool]string{true: "\nTeam kit\n", false: ""}[todo == 0], done)
	}
	agents := teamList(p["agents"])
	working := []string{}
	for _, a := range agents {
		if a["online"] == true && teamStr(a["activity"]) == "running" {
			working = append(working, fmt.Sprintf("  ● %s · %s", teamStr(a["name"]), teamWhose(a)))
		}
	}
	fmt.Fprintf(&b, "\nWorking now (%d)\n", len(working))
	if len(working) > 0 {
		b.WriteString(strings.Join(working, "\n") + "\n")
	}
	online := 0
	for _, a := range agents {
		if a["online"] == true {
			online++
		}
	}
	fmt.Fprintf(&b, "\nTeam (%d online)\n", online)
	// Grouped by person like the Team panel: you first, teammates in order, the Team agent last.
	rank := func(a map[string]any) int {
		switch teamWhose(a) {
		case "You":
			return 0
		case "Team agent":
			return 2
		}
		return 1
	}
	sort.SliceStable(agents, func(i, j int) bool {
		if ri, rj := rank(agents[i]), rank(agents[j]); ri != rj {
			return ri < rj
		}
		return rank(agents[i]) == 1 && teamWhose(agents[i]) < teamWhose(agents[j])
	})
	last := ""
	for _, a := range agents {
		if w := teamWhose(a); w != last {
			fmt.Fprintf(&b, "  %s\n", w)
			last = w
		}
		mark, status := "○", teamStr(a["offline_line"])
		if a["online"] == true {
			mark, status = "●", ""
		}
		fmt.Fprintf(&b, "    %s %s%s  [%s]\n", mark, teamStr(a["name"]), map[bool]string{true: " · " + status, false: ""}[status != ""], teamStr(a["id"]))
	}
	b.WriteString("\nTo open one agent's chat, ask me to open it by name.")
	return b.String()
}

var (
	teamKitMark  = map[string]string{"none": "○", "progress": "◐", "done": "✓", "key": "!"}
	teamKitShort = map[string]string{"none": "Not started", "progress": "In progress", "done": "Done", "key": "Needs a key"}
)

func teamAgentText(a map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s\n", teamStr(a["name"]), teamWhose(a))
	if d := teamStr(a["doing"]); d != "" {
		fmt.Fprintf(&b, "Working on: %s\n", d)
	}
	msgs := teamList(a["messages"])
	for _, m := range msgs {
		who := teamStr(a["name"])
		if teamStr(m["role"]) == "user" {
			who = "Asked"
		}
		fmt.Fprintf(&b, "\n%s: %s\n", who, teamStr(m["text"]))
	}
	if len(msgs) == 0 {
		if l := teamStr(a["last"]); l != "" {
			fmt.Fprintf(&b, "Last message: %s\n", l)
		}
		b.WriteString(teamNote[teamStr(a["note"])])
	}
	return strings.TrimSpace(b.String())
}

var teamNote = map[string]string{
	"not_live":    "Its chat isn't shared with the Team, so only its status shows. Its owner can share it from the Team page in AgentWorks.",
	"open_in_app": "No chat to show: it isn't shared with you, it's empty, or its Mac is off. Open it in AgentWorks, or ask it from your chat.",
	"unavailable": "Couldn't read this chat right now. Its Mac may be off; try again later.",
	"":            "No messages to show yet.",
}

func teamWhose(a map[string]any) string {
	switch {
	case a["mine"] == true:
		return "You"
	case teamStr(a["kind"]) == "team_agent":
		return "Team agent"
	case teamStr(a["person"]) != "":
		return teamStr(a["person"])
	}
	return "Teammate"
}

func teamRef(n map[string]any) string {
	if r := teamStr(n["ref"]); strings.HasPrefix(r, "#") {
		return r + " "
	}
	return ""
}

func teamStr(v any) string {
	s, _ := v.(string)
	return strings.Join(strings.Fields(s), " ")
}

func teamList(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
