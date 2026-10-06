package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Relay uses the existing Builder tools with a flat execution contract. Group
// management is omitted and group arguments are refused even for direct calls.
type relayToolRegistrar struct{ DefinitionToolRegistrar }
type relayDefinitionRegistrar struct{ DefinitionRegistrar }

func validateRelayWorkshopRunFolder(folder string) error {
	if strings.TrimSpace(folder) == "" || strings.TrimSpace(folder) != folder || strings.ContainsAny(folder, "\x00\r\n") || filepath.IsAbs(folder) || strings.ContainsAny(folder, `/\\`) || folder == "." || folder == ".." {
		return fmt.Errorf("Relay run_folder must be one run ID under runs/, without a path")
	}
	return nil
}

func relayToolDefinition(name, description string, schema map[string]interface{}, executor func(context.Context, map[string]interface{}) (string, error)) (string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error)) {
	// Copy the schema because other products may reuse the same definition.
	raw, _ := json.Marshal(schema)
	var copied map[string]interface{}
	_ = json.Unmarshal(raw, &copied)
	if properties, ok := copied["properties"].(map[string]interface{}); ok {
		for _, key := range []string{"group_name", "group_names", "group_id"} {
			delete(properties, key)
		}
		if name == "execute_step" || name == "debug_step" {
			properties["run_folder"] = map[string]interface{}{"type": "string", "description": "Relay run ID under runs/, without a path. execute_step starts a new run when omitted; pass a previous run ID to reuse its upstream outputs. debug_step defaults to the selected run."}
		}
		if name == "debug_step" {
			delete(properties, "iteration")
		}
		if name == "run_full_workflow" {
			if variableSchema, ok := properties["variables"].(map[string]interface{}); ok {
				variableSchema["description"] = "Per-run values keyed by declared variable name, overriding saved Relay configuration. Unknown names are refused. API-triggered runs use their server-bound values."
			}
		}
	}
	if required, ok := copied["required"].([]interface{}); ok {
		kept := []interface{}{}
		for _, key := range required {
			if key != "group_name" && key != "group_names" && key != "group_id" {
				kept = append(kept, key)
			}
		}
		if len(kept) == 0 {
			delete(copied, "required")
		} else {
			copied["required"] = kept
		}
	}
	switch name {
	case "execute_step":
		description = "Test a saved Relay node using flat configuration and run inputs. Starts a new run folder when omitted; pass run_folder to reuse upstream outputs. Returns an execution_id for querying or stopping the background test."
	case "debug_step":
		description = "Inspect a Relay node's saved execution logs in run_folder (defaults to the selected run)."
	case "update_variable":
		description = "Add, update or remove a Relay configuration variable. Values are stored in variables[].value and apply to future runs; each API call supplies its own INPUT."
	}
	if name == "run_full_workflow" {
		description = "Execute the saved Relay graph once, resolving flat configuration and optional per-run variables. Each Builder test has its own run ID and saved progress. Starts in background; you will be notified when complete. Use human_inputs for exact step IDs and route_selections for deterministic branches. Use the returned execution_id with send_step_message to steer an active agent turn."
	}
	original := executor
	executor = func(ctx context.Context, args map[string]interface{}) (string, error) {
		for _, key := range []string{"group_name", "group_names", "group_id"} {
			if _, exists := args[key]; exists {
				return "", fmt.Errorf("Relays do not accept %s; use flat configuration and run inputs", key)
			}
		}
		return original(ctx, args)
	}
	return description, copied, executor
}
func relayGroupTool(name string) bool {
	return name == "add_group" || name == "update_group" || name == "delete_group"
}
func (r relayToolRegistrar) RegisterCustomTool(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), c string) error {
	if relayGroupTool(n) {
		return nil
	}
	d, s, e = relayToolDefinition(n, d, s, e)
	return r.DefinitionToolRegistrar.RegisterCustomTool(n, d, s, e, c)
}
func (r relayToolRegistrar) RegisterCustomToolWithTimeout(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), t time.Duration, c string) error {
	if relayGroupTool(n) {
		return nil
	}
	d, s, e = relayToolDefinition(n, d, s, e)
	return r.DefinitionToolRegistrar.RegisterCustomToolWithTimeout(n, d, s, e, t, c)
}
func (r relayDefinitionRegistrar) RegisterCustomTool(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), c string) error {
	return (relayToolRegistrar{r.DefinitionRegistrar}).RegisterCustomTool(n, d, s, e, c)
}
func (r relayDefinitionRegistrar) RegisterCustomToolWithTimeout(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), t time.Duration, c string) error {
	return (relayToolRegistrar{r.DefinitionRegistrar}).RegisterCustomToolWithTimeout(n, d, s, e, t, c)
}
