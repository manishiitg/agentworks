package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

// #3245: a task a chat handed to another of its owner's tabs is one of the
// chat's helpers, with its state and answer; tasks other chats sent, and
// requests to other people or the Team, are not.
func TestChatLinkedHelpersListsTasksThisChatHandedOff(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	newExternalCrewCommandFixture(t) // a workspace for the owner's chat registry
	ctx := t.Context()
	records := map[string]ProductConversationRecord{}
	for key, session := range map[string]string{"crew:chat:lead": "lead-session", "crew:chat:codex": "codex-session", "crew:chat:other": "other-session"} {
		records[productConversationRegistryEntryKey(crewProfileID, key)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, SessionID: session}
	}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: records}); err != nil {
		t.Fatal(err)
	}
	chat := func(key, session, label string) agentMessageEndpoint {
		return agentMessageEndpoint{UserID: "owner", Kind: triggerCallerCrew, ID: "crew", ChatKey: key, Session: session, Label: label}
	}
	sent := time.Now().UTC().Add(-time.Minute)
	conversations := []agentConversation{
		{ID: "inbox-1", Next: 3, Endpoints: [2]agentMessageEndpoint{chat("crew:chat:lead", "lead-session", "Lead"), chat("crew:chat:codex", "codex-session", "Codex tests")},
			Messages: []agentMessage{
				{ID: "msg-task", From: 0, Sequence: 1, Text: "Write the new tests", Title: "Write the new tests", MessageType: "request", Delivery: "delivered", SentAt: sent},
				{ID: "msg-answer", From: 1, Sequence: 2, Text: "Done: 12 tests added.", ReplyStatus: "done", TaskReplyTo: "msg-task", Delivery: "delivered", SentAt: sent.Add(30 * time.Second)},
			}},
		{ID: "inbox-2", Next: 2, Endpoints: [2]agentMessageEndpoint{chat("crew:chat:other", "other-session", "Other"), chat("crew:chat:codex", "codex-session", "Codex tests")},
			Messages: []agentMessage{{ID: "msg-not-ours", From: 0, Sequence: 1, Text: "Another chat's task", MessageType: "request", Delivery: "delivered", SentAt: sent}}},
		{ID: "inbox-3", Next: 2, Endpoints: [2]agentMessageEndpoint{chat("crew:chat:lead", "lead-session", "Lead"), {UserID: "someone-else", Kind: triggerCallerCrew, ID: "crew", ChatKey: "crew:chat:theirs", Label: "Their agent"}},
			Messages: []agentMessage{{ID: "msg-other-person", From: 0, Sequence: 1, Text: "Ask a teammate", MessageType: "request", Delivery: "delivered", SentAt: sent}}},
	}
	agentMessageMu.Lock()
	store, err := readAgentMessageStore(ctx)
	if err == nil {
		store.Conversations = append(store.Conversations, conversations...)
		err = saveAgentMessageStore(ctx, store)
	}
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	helpers, err := api.chatLinkedHelpers(ctx, "owner", "lead-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(helpers) != 1 {
		t.Fatalf("helpers = %+v, want only the task the lead chat handed off", helpers)
	}
	got := helpers[0]
	if got.MessageID != "msg-task" || got.State != "done" || got.Reply != "Done: 12 tests added." || got.ChatKey != "crew:chat:codex" || got.SessionID != "codex-session" || got.Agent != "Codex tests" {
		t.Fatalf("helper = %+v", got)
	}
	if other, _ := api.chatLinkedHelpers(ctx, "someone-else", "lead-session"); len(other) != 0 {
		t.Fatalf("another person read this chat's helpers: %+v", other)
	}
}

// #3245: Stop on a Codex helper interrupts that helper only, by its
// spawnAgent call, through the background-work stop.
func TestCodexHelperStopInterruptsOneHelper(t *testing.T) {
	var gotOwner, gotCall string
	previous := interruptCodexSubagent
	interruptCodexSubagent = func(_ context.Context, owner, callID string) error {
		gotOwner, gotCall = owner, callID
		return nil
	}
	t.Cleanup(func() { interruptCodexSubagent = previous })
	api := &StreamingAPI{}
	if !api.stopSessionBackgroundWorkItem("chat-a", codexHelperWorkPrefix+"call-9") || gotOwner != "chat-a" || gotCall != "call-9" {
		t.Fatalf("stop went to %q %q", gotOwner, gotCall)
	}
	interruptCodexSubagent = func(context.Context, string, string) error { return errors.New("not running") }
	if api.stopSessionBackgroundWorkItem("chat-a", codexHelperWorkPrefix+"call-9") {
		t.Fatal("a failed interrupt reported stopped")
	}
}
