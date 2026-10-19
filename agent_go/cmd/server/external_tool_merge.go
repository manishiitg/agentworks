package server

import (
	"fmt"
	"reflect"
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
	{"share", "Share a Crew, Goal or Flow. get inspects the current audience; set is owner-only and keeps the existing product authoring consent. " + runShareAccessDescription, [][2]string{
		{"get", "get_dashboard_share"}, {"set", "share_dashboard"},
	}},
	{"dashboard", "Live dashboards of workflows, Flows, Crews and owned Code projects: find them, read and edit drafts, check and publish revisions, and get links.", [][2]string{
		{"list", "list_dashboards"}, {"get", "get_dashboard"}, {"link", "get_dashboard_link"},
		{"create", "create_dashboard"}, {"update", "update_dashboard"}, {"validate", "validate_dashboard"},
		{"export_motion", "export_dashboard_motion"}, {"preview", "preview_dashboard"}, {"publish", "publish_dashboard"}, {"restore", "restore_dashboard"},
	}},
	{"settings", "Read and change the setup of a workflow, Flow or Crew; inspect saved secret sources for these and your own Code projects: models, MCP servers, skills, secrets, personal Google/Slack accounts, Pulse, browser mode, notifications and after-run steps.", [][2]string{
		{"get", "get_settings"}, {"update", "update_settings"}, {"secret_sources", "get_secret_sources"},
		{"google_account", "workflow_google_account"}, {"slack_account", "workflow_slack_account"}, {"slack_bot", "workflow_slack_bot"},
	}},
	{"needs_you", "Shares Home's question/decision collector. List what is waiting on you across workflows and Crews, then answer or dismiss one; answered decisions awaiting application include apply_message for the Builder. home action=attention also includes failed-run review items.", [][2]string{
		{"list", "list_needs_you"}, {"answer", "answer_needs_you"}, {"dismiss", "dismiss_needs_you"},
	}},
	{"account", "Shared-account token use, daily and weekly limits, and which models an account may use. Limits and models are admin-only.", [][2]string{
		{"usage", "get_token_usage"}, {"set_limits", "set_token_limits"}, {"set_allowed_models", "set_allowed_models"},
	}},
	{"help", "Guidance for using this connection: your access context, guidance topics to load, and the AgentWorks skill to install.", [][2]string{
		{"context", "get_agent_context"}, {"topics", "list_guidance_topics"}, {"topic", "get_guidance_topic"}, {"skills", "get_guidance_skills"}, {"skill", "get_skill"},
	}},
	{"workflow", "Find workflows and Flows you can use, read one workflow's manifest and access, or its plan.", [][2]string{
		{"list", "list_workflows"}, {"get", "get_workflow"}, {"plan", "get_plan"},
	}},
	{"crew", "Crews you can use: list, read, create, edit, export and import. Ask a Crew with ask_crew.", [][2]string{
		{"list", "list_crews"}, {"get", "get_crew"}, {"costs", "get_crew_costs"}, {"create", "create_crew"}, {"update", "update_crew"},
		{"export", "export_crew"}, {"import", "import_crew"},
	}},
	{"files", "Browse, search, read and write project files, with revision checks, for a workflow or Flow (workflow_id), or a Crew you own (crew_id; read for anyone with access, write for its owner, text or binary). action=code lists a step's saved code.", [][2]string{
		{"list", "list_files|list_crew_files"}, {"search", "search_files|search_crew_files"}, {"read", "read_file|read_crew_file"},
		{"write", "write_file|write_crew_file"}, {"code", "list_step_code"},
	}},
	{"functions", "Workflow and Crew internal function triggers with checked inputs. Crew functions default to a named tab that keeps history and queues calls; execution=fresh starts a new chat for each call. List, call, inspect execution status/messages, read private output files and answer a pending input question.", [][2]string{
		{"list", "list_workflow_functions|list_crew_functions"}, {"call", "call_workflow_function|call_crew_function"},
		{"status", "get_workflow_function_call|get_crew_function_call"}, {"read", "get_workflow_function_call|get_crew_function_call"}, {"reply", "reply_workflow_function_call|reply_crew_function_call"},
		{"calls", "list_crew_function_calls"}, {"define", "update_crew"}, {"update", "update_crew"}, {"delete", "update_crew"},
	}},
	{"suggest_change", "Suggest a change to a workflow (workflow_id) or a Crew (crew_id) for its owner to review; it never changes anything itself.", [][2]string{
		{"suggest", "suggest_workflow_change|suggest_crew_change"},
	}},
	{"runs", "Runs of a workflow or Flow: list them, read one, its logs, poll a session started externally and answer a pending input.", [][2]string{
		{"list", "list_runs"}, {"get", "get_run"}, {"logs", "get_logs"}, {"status", "run_status"}, {"reply_input", "run_reply_input"},
	}},
	{"builder", "Edit a workflow or Flow through its Builder model in your existing workflow chat, and talk to its Pulse. Submit with action=chat, then poll status; answer a question with reply_input; cancel an operation; read and restore file edits.", [][2]string{
		{"chat", "builder_chat"}, {"status", "builder_status"}, {"reply_input", "builder_reply_input"}, {"cancel", "builder_cancel"},
		{"file_history", "builder_file_history"}, {"restore_file", "builder_restore_file"},
		{"pulse_chat", "builder_pulse_chat"}, {"pulse_status", "builder_pulse_status"},
	}},
	{"relay", "Flows: create and edit one, test the draft, publish an immutable version, run a published version and read its runs and releases. Edit its code directly with files action=read/write, then test and inspect the draft run.", [][2]string{
		{"create", "create_relay"}, {"update", "update_relay"}, {"test", "test_relay"}, {"publish", "publish_relay"},
		{"run", "run_relay"}, {"get_run", "get_relay_run"}, {"runs", "list_relay_runs"}, {"releases", "get_relay_releases"},
	}},
	{"code_review", "Read-only, audited review of every Code workspace for admins and Code reviewers: workspaces, costs, files, chats and the audit log.", [][2]string{
		{"workspaces", "list_code_workspaces"}, {"costs", "get_code_costs"}, {"files", "list_code_files"}, {"file", "read_code_file"},
		{"chats", "list_code_chats"}, {"chat", "read_code_chat"}, {"audit", "get_code_audit"},
	}},
	{"code", "Run your own Code from here (needs code:run): list your projects and their chats, ask a chat something and read the reply (commands, files and folder guards run in the real sandbox), and read a project's state (folder guard, slot, chats).", [][2]string{
		{"projects", "list_my_code_projects"}, {"chats", "list_my_code_chats"}, {"ask", "ask_my_code"}, {"read", "read_my_code_chat"}, {"state", "get_my_code_state"},
		{"open_chat", "open_my_code_chat"}, {"rename_chat", "rename_my_code_chat"}, {"close_chat", "close_my_code_chat"}, {"stop", "stop_my_code_chat"},
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
					externalMergeProperty(props, key, value)
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
		if merge.name == "functions" || merge.name == "files" || merge.name == "share" || merge.name == "settings" || merge.name == "dashboard" {
			props["workspace_id"] = externalString("Stable work:<id> or code:<id> from workspace action=list.")
		}
		if merge.name == "functions" {
			props = externalFunctionProperties(props, order)
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
				externalMergeProperty(props, key, value)
			}
		}
	}
	if len(allowed) == 0 {
		return tool, false
	}
	if original, _ := tool.InputSchema["properties"].(map[string]any); original != nil {
		for _, key := range []string{"workspace_id", "name", "description", "instructions", "input_schema", "result_schema"} {
			if v, ok := original[key]; ok {
				props[key] = v
			}
		}
	}
	if tool.Name == "functions" {
		props = externalFunctionProperties(props, allowed)
	}
	props["action"] = map[string]any{"type": "string", "enum": stringsToAny(allowed)}
	out := tool
	if len(allowed) > 1 {
		out.Description = tool.Description + "\n" + strings.Join(lines, "\n")
	}
	// A globally multi-action tool always requires action, even when this
	// connection sees only one. Dispatch uses the same full object contract.
	out.InputSchema = map[string]any{"type": "object", "properties": props, "required": externalMergedRequired(tool.actionOrder), "additionalProperties": false}
	return out, true
}

