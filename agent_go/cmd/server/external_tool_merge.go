package server

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

// One tool per object (PLAT-738): a merged tool takes an action and runs the
// existing tool for it. The existing tools stay in the catalog as internal
// members only: a client reaches them through the merged tool and action,
// never by the old name (PLAT-856). Lists and the tool menu show the merged
// tool, narrowed to the actions this connection may use. A call is translated
// to its member before any check, so every scope, owner and validation rule
// stays the member's.
//
// A member may be "workflowTool|crewTool": the call runs the second when it
// names a crew_id, else the first. A merged tool with one action takes it
// without an action argument.
type externalToolMerge struct {
	name, summary string
	actions       [][2]string // action, member tool (or "workflowTool|crewTool")
}

var externalToolMerges = []externalToolMerge{
	{"share", "Share a Crew, Goal or Flow. get inspects the current audience; set is owner-only and keeps the existing product authoring consent. " + runShareAccessDescription, dashboardMergeActions("share")},
	{"dashboard", "Live dashboards of workflows, Flows and Crews: find them, read and edit drafts, check and publish revisions, and get links.", dashboardMergeActions("dashboard")},
	{"settings", "Read or change this product's setup, accounts, linked knowledge and team delivery using its existing access.", platformMergeActions("settings")},
	{"models", "Read or change project model defaults with existing Builder and account access; other model actions appear only where already available.", platformMergeActions("models")},
	{"costs", "Read recorded usage and attributed spend for products you can access. Summary, queries and drill-down keep their existing product, root and connection checks.", platformMergeActions("costs")},
	{"needs_you", "Find and answer the questions waiting on you. Current question, decision and product access still applies.", platformMergeActions("needs_you")},
	{"account", "Manage company accounts and installed services with the existing administrator permissions.", platformMergeActions("account")},
	{"help", "Read versioned product and connection guidance.", platformMergeActions("help")},
	{"workflow", "Find workflows and Flows you can use, read one workflow's manifest and access, or its plan.", [][2]string{
		{"list", "list_workflows"}, {"get", "get_workflow"}, {"plan", "get_plan"},
	}},
	{"files", "Read and change allowed project files, links and attachments. Current Builder, sharing, private-path, file-root and revision checks still decide access.", platformMergeActions("files")},
	{"database", "Read managed project data and schemas. Offered write, migration and snapshot actions retain the current owner, connection, step and file-grant checks.", platformMergeActions("database")},
	{"skills", "Find, install and select project skills and reusable commands using existing access.", platformMergeActions("skills")},
	{"mcp", "Manage the current project's connections and call already selected tools with existing account and connection access.", platformMergeActions("mcp")},
	{"brain", "Read and maintain shared company knowledge using existing folder roles and project grants.", platformMergeActions("brain")},
	{"vault", "Select and store secrets and manage allowed connections. Values stay write-only; current project, Vault and administrator checks apply.", platformMergeActions("vault")},
	{"schedules", "Timed project callers: list, create, change, pause, run and inspect them using the same saved clock and project access.", platformMergeActions("schedules")},
	{"webhooks", "Authenticated incoming HTTP events and their deliveries. Reusable work is managed with functions.", platformMergeActions("webhooks")},
	{"watchers", "Caller-owned condition checks and their history, using the same selected credentials and authorized targets.", platformMergeActions("watchers")},
	{"functions", "Discover, call and maintain offered functions. Published access, caller identity and active-invocation checks still apply.", platformMergeActions("functions")},
	{"runs", "Start and inspect real workflow runs through their existing execution and access checks.", platformMergeActions("runs")},
	{"sessions", "Use your project chats and background work. Existing person, project, tab and operation ownership still decides access.", platformMergeActions("sessions")},
	{"relay", "Build, test, publish and use Flows through their current draft and published-run services.", platformMergeActions("relay")},
	{"messages", "Find agents, exchange messages and use configured delivery channels. Conversations keep their existing participants and sender identity.", platformMergeActions("messages")},
	{"integrations", "Use and set up selected Google and Slack accounts and bots. Personal account ownership and product selection stay the same.", platformMergeActions("integrations")},
	{"workspace", "Find, create and manage your projects, attached references and current access.", platformMergeActions("workspace")},
	{"browser", "Use the current project browser or manage your own browser connections.", platformMergeActions("browser")},
	{"terminal", "Use the existing project terminal, execution history or granted laptop connection.", platformMergeActions("terminal")},
	{"ui", "Open and inspect the current app's views using its existing browser acknowledgement.", platformMergeActions("ui")},
	{"media", "Create or show images, documents and other media with the current provider and presentation tools.", platformMergeActions("media")},
	{"metrics", "Read and maintain the current Goal's targets and observations.", platformMergeActions("metrics")},
	{"workspace_review", "Audited inspection of Crew projects for administrators and Crew reviewers: projects, costs, files, chats and the audit log.", [][2]string{
		{"workspaces", "list_review_workspaces"}, {"costs", "get_review_costs"}, {"files", "list_review_files"}, {"file", "read_review_file"},
		{"chats", "list_review_chats"}, {"chat", "read_review_chat"}, {"audit", "get_review_audit"},
	}},
}

