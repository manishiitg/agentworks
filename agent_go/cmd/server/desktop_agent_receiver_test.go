package server

import (
	"testing"
)

// PLAT-746-890: stored network mail must never recreate a deleted chat or
// follow the old name/key into a replacement conversation.
func TestDesktopAppRecipientRequiresCurrentOwnedConversation(t *testing.T) {
	env := newTriggerLinkEnv(t)
	_ = env
	ctx := t.Context()
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath("owner")
	record := ProductConversationRecord{ProfileID: crewProfileID, ResourceID: "alpha", ConversationKey: "alpha:chat:recipient", ConversationID: "conversation-before", SessionID: "session-before", WorkspacePath: linkAlphaPath}
	doc := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, record.ConversationKey): record}}
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if got, err := desktopAppAgentRecipient(ctx, "owner", record); err != nil || got.ConversationID != record.ConversationID {
		t.Fatalf("current owned recipient refused: %v", err)
	}
	replacement := record
	replacement.ConversationID = "conversation-after"
	replacement.SessionID = "session-after"
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, record.ConversationKey)] = replacement
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAppAgentRecipient(ctx, "owner", record); err == nil {
		t.Fatal("old inbox followed the replacement chat")
	}
	if got, err := desktopAppAgentRecipient(ctx, "owner", replacement); err != nil || got.SessionID != replacement.SessionID {
		t.Fatalf("current replacement refused: %v", err)
	}
	delete(doc.Entries, productConversationRegistryEntryKey(crewProfileID, record.ConversationKey))
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAppAgentRecipient(ctx, "owner", replacement); err == nil {
		t.Fatal("deleted recipient admitted")
	}
	after, err := store.loadDocument(ctx, path)
	if err != nil || len(after.Entries) != 0 {
		t.Fatalf("recipient lookup recreated a chat: %v", err)
	}
	// A registry reference to someone else's workspace does not advertise their
	// chat under this laptop login, even if its record sits in the local namespace.
	foreign := record
	foreign.ResourceID = "gamma"
	foreign.ConversationKey = "gamma:chat:foreign"
	foreign.WorkspacePath = linkGammaPath
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, foreign.ConversationKey)] = foreign
	if err := store.writeDocument(ctx, path, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAppAgentRecipient(ctx, "owner", foreign); err == nil {
		t.Fatal("another person's chat advertised as own")
	}
}
