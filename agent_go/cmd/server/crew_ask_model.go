package server

import (
	"slices"
	"sort"
	"strings"
)

// crewAskModelSentence is the one sentence every Crew-facing description states about a free-text ask (856-59).
const crewAskModelSentence = "Everyone except the Crew's owner uses it in Run mode: a chat or ask there understands the Crew (files, memory, skills, functions) and runs what its owner published; it changes nothing and uses no MCP tools. To make a Crew do something, call one of its functions. The owner's own chat is Builder mode, with full access. Only the owner changes a Crew."

// crewAskAllowedTools is the whole tool surface of a guest's free-text ask (856-59, reviewer: an allow list, so a new tool
// is refused until someone classifies it). Everything here reads; nothing acts. The guest acts by calling the functions the
// owner exposes. No shell: it could send data out or use secrets; reads go through the CLI's own file tools. send_message only replies inside the same conversation; submit_crew_suggestion lands in the owner's inbox.
var crewAskAllowedTools = map[string]bool{
	"read_image": true, crewAskFilesToolName: true, "read_skill": true, "search_tools": true, "get_api_spec": true,
	"list_skills": true, "search_skills": true, "send_message": true, "read_agent_messages": true,
	"list_mcp_servers": true, "list_accessible_workflows": true, "list_attached_workflows": true,
	"list_workflow_triggers": true, "list_project_schedules": true, "list_project_triggers": true,
	"get_workflow_trigger_run": true, "list_functions": true,
	crewPublishedCallToolName: true, crewPublishedGetToolName: true,
	crewSuggestionToolName: true, "WebSearch": true, "EndConversation": true,
}

// crewAskReadOnlyTurn is the one server-side test for Run mode (856-59, owner 2026-10-13): every chat a non-owner has with
// a Crew (the app chat, also opened from a dashboard, an MCP ask) and every other turn the server already runs read-only on a
// Crew (a Slack channel mention, a pinned turn) understands the Crew and runs what the owner published. The Crew's owner is in
// Builder mode. A guest's function-call turn is not it: that is the owner's published function running with the owner's
// authority. readOnly is the turn's own read-only flag.
func crewAskReadOnlyTurn(req QueryRequest, currentUserID string, readOnly bool) bool {
	guest := crewGuestCallerForTurn(req, currentUserID)
	if guest != "" {
		return req.CrewAskReadOnly
	}
	if isCrewReaderTurn(req, currentUserID) {
		return true
	}
	return readOnly && strings.EqualFold(strings.TrimSpace(req.AgentProfileID), crewProfileID) && isCrewProjectPath(strings.TrimSpace(req.SelectedFolder))
}

// crewAskNativeBridgeTools are the ask's allowed tools that must be native bridge tools: every non-core tool is otherwise
// reached by curl through the shell, which a guest's ask does not have. The core ones (search_tools, get_api_spec, the
// shell) and built-in ones (WebSearch, EndConversation) are not custom tools. Unregistered names are skipped by the bridge.
// crewAskBridgeTools adds the ask's native tools to the bridge tool names. It must not ask the product tool gate: this
// runs before the session registers those tools (and declares them to the gate), so Admit refused every one of them
// and the guest's turn got a bridge without read_project_files or send_message (856-59, live on a server with a
// Claude bridge). The gate still decides at registration, and the bridge skips a name that was never registered.
func crewAskBridgeTools(existing []string) []string {
	out := append([]string(nil), existing...)
	for _, name := range crewAskNativeBridgeTools() {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func crewAskNativeBridgeTools() []string {
	var names []string
	for name := range crewAskAllowedTools {
		switch name {
		case "execute_shell_command", "search_tools", "get_api_spec", "WebSearch", "EndConversation":
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
