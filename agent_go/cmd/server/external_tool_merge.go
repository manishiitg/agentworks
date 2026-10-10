package server

import (
	"fmt"
	"sort"
	"strings"
)

// One tool per object (PLAT-738): a merged tool takes an action and runs the
// existing tool for it. The existing tools stay in the catalog as hidden
// aliases, so old clients and the CLI keep calling them by name; lists and
// the tool menu show only the merged tool, narrowed to the actions this
// connection may use. A call is translated to its member before any check,
// so every scope, owner and validation rule stays the member's.
//
// A member may be "workflowTool|crewTool": the call runs the second when it
// names a crew_id, else the first. A merged tool with one action takes it
// without an action argument.
type externalToolMerge struct {
	name, summary string
	actions       [][2]string // action, member tool (or "workflowTool|crewTool")
}

var externalToolMerges = []externalToolMerge{
	{"dashboard", "Live dashboards of workflows, Relays, Crews and owned Code projects: find them, read and edit drafts, check and publish revisions, and get links.", [][2]string{
		{"list", "list_dashboards"}, {"get", "get_dashboard"}, {"link", "get_dashboard_link"},
		{"create", "create_dashboard"}, {"update", "update_dashboard"}, {"validate", "validate_dashboard"},
		{"preview", "preview_dashboard"}, {"publish", "publish_dashboard"}, {"restore", "restore_dashboard"},
	}},
	{"settings", "Read and change the setup of a workflow, Relay or Crew: models, MCP servers, skills, secrets, Pulse, browser mode, notifications and after-run steps.", [][2]string{
		{"get", "get_settings"}, {"update", "update_settings"},
	}},
	{"needs_you", "What is waiting on you across workflows and Crews: agent questions and open decisions. List them, then answer or dismiss one.", [][2]string{
		{"list", "list_needs_you"}, {"answer", "answer_needs_you"},
	}},
	{"account", "Shared-account token use, daily and weekly limits, and which models an account may use. Limits and models are admin-only.", [][2]string{
		{"usage", "get_token_usage"}, {"set_limits", "set_token_limits"}, {"set_allowed_models", "set_allowed_models"},
	}},
	{"help", "Guidance for using this connection: your access context, guidance topics to load, and the AgentWorks skill to install.", [][2]string{
		{"context", "get_agent_context"}, {"topics", "list_guidance_topics"}, {"topic", "get_guidance_topic"}, {"skill", "get_skill"},
	}},
	{"workflow", "Find workflows and Relays you can use, read one workflow's manifest and access, or its plan.", [][2]string{
		{"list", "list_workflows"}, {"get", "get_workflow"}, {"plan", "get_plan"},
	}},
	{"crew", "Crews you can use: list, read, create, edit, export and import. Ask a Crew with ask_crew.", [][2]string{
		{"list", "list_crews"}, {"get", "get_crew"}, {"costs", "get_crew_costs"}, {"create", "create_crew"}, {"update", "update_crew"},
		{"export", "export_crew"}, {"import", "import_crew"},
	}},
	{"files", "Browse, search, read and write project files, with revision checks, for a workflow or Relay (workflow_id), or a Crew you own (crew_id; read for anyone with access, write for its owner, text or binary). action=code lists a step's saved code.", [][2]string{
		{"list", "list_files|list_crew_files"}, {"search", "search_files|search_crew_files"}, {"read", "read_file|read_crew_file"},
		{"write", "write_file|write_crew_file"}, {"code", "list_step_code"},
	}},
	{"functions", "A workflow's or Crew's typed entry points (workflow_id or crew_id): list them, call one, check a call and answer its question.", [][2]string{
		{"list", "list_workflow_functions|list_crew_functions"}, {"call", "call_workflow_function|call_crew_function"},
		{"status", "get_workflow_function_call|get_crew_function_call"}, {"reply", "reply_workflow_function_call|reply_crew_function_call"},
		{"calls", "list_crew_function_calls"},
	}},
	{"suggest_change", "Suggest a change to a workflow (workflow_id) or a Crew (crew_id) for its owner to review; it never changes anything itself.", [][2]string{
		{"suggest", "suggest_workflow_change|suggest_crew_change"},
	}},
	{"runs", "Runs of a workflow or Relay: list them, read one, its logs, poll a session started externally and answer a pending input.", [][2]string{
		{"list", "list_runs"}, {"get", "get_run"}, {"logs", "get_logs"}, {"status", "run_status"}, {"reply_input", "run_reply_input"},
	}},
	{"builder", "Edit a workflow or Relay through its Builder model in your existing workflow chat, and talk to its Pulse. Submit with action=chat, then poll status; answer a question with reply_input; cancel an operation; read and restore file edits.", [][2]string{
		{"chat", "builder_chat"}, {"status", "builder_status"}, {"reply_input", "builder_reply_input"}, {"cancel", "builder_cancel"},
		{"file_history", "builder_file_history"}, {"restore_file", "builder_restore_file"},
		{"pulse_chat", "builder_pulse_chat"}, {"pulse_status", "builder_pulse_status"},
	}},
	{"relay", "Relays: create and edit one, test the draft, publish an immutable version, run a published version and read its runs and releases. Edit its code with builder action=chat.", [][2]string{
		{"create", "create_relay"}, {"update", "update_relay"}, {"test", "test_relay"}, {"publish", "publish_relay"},
		{"run", "run_relay"}, {"get_run", "get_relay_run"}, {"releases", "get_relay_releases"},
	}},
	{"code_review", "Read-only, audited review of every Code workspace for admins and Code reviewers: workspaces, costs, files, chats and the audit log.", [][2]string{
		{"workspaces", "list_code_workspaces"}, {"costs", "get_code_costs"}, {"files", "list_code_files"}, {"file", "read_code_file"},
		{"chats", "list_code_chats"}, {"chat", "read_code_chat"}, {"audit", "get_code_audit"},
	}},
	{"code", "Run your own Code from here (needs code:run): list your projects and their chats, ask a chat something and read the reply (commands, files and folder guards run in the real sandbox), and read a project's state (folder guard, slot, chats).", [][2]string{
		{"projects", "list_my_code_projects"}, {"chats", "list_my_code_chats"}, {"ask", "ask_my_code"}, {"state", "get_my_code_state"},
		{"open_chat", "open_my_code_chat"}, {"close_chat", "close_my_code_chat"}, {"stop", "stop_my_code_chat"},
	}},
}

