package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// PLAT-746-971: a fork is not general Team access to somebody's private
// conversation; only the named recipient may download it, while still joined.
func TestCrewAgentForkOnlyNamedCurrentRecipient(t *testing.T) {
	now := time.Now()
	agent := crewTeamRegisteredAgent{ID: "agent", OwnerID: "maker", Tab: "tab", ChatKey: "room:chat:tab"}
	team := crewTeam{ProjectID: "room", OwnerID: "maker", On: true,
		People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent.ID: agent}}
	offer, err := newCrewAgentFork(team, "maker", "recipient", agent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("incomplete uploads must not be downloadable")
	}
	if offer.CopyAgentID == "" || offer.CopyAgentID == agent.ID {
		t.Fatal("a fork must allocate a new identity, never transfer the maker's")
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
	if _, err := newCrewAgentFork(team, "other", "recipient", agent.ID, now); err == nil {
		t.Fatal("foreign chat fork admitted")
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
// remove the entire fork namespace including native chunks and history.
func TestCrewAgentForkPrivateStageAndCleanup(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	now := time.Now()
	agent := crewTeamRegisteredAgent{ID: "agent", OwnerID: "maker", Tab: "tab", ChatKey: "room:chat:tab"}
	team := crewTeam{ProjectID: "room", OwnerID: "maker", On: true, People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent.ID: agent}}
	offer, err := newCrewAgentFork(team, "maker", "recipient", agent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveCrewAgentFork(offer); err != nil {
		t.Fatal(err)
	}
	payload := crewAgentForkPayload{Conversation: &crewHandoffConversation{ConversationID: "conversation-selected"}}
	if err = stageCrewAgentFork(team, offer, "other", payload, now); err == nil {
		t.Fatal("foreign upload accepted")
	}
	if err = stageCrewAgentFork(team, offer, "maker", payload, now); err != nil {
		t.Fatal(err)
	}
	offer, err = readCrewAgentFork(team.ProjectID, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !offer.Ready {
		t.Fatal("validated history was not made ready")
	}
	if _, err = downloadCrewAgentFork(team, offer, "other", now); err == nil {
		t.Fatal("third-party download accepted")
	}
	got, err := downloadCrewAgentFork(team, offer, "recipient", now)
	if err != nil || got.Conversation.ConversationID != payload.Conversation.ConversationID {
		t.Fatalf("recipient history: %v", err)
	}
	store, err := crewAgentForkChunkStore(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(store, "private-bytes"), []byte("selected-session"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = cancelCrewAgentFork(team, offer, "other", now); err == nil {
		t.Fatal("third-party cancel accepted")
	}
	if err = cancelCrewAgentFork(team, offer, "recipient", now); err != nil {
		t.Fatal(err)
	}
	dir, _ := crewAgentForkDirectory(team.ProjectID, offer.ID)
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("declined fork retained private data")
	}
	if err = saveCrewAgentFork(offer); err != nil {
		t.Fatal(err)
	}
	if err = expireCrewAgentForks(context.Background(), team.ProjectID, offer.Expires); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("expired fork retained private data")
	}
	if err = saveCrewAgentFork(offer); err != nil {
		t.Fatal(err)
	}
	if err = completeCrewAgentFork(team, offer, "recipient", now); err == nil {
		t.Fatal("accept removed the payload before an imported copy existed")
	}
	copy := crewTeamRegisteredAgent{ID: offer.CopyAgentID, OwnerID: "recipient", Tab: "fork-copy", ChatKey: "room:chat:fork-copy"}
	team.Agents[copy.ID] = copy
	if err = completeCrewAgentFork(team, offer, "maker", now); err == nil {
		t.Fatal("maker accepted the recipient's fork")
	}
	copy.InTeam = true
	team.Agents[copy.ID] = copy
	if err = completeCrewAgentFork(team, offer, "recipient", now); err == nil {
		t.Fatal("fork accepted without the required Only-me boundary")
	}
	copy.InTeam = false
	team.Agents[copy.ID] = copy
	if err = completeCrewAgentFork(team, offer, "recipient", now); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("accepted fork retained staged private data")
	}
	if source := team.Agents[agent.ID]; source.OwnerID != agent.OwnerID || source.ChatKey != agent.ChatKey || source.InTeam != agent.InTeam {
		t.Fatal("accept changed the maker's agent")
	}
}
