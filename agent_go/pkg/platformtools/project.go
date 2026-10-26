package platformtools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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

type projectedMember struct {
	tool   mcpagent.ToolDefinition
	source string
	action Action
}

func actionKey(action Action) string { return action.Family + "." + action.Name + "@" + action.Scope }

func Project(tools []mcpagent.ToolDefinition, selections ...Selection) ([]mcpagent.ToolDefinition, error) {
	selection := Selection{}
	if len(selections) > 0 {
		selection = selections[0]
	}
	filter := mcpagent.NewToolFilter(selection.Tools, selection.Servers, nil, nil, loggerv2.NewNoop())
	candidates, out, err := projectMembers(tools)
	if err != nil {
		return nil, err
	}
	selected := map[string]map[string]bool{}
	for _, entry := range selection.Tools {
		namespace, identity, qualified := strings.Cut(entry, ":")
		if !qualified {
			identity, namespace = namespace, ""
		}
		for _, candidate := range candidates {
			action := candidate.action
			id := action.Family + "." + action.Name
			if identity != id && identity != action.Family {
				continue
			}
			if namespace != "" && namespace != action.Family && namespace != candidate.tool.DisplayGroup {
				continue
			}
			if selected[action.Family] == nil {
				selected[action.Family] = map[string]bool{}
			}
			selected[action.Family][id] = true
		}
	}
	byKey := map[string]projectedMember{}
	for _, candidate := range candidates {
		action := candidate.action
		id := action.Family + "." + action.Name
		if actions, explicit := selected[action.Family]; explicit {
			if !actions[id] {
				continue
			}
		} else if selection.FilterCustom && !filter.ShouldIncludeTool(candidate.tool.DisplayGroup, candidate.source, true, false) {
			continue
		}
		key := actionKey(action)
		previous, found := byKey[key]
		if !found || candidate.tool.Name == action.Member || previous.tool.Name != action.Member {
			byKey[key] = candidate
		}
	}
	families := []string{}
	for _, action := range Actions {
		if !slices.Contains(families, action.Family) {
			families = append(families, action.Family)
		}
	}
	for _, family := range families {
		members := map[string][]projectedMember{}
		validators := map[string]*jsonschema.Schema{}
		props := map[string]any{}
		actions := []any{}
		lines := []string{}
		seen := map[string]bool{}
		group := mcpagent.ToolDefinition{Name: family, DisplayGroup: "workflow"}
		for _, action := range Actions {
			key := actionKey(action)
			if action.Family != family || seen[key] {
				continue
			}
			seen[key] = true
			member, ok := byKey[key]
			if !ok {
				continue
			}
			validator, err := compileSchema(member.tool.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("%s schema: %w", key, err)
			}
			validators[key] = validator
			members[action.Name] = append(members[action.Name], member)
			if !slices.Contains(actions, any(action.Name)) {
				actions = append(actions, action.Name)
			}
			line := "- " + action.Name
			if action.Scope != "" {
				line += " (" + action.Scope + ")"
			}
			line += ": " + action.Description
			if required, ok := member.tool.InputSchema["required"].([]any); ok && len(required) > 0 {
				fields := []string{}
				for _, field := range required {
					fields = append(fields, fmt.Sprint(field))
				}
				line += " Requires " + strings.Join(fields, ", ") + "."
			}
			lines = append(lines, line)
			memberProps, _ := member.tool.InputSchema["properties"].(map[string]any)
			for key, value := range memberProps {
				mergeProperty(props, key, value)
			}
			if member.tool.Timeout > group.Timeout {
				group.Timeout = member.tool.Timeout
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
			choices := members[action]
			scope, _ := args["scope"].(string)
			var selected *projectedMember
			for i := range choices {
				if scope == "" && len(choices) == 1 || choices[i].action.Scope == scope {
					selected = &choices[i]
					break
				}
			}
			if selected == nil {
				return "", fmt.Errorf("%s action %q scope %q is unavailable in this chat; refresh its tool catalog and choose an offered action and scope", family, action, scope)
			}
			member := *selected
			memberArgs := map[string]any{}
			memberProps, _ := member.tool.InputSchema["properties"].(map[string]any)
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
			if err := validators[actionKey(member.action)].Validate(memberArgs); err != nil {
				return "", fmt.Errorf("%s.%s arguments: %w", family, action, err)
			}
			if member.tool.Timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, member.tool.Timeout)
				defer cancel()
			}
			return member.tool.Execute(ctx, memberArgs)
		}
		out = append(out, group)
	}
	return out, nil
}

func mergeProperty(properties map[string]any, name string, value any) {
	old, exists := properties[name]
	if !exists {
		properties[name] = value
		return
	}
	if reflect.DeepEqual(old, value) {
		return
	}
	// The family advertises the union; the selected member still enforces its
	// exact schema. One action's provider/role enum cannot exclude another's.
	if union, ok := old.(map[string]any); ok && len(union) == 1 {
		if choices, ok := union["anyOf"].([]any); ok {
			for _, choice := range choices {
				if reflect.DeepEqual(choice, value) {
					return
				}
			}
			properties[name] = map[string]any{"anyOf": append(choices, value)}
			return
		}
	}
	properties[name] = map[string]any{"anyOf": []any{old, value}}
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
