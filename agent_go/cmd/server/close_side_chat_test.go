package server

import (
	"context"
	"encoding/json"
	"fmt"
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

// The cap on side chats is counted from the server's registry, wherever the chats were
// opened and even when the main chat has no record, and it only stops a NEW chat: reopening
// one that exists always works (PLAT-861; the owner's project once held five against four).
func TestSideChatCapIsCountedFromTheRegistry(t *testing.T) {
	files := map[string]string{}
	store := productConversationRegistryStore{
		read: func(_ context.Context, path string) (string, bool, error) {
			raw, ok := files[path]
			return raw, ok, nil
		},
		write: func(_ context.Context, path, content string) error { files[path] = content; return nil },
		now:   time.Now, newID: func() string { return "x" },
	}
	entries := map[string]ProductConversationRecord{} // no main chat record on purpose
	for i := 0; i < externalCrewMaxSideChats; i++ {
		key := fmt.Sprintf("p1:chat:%02d", i)
		entries["code/"+key] = ProductConversationRecord{ConversationKey: key, SessionID: "s" + key}
	}
	raw, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: entries})
	files[productConversationRegistryPath("u1")] = string(raw)
	if _, err := checkSideChatCapacity(context.Background(), store, "u1", "code", "p1:chat:new"); err == nil {
		t.Fatal("a new side chat past the cap must be refused")
	}
	if _, err := checkSideChatCapacity(context.Background(), store, "u1", "code", "p1:chat:03"); err != nil {
		t.Fatalf("reopening an existing side chat must work: %v", err)
	}
	if _, err := checkSideChatCapacity(context.Background(), store, "u1", "code", "p2:chat:new"); err != nil {
		t.Fatalf("another project has its own cap: %v", err)
	}
}
