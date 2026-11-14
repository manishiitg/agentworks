package server

import (
	"strings"
	"testing"
)

// PLAT-746-899: a finished Codex command or Claude background task starts a
// follow-up turn, but never after Stop, and at most
// backgroundFollowUpChainLimit in a row until the person writes again. A busy
// chat is not a refusal: the wake waits for the turn (nativeWakes).
func TestBackgroundFollowUpSkipsStoppedAndLongChains(t *testing.T) {
	chain := &backgroundFollowUpChain{count: map[string]int{}}
	const chat = "codex-chat"
	if got := chain.skipReason(chat, true); got != "stopped_by_person" {
		t.Fatalf("stopped chat: %q", got)
	}
	for i := 0; i < backgroundFollowUpChainLimit; i++ {
		if got := chain.skipReason(chat, false); got != "" {
			t.Fatalf("follow-up %d refused: %q", i+1, got)
		}
		chain.started(chat)
	}
	if got := chain.skipReason(chat, false); got != "chain_limit" {
		t.Fatalf("follow-up past the limit: %q", got)
	}
	if got := chain.skipReason("other-chat", false); got != "" {
		t.Fatalf("another chat's chain counted: %q", got)
	}
	chain.personWrote(chat)
	if got := chain.skipReason(chat, false); got != "" {
		t.Fatalf("person's message did not start a new chain: %q", got)
	}
}

// Owner, 14 Nov 2026: wakes that finish while a turn runs wait for it instead
// of being dropped; commands finishing together go into one follow-up, the same
// command is queued once, and Stop drops what is waiting.
func TestNativeWakesWaitBatchAndDropOnStop(t *testing.T) {
	const chat = "wake-chat"
	nativeWakes.addCodex(chat, codexFinishedCommand{id: "a", output: "ok"})
	nativeWakes.addCodex(chat, codexFinishedCommand{id: "b", failed: true})
	nativeWakes.addCodex(chat, codexFinishedCommand{id: "a", output: "ok"})
	commands, _, _ := nativeWakes.take(chat)
	if len(commands) != 2 {
		t.Fatalf("queued commands = %d, want 2 (one per command)", len(commands))
	}
	text := codexCommandsFinishedText(commands)
	if !strings.Contains(text, "successfully") || !strings.Contains(text, "with an error") || !strings.Contains(text, "(no output)") {
		t.Fatalf("one follow-up must carry both results: %q", text)
	}
	nativeWakes.addCodex(chat, codexFinishedCommand{id: "c"})
	dropQueuedNativeWakes(chat, "stopped")
	if commands, claude, muse := nativeWakes.take(chat); len(commands) != 0 || claude || len(muse) != 0 {
		t.Fatalf("Stop left wakes queued: %d %t", len(commands), claude)
	}
}

// PLAT-746-941: empty Muse completions are kept and coalesced with other
// wakes on the existing queue; Stop must clear them without starting a turn.
func TestMuseWakesBatchEmptyRepliesAndDropOnStop(t *testing.T) {
	const chat = "muse-wake-chat"
	t.Cleanup(func() { dropQueuedNativeWakes(chat, "test_end") })
	nativeWakes.addMuse(chat, museWake{nativeSessionID: "native", turnID: "one"})
	nativeWakes.addMuse(chat, museWake{nativeSessionID: "native", turnID: "one"})
	nativeWakes.addMuse(chat, museWake{nativeSessionID: "native", turnID: "two", text: "tests finished"})
	_, _, wakes := nativeWakes.take(chat)
	if len(wakes) != 2 {
		t.Fatalf("wakes=%d", len(wakes))
	}
	text := museWakesFinishedText(wakes)
	if !strings.Contains(text, "(no output)") || !strings.Contains(text, "tests finished") {
		t.Fatalf("missing wake results: %q", text)
	}
	nativeWakes.addMuse(chat, wakes[0])
	dropQueuedNativeWakes(chat, "stopped")
	_, _, wakes = nativeWakes.take(chat)
	if len(wakes) != 0 {
		t.Fatal("Stop retained Muse wake")
	}
}
