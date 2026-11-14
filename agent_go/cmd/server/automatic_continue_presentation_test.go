package server

import (
	"context"
	"testing"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-995: both the waiting echo and actual synthetic input are compact;
// a person's pasted restart prompt remains an ordinary user message.
func TestAutomaticContinuePresentationKeepsPromptAndMarksOnlyServerTurns(t *testing.T) {
	for _, tc := range []struct {
		trigger string
		auto    bool
		kind    string
	}{
		{"restart_continue", true, "app_restarted"}, {usageLimitContinueTrigger, true, "usage_limit_reset"},
		{"restart_continue", false, ""}, {"manual", true, ""},
	} {
		store := storeevents.NewEventStore(20)
		t.Cleanup(store.Stop)
		api := &StreamingAPI{eventStore: store}
		req := QueryRequest{Query: "full unchanged internal prompt", TriggeredBy: tc.trigger, IsAutoNotification: tc.auto}
		turn := queuedConversationTurn{ID: "turn", SessionID: "chat", UserID: "owner", Request: req}
		api.recordQueuedConversationUserMessage("chat", turn)
		observer := storeevents.NewEventObserver(store, "chat")
		message := agentevents.NewUserMessageEvent(1, req.Query, "user")
		ctx := automaticContinuePresentation(context.Background(), req, req.Query)
		if err := observer.HandleEvent(ctx, agentevents.NewAgentEvent(message)); err != nil {
			t.Fatal(err)
		}
		rows := store.GetAllEventsRaw("chat")
		if len(rows) != 2 {
			t.Fatalf("queued/live inputs missing: %d", len(rows))
		}
		for _, row := range rows {
			raw, ok := row.Data.Data.(*agentevents.UserMessageEvent)
			if !ok {
				t.Fatalf("unexpected user payload: %T", row.Data.Data)
			}
			kind, _ := raw.Metadata["system_notice_kind"].(string)
			if kind != tc.kind || raw.Content != req.Query {
				t.Fatalf("trigger=%s auto=%v kind=%q content changed=%v", tc.trigger, tc.auto, kind, raw.Content != req.Query)
			}
		}
		if message.Metadata["system_notice_kind"] != nil {
			t.Fatal("changed agent history instead of display clone")
		}
	}
}
