package platformtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Project folds only already-admitted native members. Connected third-party
// MCP sources are outside this direct-definition catalog. The execution map is
// the same partial membership used to build the advertised action enum.
type Selection struct {
	Tools, Servers []string
	FilterCustom   bool
}

func Project(tools []mcpagent.ToolDefinition, selections ...Selection) ([]mcpagent.ToolDefinition, error) {
	selection := Selection{}
	if len(selections) > 0 {
		selection = selections[0]
	}
	filter := mcpagent.NewToolFilter(selection.Tools, selection.Servers, nil, nil, loggerv2.NewNoop())
	selected := map[string]map[string]bool{}
	for _, entry := range selection.Tools {
		namespace, identity, qualified := strings.Cut(entry, ":")
		if !qualified {
			identity, namespace = namespace, ""
		}
		for _, action := range DashboardActions {
			id := action.Family + "." + action.Name
			if identity != id && identity != action.Family {
				continue
			}
			native := namespace == "" || namespace == action.Family
			for _, tool := range tools {
				if IsDashboardMember(tool.Name) && Identity(tool.Name) == id && tool.DisplayGroup == namespace {
					native = true
				}
			}
			if !native {
				continue // a foreign MCP server's same-named tool
			}
			if selected[action.Family] == nil {
				selected[action.Family] = map[string]bool{}
			}
			selected[action.Family][id] = true
		}
	}
	byID := map[string]mcpagent.ToolDefinition{}
	out := make([]mcpagent.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		if !IsDashboardMember(tool.Name) {
			out = append(out, tool)
			continue
		}
		id := Identity(tool.Name)
		if actions, explicit := selected[PublicName(id)]; explicit {
			if !actions[id] {
				continue
			}
		} else if selection.FilterCustom && !filter.ShouldIncludeTool(tool.DisplayGroup, tool.Name, true, false) {
			continue
		}
		previous, found := byID[id]
		if !found || primaryMember(id) == tool.Name || primaryMember(id) != previous.Name {
			byID[id] = tool
		}
	}
	for _, family := range []string{"dashboard", "share"} {
		members := map[string]mcpagent.ToolDefinition{}
		validators := map[string]*jsonschema.Schema{}
		props := map[string]any{}
		actions := []any{}
		lines := []string{}
		// Per-member filtering is complete. Use the existing system category
		// so the SDK does not filter the sealed family a second time by a
		// category that only one of its former members happened to use.
		group := mcpagent.ToolDefinition{Name: family, DisplayGroup: "workflow"}
		for _, action := range DashboardActions {
			if action.Family != family {
				continue
			}
			member, ok := byID[family+"."+action.Name]
			if !ok {
				continue
			}
			validator, err := compileSchema(member.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("%s.%s schema: %w", family, action.Name, err)
			}
			members[action.Name], validators[action.Name] = member, validator
			actions = append(actions, action.Name)
			line := "- " + action.Name + ": " + action.Description
			if required, ok := member.InputSchema["required"].([]any); ok && len(required) > 0 {
				fields := []string{}
				for _, field := range required {
					fields = append(fields, fmt.Sprint(field))
				}
				line += " Requires " + strings.Join(fields, ", ") + "."
			}
			lines = append(lines, line)
			memberProps, _ := member.InputSchema["properties"].(map[string]any)
			for key, value := range memberProps {
				// All action-specific shapes remain enforced by the selected
				// member. Shared fields need only one description in the menu.
				if _, exists := props[key]; !exists {
					props[key] = value
				}
			}
			if member.Timeout > group.Timeout {
				group.Timeout = member.Timeout
			}
		}
		if len(actions) == 0 {
			continue
		}
		props["action"] = map[string]any{"type": "string", "enum": actions}
		group.InputSchema = map[string]any{"type": "object", "properties": props, "required": []any{"action"}, "additionalProperties": false}
		group.Description = "Use this project's " + family + " actions.\n" + strings.Join(lines, "\n")
		group.Execute = func(ctx context.Context, args map[string]any) (string, error) {
			action, _ := args["action"].(string)
			member, allowed := members[action]
			if !allowed {
				return "", fmt.Errorf("%s action %q is unavailable in this chat; refresh its tool catalog", family, action)
			}
			memberArgs := map[string]any{}
			memberProps, _ := member.InputSchema["properties"].(map[string]any)
			for key, value := range args {
				if key == "action" {
					continue
				}
				if _, declared := memberProps[key]; !declared {
					if _, known := props[key]; known {
						continue
					}
				}
				memberArgs[key] = value
			}
			if err := validators[action].Validate(memberArgs); err != nil {
				return "", fmt.Errorf("%s.%s arguments: %w", family, action, err)
			}
			if member.Timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, member.Timeout)
				defer cancel()
			}
			return member.Execute(ctx, memberArgs)
		}
		out = append(out, group)
	}
	return out, nil
}

func primaryMember(id string) string {
	for _, action := range DashboardActions {
		if action.Family+"."+action.Name == id {
			return action.Member
		}
	}
	return ""
}

func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://example.com/native-tool.json"
	if err := compiler.AddResource(location, document); err != nil {
		return nil, err
	}
	return compiler.Compile(location)
}
