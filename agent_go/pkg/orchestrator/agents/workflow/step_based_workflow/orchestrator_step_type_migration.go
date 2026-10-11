package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// legacyOrchestratorTypePattern matches the plan-type discriminator of a step
// still written with the legacy name. Only the `"type"` key is rewritten, so a
// description that happens to mention todo_task is left alone. Working on the
// raw text (rather than re-marshaling a typed plan) keeps every other byte of
// plan.json — key order, unknown fields — exactly as the builder wrote it.
var legacyOrchestratorTypePattern = regexp.MustCompile(`("type"\s*:\s*)"todo_task"`)

// migrateOrchestratorStepTypeContent rewrites every legacy `"type": "todo_task"`
// discriminator to `"orchestrator"` and reports how many were changed. It is
// idempotent: a plan already on the new name comes back unchanged with 0.
func migrateOrchestratorStepTypeContent(planContent string) (string, int) {
	count := len(legacyOrchestratorTypePattern.FindAllStringIndex(planContent, -1))
	if count == 0 {
		return planContent, 0
	}
	return legacyOrchestratorTypePattern.ReplaceAllString(planContent, `${1}"orchestrator"`), count
}

// createMigrateOrchestratorStepTypeExecutor is the trusted tool behind the
// orchestrator-step-type contract upgrade. The runtime already parses both
// names (StepTypeTodoTaskLegacy is a read alias), so this migration only makes
// the rename visible in plan.json: it rewrites the discriminator, validates the
// result parses and passes plan validation, writes it back through the managed
// planning writer, and records the change in the plan changelog.
func createMigrateOrchestratorStepTypeExecutor(
	workspacePath string,
	logger loggerv2.Logger,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		planPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "plan.json"), workspacePath)
		planContent, err := readFile(ctx, planPath)
		if err != nil {
			return "", fmt.Errorf("read planning/plan.json: %w", err)
		}
		migrated, count := migrateOrchestratorStepTypeContent(planContent)
		if count == 0 {
			return `{"status":"no_op","message":"No legacy todo_task step types found; plan.json already uses orchestrator."}`, nil
		}

		var typedPlan PlanningResponse
		if err := json.Unmarshal([]byte(migrated), &typedPlan); err != nil {
			return "", fmt.Errorf("validate migrated plan parse: %w", err)
		}
		if err := ValidatePlanStructure(&typedPlan); err != nil {
			return "", fmt.Errorf("validate migrated plan: %w", err)
		}
		stepIDs := make([]string, 0, count)
		for _, step := range typedPlan.Steps {
			if step != nil && step.StepType() == StepTypeAgent {
				stepIDs = append(stepIDs, step.GetID())
			}
		}

		managedCtx := workspacepkg.WithSystemManagedWritePaths(ctx, planPath)
		if err := writeFile(managedCtx, planPath, migrated); err != nil {
			return "", fmt.Errorf("write migrated planning/plan.json: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:           "migrate_orchestrator_step_type",
			Reason:         fmt.Sprintf("Rename %d legacy todo_task step type discriminator(s) to orchestrator for workflow contract v%s; behavior is unchanged.", count, workflowContractOrchestratorStepTypeVersionLabel),
			StepIDs:        stepIDs,
			BeforeSnapshot: json.RawMessage(planContent),
			AfterSnapshot:  json.RawMessage(migrated),
		}, readFile, withPlanMutationWriteAccess(workspacePath, writeFile), logger)

		payload, _ := json.Marshal(map[string]interface{}{
			"renamed_type_discriminators": count,
			"orchestrator_step_ids":       stepIDs,
		})
		return fmt.Sprintf(`{"status":"migrated","result":%s}`, payload), nil
	}
}

// workflowContractOrchestratorStepTypeVersionLabel is the contract version this
// migration belongs to. The authoritative constant lives in cmd/server
// (workflowContractOrchestratorStepTypeVersion); this copy only names it in the
// changelog reason and must be kept in step with it.
const workflowContractOrchestratorStepTypeVersionLabel = "1.0.35"

// IsOrchestratorStepType reports whether a plan-type string names an
// orchestrator step, under either the current name or the legacy todo_task
// alias that plans written before contract v1.0.35 still carry.
func IsOrchestratorStepType(stepType string) bool {
	switch StepType(strings.TrimSpace(stepType)) {
	case "orchestrator", "todo_task":
		return true
	}
	return false
}
