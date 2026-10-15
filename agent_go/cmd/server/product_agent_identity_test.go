package server

import (
	"context"
	"strings"
	"testing"
)

// An agent's name and purpose belong to its tab: they are listed by conversation key, changed one field at a time
// with the actor kept, and refused for a tab that no longer exists (PLAT-571).
func TestAgentIdentityBelongsToTheTab(t *testing.T) {
	store, _ := memoryProductConversationStore()
	ctx := context.Background()
	path := productConversationRegistryPath("user-1")
	main := ProductConversationRecord{ConversationKey: "proj-1", SessionID: "s-main", ProfileID: "code"}
	side := ProductConversationRecord{ConversationKey: "proj-1:chat:ab12", SessionID: "s-side", ProfileID: "code"}
	document := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{
		productConversationRegistryEntryKey("code", "proj-1"):           main,
		productConversationRegistryEntryKey("code", "proj-1:chat:ab12"): side,
	}}
	if err := store.writeDocument(ctx, path, document); err != nil {
		t.Fatal(err)
	}
	name, purpose := "Reviewer", "Reads every change and says what is risky"
	if _, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:ab12", &name, &purpose, "the person"); err != nil {
		t.Fatal(err)
	}
	purposeOnly := "Reads changes and tests them"
	identity, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:ab12", nil, &purposeOnly, "agent Reviewer")
	if err != nil || identity.Name != "Reviewer" || identity.Purpose != purposeOnly || identity.UpdatedBy != "agent Reviewer" {
		t.Fatalf("a purpose-only change must keep the name and record who changed it: %+v %v", identity, err)
	}
	listed, err := store.agentIdentities(ctx, "user-1", "code", "proj-1")
	if err != nil || listed["proj-1:chat:ab12"].Name != "Reviewer" || len(listed) != 1 {
		t.Fatalf("identities are keyed by conversation key: %+v %v", listed, err)
	}
	if _, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:gone", &name, nil, "the person"); err == nil {
		t.Fatal("a tab that no longer exists cannot be given an identity")
	}
	long := strings.Repeat("x", productAgentPurposeMaxRunes+1)
	if _, err := store.setAgentIdentity(ctx, "user-1", "code", "proj-1:chat:ab12", nil, &long, "the person"); err == nil {
		t.Fatal("a purpose longer than one short line is refused")
	}
}
