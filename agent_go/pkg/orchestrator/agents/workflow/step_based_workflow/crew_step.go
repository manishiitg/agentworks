package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// DefaultCrewStepTimeoutSeconds bounds how long a crew step waits for the
// Crew run when the step sets no timeout_seconds.
const DefaultCrewStepTimeoutSeconds = 1800

// CrewPlanStep invokes one trigger of a Crew project and waits for its final
// response. The trigger owns its saved base instruction and conversation
// destination; the step supplies the workflow-specific instruction and
// runtime input. Crew steps run only as top-level steps.
type CrewPlanStep struct {
	Type StepType `json:"type"` // Always "crew" - required for JSON marshaling/unmarshaling
	CommonStepFields
	CrewProfileID string `json:"crew_profile_id"` // Product profile of the target Crew; initially "work"
	CrewProjectID string `json:"crew_project_id"` // Stable target Crew project ID
	TriggerID     string `json:"trigger_id"`      // Crew trigger to invoke
	// Agent names the Crew agent (one of its chats) this step talks to, e.g. "weekly report".
	// It is created on first use and keeps its own conversation; empty uses the Crew's main chat (PLAT-861-261).
	Agent          string `json:"agent,omitempty"`
	Instruction    string `json:"instruction"`               // Workflow-specific instruction, rendered with variables at run time
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"` // Maximum wait for the Crew run; defaults to DefaultCrewStepTimeoutSeconds
	NextStepID     string `json:"next_step_id,omitempty"`    // Optional explicit successor; empty preserves sequential execution
}

// Implement PlanStepInterface for CrewPlanStep
func (c *CrewPlanStep) GetID() string                           { return c.ID }
func (c *CrewPlanStep) GetTitle() string                        { return c.Title }
func (c *CrewPlanStep) GetDescription() string                  { return c.Description }
func (c *CrewPlanStep) GetContextDependencies() []string        { return c.ContextDependencies }
func (c *CrewPlanStep) GetContextOutput() FlexibleContextOutput { return c.ContextOutput }
func (c *CrewPlanStep) GetValidationSchema() *ValidationSchema  { return c.ValidationSchema }
func (c *CrewPlanStep) StepType() StepType                      { return StepTypeCrew }
func (c *CrewPlanStep) GetCommonFields() CommonStepFields       { return c.CommonStepFields }

// MarshalJSON ensures the type field is always set when marshaling
func (c *CrewPlanStep) MarshalJSON() ([]byte, error) {
	c.Type = StepTypeCrew
	type Alias CrewPlanStep
	return json.Marshal((*Alias)(c))
}

// UnmarshalJSON implements custom unmarshaling for CrewPlanStep
func (c *CrewPlanStep) UnmarshalJSON(data []byte) error {
	type Alias CrewPlanStep
	var temp Alias
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal crew step: %w", err)
	}
	*c = CrewPlanStep(temp)
	return nil
}

// validateCrewStepFieldsTyped validates that a CrewPlanStep has all required
// fields. It runs on plan load, on add/update tools, and before execution.
func validateCrewStepFieldsTyped(step *CrewPlanStep) error {
	if step.ID == "" {
		return fmt.Errorf("crew step (title: %q) is missing required ID field", step.Title)
	}
	if strings.TrimSpace(step.Title) == "" {
		return fmt.Errorf("crew step (ID: %s) is missing required title field", step.ID)
	}
	if strings.TrimSpace(step.CrewProfileID) == "" {
		return fmt.Errorf("crew step (title: %q, ID: %s) is missing required crew_profile_id field", step.Title, step.ID)
	}
	if strings.TrimSpace(step.CrewProjectID) == "" {
		return fmt.Errorf("crew step (title: %q, ID: %s) is missing required crew_project_id field", step.Title, step.ID)
	}
	if strings.TrimSpace(step.TriggerID) == "" {
		return fmt.Errorf("crew step (title: %q, ID: %s) is missing required trigger_id field", step.Title, step.ID)
	}
	if strings.TrimSpace(step.Instruction) == "" {
		return fmt.Errorf("crew step (title: %q, ID: %s) is missing required instruction field", step.Title, step.ID)
	}
	if step.TimeoutSeconds < 0 {
		return fmt.Errorf("crew step (title: %q, ID: %s) has negative timeout_seconds %d", step.Title, step.ID, step.TimeoutSeconds)
	}
	return nil
}

// CrewStepRequest is one crew step invocation: the rendered instruction plus
// the workflow outputs declared in context_dependencies, keyed by file base
// name without extension.
type CrewStepRequest struct {
	WorkflowID        string
	WorkflowRunFolder string
	// ExecutionID is the workflow run's immutable execution identity. It
	// keys the Crew delivery identity: stable across retries of one step
	// attempt, unique across executions. Run folders are reused between
	// executions and must never key deliveries.
	ExecutionID    string
	StepID         string
	Group          string
	ProfileID      string
	ProjectID      string
	TriggerID      string
	Agent          string
	Instruction    string
	Inputs         map[string]interface{}
	TimeoutSeconds int
}

// CrewStepResult is the outcome of a crew step invocation. FinalResponse is
// set only for a successful Crew run; the run metadata (IDs, status, error
// detail, timestamps) is populated whenever a Crew run exists, including
// on failures, so the step can persist a complete execution record.
type CrewStepResult struct {
	CrewRunID     string
	SessionID     string
	Status        string
	FinalResponse string
	Error         string
	StartedAt     time.Time
	CompletedAt   *time.Time
	// Usage is the Crew run's token spend, present once the Crew side
	// records it. The executor persists it under the crew step so the
	// workflow run's cost breakdown shows its true cost.
	Usage *workflowtypes.CrewRunTokenUsage
	// Timeline records every observed Crew run status change (queued,
	// running, terminal) with the time it was first seen, so execution
	// logs can show the polling history behind the final state.
	Timeline []CrewPollTransition
}

// CrewPollTransition is one observed Crew run status with the time the
// workflow first saw it.
type CrewPollTransition struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

// CrewStepRunner invokes Crew triggers for workflow crew steps. The executor
// cannot reach the Crew services directly, so the server binds an
// implementation on ExecutionOptions; a nil runner means the current run
// path cannot execute crew steps. Failures — crew run failed or stopped,
// timeout, or lost access — return an error along with whatever run
// metadata exists.
type CrewStepRunner interface {
	RunCrewStep(ctx context.Context, req CrewStepRequest) (CrewStepResult, error)
}
