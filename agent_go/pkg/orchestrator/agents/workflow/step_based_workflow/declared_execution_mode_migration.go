package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// workflowContractDeclaredExecutionModeRetiredVersionLabel names the contract
// version this migration belongs to in its changelog reason. The
// authoritative constant is cmd/server's
// workflowContractDeclaredExecutionModeRetiredVersion; keep them in step.
const workflowContractDeclaredExecutionModeRetiredVersionLabel = "1.0.38"

// declaredModeTypeChange records one plan-type rewrite the migration made.
type declaredModeTypeChange struct {
	StepID string `json:"step_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	before PlanStepInterface
	after  PlanStepInterface
}

type declaredModeMigrationResult struct {
	TypeChanges []declaredModeTypeChange `json:"type_changes"`
}

func (r *declaredModeMigrationResult) noOp() bool {
	return len(r.TypeChanges) == 0
}

// migrateDeclaredExecutionModeInPlan makes the plan say explicitly what the
// runtime already did (PLAT-287, half 1): a regular step without a declared
// scripted mode is the legacy agentic shape that the runtime normalizes to a
// message_sequence at execution time (shouldNormalizeRegularStepToAgent),
// so it becomes one on disk; a message_sequence still declared scripted
// (PLAT-280 drift) becomes regular. After this, every regular step in the
// plan is a declared scripted step, which is what lets a later release make
// the plan type alone decide the model and delete the field.
//
// It deliberately does NOT strip declared_execution_mode from step_config:
// today's runtime still reads it to tell a scripted regular step from a
// legacy agentic one, so removing it here would turn every real scripted
// step into a message_sequence whose main.py never runs -- caught in review
// before this ever ran anywhere. The field goes in the same release as the
// runtime rule that makes it redundant, not before.
//
// Two passes: the first only looks for the one case this refuses to guess
// about -- a step declared scripted with no learnings/<id>/main.py, which is
// already broken -- so a refusal leaves the plan untouched. Idempotent: a
// second run finds every regular step declared scripted and does nothing.
func migrateDeclaredExecutionModeInPlan(plan *PlanningResponse, configs []StepConfig, scriptExists func(stepID string) bool) (*declaredModeMigrationResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is unavailable")
	}
	infos := append(collectAllSteps(plan.Steps), collectAllSteps(plan.OrphanSteps)...)

	// Pre-1.0.38 semantics on purpose: a regular step counted as scripted
	// only when its step_config declared it, so a regular step with no
	// declaration is a legacy agentic one that ran as a message_sequence.
	declaredScripted := func(stepID string) bool {
		return MatchStepConfigByID(stepID, configs).legacyDeclared() == StepModeScripted
	}

	// Pass 1: refuse before touching anything.
	var broken []string
	for _, info := range infos {
		step := info.Step
		if step == nil {
			continue
		}
		switch step.(type) {
		case *RegularPlanStep, *AgentPlanStep:
			if declaredScripted(step.GetID()) && !scriptExists(step.GetID()) {
				broken = append(broken, step.GetID())
			}
		}
	}
	if len(broken) > 0 {
		return nil, fmt.Errorf("refusing to migrate: step(s) %s are declared scripted but have no learnings/<step-id>/main.py -- they are already broken, and this migration will not guess whether each should become a message_sequence (change_step_type target_type=message_sequence) or get its script written (update_scripted_step code=...); fix them first, then re-run", strings.Join(broken, ", "))
	}

	// Pass 2: rewrite plan types so every regular step is a declared scripted one.
	result := &declaredModeMigrationResult{}
	replace := func(stepID string, replacement PlanStepInterface) error {
		replaced, _ := replaceStepRecursively(plan.Steps, stepID, replacement)
		if !replaced {
			replaced, _ = replaceStepRecursively(plan.OrphanSteps, stepID, replacement)
		}
		if !replaced {
			return fmt.Errorf("failed to replace step %q in the plan", stepID)
		}
		return nil
	}
	for _, info := range infos {
		switch step := info.Step.(type) {
		case *RegularPlanStep:
			if declaredScripted(step.GetID()) {
				continue // a true scripted step: type and declaration agree
			}
			replacement := normalizeRegularStepToAgent(step)
			if err := replace(step.GetID(), replacement); err != nil {
				return nil, err
			}
			result.TypeChanges = append(result.TypeChanges, declaredModeTypeChange{
				StepID: step.GetID(), From: string(StepTypeRegular), To: string(StepTypeAgent), before: step, after: replacement,
			})
		case *AgentPlanStep:
			if !declaredScripted(step.GetID()) {
				continue
			}
			replacement := normalizeAgentStepToRegular(step)
			if err := replace(step.GetID(), replacement); err != nil {
				return nil, err
			}
			result.TypeChanges = append(result.TypeChanges, declaredModeTypeChange{
				StepID: step.GetID(), From: string(StepTypeAgent), To: string(StepTypeRegular), before: step, after: replacement,
			})
		}
	}
	return result, nil
}

// createMigrateDeclaredExecutionModeExecutor is the trusted tool behind the
// v1.0.38 contract upgrade (see upgradeDeclaredExecutionModeRetired in
// cmd/server). It changes no behavior and touches only planning/plan.json:
// it writes down, as the plan type, the execution model each step already
// had. step_config.json is read (to tell scripted from legacy agentic) but
// never written.
func createMigrateDeclaredExecutionModeExecutor(
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
		before, err := json.Marshal(plan)
		if err != nil {
			return "", fmt.Errorf("snapshot plan: %w", err)
		}
		scriptExists := func(stepID string) bool {
			content, readErr := readFile(ctx, normalizePathForWorkspaceAPI(filepath.Join("learnings", stepID, "main.py"), workspacePath))
			return readErr == nil && strings.TrimSpace(content) != ""
		}

		result, err := migrateDeclaredExecutionModeInPlan(plan, configs, scriptExists)
		if err != nil {
			return "", err
		}
		if result.noOp() {
			return `{"status":"no_op","message":"Every regular step is already a declared scripted step and no agent is declared scripted; the plan already states each step's execution model."}`, nil
		}

		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("validate migrated plan: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("validate migrated plan: %w", err)
		}
		if err := ValidatePlanStructure(plan); err != nil {
			return "", fmt.Errorf("validate migrated plan: %w", err)
		}
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("write migrated planning/plan.json: %w", err)
		}
		after, _ := json.Marshal(plan)

		stepIDs := make([]string, 0, len(result.TypeChanges))
		changes := make([]PlanFieldChange, 0, len(result.TypeChanges))
		var deleted, added []json.RawMessage
		toSequence, toRegular := 0, 0
		for _, change := range result.TypeChanges {
			stepIDs = append(stepIDs, change.StepID)
			changes = append(changes, PlanFieldChange{StepID: change.StepID, Field: "type", OldValue: change.From, NewValue: change.To})
			if raw, err := json.Marshal(change.before); err == nil {
				deleted = append(deleted, raw)
			}
			if raw, err := json.Marshal(change.after); err == nil {
				added = append(added, raw)
			}
			if change.To == string(StepTypeAgent) {
				toSequence++
			} else {
				toRegular++
			}
		}
		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool: "migrate_declared_execution_mode",
			Reason: fmt.Sprintf(
				"Workflow contract v%s (PLAT-287, half 1): the plan type now states each step's execution model explicitly. %d legacy agentic regular step(s) made message_sequence (the runtime already ran them as one), %d declared-scripted agent step(s) made regular; step_config.json untouched -- declared_execution_mode stays until the runtime stops reading it.",
				workflowContractDeclaredExecutionModeRetiredVersionLabel, toSequence, toRegular,
			),
			StepIDs:        stepIDs,
			Changes:        changes,
			DeletedSteps:   deleted,
			AddedSteps:     added,
			BeforeSnapshot: json.RawMessage(before),
			AfterSnapshot:  json.RawMessage(after),
		}, readFile, writeFile, logger)

		payload, _ := json.Marshal(result)
		logger.Info(fmt.Sprintf("✅ migrate_declared_execution_mode: %d type change(s)", len(result.TypeChanges)))
		return fmt.Sprintf(`{"status":"migrated","result":%s}`, payload), nil
	}
}