func dashboardMergeActions(family string) [][2]string {
	out := [][2]string{}
	for _, action := range platformtools.DashboardActions {
		if action.Family == family {
			out = append(out, [2]string{action.Name, action.Member})
		}
	}
	return out
}

func mergeExternalTools(catalog []externalTool) ([]externalTool, error) {
	var err error
	catalog, err = configurationExternalMembers(catalog)
	if err == nil {
		catalog, err = dataExternalMembers(catalog)
	}
	if err == nil {
		catalog, err = agentExternalMembers(catalog)
	}
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(catalog))
	for i, tool := range catalog {
		index[tool.Name] = i
		if projectServiceTool(tool.Name) || platformtools.IsMember(tool.Name) {
			catalog[i].hidden, catalog[i].internalOnly = true, true
		}
	}
	for _, merge := range externalToolMerges {
		if _, taken := index[merge.name]; taken {
			return nil, fmt.Errorf("merged tool %q collides with an existing tool", merge.name)
		}
		props := map[string]any{}
		passThrough := false
		actions := map[string]string{}
		order := []string{}
		membersOf := map[string][]externalTool{}
		for _, pair := range merge.actions {
			admitted := []string{}
			for _, name := range externalMemberNames(pair[1]) {
				i, ok := index[name]
				if !ok {
					continue // not admitted on this server
				}
				admitted = append(admitted, name)
				member := &catalog[i]
				if member.InputSchema["additionalProperties"] == true {
					passThrough = true
				}
				if _, has := member.InputSchema["properties"].(map[string]any)["action"]; has {
					return nil, fmt.Errorf("merged tool %q: member %q already has an action field", merge.name, member.Name)
				}
				// One name per job (the decisions log, PLAT-825): a merged member is
				// reached only through its merged tool, never by its old name.
				member.hidden, member.internalOnly = true, true
				membersOf[pair[0]] = append(membersOf[pair[0]], *member)
				for key, value := range member.InputSchema["properties"].(map[string]any) {
					externalMergeProperty(props, key, value)
				}
			}
			if len(admitted) > 0 {
				actions[pair[0]] = strings.Join(admitted, "|")
				order = append(order, pair[0])
			}
		}
		if len(order) == 0 {
			continue
		}
		if merge.name == "files" || merge.name == "share" || merge.name == "settings" || merge.name == "models" || merge.name == "costs" || merge.name == "database" || merge.name == "dashboard" || merge.name == "skills" || merge.name == "mcp" || merge.name == "sessions" {
			props["workspace_id"] = externalString("Stable work:<id> from workspace action=list.")
		}
		if merge.name == "functions" {
			props = externalFunctionProperties(props, order, func(name string) (externalTool, bool) {
				i, exists := index[name]
				if !exists {
					return externalTool{}, false
				}
				return catalog[i], true
			})
		}
		props["action"] = externalActionProperty(order, membersOf)
		catalog = append(catalog, externalTool{
			Name:        merge.name,
			Description: merge.summary,
			InputSchema: map[string]any{"type": "object", "properties": props, "required": externalMergedRequired(order), "additionalProperties": passThrough},
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

// externalMemberNames splits a "workflowTool|crewTool" member spec.
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
	membersOf := map[string][]externalTool{}
	props := map[string]any{}
	passThrough := false
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
		membersOf[action] = members
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
			if member.InputSchema["additionalProperties"] == true {
				passThrough = true
			}
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
		props = externalFunctionProperties(props, allowed, externalCatalogTool)
	}
	props["action"] = externalActionProperty(allowed, membersOf)
	out := tool
	if len(allowed) > 1 {
		out.Description = tool.Description + "\n" + strings.Join(lines, "\n")
	}
	// A globally multi-action tool always requires action, even when this
	// connection sees only one. Dispatch uses the same full object contract.
	out.InputSchema = map[string]any{"type": "object", "properties": props, "required": externalMergedRequired(tool.actionOrder), "additionalProperties": passThrough}
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
func externalFunctionProperties(in map[string]any, actions []string, lookup func(string) (externalTool, bool)) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"project", "definition", "function", "input", "wait_seconds", "submission_id", "run_mode", "call_id", "request_id", "response", "limit", "after", "after_event", "message_limit", "file", "offset"} {
		if value, ok := in[key]; ok {
			out[key] = value
		}
	}
	// Carry every admitted call/trigger payload from its real schema. Only
	// update_crew needs the flat authoring adapter below; its other settings
	// must not become function fields.
	for _, pair := range platformMergeActions("functions") {
		if !slices.Contains(actions, pair[0]) {
			continue
		}
		for _, name := range externalMemberNames(pair[1]) {
			if name == "update_crew" {
				continue
			}
			if member, ok := lookup(name); ok {
				props, _ := member.InputSchema["properties"].(map[string]any)
				for key, value := range props {
					externalMergeProperty(out, key, value)
				}
			}
		}
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
	if id := externalArg(args, "workspace_id"); id != "" && tool.Name != "workspace" {
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

	if action == "" && len(tool.actionOrder) == 1 {
		action = tool.actionOrder[0]
	}
	spec, ok := tool.actions[action]
	if !ok {
		return tool, args, fmt.Errorf("%s needs action, one of: %s", tool.Name, strings.Join(tool.actionOrder, ", "))
	}
	names := externalMemberNames(spec)
	name := names[0]
	if tool.Name == "models" {
		var err error
		name, err = scopedExternalModelMember(names, args)
		if err != nil {
			return tool, args, err
		}
	} else if tool.Name == "costs" && action == "summary" && externalArg(args, "workflow_id") == "" && slices.Contains(names, "get_crew_costs") {
		// Crew summary already supports an omitted ID (all owned Crews).
		// The workflow summary is a separate, workflow-bound Run operation.
		name = "get_crew_costs"
	} else if tool.Name == "functions" && (action == "status" || action == "reply") && externalArg(args, "workflow_id") == "" && len(names) == 2 {
		// Crew call IDs already identify their target. Its existing status
		// and answer handlers recheck the caller and refuse workflow calls;
		// the typed Crew CLI need not repeat a project ID it never required.
		name = names[1]
	} else if experienceFamily(tool.Name) {
		name = experienceTargetMember(names, args)
	} else if len(names) == 2 && externalArg(args, "crew_id") != "" {
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
	if tool.Name == "workspace" && externalArg(args, "workspace_id") != "" {
		source := agentConsentMember(member.Name)
		if source != "workspace" {
			if _, crewTarget := memberProps["crew_id"]; crewTarget {
				if externalArg(args, "crew_id") != "" || externalArg(args, "workflow_id") != "" {
					return tool, args, fmt.Errorf("pass workspace_id or another target, not both")
				}
				args["crew_id"] = strings.TrimPrefix(externalArg(args, "workspace_id"), "work:")
				delete(args, "workspace_id")
			}
		}
	}
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

// Project service definitions retain their native authorization and validators,
// but only canonical action families are public entry points.
func projectServiceTool(name string) bool {
	switch name {
	case "manage_schedules", "manage_triggers", "manage_condition_triggers", "create_workflow", "create_relay", "get_dashboard_share", "share_dashboard", "manage_crew_chats",
		"ask_crew", "list_crews", "get_crew", "get_crew_costs", "create_crew", "update_crew", "export_crew", "import_crew",
		"list_crew_files", "search_crew_files", "read_crew_file", "write_crew_file", "suggest_crew_change", "list_crew_snapshots", "compare_crew_snapshot", "review_crew_snapshot", "undo_crew_reply",
		"crew_git_status", "crew_git_diff", "crew_git_branch_compare", "crew_git_branch_diff",
		"project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_reply", "project_functions_calls",
		"list_review_workspaces", "get_review_costs", "list_review_files", "read_review_file", "list_review_chats", "read_review_chat", "get_review_audit":
		return true
	}
	return false
}

// Choose among already-admitted product adapters by their existing target
// fields. Authorization remains in the selected source service.
func experienceFamily(name string) bool {
	return slices.ContainsFunc(platformtools.ExperienceActions, func(action platformtools.Action) bool { return action.Family == name })
}
func experienceTargetMember(names []string, args map[string]any) string {
	for _, key := range []string{"crew_id", "workflow_id", "workspace_id", "workspace_path"} {
		if externalArg(args, key) == "" {
			continue
		}
		for _, name := range names {
			member, ok := externalCatalogTool(name)
			if !ok {
				continue
			}
			props, _ := member.InputSchema["properties"].(map[string]any)
			if _, has := props[key]; has {
				return name
			}
		}
	}
	return names[0]
}

// externalActionProperty is a merged tool's action selector. Its description says in plain words what each action
// needs, from the members' own required fields: the schema stays flat, because many MCP clients pass it to model
// APIs that reject top-level allOf/anyOf/oneOf and if/then (TestListedExternalToolSchemasAreFlat). The member's own
// schema still validates the call after translation.
func externalActionProperty(order []string, membersOf map[string][]externalTool) map[string]any {
	property := map[string]any{"type": "string", "enum": stringsToAny(order)}
	var needs []string
	for _, action := range order {
		var common []string
		for i, member := range membersOf[action] {
			fields := externalSchemaRequired(member.InputSchema, action)
			if i == 0 {
				common = fields
				continue
			}
			common = slices.DeleteFunc(common, func(field string) bool { return !slices.Contains(fields, field) })
		}
		if len(common) > 0 {
			needs = append(needs, action+": "+strings.Join(common, ", "))
		}
	}
	if len(needs) > 0 {
		property["description"] = "Required fields by action. " + strings.Join(needs, "; ") + "."
	}
	return property
}

// externalSchemaRequired lists the fields a member always requires, sorted: its own, those of every allOf branch
// (a specialized source keeps its selected branch there) and those of its "if action is this action" rule.
func externalSchemaRequired(schema map[string]any, action string) []string {
	seen := map[string]bool{}
	var collect func(map[string]any)
	collect = func(schema map[string]any) {
		// Schemas built in Go may hold []string and []map[string]any rather than decoded []any.
		var fields []string
		switch raw := schema["required"].(type) {
		case []string:
			fields = raw
		case []any:
			for _, field := range raw {
				if name, ok := field.(string); ok {
					fields = append(fields, name)
				}
			}
		}
		for _, name := range fields {
			if name != "action" {
				seen[name] = true
			}
		}
		var rules []map[string]any
		switch raw := schema["allOf"].(type) {
		case []map[string]any:
			rules = raw
		case []any:
			for _, entry := range raw {
				if rule, ok := entry.(map[string]any); ok {
					rules = append(rules, rule)
				}
			}
		}
		for _, rule := range rules {
			if ifPart, conditional := rule["if"].(map[string]any); conditional {
				props, _ := ifPart["properties"].(map[string]any)
				if selector, _ := props["action"].(map[string]any); selector["const"] == action {
					thenPart, _ := rule["then"].(map[string]any)
					collect(thenPart)
				}
				continue
			}
			collect(rule)
		}
	}
	collect(schema)
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
