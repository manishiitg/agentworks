package step_based_workflow

import (
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const currentWorkflowRunFolder = "iteration-0"

// RoutingEvaluationCount tracks how many times a specific route has been selected
// Key format: "{stepID}:{routeID}"
type RoutingEvaluationCount map[string]int

// OrchestrationRoute represents a possible route/sub-agent (private to orchestration step)
type OrchestrationRoute struct {
	RouteID      string            `json:"route_id"`       // Unique ID for this route
	RouteName    string            `json:"route_name"`     // Human-readable name
	Condition    string            `json:"condition"`      // Condition description (e.g., "If error is authentication-related")
	SubAgentStep PlanStepInterface `json:"sub_agent_step"` // The sub-agent step to execute (private, not in main workflow)
}

// StepProgress tracks which steps have been completed
type StepProgress struct {
	CompletedStepIndices    []int                  `json:"completed_step_indices"` // 0-based indices
	TotalSteps              int                    `json:"total_steps"`
	LastUpdated             time.Time              `json:"last_updated"`
	ValidationFailures      map[string]int         `json:"validation_failures,omitempty"` // key is step path, value is failure count
	RoutingEvaluationCounts RoutingEvaluationCount `json:"-"`                             // in-memory only: tracks routing step evaluations to prevent infinite loops (not persisted)
	JumpCounts              map[string]int         `json:"-"`                             // in-memory only: counts identical "source->target" next_step_id jumps to prevent infinite loops (not persisted)
	ArchivalCounts          map[int]int            `json:"archival_counts,omitempty"`     // key is stepNumber (1-based), value is archive run count
}

// ExecutionOptions carries the execution details that remain meaningful after a
// run is selected. Full workflow runs always use the current run slot.
type ExecutionOptions struct {
	TriggerContextFile string            `json:"-"` // Server-owned, read-only channel context for this invocation.
	WebhookStepID      string            `json:"-"` // Saved direct webhook step; resolved against the loaded plan.
	WebhookVariables   map[string]string `json:"-"`
	// Read-only compatibility for a frozen pre-group-free Relay trigger. This
	// selects configuration values only, never an execution group or batch.
	RelayLegacyGroupName string `json:"-"`
	WebhookInputFile     string `json:"-"`                             // Internal API delivery file; cannot be supplied by tool JSON.
	SelectedRunFolder    string `json:"selected_run_folder,omitempty"` // Current run slot (iteration-0) for full workflow runs
	// Trusted scheduled-run identity. These fields are server-owned and never
	// decoded from public tool/API JSON.
	RunKind           string    `json:"-"`
	ScheduleRunID     string    `json:"-"`
	ScheduleID        string    `json:"-"`
	TriggerSource     string    `json:"-"`
	ScheduledFor      time.Time `json:"-"`
	ExecutionID       string    `json:"-"`
	ExecutionStrategy string    `json:"execution_strategy"`           // Execution strategy (see constants below)
	ResumeFromStep    int       `json:"resume_from_step,omitempty"`   // 1-based step number to resume from (for top-level steps)
	PlanChangeAction  string    `json:"plan_change_action,omitempty"` // "keep_old_progress" or "delete_old_progress"

	// CapacityAccountKey identifies the provider account this run draws on, as
	// a hash of its credential. The orchestrator cannot resolve credentials
	// itself — the scheduler already does, so it passes the key down rather
	// than duplicating secret access into this layer.
	CapacityAccountKey string `json:"capacity_account_key,omitempty"`
	// PaceThresholdPercent > 0 enables quota pacing for this run.
	PaceThresholdPercent int `json:"pace_threshold_percent,omitempty"`

	// Variable group execution options (for batch execution with multiple groups)
	EnabledGroupNames []string `json:"enabled_group_names,omitempty"` // Group names to execute (if empty, uses groups' enabled flags)

	// Human input overrides, keyed by step ID. Human-input steps consume the
	// value as their response; executable steps receive it as run-scoped prompt
	// context. When SkipHumanInput is true, human-input responses take priority
	// over the variableValues fallback.
	HumanInputs map[string]string `json:"human_inputs,omitempty"`

	// RouteSelections overrides routing steps deterministically by step ID.
	// Values may be route_id or a unique next_step_id for that routing step.
	RouteSelections map[string]string `json:"route_selections,omitempty"`

	// CrewRunner invokes Crew triggers for crew steps. Server-owned: the
	// executor cannot reach the Crew services itself, so the server binds
	// an implementation here. Nil means the current run path cannot
	// execute crew steps. Never decoded from public tool/API JSON.
	CrewRunner CrewStepRunner `json:"-"`
}

// BatchExecutionProgress tracks execution progress across multiple variable groups
type BatchExecutionProgress struct {
	TotalGroups     int                      `json:"total_groups"`     // Total number of enabled groups
	EnabledGroups   []string                 `json:"enabled_groups"`   // Group IDs to execute
	CompletedGroups []string                 `json:"completed_groups"` // Group IDs that finished
	CurrentGroup    string                   `json:"current_group"`    // Currently executing group ID
	GroupProgress   map[string]*StepProgress `json:"group_progress"`   // Per-group step progress
	LastUpdated     time.Time                `json:"last_updated"`
	IterationNumber int                      `json:"iteration_number"` // Current iteration number (e.g., 1 for iteration-1)
}

// ExecutionContext represents immutable execution configuration
// Created once at execution start and passed through the call chain
type ExecutionContext struct {
	SkipHumanInput    bool // Whether to skip human feedback requests (auto-approve steps)
	RunSingleStepOnly bool // Whether to run only a single step and stop
	SingleStepTarget  int  // Target step index to run (0-based)
	SavedScriptOnly   bool // Whether to run only saved learnings/{step-id}/main.py with no LLM fallback
	// AllowScriptRepair lets an LLM author or repair a scripted step's main.py
	// in this execution. Only the Builder's own execute_step sets it; every run
	// (schedule, webhook/Relay, run_full_workflow, a route called by an agent)
	// runs the saved script and fails on its error (PLAT-436).
	AllowScriptRepair bool
	StepPathOverride  string // If set, overrides the default "step-{N}" path for the target step (used for inner steps in workshop)

	// ArtifactFolderNameOverride writes execution artifacts/logs to this folder
	// instead of the stable step ID folder. Logical step ID lookup for configs,
	// learnings, and metadata is unchanged.
	ArtifactFolderNameOverride string

	// Human input overrides, keyed by step ID. Propagated from
	// ExecutionOptions.HumanInputs and scoped to the matching step before it is
	// dispatched. This must never be applied to the shared context directly.
	HumanInputs map[string]string

	// WorkshopHumanInput is the single-step response the builder agent supplies via
	// execute_step(human_input="..."). Human input steps consume it as the response
	// substitute; executable steps receive it as high-priority prompt context.
	WorkshopHumanInput string

	// AgentRestart forces an agent step to archive any existing
	// session and run its configured item queue from scratch.
	AgentRestart bool

	// ConversationHistoryCapture is an optional pointer; when non-nil the execution engine
	// writes the agent's full conversation history into it after Execute() returns.
	// This is used by sub-agent callers (e.g., get_sub_agent_conversation tool) to
	// retrieve the internal conversation without modifying the execution path.
	ConversationHistoryCapture *[]llmtypes.MessageContent
}

// Execution strategy constants
// All strategies run with learning enabled and no human feedback.
const (
	// Fresh start
	ExecutionStrategyStartFromBeginningNoHuman = "start_from_beginning_no_human" // Default: fresh start with learning, no human feedback

	// Resume
	ExecutionStrategyResumeFromStepNoHuman = "resume_from_step_no_human" // Resume from specific step with learning, no human feedback

	// Single step execution
	ExecutionStrategyRunSingleStep = "run_single_step" // Run only the specified step and stop

	// Plan change actions
	PlanChangeActionKeepOldProgress   = "keep_old_progress"
	PlanChangeActionDeleteOldProgress = "delete_old_progress"
)

// TodoStep has been removed - use PlanStepInterface instead
// All execution code now uses PlanStepInterface directly for type safety
