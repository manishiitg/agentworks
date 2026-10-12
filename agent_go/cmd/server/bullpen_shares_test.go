package server

import (
	"testing"
	"time"
)

// A grant must stop working the moment it is revoked, blocked or expired, even
// for a conversation that remembered the earlier revocation version.
func TestAuthorizeShareStopsAtRevokeBlockAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	active := map[string]bool{"owner": true, "guest": true}
	defer func(prev func(string) bool) { shareAccountActive = prev }(shareAccountActive)
	shareAccountActive = func(id string) bool { return active[id] }
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
	active["guest"] = false // account deleted, disabled or without Bullpen
	if _, err := s.authorizeShare("share-1", 1, now); err == nil {
		t.Fatal("grant for an inactive or deleted account accepted")
	}
	active["guest"] = true
	gone := s
	gone.Agents = nil
	if _, err := gone.authorizeShare("share-1", 1, now); err == nil {
		t.Fatal("grant for a removed agent accepted")
	}
}

func TestPruneSharesDropsOnlyLongEndedGrants(t *testing.T) {
	now := time.Now().UTC()
	s := agentMessageStore{Shares: []agentShare{
		{ID: "live", ExpiresAt: now.Add(time.Hour)},
		{ID: "revoked-recent", ExpiresAt: now.Add(time.Hour), RevokedAt: now.Add(-time.Hour)},
		{ID: "revoked-old", ExpiresAt: now.Add(time.Hour), RevokedAt: now.Add(-60 * 24 * time.Hour)},
		{ID: "expired-old", ExpiresAt: now.Add(-60 * 24 * time.Hour)},
	}}
	s.pruneShares(now)
	if len(s.Shares) != 2 || s.Shares[0].ID != "live" || s.Shares[1].ID != "revoked-recent" {
		t.Fatalf("unexpected shares after pruning: %+v", s.Shares)
	}
}

// A cross-account conversation works only between the grant's owner and subject
// agents, stops for a forged or swapped endpoint, and revoking it suppresses
// messages that were stored but not yet admitted.
func TestSharedConversationFenceAndSuppression(t *testing.T) {
	now := time.Now().UTC()
	defer func(prev func(string) bool) { shareAccountActive = prev }(shareAccountActive)
	shareAccountActive = func(string) bool { return true }
	s := agentMessageStore{
		Agents: []namedMessageAgent{{ID: "agent-a", UserID: "owner"}, {ID: "agent-b", UserID: "guest"}, {ID: "agent-x", UserID: "other"}},
		Shares: []agentShare{{ID: "share-1", OwnerUserID: "owner", AgentID: "agent-a", SubjectUserID: "guest", Capability: shareCapabilityChat, ExpiresAt: now.Add(time.Hour), RevocationVersion: 1}},
	}
	end := func(user, agent string) agentMessageEndpoint {
		return agentMessageEndpoint{UserID: user, Kind: triggerCallerUser, ID: user, External: true, NamedAgentID: agent, Label: agent, ShareID: "share-1", ShareVersion: 1}
	}
	c := agentConversation{ID: "inbox-1", Endpoints: [2]agentMessageEndpoint{end("guest", "agent-b"), end("owner", "agent-a")}, Messages: []agentMessage{{ID: "m1", From: 0, Delivery: "stored"}, {ID: "m2", From: 0, Delivery: "processed"}}}
	if err := s.conversationShareErr(c, now); err != nil {
		t.Fatalf("legitimate shared conversation refused: %v", err)
	}
	if !s.shareSenderAllowed(c, 0, "owner", now) || s.shareSenderAllowed(c, 0, "other", now) {
		t.Fatal("sender fence must admit only the grant's owner as recipient")
	}
	forged := c
	forged.Endpoints[0] = end("other", "agent-x")
	if err := s.conversationShareErr(forged, now); err == nil {
		t.Fatal("third account riding the grant was accepted")
	}
	wrongAgent := c
	wrongAgent.Endpoints[1] = end("owner", "agent-z")
	if err := s.conversationShareErr(wrongAgent, now); err == nil {
		t.Fatal("a different agent of the owner was reachable through the grant")
	}
	s.Conversations = []agentConversation{c}
	s.suppressShare("share-1")
	if got := s.Conversations[0].Messages; got[0].Delivery != "suppressed" || got[1].Delivery != "processed" {
		t.Fatalf("suppression changed the wrong receipts: %+v", got)
	}
}
