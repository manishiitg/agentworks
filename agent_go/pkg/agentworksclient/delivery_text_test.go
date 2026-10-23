package agentworksclient

import (
	"strings"
	"testing"
	"time"
)

// A server status line is shown as plain information and is not counted as a waiting message; held messages are named
// by reason class and their text is never shown.
func TestWaitingAndHeldLinesIgnoreStatusLinesAndHideHeldText(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	messages := []AgentLiveMessage{
		{Sender: "agent-a", Message: "one", SentAt: now.Add(-12 * time.Minute).Format(time.RFC3339Nano)},
		{Sender: "agent-b", Message: "two", SentAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano)},
		{Sender: "AgentWorks", Kind: "status", Message: "x has not read your message"},
	}
	if got := WaitingLine(messages, now); got != "AgentWorks: 2 messages waiting from agent-a, agent-b (oldest 12 min ago)." {
		t.Fatalf("waiting line: %q", got)
	}
	if got := MessageBlock(messages[2], "att"); !strings.Contains(got, "no reply needed") || strings.Contains(got, "ack:") {
		t.Fatalf("a status line must not ask for an ack or reply: %q", got)
	}
	held := HeldLine(LocalBridgeReply{Held: 2, HeldNotice: true, HeldDetails: []HeldMessageDetail{{Sender: "sender", Reason: "Sharing has ended.", Next: "Ask the sender to send again after access is restored."}}})
	if !strings.Contains(held, "2 messages") || !strings.Contains(held, "From sender: Sharing has ended.") || strings.Contains(held, "admin") {
		t.Fatalf("held line: %q", held)
	}
}
