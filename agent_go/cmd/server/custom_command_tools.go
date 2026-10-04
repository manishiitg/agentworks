package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

var commandToolSlugPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

func commandToolSlug(raw string) string {
	return strings.Trim(commandToolSlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-"), "-")
}

func commandToolContent(name, description, icon, prompt, mode string) string {
	if icon == "" {
		icon = "terminal"
	}
	if mode == "" {
		mode = "multi-agent"
	}
	return "---\nname: " + name + "\ndescription: " + strconv.Quote(strings.TrimSpace(description)) +
		"\nicon: " + strconv.Quote(icon) + "\nmodes:\n  - " + mode + "\n---\n\n" + strings.TrimSpace(prompt) + "\n"
}

// registerCustomCommandTools gives AgentWorks Builder the same command model
// used by the browser editor. activeWorkspace is a workflow for Workshop and
// empty for the user's personal AgentWorks commands.
func (api *StreamingAPI) registerCustomCommandTools(reg definitionToolRegistrar, userID, activeWorkspace string) error {
	commandsPath := workspaceref.PhysicalPath(userID, commands.CustomCommandsSubPath)
	mode := "multi-agent"
	if strings.HasPrefix(strings.TrimSpace(activeWorkspace), "Workflow/") {
		commandsPath = path.Join(strings.Trim(activeWorkspace, "/"), commands.CustomCommandsSubPath)
		mode = "workflow"
	}
	return reg.RegisterCustomTool("manage_custom_commands", "List, create, update, or delete reusable slash commands in the current chat scope when the user asks. A saved command appears the next time the slash menu opens. Commands declared by product.yaml are immutable.", map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"action":      map[string]interface{}{"type": "string", "enum": []string{"list", "create", "update", "delete"}},
			"name":        map[string]interface{}{"type": "string", "description": "Slash command name without the slash."},
			"description": map[string]interface{}{"type": "string", "description": "Short label shown in the slash menu."},
			"prompt":      map[string]interface{}{"type": "string", "description": "Prompt submitted by the command. {{context}} inserts the text the user types with the command; put it on its own line (optionally after a \"Label:\" line) so it disappears when nothing is typed. It is appended automatically when missing."},
			"icon":        map[string]interface{}{"type": "string", "enum": []string{"terminal", "zap", "eye", "code", "file-text", "message-circle", "search", "bookmark", "star"}},
		}, "required": []string{"action"},
	}, func(_ context.Context, args map[string]interface{}) (string, error) {
		action, _ := args["action"].(string)
		name, _ := args["name"].(string)
		name = commandToolSlug(name)
		if action == "list" {
			items, err := commands.DiscoverCommandsAt(getWorkspaceAPIURL(), commandsPath)
			if err != nil {
				return "", err
			}
			data, _ := json.Marshal(map[string]interface{}{"commands": items, "total": len(items)})
			return string(data), nil
		}
		if name == "" {
			return "", fmt.Errorf("name is required")
		}
		if action == "delete" {
			if err := commands.DeleteCommandAt(getWorkspaceAPIURL(), commandsPath, name); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted /%s. The slash menu will refresh the next time it opens.", name), nil
		}
		if action != "create" && action != "update" {
			return "", fmt.Errorf("action must be list, create, update, or delete")
		}
		description, _ := args["description"].(string)
		prompt, _ := args["prompt"].(string)
		icon, _ := args["icon"].(string)
		if strings.TrimSpace(description) == "" || strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("description and prompt are required")
		}
		content := commandToolContent(name, description, icon, prompt, mode)
		var err error
		if action == "create" {
			_, err = commands.CreateCommandAt(getWorkspaceAPIURL(), commandsPath, name, content)
		} else {
			_, err = commands.UpdateCommandAt(getWorkspaceAPIURL(), commandsPath, name, content)
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Saved /%s. It is available the next time the slash menu opens.", name), nil
	}, "custom_commands")
}
