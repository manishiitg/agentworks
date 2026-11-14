package server

import (
	"context"
	"log"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Display-only, ephemeral control data on the existing authorized session
// stream. Never a user message, a send/queue action or durable model history.
func (api *StreamingAPI) withNextPrompt(ctx context.Context, session, provider, query string, attended bool) context.Context {
	if provider != "claude-code" && provider != "codex-cli" {
		log.Printf("[NEXT_PROMPT] session=%s provider=%s skipped reason=unsupported_program", session, provider)
		return ctx
	}
	return llmtypes.WithNextPrompt(ctx, llmtypes.NextPromptScope{Owner: session, Provider: provider, TurnID: query, Eligible: attended, StillCurrent: func(turn string) bool { return api.nextPromptBoundary(session, turn) != "" }, Complete: func(result llmtypes.NextPromptResult) {
		reason := result.Reason
		text := strings.TrimSpace(result.Text)
		boundary := ""
		if text != "" {
			boundary = api.nextPromptBoundary(session, result.HostTurn)
		}
		if text == "" || boundary == "" {
			if text != "" {
				reason = "newer_input_or_stopped"
			}
			if reason == "" {
				reason = "empty"
			}
			log.Printf("[NEXT_PROMPT] session=%s provider=%s skipped reason=%s", session, provider, reason)
			return
		}
		data := agentevents.NewAgentEvent(storeevents.NewGenericEventData("next_prompt", map[string]any{"text": text, "for_message_id": boundary, "turn_id": result.HostTurn, "provider": provider}))
		data.SessionID = session
		data.TurnID = result.HostTurn
		api.eventStore.AddEvent(session, storeevents.Event{ID: "next-prompt:" + result.HostTurn, Type: "next_prompt", SessionID: session, Timestamp: time.Now(), ExecutionKind: "main_agent", Data: data})
		// shown means offered on the chat stream; the browser still hides it when
		// the composer is busy or has a draft. Never log the prediction text.
		log.Printf("[NEXT_PROMPT] session=%s provider=%s shown", session, provider)
	}})
}

func (api *StreamingAPI) nextPromptBoundary(session, turn string) string {
	if api.eventStore == nil || api.isSessionMarkedStopped(session) {
		return ""
	}
	rows := api.eventStore.GetAllEventsRaw(session)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Type != "user_message" {
			continue
		}
		switch row.ExecutionKind {
		case "", "main", "main_agent", "chat":
		default:
			continue
		}
		if row.Data != nil && row.Data.TurnID == turn {
			return row.ID
		}
		break
	}
	return ""
}
