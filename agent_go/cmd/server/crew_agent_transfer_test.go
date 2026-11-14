package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// PLAT-746-971: a transfer is not general Team access to somebody's private
// conversation; only the named recipient may download it, while still joined.
func TestCrewAgentTransferOnlyNamedCurrentRecipient(t *testing.T) {
	now := time.Now()
	agent := crewTeamRegisteredAgent{ID: "agent", OwnerID: "maker", Tab: "tab", ChatKey: "room:chat:tab"}
	team := crewTeam{ProjectID: "room", OwnerID: "maker", On: true,
		People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent.ID: agent}}
	offer, err := newCrewAgentTransfer(team, "maker", "recipient", agent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("incomplete uploads must not be downloadable")
	}
	offer.Ready = true
	if err := offer.admit(team, "recipient", true, now); err != nil {
		t.Fatal(err)
	}
	for _, person := range []string{"maker", "other", ""} {
		if err := offer.admit(team, person, true, now); err == nil {
			t.Fatalf("private bundle admitted %q", person)
		}
	}
	if _, err := newCrewAgentTransfer(team, "other", "recipient", agent.ID, now); err == nil {
		t.Fatal("foreign chat transfer admitted")
	}
	if err := offer.admit(team, "recipient", true, offer.Expires); err == nil {
		t.Fatal("expired offer admitted")
	}
	team.People[0].Invited = true
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("removed recipient admitted")
	}
	team.People[0].Invited = false
	changed := agent
	changed.OwnerID = "other"
	team.Agents[agent.ID] = changed
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("stale owner admitted")
	}
}

// A completed upload is private to its intended recipient, and cancel/expiry
// remove the entire transfer namespace including native chunks and history.
func TestCrewAgentTransferPrivateStageAndCleanup(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	now := time.Now()
	agent := crewTeamRegisteredAgent{ID: "agent", OwnerID: "maker", Tab: "tab", ChatKey: "room:chat:tab"}
	team := crewTeam{ProjectID: "room", OwnerID: "maker", On: true, People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent.ID: agent}}
	offer, err := newCrewAgentTransfer(team, "maker", "recipient", agent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveCrewAgentTransfer(offer); err != nil {
		t.Fatal(err)
	}
	payload := crewAgentTransferPayload{Conversation: &crewHandoffConversation{ConversationID: "conversation-selected"}}
	if err = stageCrewAgentTransfer(team, offer, "other", payload, now); err == nil {
		t.Fatal("foreign upload accepted")
	}
	if err = stageCrewAgentTransfer(team, offer, "maker", payload, now); err != nil {
		t.Fatal(err)
	}
	offer, err = readCrewAgentTransfer(team.ProjectID, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !offer.Ready {
		t.Fatal("validated history was not made ready")
	}
	if _, err = downloadCrewAgentTransfer(team, offer, "other", now); err == nil {
		t.Fatal("third-party download accepted")
	}
	got, err := downloadCrewAgentTransfer(team, offer, "recipient", now)
	if err != nil || got.Conversation.ConversationID != payload.Conversation.ConversationID {
		t.Fatalf("recipient history: %v", err)
	}
	store, err := crewAgentTransferChunkStore(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(store, "private-bytes"), []byte("selected-session"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = cancelCrewAgentTransfer(team, offer, "other", now); err == nil {
		t.Fatal("third-party cancel accepted")
	}
	if err = cancelCrewAgentTransfer(team, offer, "recipient", now); err != nil {
		t.Fatal(err)
	}
	dir, _ := crewAgentTransferDirectory(team.ProjectID, offer.ID)
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("declined transfer retained private data")
	}
	if err = saveCrewAgentTransfer(offer); err != nil {
		t.Fatal(err)
	}
	if err = expireCrewAgentTransfers(context.Background(), team.ProjectID, offer.Expires); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("expired transfer retained private data")
	}
}
