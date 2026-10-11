package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// change_step_type converts a step between the two execution models in place
// (PLAT-286). Until now the only way from a conversational message_sequence
// to a deterministic scripted step was to rebuild it -- add_scripted_step +
// delete_plan_steps + rewiring every dependency and route. The converters
// already existed for the runtime's own compatibility paths
// (normalizeRegularStepToAgent, normalizeAgentStepToRegular);
// this tool exposes them as one atomic plan change with a revertable
// changelog entry.
//
// Since PLAT-287 the plan type alone decides how a step runs: a regular step
// IS a scripted step (its work is the checked-in learnings/<step-id>/main.py)
// and an agent step is conversational. There is no separate mode
// to keep in step; this tool only rewrites the plan and, for the scripted
// direction, makes sure the step's config has code execution on.
const (
	changeStepTypeTargetScripted = "scripted"
	changeStepTypeTargetAgent    = "agent"
)

func getChangeStepTypeSchema() string {
	return `{
		"type": "object",
		"properties": {
			"step_id": {"type": "string", "minLength": 1, "description": "REQUIRED: id of the step to convert (its id field in the plan). Nested and orphan steps are accepted."},
			"target_type": {"type": "string", "enum": ["scripted", "agent"], "description": "REQUIRED: scripted = deterministic work in a checked-in learnings/<step-id>/main.py (the internal regular plan type); message_sequence = conversational turns."},
			"reason": {"type": "string", "minLength": 1, "description": "REQUIRED: why this step's execution model changes. Recorded in planning/changelog."}
		},
		"required": ["step_id", "target_type", "reason"]
	}`
}

type changeStepTypeResult struct {
	configs      []StepConfig
	noOp         bool
	oldType      string
	newType      string
	droppedItems int
	// before/after are the step as found and as written, recorded in the
	// changelog as DeletedSteps/AddedSteps -- the persisted revert data
	// (snapshots are only hashed into before_ref/after_ref, never stored).
	before PlanStepInterface
	after  PlanStepInterface
}

// ensureStepConfig returns the step's AgentConfigs, creating the step_config
// entry when the step has none yet. MatchStepConfigByID hands back the
// pointer stored in the slice, so edits through it persist on write.
func ensureStepConfig(configs []StepConfig, stepID string) ([]StepConfig, *AgentConfigs) {
	for i := range configs {
		if configs[i].ID == stepID {
			if configs[i].AgentConfigs == nil {
				configs[i].AgentConfigs = &AgentConfigs{}
			}
			return configs, configs[i].AgentConfigs
		}
	}
	cfg := &AgentConfigs{}
	return append(configs, StepConfig{ID: stepID, AgentConfigs: cfg}), cfg
}

// changeStepTypeInPlan is the pure mutation: it rewrites the step in plan
// (in place, wherever it sits) and returns the step_config slice to persist.
func changeStepTypeInPlan(plan *PlanningResponse, configs []StepConfig, stepID, target string) (*changeStepTypeResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("cannot change the type of step %q: plan is unavailable", stepID)
	}
	existing, _, _ := findStepByID(plan.Steps, stepID)
	if existing == nil {
		existing, _, _ = findStepByID(plan.OrphanSteps, stepID)
	}
	if existing == nil {
		return nil, fmt.Errorf("step %q was not found in the plan (checked nested and orphan steps)", stepID)
	}

	cfg := MatchStepConfigByID(stepID, configs)
	res := &changeStepTypeResult{configs: configs, before: existing, after: existing}

	configureScripted := func() {
		res.configs, cfg = ensureStepConfig(res.configs, stepID)
		// A scripted step runs through the code executor; that is the one
		// config flag the type implies. Any retired declared_execution_mode
		// still on the entry is cleared so the transitional shim cannot read
		// the step as legacy agentic again.
		useCode := true
		cfg.UseCodeExecutionMode = &useCode
		cfg.LegacyDeclaredExecutionMode = ""
		cfg.LegacyDeclaredExecutionModeReason = ""
	}
	configureSequence := func() {
		if cfg == nil {
			return
		}
		// A scripted step locks its checked-in script; a sequence has none.
		cfg.LockCode = nil
		cfg.LegacyDeclaredExecutionMode = ""
		cfg.LegacyDeclaredExecutionModeReason = ""
	}
	replace := func(replacement PlanStepInterface) error {
		replaced, _ := replaceStepRecursively(plan.Steps, stepID, replacement)
		if !replaced {
			replaced, _ = replaceStepRecursively(plan.OrphanSteps, stepID, replacement)
		}
		if !replaced {
			return fmt.Errorf("failed to replace step %q in the plan", stepID)
		}
		res.after = replacement
		return nil
	}

	switch step := existing.(type) {
	case *AgentPlanStep:
		res.oldType = string(StepTypeAgent)
		if target == changeStepTypeTargetAgent {
			res.newType = res.oldType
			res.noOp = true
			return res, nil
		}
		res.droppedItems = len(step.Items)
		if err := replace(normalizeAgentStepToRegular(step)); err != nil {
			return nil, err
		}
		res.newType = string(StepTypeRegular)
		configureScripted()
		return res, nil

	case *RegularPlanStep:
		res.oldType = string(StepTypeRegular)
		if target == changeStepTypeTargetScripted {
			res.newType = res.oldType
			if isScriptedStep(step, cfg) {
				res.noOp = true
				return res, nil
			}
			// A regular step still carrying the retired
			// declared_execution_mode="agentic" runs as a message_sequence
			// through the transitional shim; clearing that key is the whole
			// conversion (the plan type already says scripted).
			configureScripted()
			return res, nil
		}
		if err := replace(normalizeRegularStepToAgent(step)); err != nil {
			return nil, err
		}
		res.newType = string(StepTypeAgent)
		configureSequence()
		return res, nil

	default:
		return nil, fmt.Errorf("change_step_type converts only between scripted and message_sequence; step %q is a %s step, which has its own add/update/delete tools", stepID, existing.StepType())
	}
}

