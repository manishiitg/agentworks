package step_based_workflow

import (
	"context"
	"fmt"
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// workflowContractDeclaredExecutionModeStrippedVersionLabel names the
// contract version the v1.0.39 migration stamps. cmd/server owns the ladder
// (workflowContractDeclaredExecutionModeStrippedVersion); keep them in step.
const workflowContractDeclaredExecutionModeStrippedVersionLabel = "1.0.39"

// strippedDeclaredMode is one removed declared_execution_mode entry.
type strippedDeclaredMode struct {
	StepID string
	Scope  string // always "planning"; evaluation files are retired, never stripped
	Mode   string
	Reason string
}

// stripDeclaredExecutionModeFromConfigs clears the retired keys on every
// config that still carries them and reports what was removed.
func stripDeclaredExecutionModeFromConfigs(scope string, configs []StepConfig) []strippedDeclaredMode {
	var stripped []strippedDeclaredMode
	for i := range configs {
		cfg := configs[i].AgentConfigs
		if cfg == nil || (cfg.LegacyDeclaredExecutionMode == "" && cfg.LegacyDeclaredExecutionModeReason == "") {
			continue
		}
		stripped = append(stripped, strippedDeclaredMode{
			StepID: configs[i].ID,
			Scope:  scope,
			Mode:   cfg.legacyDeclared(),
			Reason: strings.TrimSpace(cfg.LegacyDeclaredExecutionModeReason),
		})
		cfg.LegacyDeclaredExecutionMode = ""
		cfg.LegacyDeclaredExecutionModeReason = ""
	}
	return stripped
}

// legacyAgenticRegularStepIDs lists the regular steps that still run as a
// agent only because of the retired key. Stripping it would flip
// them to scripted, so the v1.0.38 migration has to have converted them first.
func legacyAgenticRegularStepIDs(plan *PlanningResponse, configs []StepConfig) []string {
	var ids []string
	for _, info := range append(collectAllSteps(plan.Steps), collectAllSteps(plan.OrphanSteps)...) {
		if info.Step == nil {
			continue
		}
		if isLegacyAgenticRegularStep(info.Step, MatchStepConfigByID(info.Step.GetID(), configs)) {
			ids = append(ids, info.Step.GetID())
		}
	}
	return ids
}

// createStripDeclaredExecutionModeExecutor is the trusted tool behind the
// v1.0.39 contract upgrade (see upgradeDeclaredExecutionModeStripped in
// cmd/server), the second half of PLAT-287. The runtime now decides a step's
// execution model from its plan type alone, so the retired
// declared_execution_mode / declared_execution_mode_reason keys are removed
// from planning/step_config.json. Evaluation files are retired and are never
// read or written here. Every removed reason is kept in the changelog entry.
func createStripDeclaredExecutionModeExecutor(
	workspacePath string,
	logger loggerv2.Logger,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("read planning/plan.json: %w", err)
		}
		configs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("read planning/step_config.json: %w", err)
		}
		if legacy := legacyAgenticRegularStepIDs(plan, configs); len(legacy) > 0 {
			return "", fmt.Errorf("refusing to strip declared_execution_mode: regular step(s) %s still carry declared_execution_mode=\"agentic\" and would flip to scripted without it; run migrate_declared_execution_mode (workflow contract v1.0.38) first so they become explicit agent steps", strings.Join(legacy, ", "))
		}

		planningStripped := stripDeclaredExecutionModeFromConfigs("planning", configs)
		if len(planningStripped) == 0 {
			return `{"status":"no_op","message":"No step_config entry carries declared_execution_mode any more; the plan type alone states each step's execution model."}`, nil
		}

		if err := writeStepConfigViaFileCallback(ctx, workspacePath, configs, writeFile); err != nil {
			return "", fmt.Errorf("write planning/step_config.json: %w", err)
		}

		stepIDs := make([]string, 0, len(planningStripped))
		var changes []PlanFieldChange
		for _, item := range planningStripped {
			stepIDs = append(stepIDs, item.StepID)
			changes = append(changes, PlanFieldChange{StepID: item.StepID, Field: item.Scope + ".declared_execution_mode", OldValue: item.Mode, NewValue: ""})
			if item.Reason != "" {
				changes = append(changes, PlanFieldChange{StepID: item.StepID, Field: item.Scope + ".declared_execution_mode_reason", OldValue: item.Reason, NewValue: ""})
			}
		}
		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool: "strip_declared_execution_mode",
			Reason: fmt.Sprintf(
				"Workflow contract v%s (PLAT-287, half 2): the plan type alone decides how a step runs, so the retired declared_execution_mode keys were removed from %d planning step_config entries (original reasons preserved in the changes below).",
				workflowContractDeclaredExecutionModeStrippedVersionLabel, len(planningStripped),
			),
			StepIDs: stepIDs,
			Changes: changes,
		}, readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ strip_declared_execution_mode: planning=%d", len(planningStripped)))
		return fmt.Sprintf(`{"status":"migrated","planning_stripped":%d,"message":"declared_execution_mode removed; the plan type states each step's execution model. Call set_workflow_contract_version(version=%q)."}`,
			len(planningStripped), workflowContractDeclaredExecutionModeStrippedVersionLabel), nil
	}
}
