package server

import "testing"

// PLAT-746-899: a finished Codex command or Claude background task starts a
// follow-up turn, but never after Stop or while a turn runs, and at most
// backgroundFollowUpChainLimit in a row until the person writes again (each
// one is a full turn on their account).
func TestBackgroundFollowUpSkipsStoppedBusyAndLongChains(t *testing.T) {
	chain := &backgroundFollowUpChain{count: map[string]int{}}
	const chat = "codex-chat"
	if got := chain.skipReason(chat, true, false); got != "stopped_by_person" {
		t.Fatalf("stopped chat: %q", got)
	}
	if got := chain.skipReason(chat, false, true); got != "chat_busy" {
		t.Fatalf("busy chat: %q", got)
	}
	for i := 0; i < backgroundFollowUpChainLimit; i++ {
		if got := chain.skipReason(chat, false, false); got != "" {
			t.Fatalf("follow-up %d refused: %q", i+1, got)
		}
		chain.started(chat)
	}
	if got := chain.skipReason(chat, false, false); got != "chain_limit" {
		t.Fatalf("follow-up past the limit: %q", got)
	}
	if got := chain.skipReason("other-chat", false, false); got != "" {
		t.Fatalf("another chat's chain counted: %q", got)
	}
	chain.personWrote(chat)
	if got := chain.skipReason(chat, false, false); got != "" {
		t.Fatalf("person's message did not start a new chain: %q", got)
	}
}
