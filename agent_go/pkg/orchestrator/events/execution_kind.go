package events

import "strings"

// ExecutionKind names what a unit of work IS, so that every consumer can stop
// inferring it from ID string prefixes, metadata sniffing, or content shape.
//
// Two questions answer almost every presentation decision, and both are
// properties of the kind alone — not of the ID format, not of the transport,
// not of what the content happens to look like:
//
//	OwnsTerminal()     does this have its own LLM conversation a user would watch?
//	FoldsIntoParent()  is this an internal turn of its parent, not a peer of it?
//
//	kind                    OwnsTerminal  FoldsIntoParent  notes
//	full_run                     no             no         container only; not an agent
//	orchestrator                 yes            no         todo_task; dispatches sub-agents
//	sub_agent                    yes            no         delegated LLM agent
//	message_sequence             yes            no         one terminal for the whole step
//	agent_item        no             yes        internal turn of its step
//	scripted_step                yes            no         python main.py — a real output pane
//	router                       no             no         a decision record; emits no pane
//	main_agent                   yes            no         the user's own chat agent
//
// IMPORTANT — Unknown is not "does not own a terminal". Most events in flight
// today declare no kind at all, so consumers must gate on
// `kind != ExecutionKindUnknown && !kind.OwnsTerminal()` and otherwise fall
// through to their existing behavior. Treating an undeclared kind as
// "suppress" would silently delete almost every terminal.
//
// Historically these facts were re-derived independently in the terminal store,
// the rail, and the event dispatcher, each by pattern-matching ID prefixes
// ("msgseq-", "exec-", "workflow-full-"). Every divergence between those copies
// was a bug: agent items fragmenting into empty terminals, "Full run"
// appearing in the rail as if it were an agent, scripted steps synthesizing fake
// conversation panes. Declaring the kind once at creation removes the whole class.
type ExecutionKind string

const (
	// ExecutionKindUnknown is the zero value: kind was not declared. Consumers
	// must fall back to legacy inference rather than assume anything.
	ExecutionKindUnknown ExecutionKind = ""

	// ExecutionKindMainAgent is the user's own chat agent for a session.
	ExecutionKindMainAgent ExecutionKind = "main_agent"

	// ExecutionKindFullRun is a whole workflow run (scheduled, Pulse, or manual).
	// It is a CONTAINER, not an agent: it has no conversation of its own, and
	// must never occupy a rail slot beside the real agents it contains.
	ExecutionKindFullRun ExecutionKind = "full_run"

	// ExecutionKindSubAgent is a delegated LLM agent (call_sub_agent,
	// call_generic_agent, background delegation).
	ExecutionKindSubAgent ExecutionKind = "sub_agent"

	// ExecutionKindAgent is an agent step — one multi-turn
	// conversation. The STEP owns exactly one terminal; its items do not.
	ExecutionKindAgent ExecutionKind = "agent"

	// ExecutionKindAgentItem is a single turn inside a
	// agent step (including the automatic final-validation pass).
	// It is part of its parent's conversation, never a peer terminal.
	ExecutionKindAgentItem ExecutionKind = "agent_item"

	// ExecutionKindScriptedStep is a deterministic python main.py step. It
	// produces OUTPUT, not a conversation, so it belongs inline in its parent
	// rather than in a pane that imitates an agent.
	ExecutionKindScriptedStep ExecutionKind = "scripted_step"

	// ExecutionKindRouter is a routing-step evaluation: a decision record, not
	// a conversation.
	ExecutionKindRouter ExecutionKind = "router"

	// ExecutionKindWorkflowStep is a generic workflow step that is not one of
	// the more specific kinds above. It owns a terminal.
	ExecutionKindWorkflowStep ExecutionKind = "workflow_step"
)

// OwnsTerminal reports whether this kind has its own LLM conversation, and so
// deserves its own entry in the terminal rail.
//
// Kinds that return false are not "unimportant" — a scripted step's output and a
// router's decision both matter. They are simply not conversations, so they are
// rendered in their parent's transcript instead of competing with it for a slot.
func (k ExecutionKind) OwnsTerminal() bool {
	switch k {
	case ExecutionKindMainAgent,
		ExecutionKindSubAgent,
		ExecutionKindAgent,
		ExecutionKindWorkflowStep:
		return true
	default:
		// full_run, agent_item, scripted_step, router, unknown
		return false
	}
}

// FoldsIntoParent reports whether this kind's events belong to its parent's
// transcript rather than to a terminal of its own.
//
// Deliberately NOT the inverse of OwnsTerminal: a full_run neither owns a
// terminal nor folds into a parent — it is a pure container whose children own
// terminals. Collapsing a full run's events into its (nonexistent) parent would
// dump an entire workflow's output into the main agent chat.
func (k ExecutionKind) FoldsIntoParent() bool {
	switch k {
	case ExecutionKindAgentItem,
		ExecutionKindScriptedStep,
		ExecutionKindRouter:
		return true
	default:
		return false
	}
}

// IsContainer reports whether this kind groups other executions beneath it.
func (k ExecutionKind) IsContainer() bool {
	switch k {
	case ExecutionKindFullRun, ExecutionKindAgent:
		return true
	default:
		return false
	}
}

// ParseExecutionKind maps a wire/metadata string onto a kind, tolerating the
// legacy aliases that predate this type. Unrecognized values return
// ExecutionKindUnknown so callers fall back to legacy inference instead of
// silently mis-classifying work.
func ParseExecutionKind(value string) ExecutionKind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "main_agent", "main", "chat":
		return ExecutionKindMainAgent
	case "full_run", "workflow_full", "workflow_run":
		return ExecutionKindFullRun
		// Sub-agent aliases are numerous because five call sites each invented
	// their own: controller_todo_task declares workflow_sub_agent /
	// workflow_generic_agent, interactive_workshop_manager declares
	// generic_agent / pulse_reviewer, the delegate tool declares delegation,
	// and cmd/server defaults anything unlabelled to workshop_background.
	case "sub_agent", "subagent", "delegation", "background_agent", "background",
		"workflow_sub_agent", "workflow_generic_agent", "generic_agent",
		"pulse_reviewer", "workshop_background":
		return ExecutionKindSubAgent
	case "agent", "message_sequence", "message-sequence", "orchestrator", "todo_task":
		return ExecutionKindAgent
	case "agent_item", "agent-item", "message_sequence_item", "message-sequence-item":
		return ExecutionKindAgentItem
	case "scripted_step", "scripted", "learn_code":
		return ExecutionKindScriptedStep
	case "router", "routing", "routing_step":
		return ExecutionKindRouter
	case "workflow_step", "workflow-step", "step", "execution_only":
		return ExecutionKindWorkflowStep
	default:
		return ExecutionKindUnknown
	}
}

// String returns the canonical wire value.
func (k ExecutionKind) String() string { return string(k) }
