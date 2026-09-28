package events

import (
	"time"

	"github.com/manishiitg/mcpagent/events"
)

// Orchestrator Events
type OrchestratorEndEvent struct {
	events.BaseEventData
	Objective        string        `json:"objective"`
	Result           string        `json:"result"`
	Duration         time.Duration `json:"duration"`
	Status           string        `json:"status"`
	Error            string        `json:"error,omitempty"`
	OrchestratorType string        `json:"orchestrator_type,omitempty"`
	ExecutionMode    string        `json:"execution_mode,omitempty"`
}

func (e *OrchestratorEndEvent) GetEventType() events.EventType {
	return OrchestratorEnd
}

// Orchestrator Agent Events
type OrchestratorAgentStartEvent struct {
	events.BaseEventData
	AgentType            string            `json:"agent_type"`                        // planning, execution, validation, organizer
	AgentName            string            `json:"agent_name"`                        // specific agent name
	Objective            string            `json:"objective"`                         // what the agent is trying to accomplish
	InputData            map[string]string `json:"input_data"`                        // template variables passed to agent
	ModelID              string            `json:"model_id"`                          // which LLM model
	Provider             string            `json:"provider"`                          // which LLM provider
	ServersCount         int               `json:"servers_count"`                     // number of MCP servers available
	MaxTurns             int               `json:"max_turns"`                         // maximum conversation turns
	PlanID               string            `json:"plan_id,omitempty"`                 // associated plan ID
	StepIndex            int               `json:"step_index,omitempty"`              // which step in the plan
	Iteration            int               `json:"iteration,omitempty"`               // which iteration of the loop
	UseCodeExecutionMode bool              `json:"use_code_execution_mode,omitempty"` // code execution mode enabled
	UseScriptedMode      bool              `json:"use_learn_code_mode,omitempty"`     // scripted mode enabled (persistent main.py replayed across runs). Wire-format tag kept as use_learn_code_mode for back-compat; rename deferred.
	SystemPrompt         string            `json:"system_prompt,omitempty"`           // full system prompt sent to LLM
	UserMessage          string            `json:"user_message,omitempty"`            // user message sent to LLM
}

func (e *OrchestratorAgentStartEvent) GetEventType() events.EventType {
	return OrchestratorAgentStart
}

type OrchestratorAgentEndEvent struct {
	events.BaseEventData
	AgentType    string            `json:"agent_type"`           // planning, execution, validation, organizer
	AgentName    string            `json:"agent_name"`           // specific agent name
	Objective    string            `json:"objective"`            // what the agent was trying to accomplish
	InputData    map[string]string `json:"input_data"`           // template variables passed to agent
	Result       string            `json:"result"`               // agent's output/result (text summary)
	Success      bool              `json:"success"`              // whether agent completed successfully
	Error        string            `json:"error,omitempty"`      // error message if failed
	Duration     time.Duration     `json:"duration"`             // how long the agent took
	ModelID      string            `json:"model_id"`             // which LLM model was used
	Provider     string            `json:"provider"`             // which LLM provider
	ServersCount int               `json:"servers_count"`        // number of MCP servers used
	MaxTurns     int               `json:"max_turns"`            // maximum conversation turns
	PlanID       string            `json:"plan_id,omitempty"`    // associated plan ID
	StepIndex    int               `json:"step_index,omitempty"` // which step in the plan
	Iteration    int               `json:"iteration,omitempty"`  // which iteration of the loop
	// Token usage fields
	PromptTokens          int `json:"prompt_tokens,omitempty"`
	CompletionTokens      int `json:"completion_tokens,omitempty"`
	TotalTokens           int `json:"total_tokens,omitempty"`
	CacheTokens           int `json:"cache_tokens,omitempty"`
	ReasoningTokens       int `json:"reasoning_tokens,omitempty"`
	LLMCallCount          int `json:"llm_call_count,omitempty"`
	CacheEnabledCallCount int `json:"cache_enabled_call_count,omitempty"`
}