func firstSentence(text string) string {
	if i := strings.Index(text, ". "); i >= 0 {
		return text[:i+1]
	}
	return text
}

// Function authoring takes one flat definition. Do not inherit project-update
// fields (including its batch functions patch) from the update_crew executor.
func externalFunctionProperties(in map[string]any, actions []string) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"workspace_id", "crew_id", "workflow_id", "function", "args", "wait_seconds", "submission_id", "run_mode", "call_id", "request_id", "response", "limit", "after", "after_event", "message_limit", "file", "offset"} {
		if value, ok := in[key]; ok {
			out[key] = value
		}
	}
	for _, action := range actions {
		if action != "define" && action != "update" && action != "delete" {
			continue
		}
		out["crew_id"] = externalString("Crew to edit; alternatively pass workspace_id.")
		for _, key := range []string{"name", "description", "instructions"} {
			out[key] = externalString("define/update: function " + key + " (required). delete: only name is required.")
		}
		out["input_schema"] = map[string]any{"type": "object"}
		out["result_schema"] = map[string]any{"type": "object"}
		out["execution"] = map[string]any{"type": "string", "enum": []any{"tab", "fresh"}, "description": crewFunctionExecutionHelp}
		out["agent"] = externalString(crewFunctionAgentHelp)
		break
	}
	return out
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
	if id := externalArg(args, "workspace_id"); id != "" {
		copyArgs := map[string]any{}
		for k, v := range args {
			copyArgs[k] = v
		}
		args = copyArgs
		if externalArg(args, "crew_id") != "" || externalArg(args, "workflow_id") != "" {
			return tool, args, fmt.Errorf("pass workspace_id or another target, not both")
		}
		args["crew_id"] = strings.TrimPrefix(id, "work:")
		delete(args, "workspace_id")
	}
	action := externalArg(args, "action")
	if tool.Name == "functions" && (action == "define" || action == "update" || action == "delete") {
		if _, nested := args["functions"]; nested {
			return tool, args, fmt.Errorf("functions action=%s takes one function using top-level name, description and instructions; use crew action=update for a functions batch", action)
		}
		fn := map[string]any{}
		for _, key := range []string{"name", "description", "instructions", "input_schema", "result_schema", "execution", "agent"} {
			if v, ok := args[key]; ok {
				fn[key] = v
			}
		}
		if action == "delete" {
			args["functions"] = map[string]any{"delete": []any{args["name"]}}
		} else {
			args["functions"] = map[string]any{"upsert": []any{fn}}
		}
		// These fields describe the function, not the project being updated.
		for key := range fn {
			delete(args, key)
		}
	}
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

// externalMergeProperty adds a member's argument to a merged tool. When two
// actions use the same name with different shapes (create_crew takes functions
// as an array, update_crew as {upsert, delete}), both stay visible as anyOf
// instead of the first one hiding the other.
func externalMergeProperty(props map[string]any, key string, value any) {
	existing, seen := props[key]
	if !seen {
		props[key] = value
		return
	}
	if reflect.DeepEqual(existing, value) {
		return
	}
	if merged, ok := existing.(map[string]any); ok {
		if options, isUnion := merged["anyOf"].([]any); isUnion && len(merged) == 1 {
			for _, option := range options {
				if reflect.DeepEqual(option, value) {
					return
				}
			}
			props[key] = map[string]any{"anyOf": append(append([]any{}, options...), value)}
			return
		}
	}
	props[key] = map[string]any{"anyOf": []any{existing, value}}
}
