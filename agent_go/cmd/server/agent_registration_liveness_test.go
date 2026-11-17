package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #2980: one tab had three hosted registrations, and unattached development
// names persisted for weeks. Keep message history but back up expired names.
func TestAgentRegistrationExpiryAndHostedBindingIdentity(t *testing.T) {
	_ = newTriggerLinkEnv(t)
	now := time.Now().UTC()
	binding := hostedBridgeBinding{Profile: crewProfileID, Project: "alpha", Chat: "alpha:chat:one"}
	first := namedMessageAgent{ID: "first", UserID: "owner", Name: "old-alias", Hosted: &binding, CreatedAt: now.Add(-30 * 24 * time.Hour)}
	second := first
	second.ID = "second"
	second.Name = "new-alias"
	store := agentMessageStore{Agents: []namedMessageAgent{
		{ID: "abandoned", UserID: "owner", Name: "old-dev", CreatedAt: now.Add(-30 * 24 * time.Hour)},
		{ID: "recent", UserID: "owner", Name: "new-dev", CreatedAt: now},
		{ID: "attached", UserID: "owner", CreatedAt: now.Add(-30 * 24 * time.Hour), Attachment: &namedAgentAttachment{SessionID: "native-session"}}, first, second,
	}, Conversations: []agentConversation{{ID: "history", Messages: []agentMessage{{Text: "kept"}}}}}
	if err := expireAbandonedAgentRegistrations(t.Context(), &store, now); err != nil {
		t.Fatal(err)
	}
	if len(store.Agents) != 4 || len(store.Conversations) != 1 {
		t.Fatalf("wrong expiry: %v", store.Agents)
	}
	mb, err := currentAgentMailbox()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(mb.path), "registration-backups", now.UTC().Format("20060102T150405.000000000")+".json")
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "abandoned") {
		t.Fatal("expired registration was not backed up", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("registration backup permissions", err)
	}
	got, ok, err := existingHostedBridgeRegistration(store, "owner", binding, "new-alias")
	if err != nil || !ok || got.ID != "first" {
		t.Fatalf("registration created a second identity for one binding: %v %v %v", got, ok, err)
	}
	if len(uniqueHostedAgents(store.Agents)) != 3 {
		t.Fatal("same tab listed twice")
	}
	// Dedup runs after sharing checks, never hide an admitted alias because the
	// canonical/older registration itself was not shared with this caller.
	rows := []map[string]interface{}{{"agent_id": "second"}}
	if len(uniqueHostedDirectoryRows(rows, store.Agents)) != 1 {
		t.Fatal("private alias suppressed admitted alias")
	}
	a := namedMessageAgent{ID: "unused", Attachment: &namedAgentAttachment{DeliveryRoute: appAgentDeliveryRoute}}
	if appAgentListed(a) {
		t.Fatal("unused idle app chat listed")
	}
	a.Attachment.RuntimeInfo = &agentRuntimeInfo{Activity: "Idle", ChatOpen: true}
	if !appAgentListed(a) {
		t.Fatal("idle open chat vanished")
	}
	a.CreatedAt = now.Add(-30 * 24 * time.Hour)
	if !namedAgentDiscoverable(a, now) || namedAgentOnline(a) {
		t.Fatal("idle current tab must list offline without a connected stream")
	}
}
