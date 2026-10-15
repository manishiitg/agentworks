package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// 2026-10-15: the person switched a chat from Claude to Codex. The old Claude pane still counted as "a live pane that
// owns the context", so the conversation was neither resumed nor replayed and Codex started with no history. Only a
// pane of the turn's own provider may own the context.
func TestLivePaneOwnsContextOnlyForItsOwnProvider(t *testing.T) {
	const sessionID = "provider-switch-context"
	store := terminals.NewStore()
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, "mlp-claude-code-1792078336078607000-5d9d2c73"))
	api := &StreamingAPI{terminalStore: store}
	if !api.sessionHasLiveMainCodingTmuxOf(sessionID, "claude-code") {
		t.Fatal("the live Claude pane must own the context of a Claude turn")
	}
	if api.sessionHasLiveMainCodingTmuxOf(sessionID, "codex-cli") {
		t.Fatal("a live Claude pane must not own the context of a Codex turn")
	}
	if got := api.retainedCLIProvider(sessionID); got != "claude-code" {
		t.Fatalf("retained provider = %q, want claude-code", got)
	}
}
