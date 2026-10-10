package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// pulseImpactUpdateShape is appended to decode failures. The bare
// json.Unmarshal text names an internal Go field ("PulseGoalObservations") and
// never says that the field takes an array of objects, so a caller that passed
// a string or a bare object has nothing to correct against.
const pulseImpactUpdateShape = `interventions, observations, and assessments are each an array of objects, never a string or a bare object; ` +
	`minimal example: {"observations": [{"criterion_id": "sc-1", "metric": "posts_published", "run_id": "run-2026-08-01", "observed_at": "2026-08-01T09:00:00Z", "value": 4}], ` +
	`"interventions": [{"title": "<what changed>", "criterion_id": "sc-1", "impact_type": "reliability", "metric": "posts_published", "expected_direction": "increase"}]}`

func createRecordPulseImpactTool() (llmtypes.Tool, func(context.Context, map[string]interface{}) (string, error)) {
	stringArray := map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}
	sourceSchema := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"source_type": map[string]interface{}{"type": "string", "enum": []string{"attempt", "experiment", "finding", "review"}},
			"source_id":   map[string]interface{}{"type": "string"},
		},
		"required": []string{"source_type", "source_id"},
	}
	interventionSchema := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"effects":               map[string]interface{}{"type": "array", "description": "Additional configured metrics affected by this same change. The top-level metric remains its lead effect. Effects are retained on updates; assess each separately using assessment.metric.", "items": map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{"metric": map[string]interface{}{"type": "string"}, "expected_direction": map[string]interface{}{"type": "string", "enum": []string{"increase", "decrease", "maintain"}}}, "required": []string{"metric", "expected_direction"}}},
			"intervention_id":       map[string]interface{}{"type": "string", "description": "Stable id; omit to derive it from criterion, metric, and title."},
			"kind":                  map[string]interface{}{"type": "string", "enum": []string{"fix_bundle", "strategy_experiment", "architecture_improvement"}, "description": "Use architecture_improvement for construction improvements and strategy_experiment for business strategy experiments. Both share proposal, decision, baseline, guardrail and outcome tracking. Omit for ordinary fix bundles."},
			"title":                 map[string]interface{}{"type": "string"},
			"criterion_id":          map[string]interface{}{"type": "string", "description": "Stable success-criterion id from the workflow's durable goal contract."},
			"impact_type":           map[string]interface{}{"type": "string", "enum": []string{"direct_goal", "reliability", "measurement", "presentation_maintenance"}},
			"metric":                map[string]interface{}{"type": "string"},
			"expected_direction":    map[string]interface{}{"type": "string", "enum": []string{"increase", "decrease", "maintain"}},
			"scope":                 stringArray,
			"provenance":            map[string]interface{}{"type": "string", "description": "Exact query or artifact that supplies comparable measurements."},
			"baseline_window":       map[string]interface{}{"type": "string"},
			"checkpoint":            map[string]interface{}{"type": "string"},
			"guardrails":            stringArray,
			"rollback_condition":    map[string]interface{}{"type": "string"},
			"interference_domains":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Required when a strategy experiment is running or measuring. Stable names for every applicable goal criterion, control surface, channel/cohort, metric stream, shared resource, or contamination boundary, e.g. control:reply-copy or metric:qualified-replies. Concurrent experiments may not overlap."},
			"human_input_id":        map[string]interface{}{"type": "string", "description": "Link the create_human_input_request id when approval is needed."},
			"terminal_outcome":      map[string]interface{}{"type": "string", "description": "Required when a strategy experiment is adopted, rejected, or retired."},
			"minimum_evidence_runs": map[string]interface{}{"type": "integer", "minimum": 1},
			"status":                map[string]interface{}{"type": "string", "enum": []string{"awaiting_evidence", "measuring", "assessed", "retired", "proposed", "deferred", "approved", "running", "blocked", "adopted", "rejected"}},
			"sources":               map[string]interface{}{"type": "array", "items": sourceSchema},
		},
		"required": []string{"title", "criterion_id", "impact_type", "metric", "expected_direction"},
	}
	observationSchema := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"observation_id": map[string]interface{}{"type": "string", "description": "Optional stable id; the backend derives one from criterion, metric, run, route, and environment."},
			"criterion_id":   map[string]interface{}{"type": "string"},
			"metric":         map[string]interface{}{"type": "string"},
			"run_id":         map[string]interface{}{"type": "string"},
			"route":          map[string]interface{}{"type": "string"},
			"environment":    map[string]interface{}{"type": "string"},
			"value":          map[string]interface{}{"type": "number"},
			"status":         map[string]interface{}{"type": "string", "description": "Qualitative state when no honest numeric value exists."},
			"unit":           map[string]interface{}{"type": "string"},
			"observed_at":    map[string]interface{}{"type": "string"},
			"window_start":   map[string]interface{}{"type": "string"},
			"window_end":     map[string]interface{}{"type": "string"},
			"evidence":       stringArray,
		},
		"required": []string{"criterion_id", "metric", "run_id", "observed_at"},
	}
	assessmentSchema := map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"metric":          map[string]interface{}{"type": "string", "description": "Affected metric ID. Omit only for the intervention lead metric (legacy compatibility). Record a separate assessment for every additional effect; never average outcomes."},
			"assessment_id":   map[string]interface{}{"type": "string"},
			"intervention_id": map[string]interface{}{"type": "string"},
			"verdict":         map[string]interface{}{"type": "string", "enum": []string{"improved", "unchanged", "regressed", "inconclusive", "confounded"}},
			"before_window":   map[string]interface{}{"type": "string"},
			"after_window":    map[string]interface{}{"type": "string"},
			"before_value":    map[string]interface{}{"type": "number"},
			"after_value":     map[string]interface{}{"type": "number"},
			"absolute_change": map[string]interface{}{"type": "number"},
			"relative_change": map[string]interface{}{"type": "number"},
			"confidence":      map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high"}},
			"confounders":     stringArray,
			"evidence":        stringArray,
			"next_checkpoint": map[string]interface{}{"type": "string"},
			"assessed_at":     map[string]interface{}{"type": "string"},
		},
		"required": []string{"intervention_id", "verdict", "before_window", "after_window", "confidence", "assessed_at"},
	}

	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "record_pulse_impact",
		Description: "Append durable Pulse goal-impact evidence to the workflow database. The consolidated Fixer uses this after lifecycle work: record current-run criterion observations, create/update one intervention for each coherent verified fix bundle or approved strategy experiment, and append a matured before/after assessment. Reliability, measurement, and presentation work must not claim direct goal impact. Observations and assessments are immutable; inconclusive is correct when the evidence window has not matured or overlapping changes confound attribution.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object", "additionalProperties": false,
			"properties": map[string]interface{}{
				"workspace_path": map[string]interface{}{"type": "string"},
				"pulse_run_id":   map[string]interface{}{"type": "string"},
				"interventions":  map[string]interface{}{"type": "array", "items": interventionSchema},
				"observations":   map[string]interface{}{"type": "array", "items": observationSchema},
				"assessments":    map[string]interface{}{"type": "array", "items": assessmentSchema},
			},
			"required": []string{"workspace_path", "pulse_run_id"},
		}),
	}}

	executor := func(ctx context.Context, args map[string]interface{}) (string, error) {
		workspacePath, _ := args["workspace_path"].(string)
		pulseRunID, _ := args["pulse_run_id"].(string)
		pulseRunID = pulseRunIDForSession(ctx, pulseRunID)
		if err := validatePulseToolRunID(ctx, pulseRunID); err != nil {
			return "", err
		}
		raw := map[string]interface{}{
			"interventions": args["interventions"],
			"observations":  args["observations"],
			"assessments":   args["assessments"],
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return "", fmt.Errorf("encode Pulse impact update (%s): %w", pulseImpactUpdateShape, err)
		}
		var update step_based_workflow.PulseImpactUpdate
		if err := json.Unmarshal(encoded, &update); err != nil {
			return "", fmt.Errorf("decode Pulse impact update (%s): %w", pulseImpactUpdateShape, err)
		}
		for index := range update.Interventions {
			if strings.TrimSpace(update.Interventions[index].PulseRunID) == "" {
				update.Interventions[index].PulseRunID = pulseRunID
			}
		}
		ledger, err := step_based_workflow.RecordPulseImpactUpdate(ctx, workspacePath, update)
		if err != nil {
			return "", err
		}
		result, _ := json.Marshal(map[string]interface{}{
			"status":             "recorded",
			"intervention_count": len(ledger.Interventions),
			"observation_count":  len(ledger.Observations),
			"assessment_count":   len(ledger.Assessments),
		})
		return string(result), nil
	}
	return tool, executor
}
