package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/mcpagent/events"
)

type steeringReplyConnector struct {
	testBotConnector
	replies chan string
}

func (c *steeringReplyConnector) SendThreadMessage(_ context.Context, _ ThreadID, text string) (string, error) {
	c.replies <- text
	return "reply", nil
}

func TestBotSteeringDeliveryFailureDoesNotBlockRetryP0(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	connector := &steeringReplyConnector{replies: make(chan string, 1)}
	manager.RegisterConnector(connector)
	inputs := make(chan string, 2)
	manager.SetFollowUpFunc(func(_ context.Context, req map[string]interface{}, _, _ string) error {
		text := req["query"].(string)
		inputs <- text
		if text == "first steer" {
			return errors.New("live input unavailable")
		}
		return nil
	})
	active := &activeBotSession{SessionID: "session-1", UserID: "user-1", Platform: "whatsapp",
		ThreadID: ThreadID{Platform: "whatsapp", ChannelID: "dm", ThreadTS: "dm"},
		Status:   chathistory.BotSessionStatusRunning, LastActivity: time.Now()}
	send := func(text string) {
		manager.handleExistingSession(active, BotIncomingMessage{Platform: "whatsapp", ChannelID: "dm", Text: text}, false)
		select {
		case got := <-inputs:
			if got != text {
				t.Fatalf("input=%q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("message was blocked")
		}
	}
	send("first steer")
	select {
	case reply := <-connector.replies:
		if !strings.Contains(reply, "couldn't deliver your message") || !strings.Contains(reply, "execution logs") {
			t.Fatalf("missing delivery failure guidance: %q", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("delivery failed silently")
	}
	send("try again")
}

func TestBotTerminalErrorReleasesBusyAndBlockingStateP0(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	connector := &testBotConnector{}
	filter := NewBotEventFilter(connector, ThreadID{Platform: "whatsapp"}, "session-1", "", "user-1")
	active := &activeBotSession{Status: chathistory.BotSessionStatusRunning, eventFilter: filter,
		awaitingUserInput: true, blockingEventType: "blocking_human_feedback", blockingRequestID: "old-prompt"}
	filter.awaitingInput = true
	filter.pendingDelegations = 1
	done := 0
	filter.SetSessionDoneCallback(func() { done++; manager.markBotBuilderDone(active) })
	failure := BotEventData{Type: "unified_completion", Data: &events.AgentEvent{HierarchyLevel: 0,
		Data: &events.UnifiedCompletionEvent{Status: "error"}}}
	filter.processEvent(context.Background(), failure)
	filter.processEvent(context.Background(), failure)
	if done != 1 || !active.builderDone || active.Status != chathistory.BotSessionStatusFailed {
		t.Fatalf("failed turn not released once: done=%d state=%+v", done, active)
	}
	if active.awaitingUserInput || active.blockingEventType != "" || active.blockingRequestID != "" {
		t.Fatal("failed turn left a stale input prompt")
	}
	filter.ResetForNewTurn()
	if filter.SessionFailed() {
		t.Fatal("failure leaked into next turn")
	}
}

func TestBotNestedErrorDoesNotFinishParentP0(t *testing.T) {
	filter := NewBotEventFilter(nil, ThreadID{Platform: "whatsapp"}, "session-1", "", "user-1")
	filter.baseHierarchySet, filter.baseHierarchy = true, 0
	done := 0
	filter.SetSessionDoneCallback(func() { done++ })
	filter.processEvent(context.Background(), BotEventData{Type: "unified_completion", Data: &events.AgentEvent{HierarchyLevel: 1, Data: &events.UnifiedCompletionEvent{Status: "error"}}})
	if done != 0 || filter.SessionFailed() {
		t.Fatal("nested failure finished the main builder")
	}
}

func TestBotErrorAndTerminalCompletionNotifyOnceP0(t *testing.T) {
	for _, errorFirst := range []bool{true, false} {
		connector := &testBotConnector{}
		filter := NewBotEventFilter(connector, ThreadID{Platform: "whatsapp"}, "session-1", "", "user-1")
		done := 0
		filter.SetSessionDoneCallback(func() {
			if len(connector.sent) != 1 {
				t.Error("completion fired before reporting failure")
			}
			done++
		})
		errorEvent := BotEventData{Type: "conversation_error", Data: &events.AgentEvent{Data: &events.ConversationErrorEvent{Error: "Muse returned no assistant text"}}}
		terminalEvent := BotEventData{Type: "unified_completion", Data: &events.AgentEvent{Data: &events.UnifiedCompletionEvent{Status: "error", Error: "Muse returned no assistant text"}}}
		sequence := []BotEventData{terminalEvent, errorEvent, terminalEvent}
		if errorFirst {
			sequence = []BotEventData{errorEvent, terminalEvent, terminalEvent}
		}
		for _, event := range sequence {
			filter.processEvent(context.Background(), event)
		}
		if len(connector.sent) != 1 || done != 1 {
			t.Fatalf("errorFirst=%v messages=%v done=%d", errorFirst, connector.sent, done)
		}
	}
}

// A restored session's relaunch replays terminal output before the turn's
// user message, at a different hierarchy level than the turn itself. That
// preamble must not calibrate "main": the turn's reply was skipped as a
// sub-agent on server A 2026-09-25 (preamble level 0, turn level 3).
func TestBotPreambleEventsDoNotCalibrateMainLevel(t *testing.T) {
	connector := &testBotConnector{}
	filter := NewBotEventFilter(connector, ThreadID{Platform: "slack"}, "session-1", "", "user-1")
	ctx := context.Background()
	filter.processEvent(ctx, BotEventData{Type: "status_line", Data: &events.AgentEvent{HierarchyLevel: 0}})
	if !filter.isMainLevel(BotEventData{Data: &events.AgentEvent{HierarchyLevel: 0}}) {
		t.Fatal("preamble did not calibrate provisionally")
	}
	filter.processEvent(ctx, BotEventData{Type: "user_message", Data: &events.AgentEvent{HierarchyLevel: 3}})
	filter.processEvent(ctx, BotEventData{Type: "unified_completion", Data: &events.AgentEvent{HierarchyLevel: 3, Data: &events.UnifiedCompletionEvent{FinalResult: "Rechecking via the Debug route; I'll follow up here."}}})
	if len(connector.sent) != 1 || !strings.Contains(connector.sent[0], "Rechecking via the Debug route") {
		t.Fatalf("turn reply not sent after a preamble calibration: %q", connector.sent)
	}
}

// The "Working on…" placeholder is removed when the reply lands, and no new
// one appears afterwards for background work the reply already announced.
func TestBotReplyClearsPlaceholderAndStopsHeartbeat(t *testing.T) {
	connector := &testBotConnector{}
	filter := NewBotEventFilter(connector, ThreadID{Platform: "slack"}, "session-1", "", "user-1")
	filter.baseHierarchySet, filter.baseHierarchy = true, 0
	ctx := context.Background()
	filter.sendProgressMessage(ctx, "_Working on the request…_")
	if filter.progressMessageID == "" {
		t.Skip("test connector does not support progress messages")
	}
	filter.sendMainText(ctx, "Debug recheck is running; I'll update this thread.")
	if filter.progressMessageID != "" {
		t.Fatal("placeholder left in the thread after the reply")
	}
	if !filter.HasSentMainText() {
		t.Fatal("reply not recorded")
	}
}

// Thinking (Cursor reasoning, Pi thinking blocks) is never forwarded to a bot
// thread; only the reply text is.
func TestBotNeverForwardsThinking(t *testing.T) {
	connector := &testBotConnector{}
	filter := NewBotEventFilter(connector, ThreadID{Platform: "slack"}, "session-1", "", "user-1")
	filter.baseHierarchySet, filter.baseHierarchy = true, 0
	thinking := &events.ConversationThinkingEvent{Thinking: "Identifying session clues."}
	thinking.Metadata = map[string]interface{}{"presentation": "assistant_update"}
	if filter.processEvent(context.Background(), BotEventData{Type: "conversation_thinking", Data: &events.AgentEvent{HierarchyLevel: 0, Data: thinking}}) || len(connector.sent) != 0 {
		t.Fatalf("thinking reached the bot thread: %q", connector.sent)
	}
}