// Hidden with no merged tool: a narrower duplicate of a merged action.
var externalHiddenAliases = map[string]string{"get_report_link": "dashboard action=link"}

func mergeExternalTools(catalog []externalTool) ([]externalTool, error) {
	index := make(map[string]int, len(catalog))
	for i, tool := range catalog {
		index[tool.Name] = i
	}
	for name := range externalHiddenAliases {
		if i, ok := index[name]; ok {
			catalog[i].hidden = true
		}
	}
	for _, merge := range externalToolMerges {
		if _, taken := index[merge.name]; taken {
			return nil, fmt.Errorf("merged tool %q collides with an existing tool", merge.name)
		}
		props := map[string]any{}
		actions := map[string]string{}
		order := []string{}
		for _, pair := range merge.actions {
			found := false
			for _, name := range externalMemberNames(pair[1]) {
				i, ok := index[name]
				if !ok {
					continue // not admitted on this server
				}
				found = true
				member := &catalog[i]
				if _, has := member.InputSchema["properties"].(map[string]any)["action"]; has {
					return nil, fmt.Errorf("merged tool %q: member %q already has an action field", merge.name, member.Name)
				}
				member.hidden = true
				for key, value := range member.InputSchema["properties"].(map[string]any) {
					if _, seen := props[key]; !seen {
						props[key] = value
					}
				}
			}
			if found {
				actions[pair[0]] = pair[1]
				order = append(order, pair[0])
			}
		}
		if len(order) == 0 {
			continue
		}
		props["action"] = map[string]any{"type": "string", "enum": stringsToAny(order)}
		catalog = append(catalog, externalTool{
			Name:        merge.name,
			Description: merge.summary,
			InputSchema: map[string]any{"type": "object", "properties": props, "required": externalMergedRequired(order), "additionalProperties": false},
			actions:     actions,
			actionOrder: order,
		})
	}
	return catalog, nil
}

func externalCatalogTool(name string) (externalTool, bool) {
	catalog, err := externalTools()
	if err != nil {
		return externalTool{}, false
	}
	for _, tool := range catalog {
		if tool.Name == name {
			return tool, true
		}
	}
	return externalTool{}, false
}

// externalMergedForClaims narrows a merged tool to the actions this
// connection may call and describes each one with its required fields.
// ok is false when no action is allowed.
func externalMemberNames(spec string) []string { return strings.Split(spec, "|") }

// externalMergedRequired: the action is required unless the tool has just one.
func externalMergedRequired(actions []string) []any {
	if len(actions) <= 1 {
		return []any{}
	}
	return []any{"action"}
}

