package platformtools

import (
	"context"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// Noninteractive native Builders keep their admitted configuration executor.
// Publish the same nested field names without adding secret-value authority or
// the richer fields that only the interactive settings service offered.
func nativeSettingsMember(source mcpagent.ToolDefinition) mcpagent.ToolDefinition {
	original, _ := source.InputSchema["properties"].(map[string]any)
	props := map[string]any{}
	for _, key := range []string{"knowledgebase_sources", "kb_write_grants", "browser_mode", "backup", "publish"} {
		if value, exists := original[key]; exists {
			props[key] = value
		}
	}
	groups := map[string]map[string]string{
		"mcp_servers":   {"add": "add_servers", "remove": "remove_servers"},
		"tools":         {"add": "add_tools", "remove": "remove_tools"},
		"skills":        {"add": "add_skills", "remove": "remove_skills"},
		"secrets":       {"select": "add_secrets", "unselect": "remove_secrets"},
		"notifications": {"run_instructions": "run_notification_instructions", "run_channels": "run_notification_channels", "run_recipients": "run_notification_recipients", "gmail_connection_id": "notification_gmail_connection_id", "break_alerts": "break_alerts"},
	}
	for group, fields := range groups {
		nested := map[string]any{}
		for field, originalName := range fields {
			if value, exists := original[originalName]; exists {
				nested[field] = value
			}
		}
		if len(nested) > 0 {
			props[group] = map[string]any{"type": "object", "properties": nested, "additionalProperties": false}
		}
	}
	member := source
	member.InputSchema = map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	member.Execute = func(ctx context.Context, args map[string]any) (string, error) {
		input := map[string]any{}
		for key, value := range args {
			if fields, grouped := groups[key]; grouped {
				nested, _ := value.(map[string]any)
				for name, fieldValue := range nested {
					if originalName := fields[name]; originalName != "" {
						input[originalName] = fieldValue
					}
				}
			} else {
				input[key] = value
			}
		}
		return source.Execute(ctx, input)
	}
	return member
}

func nativeModelMember(source mcpagent.ToolDefinition) mcpagent.ToolDefinition {
	original, _ := source.InputSchema["properties"].(map[string]any)
	models := map[string]any{"schema_version": map[string]any{"type": "integer", "enum": []any{2}}}
	tiers := map[string]any{}
	for key, value := range original {
		switch key {
		case "tier_1", "tier_2", "tier_3":
			tiers[key] = value
		default:
			models[key] = value
		}
	}
	models["tiered_config"] = map[string]any{"type": "object", "properties": tiers, "additionalProperties": false}
	member := source
	member.InputSchema = map[string]any{"type": "object", "required": []any{"models"}, "properties": map[string]any{"models": map[string]any{"type": "object", "properties": models, "required": []any{"mode"}, "additionalProperties": false}}, "additionalProperties": false}
	member.Execute = func(ctx context.Context, args map[string]any) (string, error) {
		input := map[string]any{}
		config, _ := args["models"].(map[string]any)
		for key, value := range config {
			if key == "schema_version" {
				continue
			}
			if key == "tiered_config" {
				if tiers, ok := value.(map[string]any); ok {
					for tier, entry := range tiers {
						input[tier] = entry
					}
				}
				continue
			}
			input[key] = value
		}
		return source.Execute(ctx, input)
	}
	return member
}
