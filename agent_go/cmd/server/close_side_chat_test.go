package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// Only a side chat (tab) can be closed; the main chat never is.
func TestIsSideChatConversationKey(t *testing.T) {
	for key, want := range map[string]bool{
		"proj1:chat:abc": true,
		"proj1":          false,
		"proj1:chat:":    false,
		":chat:abc":      false,
		"proj1:chat:a:b": false,
		"a:b:chat:c":     false,
		"":               false,
	} {
		if got := isSideChatConversationKey(key); got != want {
			t.Errorf("isSideChatConversationKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// A coding CLI pane stays alive between turns. It makes the session "active" (the tab close refuses it), but it is
// not a turn in flight, which is what lets the close stop an idle chat itself (PLAT-819).
func TestIdleCodingPaneIsNotARunningTurn(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "idle-side-chat"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, "mlp-pi-cli-idle"))
	if _, ok := store.MarkTurnCompleted(sessionID + ":main:" + sessionID); !ok {
		t.Fatal("expected to settle the turn")
	}
	api := &StreamingAPI{terminalStore: store}
	if !api.sessionHasActiveWork(sessionID) {
		t.Fatal("a live pane between turns still counts as active work")
	}
	if api.sessionRunsATurn(sessionID) {
		t.Fatal("a live pane between turns is not a turn in flight")
	}
}
