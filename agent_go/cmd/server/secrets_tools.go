package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// registerSecretManagementTools registers list_secrets on the given agent. In
// project contexts it also registers set_workflow_secret (save or copy a key
// where this project reads it, or a named place; #4144) and
// delete_workflow_secret (stop using here).
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
		"List where keys live and which this project uses: your own Vault (only you), this online project's vault (everyone in the project), Team vaults (the Team's people) and company keys (from a Vault administrator), with the key names in each and who each place reaches. Values are never returned. Call this before saving, copying or removing a key, and pick the place by who the key should reach.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			out, _ := json.MarshalIndent(api.secretPlaces(ctx, userID, workflowPath), "", "  ")
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
			"Save a key for this project, from a value the person gave (value) or by copying a key they can already use (copy_from; the server copies it and you never see the value). By default it goes where this project reads keys: an online project's own vault (everyone in the project uses it; only its owner and co-owners may add), otherwise your own Vault. Name a place with where to choose another. Saving a name again replaces its value. $SECRET_<NAME> is usable in this session at once when this project reads that place.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Key name; scripts read it as $SECRET_<name>. Use UPPER_SNAKE_CASE (e.g. SLACK_TOKEN). With copy_from it defaults to that key's name.",
					},
					"value": map[string]interface{}{
						"type":        "string",
						"description": "The value the person gave. Encrypted before storage; not logged. Leave empty when copying.",
					},
					"copy_from": map[string]interface{}{
						"type":        "string",
						"description": "A key the person can already use (a name from list_secrets) to copy here instead of a value.",
					},
					"where": map[string]interface{}{
						"type":        "string",
						"enum":        []string{secretPlacePersonal, secretPlaceProject, secretPlaceTeam, secretPlaceCompany},
						"description": "Where to save: personal (only you), project (this online project's vault, everyone in it), team or company (these answer how to add there). Omit for this project's default.",
					},
				},
			},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				name, _ := args["name"].(string)
				value, _ := args["value"].(string)
				from, _ := args["copy_from"].(string)
				where, _ := args["where"].(string)
				name = strings.TrimSpace(name)
				var full, place string
				var err error
				switch {
				case strings.TrimSpace(from) != "":
					if where == "" {
						where = defaultSecretPlace(workflowPath)
					}
					full, name, value, err = api.copySecretInto(ctx, userID, workflowPath, from, where)
					place = where
				case name == "":
					return "Error: 'name' is required.", nil
				case value == "":
					return "Error: give 'value' (or 'copy_from' to copy a key you can already use).", nil
				default:
					full, place, err = api.saveProjectSecret(ctx, userID, workflowPath, name, value, where)
				}
				if err != nil {
					return "", fmt.Errorf("save secret: %w", err)
				}
				saved := map[string]string{secretPlaceProject: "this project's vault (everyone in the project can use it)", secretPlacePersonal: "your own Vault"}[place]
				reads := defaultSecretPlace(workflowPath) == place
				if afterUpsert != nil && strings.TrimSpace(workflowPath) != "" && reads {
					if hookErr := afterUpsert(ctx, name, full, value); hookErr != nil {
						return fmt.Sprintf("Saved %q in %s, but this session could not be given it: %v. It reaches this project from the next message.", name, saved, hookErr), nil
					}
					return fmt.Sprintf("Saved %q in %s. $SECRET_%s is available in this session now.", name, saved, name), nil
				}
				if place == secretPlacePersonal && serverCrewForPath(workflowPath) != "" {
					return fmt.Sprintf("Saved %q in your own Vault. This online project uses only its own vault, so it does not get it; copy it in with copy_from=%q to use it here.", name, name), nil
				}
				return fmt.Sprintf("Saved %q in %s. Choose it in the project's Secrets list so it receives $SECRET_%s.", name, saved, name), nil
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
				if serverCrewForPath(workflowPath) != "" {
					return fmt.Sprintf("This online project uses every key in its own vault, so %q stays until it is removed there: the project's vault in Settings, or manage_my_vaults (remove_secret with crew_id), by the owner or a co-owner.", name), nil
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
