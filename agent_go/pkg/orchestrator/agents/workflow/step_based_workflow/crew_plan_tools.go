package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// getAddCrewStepSchema returns the JSON schema for add_crew_step tool
func getAddCrewStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "REQUIRED: Stable step ID for this crew step. Generate a unique, URL-friendly ID based on the step title."
			},
			"title": {
				"type": "string",
				"description": "REQUIRED: Short, clear title for the crew step."
			},
			"description": {
				"type": "string",
				"description": "OPTIONAL: What this crew invocation is for. Unlike instruction, this is never sent to the Crew."
			},
			"crew_profile_id": {
				"type": "string",
				"description": "REQUIRED: Product profile of the target Crew; initially \"work\"."
			},
			"crew_project_id": {
				"type": "string",
				"description": "REQUIRED: Stable target Crew project ID. Names and paths are display metadata and must not be used here."
			},
			"trigger_id": {
				"type": "string",
				"description": "REQUIRED: ID of the Crew trigger to invoke. The trigger owns its saved base instruction and conversation destination."
			},
			"instruction": {
				"type": "string",
				"description": "REQUIRED: Workflow-specific instruction for this invocation. Variables in {{name}} form are rendered at run time."
			},
			"required_variables": {"type": "array", "items": {"type": "string"}, "description": "Declared launch variable names that must be nonblank before this step runs."},
			"context_dependencies": {
				"type": "array",
				"items": { "type": "string" },
				"description": "REQUIRED: Workflow outputs made available as trigger input, keyed by file base name without extension. Use empty array [] if none."
			},
			"context_output": {
				"type": "string",
				"description": "OPTIONAL: Response filename for the Crew's final text; defaults to response.md."
			},
			"timeout_seconds": {
				"type": "integer",
				"description": "OPTIONAL: Maximum wait for the Crew run in seconds; defaults to 1800."
			},
			"next_step_id": {
				"type": "string",
				"description": "OPTIONAL: Explicit successor step ID, or 'end'. Omit for sequential execution."
			},
			"insert_after_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the step to insert after. Use the step's id field from the plan. Use empty string to insert at the beginning."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this crew step is being added. Captured into the plan changelog."
			}
		},
		"required": ["id", "title", "crew_profile_id", "crew_project_id", "trigger_id", "instruction", "context_dependencies", "insert_after_step_id", "reason"]
	}`
}

// getUpdateCrewStepSchema returns the JSON schema for update_crew_step tool
func getUpdateCrewStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the crew step to update. Use the step's id field from the plan."
			},
			"title": {
				"type": "string",
				"description": "OPTIONAL: New title for the step."
			},
			"description": {
				"type": "string",
				"description": "OPTIONAL: Updated purpose note. Unlike instruction, this is never sent to the Crew."
			},
			"crew_profile_id": {
				"type": "string",
				"description": "OPTIONAL: Updated Crew product profile."
			},
			"crew_project_id": {
				"type": "string",
				"description": "OPTIONAL: Updated target Crew project ID."
			},
			"trigger_id": {
				"type": "string",
				"description": "OPTIONAL: Updated Crew trigger ID."
			},
			"instruction": {
				"type": "string",
				"description": "OPTIONAL: Updated workflow-specific instruction."
			},
			"required_variables": {"type": "array", "items": {"type": "string"}, "description": "Declared launch variable names that must be nonblank before this step runs."},
			"context_dependencies": {
				"type": "array",
				"items": { "type": "string" },
				"description": "OPTIONAL: Updated workflow outputs made available as trigger input."
			},
			"context_output": {
				"type": "string",
				"description": "OPTIONAL: Updated response filename for the Crew's final text."
			},
			"timeout_seconds": {
				"type": "integer",
				"description": "OPTIONAL: Updated maximum wait for the Crew run in seconds."
			},
			"next_step_id": {
				"type": "string",
				"description": "OPTIONAL: Updated explicit successor step ID, or 'end'."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this crew step is being updated. Captured into the plan changelog."
			}
		},
		"required": ["existing_step_id", "reason"]
	}`
}

// createAddCrewStepExecutor creates an executor function for add_crew_step tool
func createAddCrewStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return createSingleStepAdder(workspacePath, logger, readFile, writeFile, moveFile, "crew")
}

// createUpdateCrewStepExecutor creates an executor function for update_crew_step
// tool. Mirrors createUpdateRoutingStepExecutor -- same shape, *CrewPlanStep
// instead of *RoutingPlanStep.
func createUpdateCrewStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}

		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}

		var partialUpdate PartialPlanStep
		if err := json.Unmarshal(stepJSON, &partialUpdate); err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}

		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		var existingStep PlanStepInterface
		for _, step := range plan.Steps {
			if step.GetID() == partialUpdate.ExistingStepID {
				existingStep = step
				break
			}
		}
		if existingStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("step ID '%s' not found in existing plan. Available step IDs: %v", partialUpdate.ExistingStepID, availableIDs)
		}

		if _, ok := existingStep.(*CrewPlanStep); !ok {
			return "", fmt.Errorf("step with ID '%s' is not a crew step", partialUpdate.ExistingStepID)
		}

		fieldChanges := make([]PlanFieldChange, 0)
		stepIndex, _, err := updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}

		// Validate the updated step
		updatedStep := plan.Steps[stepIndex]
		updatedCrewStep, ok := updatedStep.(*CrewPlanStep)
		if !ok {
			return "", fmt.Errorf("updated step is not a crew step")
		}

		if err := validateCrewStepFieldsTyped(updatedCrewStep); err != nil {
			return "", fmt.Errorf("validation failed after update: %w", err)
		}

		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}

		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_crew_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Updated crew step '%s' in plan", partialUpdate.ExistingStepID))
		return fmt.Sprintf("Successfully updated crew step '%s' in the plan%s", partialUpdate.ExistingStepID, dependentReviewNotice), nil
	}
}
