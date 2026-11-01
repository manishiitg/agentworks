package server

import (
	"log"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// recordMuseWakeReply adds a reply Muse wrote between the chat's turns (a Monitor fired, a background task
// finished) to the chat as an assistant message, saved like any reply. The first line names what woke Muse.
func (api *StreamingAPI) recordMuseWakeReply(reply musecli.MuseMSPReply) {
	sessionID := strings.TrimSpace(reply.Owner)
	text := strings.TrimSpace(reply.Text)
	if api == nil || api.eventStore == nil || sessionID == "" || text == "" {
		return
	}
	header := "**Muse, on its own**"
	if sources := museWakeSources(reply.Sources); sources != "" {
		header = "**Monitor: " + sources + "**"
	}
	now := time.Now()
	completion := agentevents.NewUnifiedCompletionEvent("simple", "simple", "", header+"\n\n"+text, "completed", 0, 1)
	completion.Metadata = map[string]interface{}{"source": "muse_wake", "native_session_id": reply.NativeSessionID, "turn_id": "muse-wake-" + reply.TurnID}
	wrapped := agentevents.NewAgentEvent(completion)
	wrapped.SessionID = sessionID
	wrapped.TurnID = "muse-wake-" + reply.TurnID
	event := storeevents.Event{ID: "muse-wake-" + reply.TurnID, Type: string(agentevents.EventTypeUnifiedCompletion), Timestamp: now, SessionID: sessionID, Data: wrapped}
	err := api.eventStore.AddEventChecked(sessionID, event)
	saved := err == nil && api.persistRetainedStructuredCompletion(sessionID, event)
	log.Printf("[MUSE_WAKE] session=%s native=%s turn=%s sources=%q chars=%d shown=%t saved=%t err=%v", sessionID, reply.NativeSessionID, reply.TurnID, museWakeSources(reply.Sources), len(text), err == nil, saved, err)
}

// museWakeSources joins the Monitors' own descriptions, cut to one short line (model-written, shown as text).
func museWakeSources(sources []string) string {
	var parts []string
	for _, source := range sources {
		if source = strings.Join(strings.Fields(source), " "); source != "" {
			parts = append(parts, source)
		}
	}
	joined := strings.Join(parts, ", ")
	if len([]rune(joined)) > 120 {
		joined = string([]rune(joined)[:117]) + "..."
	}
	return strings.ReplaceAll(joined, "*", "")
}
