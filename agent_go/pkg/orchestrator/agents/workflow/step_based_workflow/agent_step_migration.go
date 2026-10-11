package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

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
	legacy := typ == "message_sequence" || typ == "orchestrator" || typ == "todo_task"
	if typ != "agent" && !legacy {
		return data, false, nil
	}
	before, _ := json.Marshal(step)
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
	if _, ok := step["items"]; !ok || string(step["items"]) == "null" {
		if messages, ok := step["messages"]; ok {
			step["items"] = messages
		}
	}
	delete(step, "messages")
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
	return after, !reflect.DeepEqual(before, after), err
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