func (e *OrchestratorAgentEndEvent) GetEventType() events.EventType {
	return OrchestratorAgentEnd
}

type OrchestratorAgentErrorEvent struct {
	events.BaseEventData
	AgentType    string        `json:"agent_type"`           // planning, execution, validation, organizer
	AgentName    string        `json:"agent_name"`           // specific agent name
	Objective    string        `json:"objective"`            // what the agent was trying to accomplish
	Error        string        `json:"error"`                // error message
	Duration     time.Duration `json:"duration"`             // how long before error occurred
	ModelID      string        `json:"model_id"`             // which LLM model was used
	Provider     string        `json:"provider"`             // which LLM provider
	ServersCount int           `json:"servers_count"`        // number of MCP servers available
	MaxTurns     int           `json:"max_turns"`            // maximum conversation turns
	PlanID       string        `json:"plan_id,omitempty"`    // associated plan ID
	StepIndex    int           `json:"step_index,omitempty"` // which step in the plan
	Iteration    int           `json:"iteration,omitempty"`  // which iteration of the loop
}

func (e *OrchestratorAgentErrorEvent) GetEventType() events.EventType {
	return OrchestratorAgentError
}

// Background Agent Events
//
// These report the lifecycle of a background/delegated agent: a sub-agent
// call, a todo-task step, or a message-sequence item — anything the main
// agent dispatches and gets notified about asynchronously. AgentID is the
// single universal identity field across all four event types below (never
// "background_agent_id" — that name exists only as a generic fallback
// candidate elsewhere and is never actually populated for these events).
//
// ParentExecutionID links a background agent to the execution node that
// owns it (e.g. a message-sequence item -> its parent workflow step). It is
// often not set by the caller and is instead backfilled from the background
// agent registry at emission time — see emitBackgroundAgentEvent.
type BackgroundAgentStartedEvent struct {
	events.BaseEventData
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Instruction string `json:"instruction,omitempty"`
	// Kind is what this execution IS (see ExecutionKind). It is declared by
	// whoever creates the execution and must not be re-inferred downstream
	// from the AgentID's string prefix — that inference is exactly what let
	// message-sequence items and full runs be misclassified.
	Kind              ExecutionKind `json:"execution_kind,omitempty"`
	ParentExecutionID string        `json:"parent_execution_id,omitempty"`
}

func (e *BackgroundAgentStartedEvent) GetEventType() events.EventType {
	return BackgroundAgentStarted
}

// BackgroundAgentCompletedEvent reports a background agent finishing.
// Result and Error are mutually exclusive: Result is set for
// Status == "completed", Error for Status == "failed".
type BackgroundAgentCompletedEvent struct {
	events.BaseEventData
	AgentID           string        `json:"agent_id"`
	Name              string        `json:"name"`
	Status            string        `json:"status"` // "completed" | "failed"
	Result            string        `json:"result,omitempty"`
	Error             string        `json:"error,omitempty"`
	Duration          string        `json:"duration,omitempty"`
	ParentExecutionID string        `json:"parent_execution_id,omitempty"`
	Kind              ExecutionKind `json:"execution_kind,omitempty"`
}

func (e *BackgroundAgentCompletedEvent) GetEventType() events.EventType {
	return BackgroundAgentCompleted
}

// BackgroundAgentTerminatedEvent reports a background agent being canceled
// or torn down before it produced a normal completion.
type BackgroundAgentTerminatedEvent struct {
	events.BaseEventData
	AgentID           string `json:"agent_id"`
	Name              string `json:"name"`
	Status            string `json:"status,omitempty"` // e.g. "canceled"
	ParentExecutionID string `json:"parent_execution_id,omitempty"`
}

func (e *BackgroundAgentTerminatedEvent) GetEventType() events.EventType {
	return BackgroundAgentTerminated
}

