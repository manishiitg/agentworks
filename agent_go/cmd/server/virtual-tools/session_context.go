package virtualtools

import (
	"context"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Context keys the server sets on a chat session's tool calls.
type sessionContextKey string

const (
	// SessionEventEmitterKey is the context key for the session event emitter (used for human feedback UI)
	SessionEventEmitterKey sessionContextKey = "session_event_emitter"
	// BotNotificationDestinationKey carries the originating bot thread/channel
	// so human tools can notify the same connector conversation.
	BotNotificationDestinationKey sessionContextKey = "bot_notification_destination"
	// ChatsFolderPath is the fallback per-user Chats folder when session context is unavailable.
	// Always prefer GetChatsFolder(ctx) which reads the session-scoped per-user path.
	ChatsFolderPath = workspaceref.UsersDir + "/default/Chats"
	// ChatsFolderKey is the context key for the session-scoped Chats folder path (usually _users/<userID>/Chats).
	// Set by the server at session setup and propagated to every sub-agent via context.
	ChatsFolderKey sessionContextKey = "chats_folder"
	// BGAgentSessionIDKey is the context key for the session ID used by background agents
	BGAgentSessionIDKey sessionContextKey = "bg_agent_session_id"
)

// SessionEventEmitter is the interface for emitting human-feedback events to the session event store
type SessionEventEmitter interface {
	EmitBlockingHumanFeedback(requestID, question, context string, yesNoOnly bool, yesLabel, noLabel string, options ...string)
	// EmitProductInteraction publishes the same product_interaction event a
	// product.yaml tool binding emits vian agentprofiles.ToolRuntimeContext.Emit
	// (pkg/orchestrator/events.ProductInteractionEvent) — the generic surface
	// a session's own UI already reads (frontend/shared/session/interactions.ts).
	// A platform-wide human tool has no product.yaml binding of its own to
	// carry that Emit callback, so it reaches the same event bus through this
	// context-scoped emitter instead: the identical shape either way,
	// independent of which coding CLI is driving the turn or how it narrates
	// its own tool use in the transcript.
	EmitProductInteraction(kind string, payload map[string]interface{})
}

// HumanFeedbackResolutionEmitter is optionally implemented by a
// SessionEventEmitter. It records that a blocking request was answered or
// expired, so a refreshed chat does not resurrect the prompt as pending.
type HumanFeedbackResolutionEmitter interface {
	EmitHumanFeedbackResolved(requestID, outcome string)
}

// GetChatsFolder returns the workspace-relative Chats folder for the current session.
// Reads ChatsFolderKey from context first (per-user path set at session setup) and falls
// back to the global ChatsFolderPath constant if no session-scoped value is present.
func GetChatsFolder(ctx context.Context) string {
	if folder, ok := ctx.Value(ChatsFolderKey).(string); ok && folder != "" {
		return folder
	}
	return ChatsFolderPath
}

// TierModel represents a specific provider+model for a tier
type TierModel struct {
	ConnectionID string                 `json:"connection_id,omitempty"`
	Provider     string                 `json:"provider"`
	ModelID      string                 `json:"model_id"`
	Options      map[string]interface{} `json:"options,omitempty"`
}
