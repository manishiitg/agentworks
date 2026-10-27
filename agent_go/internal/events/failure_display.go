package events

import (
	"errors"
	"strings"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// Stamp the server's existing provider display before journaling and publishing.
// Both transcript modes and restored history receive the same explanation.
// The event has already been cloned; never mutate a producer's metadata.
func stampFailureDisplay(event Event) {
	if event.Data == nil {
		return
	}
	var raw string
	var metadata *map[string]interface{}
	switch data := event.Data.Data.(type) {
	case *agentevents.UnifiedCompletionEvent:
		raw, metadata = data.Error, &data.Metadata
	case *agentevents.ConversationEndEvent:
		raw, metadata = data.Error, &data.Metadata
	case *agentevents.ConversationErrorEvent:
		// This reports unsent queued input, not a failed provider request.
		if data.Context == "queued_input_not_sent" {
			return
		}
		raw, metadata = data.Error, &data.Metadata
	case *agentevents.AgentErrorEvent:
		raw, metadata = data.Error, &data.Metadata
	case *GenericEventData:
		switch event.Type {
		case "unified_completion", "conversation_end", "conversation_error", "agent_error":
		default:
			return
		}
		raw, _ = data.Fields["error"].(string)
		if strings.TrimSpace(raw) == "" {
			return
		}
		meta, _ := data.Fields["metadata"].(map[string]interface{})
		if meta == nil {
			meta = map[string]interface{}{}
		}
		data.Fields["metadata"] = meta
		metadata = &meta
	default:
		return
	}
	if strings.TrimSpace(raw) == "" {
		return
	}
	display := llmerrors.ProviderDisplay(errors.New(raw))
	if *metadata == nil {
		*metadata = map[string]interface{}{}
	}
	meta := *metadata
	// A typed producer knows more than classification of its diagnostic string.
	// If they disagree, leave the producer's classification for the UI to use.
	if code, _ := meta["error_code"].(string); code != "" && code != display.Code {
		return
	}
	for key, value := range map[string]interface{}{
		"error_code": display.Code, "provider_message": display.Message,
		"next_step": display.NextStep, "retryable": display.Retryable,
	} {
		if _, exists := meta[key]; !exists {
			meta[key] = value
		}
	}
}