// PresentationUpdatedEvent announces that a product tool has shown or
// re-shown something in ui_presentations (a video today; a report or any
// other kind a future product declares under the same contract). Kind
// dispatches the frontend's presentation renderer registry, the same way it
// dispatches ToolBinding.Presentation on the backend -- one declared value,
// two consumers, instead of a hardcoded name check on either side.
//
// It carries the full title and payload so a listener can render immediately
// without a second database round trip. Activity is copied from the product's
// YAML tool binding, so the shared transcript can show a real product action
// without maintaining a parallel kind-to-copy map.
type PresentationActivity struct {
	Label       string `json:"label"`
	Destination string `json:"destination"`
	Detail      string `json:"detail"`
}

type PresentationUpdatedEvent struct {
	events.BaseEventData
	PresentationID string                 `json:"presentation_id"`
	Kind           string                 `json:"kind"`
	Title          string                 `json:"title"`
	WorkspacePath  string                 `json:"workspace_path"`
	Payload        map[string]interface{} `json:"payload"`
	Activity       *PresentationActivity  `json:"activity,omitempty"`
}

func (e *PresentationUpdatedEvent) GetEventType() events.EventType {
	return PresentationUpdated
}

// ProductInteractionEvent is emitted by a product tool for its own surface:
// Product names the product, Kind is the product's own vocabulary (for
// SparkQuill: "suggestions", "celebrate", "scene", "family_updated",
// "activity_created"), Payload is the kind's data. Transient by design --
// nothing is persisted beyond the session's event stream; a product that
// needs a durable panel item uses a presentation instead.
type ProductInteractionEvent struct {
	events.BaseEventData
	Product string                 `json:"product"`
	Kind    string                 `json:"kind"`
	Payload map[string]interface{} `json:"payload"`
}

func (e *ProductInteractionEvent) GetEventType() events.EventType {
	return ProductInteraction
}

// SyntheticTurnReadyEvent notifies the main agent that background work has
// started or completed, so a synthetic turn can weave the update in.
type SyntheticTurnReadyEvent struct {
	events.BaseEventData
	Message string `json:"message"`
	AgentID string `json:"agent_id"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status"`
}

func (e *SyntheticTurnReadyEvent) GetEventType() events.EventType {
	return SyntheticTurnReady
}

// AutoNotificationSteeredEvent reports a background-agent notification being
// delivered directly into an already-running foreground CLI turn instead of
// queued for the next turn.
type AutoNotificationSteeredEvent struct {
	events.BaseEventData
	AgentID  string        `json:"agent_id"`
	Name     string        `json:"name"`
	Status   string        `json:"status"`
	Provider string        `json:"provider"`
	Kind     ExecutionKind `json:"execution_kind,omitempty"`
}

func (e *AutoNotificationSteeredEvent) GetEventType() events.EventType {
	return AutoNotificationSteered
}

type RequestHumanFeedbackEvent struct {
	events.BaseEventData
	Objective        string `json:"objective"`
	TodoListMarkdown string `json:"todo_list_markdown"`
	SessionID        string `json:"session_id"`
	WorkflowID       string `json:"workflow_id"`
	RequestID        string `json:"request_id"` // Unique ID for this feedback request
	// NEW: Dynamic verification fields
	VerificationType  string `json:"verification_type,omitempty"`  // "planning_verification", "refinement_verification", "report_verification"
	NextPhase         string `json:"next_phase,omitempty"`         // The phase to transition to after approval
	Title             string `json:"title,omitempty"`              // Custom title text
	ActionLabel       string `json:"action_label,omitempty"`       // Custom button text
	ActionDescription string `json:"action_description,omitempty"` // Custom description text
}

func (e *RequestHumanFeedbackEvent) GetEventType() events.EventType {
	return RequestHumanFeedback
}

// HumanFeedbackResolvedEvent marks a blocking request answered or expired so a
// restored chat never shows it as still pending. Fields stay flat: readers
// look up data.request_id.
type HumanFeedbackResolvedEvent struct {
	events.BaseEventData
	RequestID string `json:"request_id"`
	Outcome   string `json:"outcome"` // "answered", "expired" or "cancelled"
	SessionID string `json:"session_id,omitempty"`
}

