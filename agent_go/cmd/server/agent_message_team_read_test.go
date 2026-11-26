package server

import (
	"strings"
	"testing"
	"time"
)

// PLAT-746-951: SQL must enforce the same registry identity as the endpoint
// check, without requiring a receiving session to have been opened yet.
func TestTeamMailboxRegistryReadAndFailureReceipts(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"sender": true}, "other": {"recipient": true, "another": true}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	var recipient string
	for id, a := range team.Agents {
		if a.OwnerID == "other" && a.Tab == "recipient" {
			recipient = id
		}
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "team", ProfileID: "work"}, Chat: &codeChat{Key: "team:chat:recipient", SessionID: "current-session"}}
	for i, session := range []string{"", "stale-session"} {
		id := []string{"inbox-empty-session", "inbox-stale-session"}[i]
		c := agentConversation{ID: id, Next: 1, Endpoints: [2]agentMessageEndpoint{
			{UserID: "owner", Kind: triggerCallerCrew, ID: "team", Profile: "work", ChatKey: "team:chat:sender", Session: "sender-session"},
			{UserID: "other", Kind: triggerCallerCrew, ID: "team", Profile: "work", ChatKey: "team:chat:recipient", TeamAgentID: recipient, Session: session},
		}, Messages: []agentMessage{{ID: "msg-" + id, Sequence: 1, From: 0, Text: "Fixture message", SentAt: time.Now(), Delivery: "delivered"}}}
		agentMessageMu.Lock()
		store, err := readAgentMessageStore(t.Context())
		if err == nil {
			store.Conversations = append(store.Conversations, c)
			err = saveAgentMessageStore(t.Context(), store)
		}
		agentMessageMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		_, side, found, err := callerAgentMailboxConversation(t.Context(), id, "other", caller)
		if err != nil || !found || side != 1 {
			t.Fatalf("registered recipient session=%q read refused: found=%v side=%d %v", session, found, side, err)
		}
		wrong := caller
		wrong.Chat = &codeChat{Key: "team:chat:another", SessionID: session}
		if _, _, found, err := callerAgentMailboxConversation(t.Context(), id, "other", wrong); err != nil || found {
			t.Fatalf("another agent reached the inbox: %v %v", found, err)
		}
		if _, _, found, err := callerAgentMailboxConversation(t.Context(), id, "outsider", caller); err != nil || found {
			t.Fatalf("another person reached the inbox: %v %v", found, err)
		}
	}
	a := team.Agents[recipient]
	a.InTeam = false
	team.Agents[recipient] = a
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	caller.Chat.SessionID = "stale-session"
	if _, _, found, err := callerAgentMailboxConversation(t.Context(), "inbox-stale-session", "other", caller); err != nil || found {
		t.Fatalf("revoked registry identity fell back to matching session: %v %v", found, err)
	}
	for _, reason := range []string{"message Crew is unavailable or ownership changed", "internal path /private/fixture-sensitive"} {
		state, line := receiptState(agentMessage{Delivery: "undeliverable", Error: reason}, nil, false)
		if state != "not_delivered" || !strings.Contains(line, "Not delivered") || strings.Contains(line, "fixture-sensitive") {
			t.Fatalf("failed delivery receipt: %s %s", state, line)
		}
	}
}
