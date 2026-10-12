package server

import "sort"

// crewAskModelSentence is the one sentence every Crew-facing description states about a free-text ask (856-59).
const crewAskModelSentence = "A non-owner's ask is read-only (it reads files, memory, skills and functions; it writes nothing and uses no MCP tools); to make a Crew do something, call one of its functions. The owner's own ask has full access. Only the owner changes a Crew."

// crewAskAllowedTools is the whole tool surface of a guest's free-text ask (856-59, reviewer: an allow list, so a new tool
// is refused until someone classifies it). Everything here reads; nothing acts. The guest acts by calling the functions the
// owner exposes. No shell: it could send data out or use secrets; reads go through the CLI's own file tools. send_message only replies inside the same conversation; submit_crew_suggestion lands in the owner's inbox.
var crewAskAllowedTools = map[string]bool{
	"read_image": true, crewAskFilesToolName: true, "read_skill": true, "search_tools": true, "get_api_spec": true,
	"list_skills": true, "search_skills": true, "send_message": true, "read_agent_messages": true,
	"list_mcp_servers": true, "list_accessible_workflows": true, "list_attached_workflows": true,
	"list_workflow_triggers": true, "list_project_schedules": true, "list_project_triggers": true,
	"get_workflow_trigger_run": true, "list_work_folders": true, "list_functions": true,
	crewSuggestionToolName: true, "WebSearch": true, "EndConversation": true,
}

// crewAskNativeBridgeTools are the ask's allowed tools that must be native bridge tools: every non-core tool is otherwise
// reached by curl through the shell, which a guest's ask does not have. The core ones (search_tools, get_api_spec, the
// shell) and built-in ones (WebSearch, EndConversation) are not custom tools. Unregistered names are skipped by the bridge.
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
