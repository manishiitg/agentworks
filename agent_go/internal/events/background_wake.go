package events

import (
	"context"

	agentevents "github.com/manishiitg/mcpagent/events"
)

// BackgroundWakePresentation is display-only. The full internal prompt still
// reaches the agent and remains in its history; it is not a person's message.
type BackgroundWakePresentation struct {
	Kind   string
	Failed bool
	Output string
}
type backgroundWakeKey struct{}
type backgroundWakeContext struct {
	prompt       string
	presentation BackgroundWakePresentation
}

func WithBackgroundWakePresentation(ctx context.Context, prompt string, presentation BackgroundWakePresentation) context.Context {
	return context.WithValue(ctx, backgroundWakeKey{}, backgroundWakeContext{prompt, presentation})
}

// Only the exact input of this server-created turn is marked. Live person
// input and subsequent library control messages must keep their own display.
func backgroundWakeEvent(ctx context.Context, event *agentevents.AgentEvent) *agentevents.AgentEvent {
	wake, ok := ctx.Value(backgroundWakeKey{}).(backgroundWakeContext)
	if !ok || event == nil || event.Type != agentevents.UserMessage {
		return event
	}
	message, ok := event.Data.(*agentevents.UserMessageEvent)
	if !ok || message.Content != wake.prompt || message.Metadata["client_message_id"] != nil {
		return event
	}
	cloned := agentevents.CloneAgentEvent(event)
	if cloned == nil {
		return event
	}
	message = cloned.Data.(*agentevents.UserMessageEvent)
	if message.Metadata == nil {
		message.Metadata = map[string]interface{}{}
	}
	message.Metadata["system_notice_kind"] = wake.presentation.Kind
	message.Metadata["background_failed"] = wake.presentation.Failed
	message.Metadata["background_output"] = wake.presentation.Output
	return cloned
}