func (e *HumanFeedbackResolvedEvent) GetEventType() events.EventType {
	return HumanFeedbackResolved
}

type BlockingHumanFeedbackEvent struct {
	events.BaseEventData
	Question           string   `json:"question"`       // Question to ask user
	AllowFeedback      bool     `json:"allow_feedback"` // Whether to allow text feedback (defaults to true)
	Context            string   `json:"context"`        // Additional context (e.g., validation results)
	SessionID          string   `json:"session_id"`
	WorkflowID         string   `json:"workflow_id"`
	RequestID          string   `json:"request_id"`                      // Unique ID for this feedback request
	YesNoOnly          bool     `json:"yes_no_only"`                     // If true, show only Approve/Reject buttons (no textarea)
	YesLabel           string   `json:"yes_label,omitempty"`             // Custom label for Approve button (default: "Approve")
	NoLabel            string   `json:"no_label,omitempty"`              // Custom label for Reject button (default: "Reject")
	Options            []string `json:"options,omitempty"`               // Array of option labels for multiple choice (renders as buttons)
	RoutedToParentChat bool     `json:"routed_to_parent_chat,omitempty"` // Legacy compatibility; new requests always use direct UI submission
}

func (e *BlockingHumanFeedbackEvent) GetEventType() events.EventType {
	return BlockingHumanFeedback
}

// PlanApprovalEvent is emitted when confirm_plan_execution presents the plan for user approval (non-blocking).
type PlanApprovalEvent struct {
	events.BaseEventData
	Question string `json:"question"`
	Context  string `json:"context"`
	YesLabel string `json:"yes_label,omitempty"`
}

func (e *PlanApprovalEvent) GetEventType() events.EventType {
	return PlanApproval
}

// TodoStep represents a todo step in the execution
type TodoStep struct {
	ID                  string   `json:"id,omitempty"` // Stable step ID (from PlanStep) - required for frontend matching
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	SuccessCriteria     string   `json:"success_criteria"`
	WhyThisStep         string   `json:"why_this_step"`
	ContextDependencies []string `json:"context_dependencies"`
	ContextOutput       string   `json:"context_output"`
	SuccessPatterns     []string `json:"success_patterns,omitempty"` // what worked (includes tools)
	FailurePatterns     []string `json:"failure_patterns,omitempty"` // what failed (includes tools to avoid)
}

// StepTokenUsageEvent represents token usage summary for a workflow step
type StepTokenUsageEvent struct {
	events.BaseEventData
	Phase                 string `json:"phase"`                // e.g., "execution"
	Step                  int    `json:"step"`                 // step index (0-based)
	StepTitle             string `json:"step_title,omitempty"` // optional step title for display
	PromptTokens          int    `json:"prompt_tokens"`
	CompletionTokens      int    `json:"completion_tokens"`
	TotalTokens           int    `json:"total_tokens"`
	CacheTokens           int    `json:"cache_tokens"`
	ReasoningTokens       int    `json:"reasoning_tokens"`
	LLMCallCount          int    `json:"llm_call_count"`
	CacheEnabledCallCount int    `json:"cache_enabled_call_count"`
	// Pricing fields (in USD)
	InputCost     float64 `json:"input_cost_usd,omitempty"`
	OutputCost    float64 `json:"output_cost_usd,omitempty"`
	ReasoningCost float64 `json:"reasoning_cost_usd,omitempty"`
	CacheCost     float64 `json:"cache_cost_usd,omitempty"`
	TotalCost     float64 `json:"total_cost_usd,omitempty"`
	// Context window tracking
	ContextUsagePercent float64 `json:"context_usage_percent,omitempty"`
}

func (e *StepTokenUsageEvent) GetEventType() events.EventType {
	return StepTokenUsage
}

