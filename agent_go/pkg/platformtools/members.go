package platformtools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// Expand only definitions admitted by the caller's actual registration path.
// Source action tools keep their captured executors and caller/root checks.
func projectMembers(tools []mcpagent.ToolDefinition) ([]projectedMember, []mcpagent.ToolDefinition, error) {
	members := []projectedMember{}
	other := []mcpagent.ToolDefinition{}
	for _, tool := range tools {
		hasSource, expanded := false, false
		for _, action := range ConfigurationActions {
			if action.Source != tool.Name {
				continue
			}
			hasSource = true
			if !sourceHasAction(tool.InputSchema, action.SourceAction) {
				continue
			}
			member, err := sourceMember(tool, action)
			if err != nil {
				return nil, nil, err
			}
			members = append(members, projectedMember{tool: member, source: tool.Name, action: action})
			expanded = true
		}
		if hasSource {
			if !expanded {
				return nil, nil, fmt.Errorf("%s has no supported action in its admitted schema", tool.Name)
			}
			continue
		}
		matched := false
		for _, action := range Actions {
			if tool.Name != action.Member && !slices.Contains(action.Aliases, tool.Name) {
				continue
			}
			original := tool
			if tool.Name == "update_workflow_config" {
				tool = nativeSettingsMember(tool)
			}
			if tool.Name == "set_workflow_llm_config" {
				tool = nativeModelMember(tool)
			}
			member, err := scopedMember(tool, action.Scope)
			if err != nil {
				return nil, nil, err
			}
			members = append(members, projectedMember{tool: member, source: original.Name, action: action})
			matched = true
			break
		}
		if !matched {
			other = append(other, tool)
		}
	}
	return members, other, nil
}

func cloneSchema(schema map[string]any) (map[string]any, error) {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(data, &result)
	return result, err
}

func sourceHasAction(schema map[string]any, action string) bool {
	props, _ := schema["properties"].(map[string]any)
	choice, _ := props["action"].(map[string]any)
	values, _ := choice["enum"].([]any)
	for _, value := range values {
		if value == action {
			return true
		}
	}
	strings, _ := choice["enum"].([]string)
	return slices.Contains(strings, action)
}

func sourceMember(source mcpagent.ToolDefinition, action Action) (mcpagent.ToolDefinition, error) {
	schema, err := cloneSchema(source.InputSchema)
	if err != nil {
		return source, err
	}
	props, _ := schema["properties"].(map[string]any)
	delete(props, "action")
	for key := range props {
		if action.Fields != nil && !slices.Contains(action.Fields, key) || slices.Contains(action.ExcludeFields, key) {
			delete(props, key)
		}
	}
	required := []any{}
	if fields, ok := schema["required"].([]any); ok {
		for _, field := range fields {
			if _, exists := props[fmt.Sprint(field)]; exists {
				required = append(required, field)
			}
		}
	}
	for _, field := range action.Required {
		if _, exists := props[field]; exists && !slices.Contains(required, any(field)) {
			required = append(required, field)
		}
	}
	schema["required"] = required
	member := source
	member.Name = action.SourceMember
	if member.Name == "" {
		member.Name = action.Member
	}
	member.InputSchema = schema
	member.Execute = func(ctx context.Context, args map[string]any) (string, error) {
		input := map[string]any{"action": action.SourceAction}
		if action.Scope != "" {
			originalProps, _ := source.InputSchema["properties"].(map[string]any)
			if _, scoped := originalProps["scope"]; scoped {
				input["scope"] = action.Scope
			}
		}
		for key, value := range args {
			input[key] = value
		}
		return source.Execute(ctx, input)
	}
	return scopedMember(member, action.Scope)
}

func scopedMember(source mcpagent.ToolDefinition, scope string) (mcpagent.ToolDefinition, error) {
	if scope == "" {
		return source, nil
	}
	schema, err := cloneSchema(source.InputSchema)
	if err != nil {
		return source, err
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	props["scope"] = map[string]any{"type": "string", "enum": []any{scope}, "description": "Use the offered tab or project scope. Required when this action offers both."}
	member := source
	member.InputSchema = schema
	member.Execute = func(ctx context.Context, args map[string]any) (string, error) {
		input := map[string]any{}
		for key, value := range args {
			if key != "scope" {
				input[key] = value
			}
		}
		return source.Execute(ctx, input)
	}
	return member, nil
}
