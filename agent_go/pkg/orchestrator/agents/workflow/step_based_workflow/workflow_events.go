package step_based_workflow

import (
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	baseevents "github.com/manishiitg/mcpagent/events"
)

// RoutingResponseEvent represents the routing response data for events
type RoutingResponseEvent struct {
	SelectedRouteID string `json:"selected_route_id"` // The selected route ID
	Reasoning       string `json:"reasoning"`         // Reasoning for the selection
}

// RoutingRouteEvent represents a single route in a routing evaluated event
type RoutingRouteEvent struct {
	RouteID      string `json:"route_id"`
	RouteName    string `json:"route_name"`
	Condition    string `json:"condition"`
	NextStepID   string `json:"next_step_id"`
	NextStepType string `json:"next_step_type,omitempty"`
}

// RoutingEvaluatedEvent represents the event when a routing step is evaluated
type RoutingEvaluatedEvent struct {
	baseevents.BaseEventData
	StepID          string               `json:"step_id"`
	StepIndex       int                  `json:"step_index"`
	StepTitle       string               `json:"step_title"`
	StepPath        string               `json:"step_path"`
	RoutingQuestion string               `json:"routing_question"`
	RoutingResponse RoutingResponseEvent `json:"routing_response"`
	Routes          []RoutingRouteEvent  `json:"routes"`
	RunFolder       string               `json:"run_folder"`
	WorkspacePath   string               `json:"workspace_path"`
}

// GetEventType implements baseevents.EventData interface
func (e *RoutingEvaluatedEvent) GetEventType() baseevents.EventType {
	return events.RoutingEvaluated
}

// StepTokenUsageEvent represents token usage summary for a workflow step
// Note: This is a local type, but uses orchestrator events.StepTokenUsageEvent from the orchestrator/events package
type StepTokenUsageEvent struct {
	baseevents.BaseEventData
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

func (e *StepTokenUsageEvent) GetEventType() baseevents.EventType {
	return events.StepTokenUsage
}

// NewStepTokenUsageEvent creates a new StepTokenUsageEvent
func NewStepTokenUsageEvent(phase string, step int, stepTitle string, promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount int) *StepTokenUsageEvent {
	return &StepTokenUsageEvent{
		BaseEventData: baseevents.BaseEventData{
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

// PreValidationCompletedEvent represents the event when pre-validation completes
type PreValidationCompletedEvent struct {
	baseevents.BaseEventData
	StepID            string                    `json:"step_id"`             // Step ID from plan
	StepIndex         int                       `json:"step_index"`          // 0-based step index
	StepTitle         string                    `json:"step_title"`          // Step title
	StepPath          string                    `json:"step_path"`           // Step path (e.g., "step-1" or "step-2-sub-login")
	IsNestedExecution bool                      `json:"is_nested_execution"` // Whether this belongs to a nested route/sub-agent
	OverallPass       bool                      `json:"overall_pass"`        // Whether pre-validation passed
	TotalChecks       int                       `json:"total_checks"`        // Total number of checks performed
	PassedChecks      int                       `json:"passed_checks"`       // Number of checks that passed
	FailedChecks      int                       `json:"failed_checks"`       // Number of checks that failed
	FilesChecked      []FileCheckResultForEvent `json:"files_checked"`       // Results for each file checked
	Errors            []ValidationErrorForEvent `json:"errors,omitempty"`    // Validation errors if any
	RunFolder         string                    `json:"run_folder"`          // Run folder name (e.g., "iteration-1")
	WorkspacePath     string                    `json:"workspace_path"`      // Workspace path for file operations
}

// FileCheckResultForEvent is a simplified version of FileCheckResult for events
type FileCheckResultForEvent struct {
	FileName   string                    `json:"file_name"`   // Name of the file checked
	Exists     bool                      `json:"exists"`      // Whether file exists
	IsJSON     bool                      `json:"is_json"`     // Whether file is valid JSON
	JSONChecks []JSONCheckResultForEvent `json:"json_checks"` // Results of JSON validation checks
}

// JSONCheckResultForEvent is a simplified version of JSONCheckResult for events
type JSONCheckResultForEvent struct {
	Path      string `json:"path"`                // JSONPath expression
	Passed    bool   `json:"passed"`              // Whether check passed
	CheckType string `json:"check_type"`          // Type of check (must_exist, value_type, etc.)
	ErrorMsg  string `json:"error_msg,omitempty"` // Error message if check failed
}

// ValidationErrorForEvent is a simplified version of ValidationError for events
type ValidationErrorForEvent struct {
	File      string `json:"file"`       // File where error occurred
	Path      string `json:"path"`       // JSONPath where error occurred
	CheckType string `json:"check_type"` // Type of check that failed
	Expected  string `json:"expected"`   // Expected value
	Actual    string `json:"actual"`     // Actual value
	Message   string `json:"message"`    // Error message
}

func (e *PreValidationCompletedEvent) GetEventType() baseevents.EventType {
	return events.PreValidationCompleted
}

// AgentRouteSelectedEvent represents when the todo task orchestrator selects a route/sub-agent
type AgentRouteSelectedEvent struct {
	baseevents.BaseEventData
	StepIndex              int    `json:"step_index"`
	StepPath               string `json:"step_path"`
	StepID                 string `json:"step_id"`
	StepTitle              string `json:"step_title"`
	Iteration              int    `json:"iteration"`
	NextAction             string `json:"next_action"`                         // "delegate", "complete", "continue"
	SelectedRouteID        string `json:"selected_route_id,omitempty"`         // Route ID if predefined agent selected
	SelectedRouteName      string `json:"selected_route_name,omitempty"`       // Route name if predefined agent selected
	UseGenericAgent        bool   `json:"use_generic_agent"`                   // True if generic agent selected
	TodoIDToExecute        string `json:"todo_id_to_execute,omitempty"`        // Todo item being worked on
	TodoTitle              string `json:"todo_title,omitempty"`                // Title of the todo item
	InstructionsToSubAgent string `json:"instructions_to_sub_agent,omitempty"` // Instructions given to sub-agent
	SelectionReasoning     string `json:"selection_reasoning,omitempty"`       // Why this route was selected
	AllTasksComplete       bool   `json:"all_tasks_complete"`                  // Whether all tasks are complete
	ProgressSummary        string `json:"progress_summary,omitempty"`          // Summary of progress
	Model                  string `json:"model,omitempty"`                     // LLM model used for this decision
	PreferredTier          int    `json:"preferred_tier,omitempty"`            // LLM tier chosen (1=High, 2=Medium, 3=Low)
	PreferredTierLabel     string `json:"preferred_tier_label,omitempty"`      // Human-readable tier label
}

func (e *AgentRouteSelectedEvent) GetEventType() baseevents.EventType {
	return events.AgentRouteSelected
}

// AgentStepCompletedEvent represents when the entire todo task step is completed
type AgentStepCompletedEvent struct {
	baseevents.BaseEventData
	StepIndex        int    `json:"step_index"`
	StepPath         string `json:"step_path"`
	StepID           string `json:"step_id"`
	StepTitle        string `json:"step_title"`
	TotalIterations  int    `json:"total_iterations"`
	TotalTodosCount  int    `json:"total_todos_count"`
	CompletedCount   int    `json:"completed_count"`
	CompletionReason string `json:"completion_reason,omitempty"`
	NextStepID       string `json:"next_step_id,omitempty"`
}

func (e *AgentStepCompletedEvent) GetEventType() baseevents.EventType {
	return events.AgentStepCompleted
}
