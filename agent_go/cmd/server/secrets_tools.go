package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// registerSecretManagementTools registers list_secrets on the given agent. In
// project contexts it also registers set_workflow_secret (save in the owner's
// Vault and use here) and delete_workflow_secret (stop using here).
// toolCategory groups the tools in the agent's tool registry (e.g. "secret_tools").
// afterUpsert, if non-nil, is invoked after set_workflow_secret saved the value
// in the owner's Vault: it selects vaultName on the project and pushes
// SECRET_<name> into the live shell, so it is usable in the same session.
// Errors are surfaced to the agent but do NOT fail the save.
// afterDelete, if non-nil, unselects the secret and clears it from the live
// shell; without it delete_workflow_secret changes nothing.
// readOnly (PLAT-262), when true, registers only list_secrets — the two
// mutating tools (set_workflow_secret, delete_workflow_secret) are skipped entirely so a read-only session never
// sees them in its tool catalog.
func (api *StreamingAPI) registerSecretManagementTools(agent definitionToolRegistrar, userID, workflowPath, toolCategory string, readOnly bool, afterUpsert func(ctx context.Context, name, vaultName, value string) error, afterDelete func(ctx context.Context, name string) error) error {
	if agent == nil {
		return fmt.Errorf("agent is nil")
	}
	if userID == "" {
		return fmt.Errorf("userID is required for secret tools")
	}

	registerTool := func(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error)) error {
		return agent.RegisterCustomTool(name, description, params, exec, toolCategory)
	}

	if err := registerTool(
		"list_secrets",
		"List all secrets available to the current user. Returns JSON buckets: 'global' (server-wide, admin-managed) and 'workflow' (encrypted and scoped to the active project/workflow when applicable). Values are never returned — only names. Use this before setting, deleting, or attaching secrets. Admins may supply source_workflow_path to list another accessible Crew project or workflow before global promotion.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"source_workflow_path": map[string]interface{}{"type": "string", "description": "Admin-only optional source workspace path, e.g. Workflow/latency-report or an exact Crew project path. Defaults to the active project/workflow. Returns names only."},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			sourcePath := workflowPath
			if raw, supplied := args["source_workflow_path"]; supplied {
				if !canManageGlobalSecrets(userID) {
					return "", errGlobalAdmin
				}
				path, ok := raw.(string)
				if !ok || strings.TrimSpace(path) == "" {
					return "", fmt.Errorf("source_workflow_path must be a non-empty project or workflow workspace path")
				}
				_, readRoots, err := authorizeWorkflowContextPathsWithReadRoots(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: userID}), []string{path})
				if err != nil {
					return "", fmt.Errorf("Source project or workflow is unavailable")
				}
				sourcePath = readRoots[0]
			}
			globals := visibleGlobalSecrets(ctx, userID)
			globalNames := make([]string, 0, len(globals))
			for _, gs := range globals {
				globalNames = append(globalNames, gs.Name)
			}
			sort.Strings(globalNames)

			workflowNames := []string{}
			if strings.TrimSpace(sourcePath) != "" {
				workflowSecrets, err := api.ensureSharedWorkflowSecrets(ctx, sourcePath, userID)
				if err != nil {
					return "", fmt.Errorf("failed to list workflow secrets: %w", err)
				}
				workflowNames = make([]string, 0, len(workflowSecrets))
				for _, ws := range workflowSecrets {
					workflowNames = append(workflowNames, ws.Name)
				}
				sort.Strings(workflowNames)
			}

			out, _ := json.MarshalIndent(map[string]interface{}{
				"global": map[string]interface{}{
					"read_only": true,
					"source":    "server environment and encrypted managed globals",
					"names":     globalNames,
					// Names that exist in Vault for this person but have no stored value yet. They cannot be selected until a
					// value is added in the Vault panel.
					"needs_value": globalSecretNamesWithoutValue(ctx, userID),
				},
				"workflow": map[string]interface{}{
					"read_only":     false,
					"source":        "encrypted workflow store, shared by every user with access to this workflow",
					"workflow_path": sourcePath,
					"names":         workflowNames,
				},
				"note": "Secret VALUES are never returned by any tool. A name in global.needs_value exists in Vault for this person but has no stored value yet: it cannot be selected until they add its value in the Vault panel, so tell them that. In a workflow builder, attach an existing name with update_workflow_config(add_secrets=[...]). In an active Crew project, select an existing global with update_project_global_secret_selection(action=\"select\", name=\"...\").",
			}, "", "  ")
			return string(out), nil
		},
	); err != nil {
		return fmt.Errorf("register list_secrets: %w", err)
	}

	if readOnly {
		return nil
	}

	if strings.TrimSpace(workflowPath) != "" {
		if err := registerTool(
			"set_workflow_secret",
			"Save a secret for this project (owner only). The value is saved in the owner's own Vault (My accounts) and this project uses it in the same step; everyone who uses the project gets it lent by the owner without seeing the value. Saving a name again replaces its value everywhere the owner uses it. $SECRET_<NAME> is usable immediately in this session.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Secret name. Becomes SECRET_<name> env var at execution. Use UPPER_SNAKE_CASE (e.g. SLACK_TOKEN, OPENAI_API_KEY).",
					},
					"value": map[string]interface{}{
						"type":        "string",
						"description": "Plaintext secret value. Encrypted before storage; not logged.",
					},
				},
				"required": []string{"name", "value"},
			},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				name, _ := args["name"].(string)
				value, _ := args["value"].(string)
				name = strings.TrimSpace(name)
				if name == "" {
					return "Error: 'name' is required.", nil
				}
				if value == "" {
					return "Error: 'value' is required (use delete_workflow_secret to remove a workflow secret).", nil
				}
				full, err := api.saveProjectSecret(ctx, userID, workflowPath, name, value)
				if err != nil {
					return "", fmt.Errorf("save secret: %w", err)
				}
				if afterUpsert != nil {
					if hookErr := afterUpsert(ctx, name, full, value); hookErr != nil {
						return fmt.Sprintf("Saved %q in your Vault, but this project could not be set to use it: %v\nTick it in the project's Secrets list so it receives $SECRET_%s.", name, hookErr, name), nil
					}
					return fmt.Sprintf("Saved %q in your Vault and used it in this project. $SECRET_%s is available in this session now.", name, name), nil
				}
				return fmt.Sprintf("Saved %q in your Vault. Choose it in the project's Secrets list so it receives $SECRET_%s.", name, name), nil
			},
		); err != nil {
			return fmt.Errorf("register set_workflow_secret: %w", err)
		}

		if err := registerTool(
			"delete_workflow_secret",
			"Stop using a secret in this project. Its value stays in the owner's Vault, where they can delete it.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the secret this project should stop using.",
					},
				},
				"required": []string{"name"},
			},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				name, _ := args["name"].(string)
				name = strings.TrimSpace(name)
				if name == "" {
					return "Error: 'name' is required.", nil
				}
				if afterDelete == nil {
					return "Remove it from the project's Secrets list.", nil
				}
				if hookErr := afterDelete(ctx, name); hookErr != nil {
					return "", fmt.Errorf("remove secret from this project: %w", hookErr)
				}
				return fmt.Sprintf("This project no longer uses %q; $SECRET_%s is gone from this session. Its value is still in the owner's Vault.", name, vaultSecretShortName(name)), nil
			},
		); err != nil {
			return fmt.Errorf("register delete_workflow_secret: %w", err)
		}
	}

	return nil
}
