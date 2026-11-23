package llmtypes

import "testing"

// PLAT-872: two queued requests must both reach a resumed coding agent; old
// conversation and tool output are already held by its native session.
func TestPendingHumanMessagesPreservesAllQueuedRequests(t *testing.T) {
	messages := []MessageContent{TextPart(ChatMessageTypeHuman, "old"), TextPart(ChatMessageTypeAI, "done"), TextPart(ChatMessageTypeHuman, "first queued"), TextPart(ChatMessageTypeHuman, "second queued")}
	pending := PendingHumanMessages(messages)
	if len(pending) != 2 || pending[0].Parts[0].(TextContent).Text != "first queued" || pending[1].Parts[0].(TextContent).Text != "second queued" {
		t.Fatalf("lost or replayed input: %#v", pending)
	}
	if got := PendingHumanMessages(messages[:2]); len(got) != 0 {
		t.Fatalf("replayed answered input: %#v", got)
	}
}
