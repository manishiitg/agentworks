package events

import (
	"github.com/manishiitg/mcpagent/events"
)

// AgentSessionIDKey is a context key for injecting agent session ID into event context.
// When set, the ContextAwareEventBridge tags events with this correlation ID,
// enabling the frontend to group tool calls under their parent orchestrator_agent_start.
type agentSessionIDKeyType struct{}

var AgentSessionIDKey = agentSessionIDKeyType{}

// IsSubAgentContextKey marks a context as belonging to a sub-agent (not the root agent).
// Only sub-agent events should have their correlation_id overridden for grouping.
type isSubAgentContextKeyType struct{}

var IsSubAgentContextKey = isSubAgentContextKeyType{}

// ForceCorrelationIDKey overrides the correlation ID used by ContextAwareEventBridge.
// Unlike AgentSessionIDKey, child agents do NOT overwrite this key when creating their
// own context, so it propagates through the entire call chain unchanged.
// Use this when you need all events from a subtree (including nested child agents)
// to share a single correlation ID — e.g., workshop step execution grouping.
type forceCorrelationIDKeyType struct{}

var ForceCorrelationIDKey = forceCorrelationIDKeyType{}

// ParentExecutionIDKey carries the backend execution node that should own
// events emitted from this context. It is separate from correlation_id:
// correlation groups a visible agent card, while parent_execution_id builds
// the semantic execution tree (for example background step -> workflow step).
type parentExecutionIDKeyType struct{}

var ParentExecutionIDKey = parentExecutionIDKeyType{}

// AgentItemContextKey marks an execution-only agent as an internal
// message_sequence item run rather than a user-visible sub-agent.
type agentSequenceItemContextKeyType struct{}

var AgentItemContextKey = agentSequenceItemContextKeyType{}

type AgentItemContext struct {
	StepID   string
	ItemID   string
	ItemType string
}

// Orchestrator Event Types
// These events are specific to the orchestrator application and are not part of the core mcpagent library
const (
	// Orchestrator events (only End is emitted; Start/Error never were)
	OrchestratorEnd events.EventType = "orchestrator_end"

	// Orchestrator Agent lifecycle events
	OrchestratorAgentStart events.EventType = "orchestrator_agent_start"
	OrchestratorAgentEnd   events.EventType = "orchestrator_agent_end"
	OrchestratorAgentError events.EventType = "orchestrator_agent_error"

	// Background agent lifecycle events (sub-agents, todo-task steps,
	// agent items — anything dispatched and notified about
	// asynchronously). BackgroundAgentFailed is intentionally NOT defined:
	// failure is reported via BackgroundAgentCompleted with status "failed",
	// not a distinct wire event type.
	BackgroundAgentStarted    events.EventType = "background_agent_started"
	BackgroundAgentCompleted  events.EventType = "background_agent_completed"
	BackgroundAgentTerminated events.EventType = "background_agent_terminated"
	SyntheticTurnReady        events.EventType = "synthetic_turn_ready"
	AutoNotificationSteered   events.EventType = "auto_notification_steered"

	// PresentationUpdated announces a product tool showing or re-showing
	// something in ui_presentations. See PresentationUpdatedEvent.
	PresentationUpdated events.EventType = "presentation_updated"
	// ProductInteraction is a product tool talking to its own surface in
	// the course of a turn -- suggested follow-ups, a celebration, an inline
	// scene -- without a durable presentation row. See ProductInteractionEvent.
	ProductInteraction events.EventType = "product_interaction"

	// Todo planning events
	VariablesExtracted events.EventType = "variables_extracted"

	// Batch execution events: only cancellation is emitted (user-visible
	// terminal signal); the start/end/group wrappers were never produced.
	BatchExecutionCanceled events.EventType = "batch_execution_canceled"

	// Human Verification events
	RequestHumanFeedback  events.EventType = "request_human_feedback"
	BlockingHumanFeedback events.EventType = "blocking_human_feedback"
	PlanApproval          events.EventType = "plan_approval"
	// Durable answer/expiry marker for a blocking_human_feedback request.
	HumanFeedbackResolved events.EventType = "human_feedback_resolved"

	// Step token usage event
	StepTokenUsage events.EventType = "step_token_usage"

	// Routing step evaluation events
	RoutingEvaluated events.EventType = "routing_evaluated"

	// Pre-validation events
	PreValidationCompleted events.EventType = "pre_validation_completed"

	// Scripted-mode events (legacy wire name "learn_code_script_execution"
	// kept as the EventType value for back-compat with frontend code paths
	// that still match on the old discriminator string. The next release
	// cycle will flip this value to "scripted_execution" and migrate the
	// frontend in lockstep.)
	ScriptedExecution events.EventType = "learn_code_script_execution" // When controller runs python3 main.py

	// Todo task orchestration events
	OrchestratorRouteSelected events.EventType = "todo_task_route_selected" // When orchestrator selects a route/sub-agent
	OrchestratorStepCompleted events.EventType = "todo_task_step_completed" // When the entire todo task step is completed

)

// Helper function to get component from orchestrator event type
func GetComponentFromEventType(eventType events.EventType) string {
	switch eventType {
	case OrchestratorEnd,
		OrchestratorAgentStart, OrchestratorAgentEnd, OrchestratorAgentError,
		VariablesExtracted,
		StepTokenUsage,
		BatchExecutionCanceled,
		RequestHumanFeedback, BlockingHumanFeedback, PlanApproval, HumanFeedbackResolved,
		RoutingEvaluated, PreValidationCompleted,
		OrchestratorRouteSelected, OrchestratorStepCompleted:
		return "orchestrator"
	default:
		return "system"
	}
}
