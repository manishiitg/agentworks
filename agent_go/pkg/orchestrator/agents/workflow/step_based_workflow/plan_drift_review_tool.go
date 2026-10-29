package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpexecutor "github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const (
	stepDriftCheckStatusPass  = "pass"
	stepDriftCheckStatusFail  = "fail"
	stepDriftCheckStatusFixed = "fixed"
)

// minPlanDriftCheckEvidenceLength is a low, deliberately non-strict floor —
// not a quality bar, just enough to reject the single-word "ok"/"fine"/
// "n/a" that a rubber-stamped review would produce. This is the concrete
// enforcement of the "evidence-required, not a boolean" design: the schema
// itself refuses a check with no real description of what was compared.
const minPlanDriftCheckEvidenceLength = 15

func validateStepDriftChecks(checks []StepDriftCheck) error {
	if len(checks) == 0 {
		return fmt.Errorf("checks must contain at least one entry — record every check that ran, including ones that passed, not just failures")
	}
	for i, c := range checks {
		if strings.TrimSpace(c.CheckID) == "" {
			return fmt.Errorf("checks[%d].check_id is required", i)
		}
		switch c.Status {
		case stepDriftCheckStatusPass, stepDriftCheckStatusFail, stepDriftCheckStatusFixed:
		default:
			return fmt.Errorf("checks[%d].status must be one of pass, fail, fixed (got %q)", i, c.Status)
		}
		evidence := strings.TrimSpace(c.Evidence)
		if len(evidence) < minPlanDriftCheckEvidenceLength {
			return fmt.Errorf(
				"checks[%d].evidence must describe what was actually compared and what was found, not a placeholder (got %q — need at least %d characters)",
				i, c.Evidence, minPlanDriftCheckEvidenceLength,
			)
		}

	}
	return nil
}

func getRecordPlanDriftReviewSchema() string {
	return `{
		"type": "object",
		"properties": {
			"step_id": {
				"type": "string",
				"description": "The step's id field from plan.json."
			},
			"checks": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"check_id": {
							"type": "string",
							"description": "Stable check identifier. Use the real deterministic check names when applicable: report_query_compatibility, validation_schema_db_rules, validation_schema_file_rules, scripted_code_db_queries, db_readme_contract, context_dependency_filenames, orphaned_tables. Add step_prompt_quality only when the prompt/schema/guidance changed or this is the first baseline review. Add a reference-backed step-type check only when the step type, topology, or relevant execution boundary changed. Plan Drift checks compatibility with the approved plan; general design optimization belongs to Architecture. Other checks need a clear stable id for an affected dependency, e.g. step_description_accuracy or learnings_content_staleness."
						},
						"status": {
							"type": "string",
							"enum": ["pass", "fail", "fixed"],
							"description": "fixed means real drift was found AND a repair was applied in this same review pass, not merely flagged."
						},
						"evidence": {
							"type": "string",
							"description": "What was actually compared and what was found — specific, not a bare verdict. E.g. 'compared description sentence 2 (\"reads leads table\") against the step's actual tool config, which only reads campaigns; description is stale' — not 'reviewed, looks fine'."
						}
					},
					"required": ["check_id", "status", "evidence"]
				},
				"description": "One entry per check that ran against this step in this review pass, including checks that passed — not only failures. A step's full set of checks (deterministic + judgment) should normally all be recorded together in one call."
			},
			"reviewed_by": {
				"type": "string",
				"description": "Optional actor label, e.g. \"pulse:plan_drift_review\" for the automatic AgentWorks module, or left unset for a manual run-plan-drift invocation (defaults to the current session)."
			},
			"reviewed_through_change_id": {
				"type": "string",
				"description": "Optional: the latest planning/changelog change_id this review actually consumed, if the step had prior evidence and you read the changelog entries since its last review. Persisted so a later review knows where to resume reading changelog history from. Leave unset if this step had no prior review or no relevant changelog entries existed."
			}
		},
		"required": ["step_id", "checks"]
	}`
}

// createRecordPlanDriftReviewExecutor mirrors the shape of every other
// plan-mod tool in this file (workspacePath/logger/readFile/writeFile in,
// executor func out) so it registers through registerPlanModificationTools
// exactly like update_step_config, cleanup_orphan_step_configs, etc. — same
// privileged write path, same conventions, nothing bespoke.
func createRecordPlanDriftReviewExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		stepID := strings.TrimSpace(asString(args["step_id"]))
		if stepID == "" {
			return "", fmt.Errorf("step_id is required")
		}

		rawChecks, err := json.Marshal(args["checks"])
		if err != nil {
			return "", fmt.Errorf("failed to encode checks: %w", err)
		}
		var checks []StepDriftCheck
		if err := json.Unmarshal(rawChecks, &checks); err != nil {
			return "", fmt.Errorf("checks must be an array of {check_id, status, evidence}: %w", err)
		}
		if err := validateStepDriftChecks(checks); err != nil {
			return "", err
		}

		reviewedBy := strings.TrimSpace(asString(args["reviewed_by"]))
		if reviewedBy == "" {
			if sessionID := mcpexecutor.SessionIDFromContext(ctx); sessionID != "" {
				reviewedBy = "session:" + sessionID
			} else {
				reviewedBy = "plan_drift_review"
			}
		}

		reviewedThroughChangeID := strings.TrimSpace(asString(args["reviewed_through_change_id"]))

		configs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read step_config.json: %w", err)
		}

		now := time.Now().UTC().Format(time.RFC3339)
		var passed, failed, fixed int
		for _, c := range checks {
			switch c.Status {
			case stepDriftCheckStatusPass:
				passed++
			case stepDriftCheckStatusFail:
				failed++
			case stepDriftCheckStatusFixed:
				fixed++
			}
		}
		record := &StepDriftReview{
			NeedsReview:             failed > 0,
			ReviewedAt:              now,
			ReviewedBy:              reviewedBy,
			ReviewedThroughChangeID: reviewedThroughChangeID,
			Checks:                  checks,
			ContractVersion:         planDriftReviewContractVersion,
		}

		found := false
		for i := range configs {
			if configs[i].ID != stepID {
				continue
			}
			found = true
			if configs[i].AgentConfigs == nil {
				configs[i].AgentConfigs = &AgentConfigs{}
			}
			configs[i].AgentConfigs.DriftReview = record
			break
		}
		if !found {
			// A step can legitimately have no prior step_config.json entry (its
			// config has always used preset defaults) — create one rather than
			// erroring, mirroring how update_step_config handles a first write.
			configs = append(configs, StepConfig{ID: stepID, AgentConfigs: &AgentConfigs{DriftReview: record}})
		}

		if err := writeStepConfigViaFileCallback(ctx, workspacePath, configs, writeFile); err != nil {
			return "", fmt.Errorf("failed to write step_config.json: %w", err)
		}

		logger.Info(fmt.Sprintf("📋 record_plan_drift_review: step=%q checks=%d pass=%d fail=%d fixed=%d by=%s", stepID, len(checks), passed, failed, fixed, reviewedBy))
		stateMessage := "This clears the step from plan_drift_review's due list until it is edited again."
		if failed > 0 {
			stateMessage = "Unresolved failed checks keep this step due for plan_drift_review; other review modules remain deferred until the drift is fixed."
		}
		return fmt.Sprintf(
			"Recorded drift_review for step %q: %d check(s) (%d pass, %d fail, %d fixed). %s",
			stepID, len(checks), passed, failed, fixed, stateMessage,
		), nil
	}
}
