package agentworksclient

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The Codex Team pane (#3357): the same Team projection the Claude Code pane shows (team_pane on the local bridge,
// decided by the server for this exact attached chat), as one MCP tool with an MCP Apps view. The view's buttons
// answer a Needs-you card (work_answer) and send a Team ask (messages ask): the same server paths as the Team page
// and the agent's own tools. A host without views still gets the plain-text summary. The view is a new URI when its
// contract changes.
const (
	TeamToolName       = "agentworks_team"
	TeamAnswerToolName = "agentworks_team_answer"
	teamViewURI        = "ui://agentworks/team-v2.html"
	teamViewMIME       = "text/html;profile=mcp-app"
	teamPaneTimeout    = 4 * time.Second
	teamOpenTimeout    = 18 * time.Second // opening a chat reads it from its owner's Mac
	teamForkTimeout    = 100 * time.Second
)

//go:embed team_view.html
var teamViewHTML string

// addTeamPane registers the Team tool and its view on a Codex plugin MCP child. session resolves the exact Codex
// thread of the calling chat; nothing here chooses or attaches another chat.
func addTeamPane(s *server.MCPServer, root, hostKey string, session func(mcp.CallToolRequest) (string, error)) {
	meta := mcp.NewMetaFromMap(map[string]any{"ui": map[string]any{"resourceUri": teamViewURI}, "openai/outputTemplate": teamViewURI})
	tool := mcp.NewTool(TeamToolName,
		mcp.WithDescription("Show this chat's AgentWorks Team: what needs the person, the asks sent to this agent, the Team kit, which agents are working now and every Team agent by person. agent (an id from the list) opens that agent's recent chat when it is shared with the person. Only when the person asks: fork (an id) copies a teammate's agent to their Mac; ask (an agent's address) with message sends that agent a Team ask from the person. Needs-you requests are the person's to answer, in this view or in AgentWorks; this tool never answers them or changes sharing."),
		mcp.WithString("agent", mcp.Description("Open one agent by its id from the Team list.")),
		mcp.WithString("fork", mcp.Description("Copy this teammate's agent (its id) to the person's own Mac, when the person asks.")),
		mcp.WithString("ask", mcp.Description("Send a Team ask to this agent (its address from the Team list), when the person wrote one.")),
		mcp.WithString("message", mcp.Description("The person's words for ask.")),
		mcp.WithString("offline", mcp.Enum("wait"), mcp.Description("ask: only after AgentWorks said the agent is offline and the person chose to wait; it answers when it is back.")),
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
		if to, _ := args["ask"].(string); to != "" {
			message, _ := args["message"].(string)
			offline, _ := args["offline"].(string)
			out := teamAsk(ctx, root, hostKey, thread, to, message, offline)
			return mcp.NewToolResultStructured(out, teamStr(out["note"])), nil
		}
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
	// Answering a Needs-you card is the person's own decision, so it is a separate tool only the view can call:
	// MCP Apps visibility ["app"] keeps it out of the model's tool list (Codex filters it in its tool catalog).
	answer := mcp.NewTool(TeamAnswerToolName,
		mcp.WithDescription("The person's click on a Needs-you button in the AgentWorks Team view. Called by the view only."),
		mcp.WithString("answer", mcp.Required(), mcp.Description("The Needs-you request's id.")),
		mcp.WithString("button", mcp.Required(), mcp.Description("The button the person clicked.")),
		mcp.WithTitleAnnotation("Answer a Team request"),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	)
	answer.Meta = mcp.NewMetaFromMap(map[string]any{"ui": map[string]any{"resourceUri": teamViewURI, "visibility": []string{"app"}}})
	s.AddTool(answer, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		thread, err := session(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		need, _ := req.GetArguments()["answer"].(string)
		button, _ := req.GetArguments()["button"].(string)
		out := teamAnswer(ctx, root, hostKey, thread, need, button)
		return mcp.NewToolResultStructured(out, teamStr(out["note"])), nil
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
	// server names the AgentWorks this chat is connected to, so "not in a Team" can say where.
	server := teamServerName(out.OwnServer)
	if err != nil || (out.Attached && out.Error != "") || out.Error == "unknown local bridge action" {
		return map[string]any{"attached": out.Attached, "failed": true, "server": server}
	}
	if !out.Attached {
		return map[string]any{"attached": false}
	}
	pane := map[string]any{}
	if json.Unmarshal(out.Result, &pane) != nil {
		return map[string]any{"attached": true, "failed": true, "server": server}
	}
	pane["attached"], pane["server"] = true, server
	return pane
}

// teamAnswer is the person's click on a Needs-you card: the server's work_answer, which checks the request is
// theirs and still waiting, and replies in its thread exactly as the Team page does.
func teamAnswer(ctx context.Context, root, hostKey, thread, need, button string) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, teamPaneTimeout*3)
	defer cancel()
	out, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "team_sync", Provider: "codex", SessionID: thread, HostKey: hostKey, Arguments: map[string]any{"team_action": "work_answer", "need": need, "button": button}})
	switch {
	case err != nil || out.Error == "unknown local bridge action":
		return map[string]any{"answered": false, "note": "Couldn't reach AgentWorks. Open the AgentWorks app on this Mac and try again."}
	case !out.Attached:
		return map[string]any{"answered": false, "note": "Connect this chat to AgentWorks first: type /agentworks attach."}
	case out.Error != "":
		return map[string]any{"answered": false, "note": out.Error}
	}
	reply := map[string]any{}
	_ = json.Unmarshal(out.Result, &reply)
	note := teamStr(reply["note"])
	if note == "" {
		note = "Answered."
	}
	return map[string]any{"answered": true, "note": note}
}