// NewStepTokenUsageEvent creates a new StepTokenUsageEvent
func NewStepTokenUsageEvent(phase string, step int, stepTitle string, promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount int) *StepTokenUsageEvent {
	return &StepTokenUsageEvent{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		Phase:                 phase,
		Step:                  step,
		StepTitle:             stepTitle,
		PromptTokens:          promptTokens,
		CompletionTokens:      completionTokens,
		TotalTokens:           totalTokens,
		CacheTokens:           cacheTokens,
		ReasoningTokens:       reasoningTokens,
		LLMCallCount:          llmCallCount,
		CacheEnabledCallCount: cacheEnabledCallCount,
	}
}

// NewStepTokenUsageEventWithPricing creates a new StepTokenUsageEvent with pricing and context usage
func NewStepTokenUsageEventWithPricing(phase string, step int, stepTitle string, promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount int, inputCost, outputCost, reasoningCost, cacheCost, totalCost float64, contextUsagePercent float64) *StepTokenUsageEvent {
	return &StepTokenUsageEvent{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		Phase:                 phase,
		Step:                  step,
		StepTitle:             stepTitle,
		PromptTokens:          promptTokens,
		CompletionTokens:      completionTokens,
		TotalTokens:           totalTokens,
		CacheTokens:           cacheTokens,
		ReasoningTokens:       reasoningTokens,
		LLMCallCount:          llmCallCount,
		CacheEnabledCallCount: cacheEnabledCallCount,
		InputCost:             inputCost,
		OutputCost:            outputCost,
		ReasoningCost:         reasoningCost,
		CacheCost:             cacheCost,
		TotalCost:             totalCost,
		ContextUsagePercent:   contextUsagePercent,
	}
}

// BatchExecutionCanceledEvent represents when batch execution is canceled by user.
// It is the only batch lifecycle event still emitted (the start/end/group
// wrappers were removed on 2026-09-22: never produced anywhere).
type BatchExecutionCanceledEvent struct {
	events.BaseEventData
	TotalGroups         int      `json:"total_groups"`          // Total number of enabled groups
	CompletedGroups     int      `json:"completed_groups"`      // Number of groups that completed before cancel
	CanceledGroupName   string   `json:"canceled_group_name"`   // Name of group that was running when canceled
	RemainingGroupNames []string `json:"remaining_group_names"` // Names of groups that were not executed
	Reason              string   `json:"reason"`                // Reason for cancellation
}

func (e *BatchExecutionCanceledEvent) GetEventType() events.EventType {
	return BatchExecutionCanceled
}

// NewBatchExecutionCanceledEvent creates a new BatchExecutionCanceledEvent
func NewBatchExecutionCanceledEvent(totalGroups, completedGroups int, canceledGroupName string, remainingGroupNames []string, reason string) *BatchExecutionCanceledEvent {
	return &BatchExecutionCanceledEvent{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		TotalGroups:         totalGroups,
		CompletedGroups:     completedGroups,
		CanceledGroupName:   canceledGroupName,
		RemainingGroupNames: remainingGroupNames,
		Reason:              reason,
	}
}

// ScriptedExecutionEvent is emitted when the controller runs python3 main.py in scripted mode.
type ScriptedExecutionEvent struct {
	events.BaseEventData
	StepID        string `json:"step_id"`
	StepIndex     int    `json:"step_index"`
	StepTitle     string `json:"step_title"`
	StepPath      string `json:"step_path"`
	WorkspacePath string `json:"workspace_path"`
	RunFolder     string `json:"run_folder"`
	ScriptPath    string `json:"script_path"`    // Absolute path to main.py that was executed
	ScriptContent string `json:"script_content"` // Contents of main.py (for UI display)
	Success       bool   `json:"success"`        // true if exit code 0 and validation passed
	ExitCode      int    `json:"exit_code"`
	Output        string `json:"output"`          // combined stdout
	Error         string `json:"error"`           // stderr / error message on failure
	FixIteration  int    `json:"fix_iteration"`   // 0 = first run, >0 = fix attempt number
	IsSavedScript bool   `json:"is_saved_script"` // true if running saved script from learnings, false if LLM-phase
}

func (e *ScriptedExecutionEvent) GetEventType() events.EventType {
	return ScriptedExecution
}