// externalMergedAllows: some action of the merged tool is allowed.
func externalMergedAllows(c *UserClaims, tool externalTool) bool {
	for _, spec := range tool.actions {
		for _, name := range externalMemberNames(spec) {
			if m, ok := externalCatalogTool(name); ok && externalTokenAllows(c, m) {
				return true
			}
		}
	}
	return false
}

// externalMergedForClaims narrows a merged tool to the actions this
// connection may call and describes each one with its required fields.
// ok is false when no action is allowed.
func externalMergedForClaims(claims *UserClaims, tool externalTool) (externalTool, bool) {
	allowed := []string{}
	var lines []string
	props := map[string]any{}
	for _, action := range tool.actionOrder {
		var members []externalTool
		for _, name := range externalMemberNames(tool.actions[action]) {
			if member, ok := externalCatalogTool(name); ok && externalTokenAllows(claims, member) {
				members = append(members, member)
			}
		}
		if len(members) == 0 {
			continue
		}
		allowed = append(allowed, action)
		line := "- action=" + action + ": " + firstSentence(members[0].Description)
		if len(members) == 1 {
			required := []string{}
			if raw, ok := members[0].InputSchema["required"].([]any); ok {
				for _, r := range raw {
					required = append(required, fmt.Sprint(r))
				}
			}
			sort.Strings(required)
			if len(required) > 0 {
				line += " Requires " + strings.Join(required, ", ") + "."
			}
		} else {
			line += " Pass workflow_id, or crew_id for a Crew."
		}
		lines = append(lines, line)
		for _, member := range members {
			for key, value := range member.InputSchema["properties"].(map[string]any) {
				if _, seen := props[key]; !seen {
					props[key] = value
				}
			}
		}
	}
	if len(allowed) == 0 {
		return tool, false
	}
	props["action"] = map[string]any{"type": "string", "enum": stringsToAny(allowed)}
	out := tool
	if len(allowed) > 1 {
		out.Description = tool.Description + "\n" + strings.Join(lines, "\n")
	}
	out.InputSchema = map[string]any{"type": "object", "properties": props, "required": externalMergedRequired(allowed), "additionalProperties": false}
	return out, true
}

func firstSentence(text string) string {
	if i := strings.Index(text, ". "); i >= 0 {
		return text[:i+1]
	}
	return text
}

// externalListedTools is what a connection is shown: allowed tools, merged
// tools narrowed to their allowed actions, hidden aliases left out.
func externalListedTools(claims *UserClaims, allowed []externalTool) []externalTool {
	out := make([]externalTool, 0, len(allowed))
	for _, tool := range allowed {
		if tool.hidden {
			continue
		}
		if tool.actions != nil {
			narrowed, ok := externalMergedForClaims(claims, tool)
			if !ok {
				continue
			}
			tool = narrowed
		}
		out = append(out, tool)
	}
	return out
}

// externalResolveMerged turns a merged-tool call into its member's call.
func externalResolveMerged(tool externalTool, args map[string]any) (externalTool, map[string]any, error) {
	if tool.actions == nil {
		return tool, args, nil
	}
	action := externalArg(args, "action")
	if action == "" && len(tool.actionOrder) == 1 {
		action = tool.actionOrder[0]
	}
	spec, ok := tool.actions[action]
	if !ok {
		return tool, args, fmt.Errorf("%s needs action, one of: %s", tool.Name, strings.Join(tool.actionOrder, ", "))
	}
	names := externalMemberNames(spec)
	name := names[0]
	if len(names) == 2 && externalArg(args, "crew_id") != "" {
		name = names[1]
	}
	member, ok := externalCatalogTool(name)
	if !ok {
		return tool, args, fmt.Errorf("%s action %s is unavailable here", tool.Name, action)
	}
	// The merged schema is the union of its members' fields, so a caller may pass a field only another member takes: crew_id
	// picks the crew member, whose own status tool names no crew (the call_id says which), and wait_seconds belongs to the
	// members that wait. Leave out what this member does not declare but the merged tool does; anything the merged tool
	// does not know is still passed on and refused by the member's own validation.
	memberProps, _ := member.InputSchema["properties"].(map[string]any)
	mergedProps, _ := tool.InputSchema["properties"].(map[string]any)
	rest := make(map[string]any, len(args))
	for key, value := range args {
		if key == "action" {
			continue
		}
		if _, declared := memberProps[key]; !declared {
			if _, merged := mergedProps[key]; merged {
				continue
			}
		}
		rest[key] = value
	}
	return member, rest, nil
}
