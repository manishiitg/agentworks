package server

// crewAskModelSentence is the one sentence every Crew-facing description states about a free-text ask (856-59).
const crewAskModelSentence = "A non-owner's ask is read-only (it reads files, memory, skills and functions; it writes nothing and uses no MCP tools); to make a Crew do something, call one of its functions. The owner's own ask has full access. Only the owner changes a Crew."

// crewAskAllowedTools is the whole tool surface of a guest's free-text ask (856-59, reviewer: an allow list, so a new tool
// is refused until someone classifies it). Everything here reads; nothing acts. The guest acts by calling the functions the
// owner exposes. The shell is the only way a Crew CLI reads project files (mcp_only), so it stays; it runs on the read-only filesystem with NO SECRET_* env (server.go), so there is nothing to use or leak but what the guest can already read. send_message only replies inside the same conversation; submit_crew_suggestion lands in the owner's inbox.
var crewAskAllowedTools = map[string]bool{
	"execute_shell_command": true, "read_image": true, "read_skill": true, "search_tools": true, "get_api_spec": true,
	"list_skills": true, "search_skills": true, "send_message": true, "read_agent_messages": true,
	"list_mcp_servers": true, "list_accessible_workflows": true, "list_attached_workflows": true,
	"list_workflow_triggers": true, "list_project_schedules": true, "list_project_triggers": true,
	"get_workflow_trigger_run": true, "list_work_folders": true, "list_functions": true,
	crewSuggestionToolName: true, "WebSearch": true, "EndConversation": true,
}
