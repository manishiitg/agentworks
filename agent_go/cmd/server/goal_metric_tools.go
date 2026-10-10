package server

import (
	"context"
	"encoding/json"
	"fmt"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"time"
)

func createGoalMetricTools() ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	var metric, observation map[string]interface{}
	_ = json.Unmarshal([]byte(`{"type": "object", "additionalProperties": false, "properties": {"id": {"type": "string"}, "criterion_id": {"type": "string"}, "name": {"type": "string"}, "role": {"type": "string", "enum": ["primary", "supporting"]}, "unit": {"type": "string"}, "direction": {"type": "string", "enum": ["increase", "decrease", "maintain"]}, "definition": {"type": "string"}, "source": {"type": "string"}, "window": {"type": "string"}, "route": {"type": "string"}, "environment": {"type": "string"}, "collection_frequency": {"type": "string"}, "target_date": {"type": "string"}, "freshness_hours": {"type": "number", "exclusiveMinimum": 0}, "target": {"type": "number"}}, "required": ["id", "criterion_id", "name", "role", "unit", "direction", "definition", "source", "window", "collection_frequency", "freshness_hours"]}`), &metric)
	metricProps := metric["properties"].(map[string]interface{})
	for _, key := range []string{"goal_id", "goal_name"} {
		metricProps[key] = map[string]interface{}{"type": "string"}
	}
	metricProps["supports"] = map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "uniqueItems": true, "description": "IDs of primary metrics this supporting measurement explains or constrains. Required with multiple primaries."}
	metricProps["support_kind"] = map[string]interface{}{"type": "string", "enum": []string{"breakdown", "diagnostic", "guardrail"}}
	metricProps["dimensions"] = map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}, "description": "Fixed dimension slice for this metric ID, e.g. language=English. Each slice has its own immutable metric ID; never combine percentiles by averaging."}
	_ = json.Unmarshal([]byte(`{"type": "object", "additionalProperties": false, "properties": {"criterion_id": {"type": "string"}, "metric": {"type": "string"}, "run_id": {"type": "string"}, "unit": {"type": "string"}, "route": {"type": "string"}, "environment": {"type": "string"}, "observed_at": {"type": "string"}, "window_start": {"type": "string"}, "window_end": {"type": "string"}, "status": {"type": "string"}, "value": {"type": "number"}, "evidence": {"type": "array", "items": {"type": "string"}, "minItems": 1}}, "required": ["criterion_id", "metric", "run_id", "unit", "observed_at", "evidence"]}`), &observation)
	specs := []struct {
		name, description, field string
		schema                   map[string]interface{}
	}{
		{"get_goal_metrics", "Read configured primary/supporting metric definitions and bounded per-metric source-backed history, freshness and comparable numeric differences. Builder and Pulse use the same facts to agree measurement plans and verify repairs. Numerical differences still need agent review of source quality and goal meaning.", "", nil},
		{"configure_goal_metrics", "Builder setup: replace the complete active metric list with one or more primary metrics. Group primary metrics using optional goal_id/goal_name; link supporting measurements with supports and support_kind. Multiple outcomes never force a workflow split. Reuse stable IDs; changing measurement meaning requires a new ID. Omitted metrics are retired, history retained. Confirm changes to goal meaning and targets with the user; never invent targets. This saves definitions, not a claim that the measurement step works.", "metrics", metric},
		{"record_goal_observations", "Record real measurements from ordinary workflow steps, independently of Pulse reviews. Use configured metric IDs and exact criterion/unit/route/environment. Supply source run ID, actual source observation time and evidence. For period-based readings supply window_start and window_end in RFC3339; never invent boundaries for old data. Instant/snapshot metrics need no period. Missing values need status; never substitute zero. Backfill only comparable evidence. Duplicate observations are idempotent; conflicting values are rejected.", "observations", observation},
	}
	tools := []llmtypes.Tool{}
	executors := map[string]interface{}{}
	categories := map[string]string{}
	for _, spec := range specs {
		props := map[string]interface{}{"workspace_path": map[string]interface{}{"type": "string"}}
		required := []string{"workspace_path"}
		if spec.field != "" {
			props[spec.field] = map[string]interface{}{"type": "array", "items": spec.schema, "minItems": 1}
			required = append(required, spec.field)
		}
		tools = append(tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: spec.name, Description: spec.description, Parameters: llmtypes.NewParameters(map[string]interface{}{"type": "object", "additionalProperties": false, "properties": props, "required": required})}})
		categories[spec.name] = "workflow"
	}
	executors["get_goal_metrics"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		ledger, err := step.LoadPulseImpactLedger(ctx, stringToolArg(args, "workspace_path"), 500)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(map[string]interface{}{"metrics": ledger.Metrics, "observations": ledger.Observations, "progress": step.GoalMetricSnapshots(ledger, time.Now())})
		return string(b), err
	}
	executors["configure_goal_metrics"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		raw, err := json.Marshal(args["metrics"])
		if err != nil {
			return "", err
		}
		var metrics []step.GoalMetric
		if err = json.Unmarshal(raw, &metrics); err != nil {
			return "", fmt.Errorf("metrics must be an array of metric definitions: %w", err)
		}
		if err = step.ConfigureGoalMetrics(ctx, stringToolArg(args, "workspace_path"), metrics); err != nil {
			return "", err
		}
		return `{"status":"configured","next":"Connect ordinary measurement steps and verify source-backed DB observations and history with Pulse; do not claim measurement is ready yet."}`, nil
	}
	executors["record_goal_observations"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		raw, err := json.Marshal(args["observations"])
		if err != nil {
			return "", err
		}
		var observations []step.PulseGoalObservation
		if err = json.Unmarshal(raw, &observations); err != nil {
			return "", fmt.Errorf("observations must be an array: %w", err)
		}
		if _, err = step.RecordGoalObservations(ctx, stringToolArg(args, "workspace_path"), observations); err != nil {
			return "", err
		}
		return `{"status":"recorded"}`, nil
	}
	return tools, executors, categories
}
