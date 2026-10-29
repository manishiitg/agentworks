package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

// Derive narrower action members only from capabilities already admitted to
// the MCP catalog. Their existing services and consent still decide access.
func configurationExternalMembers(catalog []externalTool) ([]externalTool, error) {
	result := []externalTool{}
	for _, tool := range catalog {
		if tool.Name == "costs" {
			for _, action := range []string{"breakdown", "detail"} {
				copy, err := configurationToolCopy(tool)
				if err != nil {
					return nil, err
				}
				copy.Name = "get_cost_" + action
				copy.Description = "Read recorded cost " + action + " within your current product and connection access."
				props, err := externalSchemaProperties(copy.Name, copy.InputSchema)
				if err != nil {
					return nil, err
				}
				delete(props, "action")
				copy.InputSchema["required"] = []any{}
				result = append(result, copy)
			}
			continue
		}
		if tool.Name == "manage_crew_chats" {
			for _, set := range []bool{false, true} {
				member, err := configurationToolCopy(tool)
				if err != nil {
					return nil, err
				}
				props, err := externalSchemaProperties(member.Name, member.InputSchema)
				if err != nil {
					return nil, err
				}
				keep := []string{"crew_id", "chat_id"}
				member.Name, member.Description, member.mutates = "get_tab_models", "Read this Crew's tab models and offered account choices through its existing chat service.", false
				member.InputSchema["required"] = []any{"crew_id"}
				if set {
					member.Name, member.Description, member.mutates = "set_tab_model", "Change one of your Crew chat models using its existing tab ownership and account checks. An empty engine uses the Crew default. idle_compaction changes that chat's Auto-compact when idle; without engine only it changes.", true
					keep = append(keep, "engine", "model_id", "connection_id", "reasoning_effort", "idle_compaction")
					member.InputSchema["required"] = []any{"crew_id", "chat_id"}
					// The app's existing reset sends an empty engine.
					engine, ok := props["engine"].(map[string]any)
					if !ok {
						return nil, fmt.Errorf("%s engine schema must be an object", tool.Name)
					}
					delete(engine, "minLength")
				}
				for key := range props {
					if !slices.Contains(keep, key) {
						delete(props, key)
					}
				}
				props["scope"] = map[string]any{"type": "string", "enum": []any{"tab"}, "description": "Your own chat's model choice."}
				result = append(result, member)
			}
			// The model setter now belongs only to models.set(scope=tab).
			var err error
			tool, err = configurationToolCopy(tool)
			if err != nil {
				return nil, err
			}
			props, err := externalSchemaProperties(tool.Name, tool.InputSchema)
			if err != nil {
				return nil, err
			}
			selector, _ := props["action"].(map[string]any)
			values, _ := selector["enum"].([]any)
			selector["enum"] = slices.DeleteFunc(values, func(value any) bool { return value == "set_model" })
		}
		if tool.Name == "get_settings" || tool.Name == "update_settings" {
			model, err := configurationToolCopy(tool)
			if err != nil {
				return nil, err
			}
			props, err := externalSchemaProperties(model.Name, model.InputSchema)
			if err != nil {
				return nil, err
			}
			keep := []string{"workflow_id", "crew_id"}
			model.Name = "get_project_models"
			model.Description = "Read this product's saved models and settings version, using its existing access."
			if tool.Name == "update_settings" {
				model.Name = "set_project_models"
				model.Description = "Change this product's model defaults using the existing Builder and account checks. Send the models object from get with your changes."
				keep = append(keep, "models", "expected_version")
				model.InputSchema["required"] = []any{"models"}
				// Models have their own job. This is a schema move, not a second setter.
				tool, err = configurationToolCopy(tool)
				if err != nil {
					return nil, err
				}
				settingsProps, err := externalSchemaProperties(tool.Name, tool.InputSchema)
				if err != nil {
					return nil, err
				}
				delete(settingsProps, "models")
			}
			for key := range props {
				if !slices.Contains(keep, key) {
					delete(props, key)
				}
			}
			props["scope"] = map[string]any{"type": "string", "enum": []any{"project"}, "description": "Project model defaults."}
			result = append(result, model)
		}
		result = append(result, tool)
	}
	return result, nil
}

// Native parameter serialization can omit an empty properties object for a
// no-argument tool. A malformed non-object schema is a catalog error.
func externalSchemaProperties(name string, schema map[string]any) (map[string]any, error) {
	if schema == nil {
		return nil, fmt.Errorf("%s has no input schema", name)
	}
	value, exists := schema["properties"]
	if !exists {
		props := map[string]any{}
		schema["properties"] = props
		return props, nil
	}
	props, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s properties schema must be an object", name)
	}
	return props, nil
}

func configurationToolCopy(tool externalTool) (externalTool, error) {
	data, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return tool, err
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return tool, err
	}
	tool.InputSchema = schema
	return tool, nil
}

func platformMergeActions(family string) [][2]string {
	result := [][2]string{}
	positions := map[string]int{}
	for _, action := range platformtools.Actions {
		if action.Family != family {
			continue
		}
		member := action.Member
		if action.ExternalMembers != "" {
			member = action.ExternalMembers
		}
		if position, exists := positions[action.Name]; exists {
			if !slices.Contains(externalMemberNames(result[position][1]), member) {
				result[position][1] += "|" + member
			}
		} else {
			positions[action.Name] = len(result)
			result = append(result, [2]string{action.Name, member})
		}
	}
	return result
}

// Classification only; public calls to retired member names remain refused.
func configurationConsentMember(name string) string {
	switch name {
	case "get_project_models":
		return "get_settings"
	case "set_project_models":
		return "update_settings"
	case "get_tab_models", "set_tab_model":
		return "manage_crew_chats"
	case "get_cost_breakdown", "get_cost_detail":
		return "costs"
	default:
		return name
	}
}

func modelSettingsMember(name string) string {
	switch name {
	case "get_project_models":
		return "get_settings"
	case "set_project_models":
		return "update_settings"
	default:
		return name
	}
}

func externalSettingsReply(w http.ResponseWriter, view map[string]any, modelsOnly bool) {
	if modelsOnly {
		externalJSON(w, map[string]any{"models": view["models"], "version": view["version"]})
		return
	}
	externalJSON(w, view)
}

func tabModelMemberAction(name string) string {
	switch name {
	case "get_tab_models":
		return "list"
	case "set_tab_model":
		return "set_model"
	default:
		return ""
	}
}

func scopedExternalModelMember(names []string, args map[string]any) (string, error) {
	scope := externalArg(args, "scope")
	available := []externalTool{}
	for _, name := range names {
		if member, exists := externalCatalogTool(name); exists {
			available = append(available, member)
		}
	}
	if scope == "" && len(available) == 1 {
		return available[0].Name, nil
	}
	for _, member := range available {
		props, _ := member.InputSchema["properties"].(map[string]any)
		field, _ := props["scope"].(map[string]any)
		values, _ := field["enum"].([]any)
		for _, value := range values {
			if value == scope {
				return member.Name, nil
			}
		}
	}
	return "", fmt.Errorf("Choose an offered model scope: project defaults or your own tab")
}
