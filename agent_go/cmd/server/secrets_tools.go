package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// registerSecretManagementTools registers list_secrets on the given agent. In
// workflow contexts it also registers set_workflow_secret and
// delete_workflow_secret. Global secrets are server-wide; admins can manage
// encrypted globals. Workflow secrets support CRUD on encrypted values.
// toolCategory groups the tools in the agent's tool registry (e.g. "secret_tools").
// afterUpsert, if non-nil, is invoked after a successful set_workflow_secret
// so callers can auto-attach the secret to workspace state (workflow.json
// manifest + live workshop shell env) — collapsing the old store-then-attach
// two-step into one action so $SECRET_<NAME> is immediately available in the
// same session. It receives the plaintext value (the handler already holds
// it) to avoid a DB re-resolve. Errors are surfaced to the agent but do NOT
// fail the store (the value is already persisted).
// afterDelete, if non-nil, is invoked after a successful
// delete_workflow_secret so callers can clean up workspace state (e.g.
// detach from workflow.json + refresh workshop shell env). Errors returned
// by afterDelete are surfaced to the agent.
// readOnly (PLAT-262), when true, registers only list_secrets — the two
// mutating tools (set_workflow_secret, delete_workflow_secret) are skipped entirely so a read-only session never
// sees them in its tool catalog.
func (api *StreamingAPI) registerSecretManagementTools(agent definitionToolRegistrar, userID, workflowPath, toolCategory string, readOnly bool, afterUpsert func(ctx context.Context, name, value string) error, afterDelete func(ctx context.Context, name string) error) error {
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
				},
				"workflow": map[string]interface{}{
					"read_only":     false,
					"source":        "encrypted workflow store, shared by every user with access to this workflow",
					"workflow_path": sourcePath,
					"names":         workflowNames,
				},
				"note": "Secret VALUES are never returned by any tool. In a workflow builder, attach an existing name with update_workflow_config(add_secrets=[...]). In an active Crew project, select an existing global with update_project_global_secret_selection(action=\"select\", name=\"...\").",
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
			"Create or update a secret scoped only to the active workflow. The value is AES-256-GCM encrypted and stored server-side in the workflow's own store, shared by every user with access to this workflow (owners manage it; read-only users can run with it but never see the value); other workflows cannot list or attach it. The secret is AUTO-ATTACHED to the active workflow and injected into the live shell env, so $SECRET_<NAME> is usable immediately in this session — no separate update_workflow_config call needed.",
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
				if err := api.upsertSharedWorkflowSecret(ctx, workflowPath, name, value); err != nil {
					return "", fmt.Errorf("store workflow secret: %w", err)
				}
				if afterUpsert != nil {
					if hookErr := afterUpsert(ctx, name, value); hookErr != nil {
						return fmt.Sprintf("✅ Stored workflow secret %q (encrypted) for %s, but auto-attach failed: %v\nAttach it manually with update_workflow_config(add_secrets=[%q]) so steps receive $SECRET_%s.", name, workflowPath, hookErr, name, name), nil
					}
					return fmt.Sprintf("✅ Stored workflow secret %q (encrypted) for %s and attached it. $SECRET_%s is available in this session now.", name, workflowPath, name), nil
				}
				return fmt.Sprintf("✅ Stored workflow-scoped secret %q (encrypted) for %s. Attach it with update_workflow_config add_secrets=[%q] so steps receive $SECRET_%s.", name, workflowPath, name, name), nil
			},
		); err != nil {
			return fmt.Errorf("register set_workflow_secret: %w", err)
		}

		if err := registerTool(
			"delete_workflow_secret",
			"Delete a workflow-scoped secret from the encrypted store. Does NOT detach the name from workflows that reference it — call update_workflow_config remove_secrets separately if needed.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the workflow-scoped secret to delete.",
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
				if err := api.deleteSharedWorkflowSecret(ctx, workflowPath, name, userID); err != nil {
					return "", fmt.Errorf("delete workflow secret: %w", err)
				}
				detached := false
				if afterDelete != nil {
					if hookErr := afterDelete(ctx, name); hookErr != nil {
						return fmt.Sprintf("✅ Deleted workflow secret %q from store, but workshop cleanup failed: %v\nYou may need to run update_workflow_config(remove_secrets=[%q]) manually.", name, hookErr, name), nil
					}
					detached = true
				}
				if detached {
					return fmt.Sprintf("✅ Deleted workflow secret %q and detached from the active workflow. $SECRET_%s is no longer available in this session.", name, name), nil
				}
				return fmt.Sprintf("✅ Deleted workflow secret %q. If this workflow still references it, run update_workflow_config(remove_secrets=[%q]) to detach.", name, name), nil
			},
		); err != nil {
			return fmt.Errorf("register delete_workflow_secret: %w", err)
		}
	}

	return nil
}
