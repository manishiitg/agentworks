package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const updateProjectGlobalSecretSelectionTool = "update_project_global_secret_selection"

// registerWorkGlobalSecretSelectionTool lets Crew select an existing global
// secret without exposing its value. The active workspace is bound at tool
// registration so the model cannot modify another Crew project.
func (api *StreamingAPI) registerWorkGlobalSecretSelectionTool(registrar definitionToolRegistrar, userID, workspacePath string) error {
	cleanWorkspace, err := cleanAgentProfileWorkspace(workspacePath, userID)
	if err != nil || cleanWorkspace != workspacePath || !isActiveWorkProjectWorkspace(userID, cleanWorkspace) {
		return fmt.Errorf("Crew global secret selection requires an active Crew project")
	}

	return registrar.RegisterCustomTool(updateProjectGlobalSecretSelectionTool, "Select or deselect one existing global secret for the active Crew project. Call list_secrets first and use an exact name from its global.names list. This changes only the project's explicit allowlist in workflow.json; it never returns or modifies the secret value. The selection is available to Crew tools from the next user message.", map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"action", "name"},
		"properties": map[string]interface{}{
			"action": map[string]interface{}{"type": "string", "enum": []string{"select", "deselect"}},
			"name":   map[string]interface{}{"type": "string", "description": "Exact global secret name returned by list_secrets."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		action := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["action"])))
		requested := strings.TrimSpace(fmt.Sprint(args["name"]))
		if action != "select" && action != "deselect" {
			return "", fmt.Errorf("action must be select or deselect")
		}
		if requested == "" || requested == "<nil>" {
			return "", fmt.Errorf("name is required")
		}

		canonical := ""
		for _, secret := range visibleGlobalSecrets(ctx, userID) {
			if strings.EqualFold(secret.Name, requested) {
				canonical = secret.Name
				break
			}
		}
		selected, readErr := productSelectedGlobalSecrets(ctx, "work", workspacePath)
		if readErr != nil {
			return "", fmt.Errorf("read Crew global secret selection: %w", readErr)
		}
		if action == "select" && canonical == "" {
			return "", fmt.Errorf("global secret %q is not available; call list_secrets and use an exact name from global.names", requested)
		}
		if action == "deselect" && canonical == "" {
			for _, name := range *selected {
				if strings.EqualFold(name, requested) {
					canonical = name
					break
				}
			}
		}
		if canonical == "" {
			canonical = requested
		}

		if err := updateProductSelectedGlobalSecrets(ctx, "work", workspacePath, func(current []string) []string {
			next := make([]string, 0, len(current)+1)
			for _, name := range current {
				if !strings.EqualFold(strings.TrimSpace(name), canonical) {
					next = append(next, name)
				}
			}
			if action == "select" {
				next = append(next, canonical)
			}
			return next
		}); err != nil {
			return "", fmt.Errorf("update Crew global secret selection: %w", err)
		}
		updated, err := productSelectedGlobalSecrets(ctx, "work", workspacePath)
		if err != nil {
			return "", fmt.Errorf("read updated Crew global secret selection: %w", err)
		}
		result := map[string]interface{}{
			"action":                       action,
			"name":                         canonical,
			"selected_global_secret_names": *updated,
			"status":                       "updated",
			"takes_effect":                 "next_user_message",
		}
		if action == "select" {
			result["message"] = fmt.Sprintf("%s is selected for this Crew project. It will be available as $SECRET_%s on the next user message.", canonical, canonical)
		} else {
			result["message"] = fmt.Sprintf("%s is no longer selected for this Crew project.", canonical)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}, "work_secrets")
}
