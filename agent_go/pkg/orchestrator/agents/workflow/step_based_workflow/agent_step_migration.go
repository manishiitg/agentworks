package step_based_workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const AgentStepContractVersion = "1.0.47"

// canonicalAgentStepJSON is the read boundary for retired saved step shapes.
// Runtime and authoring only use AgentPlanStep; saving emits type=agent/items.
func canonicalAgentStepJSON(data []byte) ([]byte, bool, error) {
	var step map[string]json.RawMessage
	if err := json.Unmarshal(data, &step); err != nil {
		return nil, false, err
	}
	var typ string
	if raw, ok := step["type"]; ok {
		if err := json.Unmarshal(raw, &typ); err != nil {
			return nil, false, err
		}
	}
	before, _ := json.Marshal(step)
	if shared, ok := step["shared_with"]; ok && string(shared) != "null" {
		var sharing map[string]json.RawMessage
		if err := json.Unmarshal(shared, &sharing); err != nil {
			return nil, false, err
		}
		if oldIDs, ok := sharing["orchestrator_ids"]; ok {
			if _, exists := sharing["agent_ids"]; !exists {
				sharing["agent_ids"] = oldIDs
			}
			delete(sharing, "orchestrator_ids")
			step["shared_with"], _ = json.Marshal(sharing)
		}
	}
	legacy := typ == "message_sequence" || typ == "orchestrator" || typ == "todo_task"
	if typ != "agent" && !legacy {
		after, err := json.Marshal(step)
		return after, !bytes.Equal(before, after), err
	}
	step["type"] = json.RawMessage(`"agent"`)
	if inner := step["todo_task_step"]; len(inner) > 0 && string(inner) != "null" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(inner, &fields); err != nil {
			return nil, false, err
		}
		for _, key := range []string{"description", "context_dependencies", "context_output", "validation_schema"} {
			value := step[key]
			if len(value) == 0 || string(value) == "null" || string(value) == `""` {
				if inherited, ok := fields[key]; ok {
					step[key] = inherited
				}
			}
		}
		delete(step, "todo_task_step")
	}
	if messages, ok := step["messages"]; ok {
		var oldItems, newItems []json.RawMessage
		if err := json.Unmarshal(messages, &oldItems); err != nil {
			return nil, false, err
		}
		if items, ok := step["items"]; ok {
			if err := json.Unmarshal(items, &newItems); err != nil {
				return nil, false, err
			}
		}
		if len(oldItems) > 0 && len(newItems) > 0 {
			oldJSON, _ := json.Marshal(oldItems)
			newJSON, _ := json.Marshal(newItems)
			if !bytes.Equal(oldJSON, newJSON) {
				return nil, false, fmt.Errorf("agent step has conflicting items and legacy messages; resolve the saved sequences before migration")
			}
		}
		if len(newItems) == 0 {
			step["items"] = messages
		}
	}
	delete(step, "messages")
	if typ == "orchestrator" || typ == "todo_task" {
		var items []map[string]json.RawMessage
		if raw, ok := step["items"]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, false, err
			}
		}
		var id string
		_ = json.Unmarshal(step["id"], &id)
		if len(items) == 0 {
			items = []map[string]json.RawMessage{{
				"id":   json.RawMessage(fmt.Sprintf("%q", id+"-execute")),
				"type": json.RawMessage(`"user_message"`), "kind": json.RawMessage(`"execution"`),
				"message": json.RawMessage(`"Execute the step charter now and verify that its requirements are satisfied."`),
			}}
		}
		for i, item := range items {
			if raw := item["id"]; len(raw) == 0 || string(raw) == `""` || string(raw) == "null" {
				item["id"] = json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("%s-item-%d", id, i+1)))
			}
			if raw := item["type"]; len(raw) == 0 || string(raw) == `""` {
				item["type"] = json.RawMessage(`"user_message"`)
			}
		}
		step["items"], _ = json.Marshal(items)
	}
	if itemsJSON, ok := step["items"]; ok {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(itemsJSON, &items); err != nil {
			return nil, false, err
		}
		for _, item := range items {
			if string(item["type"]) == `"message"` {
				item["type"] = json.RawMessage(`"user_message"`)
			}
		}
		step["items"], _ = json.Marshal(items)
	}
	after, err := json.Marshal(step)
	return after, !bytes.Equal(before, after), err
}

// MigrateAgentStepContent walks only plan step locations, preserving unknown
// fields and user text instead of replacing arbitrary strings in the document.
func MigrateAgentStepContent(content string) (string, int, error) {
	var plan map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return "", 0, err
	}
	count := 0
	var walk func(json.RawMessage) (json.RawMessage, error)
	walk = func(raw json.RawMessage) (json.RawMessage, error) {
		if string(raw) == "null" {
			return raw, nil
		}
		canonical, changed, err := canonicalAgentStepJSON(raw)
		if err != nil {
			return nil, err
		}
		if changed {
			count++
		}
		var step map[string]json.RawMessage
		if err := json.Unmarshal(canonical, &step); err != nil {
			return nil, err
		}
		if routesJSON, ok := step["predefined_routes"]; ok {
			var routes []map[string]json.RawMessage
			if err := json.Unmarshal(routesJSON, &routes); err != nil {
				return nil, err
			}
			for _, route := range routes {
				if child, ok := route["sub_agent_step"]; ok {
					route["sub_agent_step"], err = walk(child)
					if err != nil {
						return nil, err
					}
				}
			}
			step["predefined_routes"], _ = json.Marshal(routes)
		}
		return json.Marshal(step)
	}
	for _, key := range []string{"steps", "orphan_steps"} {
		if raw, ok := plan[key]; ok {
			var steps []json.RawMessage
			if err := json.Unmarshal(raw, &steps); err != nil {
				return "", 0, err
			}
			for i := range steps {
				var err error
				steps[i], err = walk(steps[i])
				if err != nil {
					return "", 0, err
				}
			}
			plan[key], _ = json.Marshal(steps)
		}
	}
	if count == 0 {
		return content, 0, nil
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	return string(data), count, err
}

func createMigrateAgentStepsExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		path := normalizePathForWorkspaceAPI("planning/plan.json", workspacePath)
		before, err := readFile(ctx, path)
		if err != nil {
			return "", err
		}
		after, count, err := MigrateAgentStepContent(before)
		if err != nil {
			return "", err
		}
		if count == 0 {
			return `{"status":"no_op"}`, nil
		}
		var plan PlanningResponse
		if err := json.Unmarshal([]byte(after), &plan); err != nil {
			return "", err
		}
		if err := ValidatePlanStructure(&plan); err != nil {
			return "", err
		}
		if err := writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, path), path, after); err != nil {
			return "", err
		}
		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool: "migrate_agent_steps", Reason: "PLAT-851: unify saved agent step types without changing their items or routes.",
			BeforeSnapshot: json.RawMessage(before), AfterSnapshot: json.RawMessage(after),
		}, readFile, withPlanMutationWriteAccess(workspacePath, writeFile), logger)
		return fmt.Sprintf(`{"status":"migrated","agent_steps":%d}`, count), nil
	}
}