func createChangeStepTypeExecutor(
	workspacePath string,
	logger loggerv2.Logger,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}
		stepID, _ := args["step_id"].(string)
		stepID = strings.TrimSpace(stepID)
		if stepID == "" {
			return "", fmt.Errorf("step_id is required")
		}
		target, _ := args["target_type"].(string)
		target = strings.ToLower(strings.TrimSpace(target))
		if target != changeStepTypeTargetScripted && target != changeStepTypeTargetAgent {
			return "", fmt.Errorf("target_type must be %q or %q, got %q", changeStepTypeTargetScripted, changeStepTypeTargetAgent, target)
		}

		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}
		stepConfigs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read step config: %w", err)
		}
		before, err := json.Marshal(plan)
		if err != nil {
			return "", fmt.Errorf("failed to snapshot plan: %w", err)
		}

		result, err := changeStepTypeInPlan(plan, stepConfigs, stepID, target)
		if err != nil {
			return "", err
		}
		if result.noOp {
			return fmt.Sprintf("Step %q is already a %s step; nothing changed.", stepID, target), nil
		}

		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after conversion: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after conversion: %w", err)
		}
		if err := ValidatePlanStructure(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after conversion: %w", err)
		}

		// Plan first, then config. The plan type is the decision; the config
		// write only carries the code-execution flag / lock cleanup, and the
		// tool is idempotent on re-run if that second write fails.
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}
		if err := writeStepConfigViaFileCallback(ctx, workspacePath, result.configs, writeFile); err != nil {
			return "", fmt.Errorf("plan type of %q is now %s but writing planning/step_config.json failed: %w -- re-run change_step_type with the same arguments to finish the config half", stepID, result.newType, err)
		}
		after, _ := json.Marshal(plan)

		fieldChanges := []PlanFieldChange{}
		if result.oldType != result.newType {
			fieldChanges = append(fieldChanges, PlanFieldChange{StepID: stepID, Field: "type", OldValue: result.oldType, NewValue: result.newType})
		}
		entry := PlanChangelogEntry{
			Tool:           "change_step_type",
			Reason:         reason,
			StepIDs:        []string{stepID},
			Changes:        fieldChanges,
			BeforeSnapshot: json.RawMessage(before),
			AfterSnapshot:  json.RawMessage(after),
		}
		if result.oldType != result.newType {
			// Full step JSON before and after, the same revert data
			// delete_plan_steps/add_* record: put the old one back to undo.
			if oldStep, err := json.Marshal(result.before); err == nil {
				entry.DeletedSteps = []json.RawMessage{oldStep}
			}
			if newStep, err := json.Marshal(result.after); err == nil {
				entry.AddedSteps = []json.RawMessage{newStep}
			}
		}
		logPlanChange(ctx, workspacePath, entry, readFile, withPlanMutationWriteAccess(workspacePath, writeFile), logger)
		reviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, stepID, fieldChanges, readFile, writeFile, logger)

		scriptDir := savedCodeDirectory(ctx, workspacePath, stepID, readFile)
		scriptPath := normalizePathForWorkspaceAPI(filepath.Join(scriptDir, "main.py"), workspacePath)
		scriptContent, scriptErr := readFile(ctx, scriptPath)
		scriptExists := scriptErr == nil && strings.TrimSpace(scriptContent) != ""

		var b strings.Builder
		switch target {
		case changeStepTypeTargetScripted:
			fmt.Fprintf(&b, "Converted step %q to a scripted step (plan type %s; a regular step runs its checked-in script).", stepID, result.newType)
			if result.droppedItems > 0 {
				fmt.Fprintf(&b, " Dropped %d conversational item(s): a scripted step's work lives entirely in %s/main.py.", result.droppedItems, scriptDir)
			}
			if scriptExists {
				fmt.Fprintf(&b, " %s/main.py already exists and will now run through the real scripted executor.", scriptDir)
			} else {
				fmt.Fprintf(&b, " %s/main.py does NOT exist yet -- author this file with the workspace editing tools, then test with execute_step(fast_path_only=true).", scriptDir)
			}
			b.WriteString(" Keep validation_schema strict, and move any judgment or verification that lived in the old turns into a message_sequence that consumes this step's output rather than into the script.")
		default:
			fmt.Fprintf(&b, "Converted step %q to an agent with one execute-and-verify item.", stepID)
			b.WriteString(" Refine the turns with update_agent_step.")
			if scriptExists {
				fmt.Fprintf(&b, " %s/main.py still exists; a script nothing runs is artifact debt -- delete it, or keep it only if the sequence is meant to invoke it.", scriptDir)
			}
		}
		b.WriteString(" Recorded in planning/changelog with the full step JSON before and after, so it can be reverted.")
		b.WriteString(reviewNotice)
		logger.Info(fmt.Sprintf("✅ change_step_type: %s -> %s for step %s", result.oldType, result.newType, stepID))
		return b.String(), nil
	}
}
