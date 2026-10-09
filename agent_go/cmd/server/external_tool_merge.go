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
type externalToolMerge struct {
	name, summary string
	actions       [][2]string // action, member tool
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
	{"code_review", "Read-only, audited review of every Code workspace for admins and Code reviewers: workspaces, costs, files, chats and the audit log.", [][2]string{
		{"workspaces", "list_code_workspaces"}, {"costs", "get_code_costs"}, {"files", "list_code_files"}, {"file", "read_code_file"},
		{"chats", "list_code_chats"}, {"chat", "read_code_chat"}, {"audit", "get_code_audit"},
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
			i, ok := index[pair[1]]
			if !ok {
				continue // not admitted on this server
			}
			member := &catalog[i]
			if _, has := member.InputSchema["properties"].(map[string]any)["action"]; has {
				return nil, fmt.Errorf("merged tool %q: member %q already has an action field", merge.name, member.Name)
			}
			member.hidden = true
			actions[pair[0]] = member.Name
			order = append(order, pair[0])
			for key, value := range member.InputSchema["properties"].(map[string]any) {
				if _, seen := props[key]; !seen {
					props[key] = value
				}
			}
		}
		if len(order) == 0 {
			continue
		}
		props["action"] = map[string]any{"type": "string", "enum": stringsToAny(order)}
		catalog = append(catalog, externalTool{
			Name:        merge.name,
			Description: merge.summary,
			InputSchema: map[string]any{"type": "object", "properties": props, "required": []any{"action"}, "additionalProperties": false},
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
func externalMergedForClaims(claims *UserClaims, tool externalTool) (externalTool, bool) {
	allowed := []string{}
	var lines []string
	props := map[string]any{}
	for _, action := range tool.actionOrder {
		member, ok := externalCatalogTool(tool.actions[action])
		if !ok || !externalTokenAllows(claims, member) {
			continue
		}
		allowed = append(allowed, action)
		required := []string{}
		if raw, ok := member.InputSchema["required"].([]any); ok {
			for _, r := range raw {
				required = append(required, fmt.Sprint(r))
			}
		}
		sort.Strings(required)
		line := "- action=" + action + ": " + firstSentence(member.Description)
		if len(required) > 0 {
			line += " Requires " + strings.Join(required, ", ") + "."
		}
		lines = append(lines, line)
		for key, value := range member.InputSchema["properties"].(map[string]any) {
			if _, seen := props[key]; !seen {
				props[key] = value
			}
		}
	}
	if len(allowed) == 0 {
		return tool, false
	}
	props["action"] = map[string]any{"type": "string", "enum": stringsToAny(allowed)}
	out := tool
	out.Description = tool.Description + "\n" + strings.Join(lines, "\n")
	out.InputSchema = map[string]any{"type": "object", "properties": props, "required": []any{"action"}, "additionalProperties": false}
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
	name, ok := tool.actions[action]
	if !ok {
		return tool, args, fmt.Errorf("%s needs action, one of: %s", tool.Name, strings.Join(tool.actionOrder, ", "))
	}
	member, ok := externalCatalogTool(name)
	if !ok {
		return tool, args, fmt.Errorf("%s action %s is unavailable", tool.Name, action)
	}
	rest := make(map[string]any, len(args))
	for key, value := range args {
		if key != "action" {
			rest[key] = value
		}
	}
	return member, rest, nil
}
