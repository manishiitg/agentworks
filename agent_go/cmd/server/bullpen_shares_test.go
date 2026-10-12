package server

import (
	"testing"
	"time"
)

// A grant must stop working the moment it is revoked, blocked or expired, even
// for a conversation that remembered the earlier revocation version.
func TestAuthorizeShareStopsAtRevokeBlockAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	s := agentMessageStore{
		Agents: []namedMessageAgent{{ID: "agent-1", UserID: "owner"}},
		Shares: []agentShare{{ID: "share-1", OwnerUserID: "owner", AgentID: "agent-1", SubjectUserID: "guest", Capability: shareCapabilityChat, ExpiresAt: now.Add(time.Hour), RevocationVersion: 1}},
	}
	if _, err := s.authorizeShare("share-1", 1, now); err != nil {
		t.Fatalf("live grant refused: %v", err)
	}
	if _, err := s.authorizeShare("share-1", 0, now); err == nil {
		t.Fatal("stale revocation version accepted")
	}
	if _, err := s.authorizeShare("share-1", 1, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired grant accepted")
	}
	blocked := s
	blocked.Blocks = []agentShareBlock{{OwnerUserID: "owner", BlockedUserID: "guest"}}
	if _, err := blocked.authorizeShare("share-1", 1, now); err == nil {
		t.Fatal("blocked sender accepted")
	}
	revoked := s
	revoked.Shares = []agentShare{s.Shares[0]}
	revoked.Shares[0].RevokedAt, revoked.Shares[0].RevocationVersion = now, 2
	if _, err := revoked.authorizeShare("share-1", 1, now); err == nil {
		t.Fatal("revoked grant accepted")
	}
	gone := s
	gone.Agents = nil
	if _, err := gone.authorizeShare("share-1", 1, now); err == nil {
		t.Fatal("grant for a removed agent accepted")
	}
}