// teamAsk sends the person's words to one Team agent as an ask from this chat, through the same messaging path as
// the agent's own messages tool; the server admits the target or refuses.
func teamAsk(ctx context.Context, root, hostKey, thread, to, message, offline string) map[string]any {
	message = strings.TrimSpace(message)
	if message == "" {
		return map[string]any{"asked": false, "note": "Type your question first."}
	}
	ctx, cancel := context.WithTimeout(ctx, teamOpenTimeout)
	defer cancel()
	args := map[string]any{"action": "ask", "target": to, "message": message}
	if offline != "" {
		args["offline"] = offline
	}
	out, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "message", Provider: "codex", SessionID: thread, HostKey: hostKey, Arguments: args})
	switch {
	case err != nil:
		return map[string]any{"asked": false, "note": "Couldn't reach AgentWorks. Open the AgentWorks app on this Mac and try again."}
	case !out.Attached:
		return map[string]any{"asked": false, "note": "Connect this chat to AgentWorks first: type /agentworks attach."}
	case out.Error != "":
		return map[string]any{"asked": false, "note": out.Error}
	}
	return map[string]any{"asked": true, "note": "Asked. The answer arrives in this chat."}
}

// teamServerName is the host of the AgentWorks this chat is connected to (the person's own setting), never a path or token.
func teamServerName(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Hostname()
	}
	return ""
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
		if server := teamStr(p["server"]); server != "" {
			return "This chat isn't in a Team on " + server + ". If your Team is on another AgentWorks, connect this chat there; otherwise ask your agent to join your project's Team."
		}
		return "This chat isn't in a project's Team yet. Ask your agent to join your project's Team."
	}
	if p["team_on"] != true {
		return "The Team for " + teamStr(p["crew_name"]) + " is off. The project's owner turns it on in AgentWorks."
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
		fmt.Fprintf(&b, "  %s%s · from %s · the person answers it in the Team view or AgentWorks\n", teamRef(n), teamStr(n["line"]), teamStr(n["agent"]))
	}
	if asks := teamList(p["asks"]); len(asks) > 0 {
		fmt.Fprintf(&b, "\nYour tasks (%d)\n", len(asks))
		for _, a := range asks {
			fmt.Fprintf(&b, "  %s%s · from %s · %s\n", teamRef(a), teamStr(a["title"]), teamStr(a["asked_by"]), strings.ReplaceAll(teamStr(a["state"]), "_", " "))
		}
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
	// Grouped by person like the Team panel: you first, then teammates in order.
	rank := func(a map[string]any) int {
		if teamWhose(a) == "You" {
			return 0
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
