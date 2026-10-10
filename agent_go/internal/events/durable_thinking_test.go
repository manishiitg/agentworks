package events

import (
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
)

// A finished thinking block survives a chat rebuild; a streamed fragment and an empty block do not (PLAT-832).
func TestOnlyFinishedThinkingIsDurable(t *testing.T) {
	thinking := func(text string, delta bool) Event {
		return Event{ID: "t", Type: "conversation_thinking", Timestamp: time.Now(),
			Data: agentevents.NewAgentEvent(&agentevents.ConversationThinkingEvent{Thinking: text, IsDelta: delta})}
	}
	if !IsDurableChatEvent(thinking("Checking the skill files", false)) {
		t.Fatal("a finished thinking block must be kept in chat history")
	}
	if IsDurableChatEvent(thinking("Check", true)) {
		t.Fatal("a streamed thinking fragment must not be stored")
	}
	if IsDurableChatEvent(thinking("  ", false)) {
		t.Fatal("an empty thinking block must not be stored")
	}
	child := thinking("sub-agent reasoning", false)
	child.ExecutionKind = "sub_agent"
	if IsDurableChatEvent(child) {
		t.Fatal("a child execution's thinking stays out of the chat history")
	}
}
