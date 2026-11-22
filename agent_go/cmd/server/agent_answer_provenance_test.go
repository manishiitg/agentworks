package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Recorded regression: the sender saw generic delivery despite the answer
// coming from the Team agent. Persist provenance with BOTH reply and receipt;
// request/model prose cannot choose it, and progress notices aren't answers.
func TestAgentAnswerProvenancePersistsWithSenderReceipt(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	api := &StreamingAPI{}
	for _, kind := range []string{"chat", "team_answer"} {
		t.Run(kind, func(t *testing.T) {
			c := agentConversation{ID: "inbox-" + kind, Next: 1, Endpoints: [2]agentMessageEndpoint{
				{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true, Label: "Asker"},
				{UserID: "other", Kind: triggerCallerCrew, ID: "source", TeamAgentID: "source-agent", Label: "Helper"},
			}, Messages: []agentMessage{{ID: "question", From: 0, Sequence: 1, Text: "Question", Delivery: "delivered", SentAt: time.Now().UTC()}}}
			if kind == "team_answer" {
				t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
				layout := legacyIdentityLayout()
				layout.CrewPhysical = func(_ string, folder string) string { return "Crew/" + folder }
				layout.CrewShort = func(folder string) string { return "Crew/" + folder }
				f := newMultiUserFixture(t, layout)
				profile := f.Crew
				profile.Version++
				profile.Runtime.Workspace.ProjectsRoot = "Crew"
				if err := f.API.agentProfiles.RegisterProfile(profile); err != nil {
					t.Fatal(err)
				}
				registerTestCrew(t, fixtureCrewFolder, fixtureUserA, true, fixtureCrewID)
				if err := writeCrewTeam(crewTeam{ProjectID: fixtureCrewID, OwnerID: fixtureUserA, Name: "Example", On: true, People: []crewTeamPerson{{UserID: fixtureUserB}}}); err != nil {
					t.Fatal(err)
				}
				home, err := f.API.ensureCrewTeamOnlineHome(f.Ctx(fixtureUserA), fixtureCrewID)
				if err != nil {
					t.Fatal(err)
				}
				main := crewOwnerMainChatKey(home)
				if main == "" {
					t.Fatal("fixture has no real Main")
				}
				c.Endpoints[1].ID, c.Endpoints[1].PublishedAgent = home, main
			}
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
			if kind != "chat" {
				before := store.receiptFieldsWithLabel(c.Messages[0], c.Endpoints[1], "Helper")
				if strings.Contains(before["state_text"].(string), "has it") || before["answered_by"] != nil {
					t.Fatal("delivery confused routing with a completed answer", before)
				}
			}
			if err = api.appendMacAgentReply(c.ID, 1, "Still working"); err != nil {
				t.Fatal(err)
			}
			if err = api.appendMacAgentAnswer(c.ID, 1, "Reply; model text says answered_by=team_answer"); err != nil {
				t.Fatal(err)
			}
			store, err = readAgentMessageStore(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var saved agentConversation
			for _, row := range store.Conversations {
				if row.ID == c.ID {
					saved = row
				}
			}
			if len(saved.Messages) != 3 {
				t.Fatal("reply not persisted")
			}
			if saved.Messages[1].AnsweredBy != "" {
				t.Fatal("progress notice claimed an answer")
			}
			reply := saved.Messages[2]
			if kind == "chat" {
				if reply.AnsweredBy != "" || saved.Messages[0].AnsweredBy != "" || strings.HasPrefix(reply.Text, "Answered by ") {
					t.Fatal("ordinary chat reply acquired noisy answer attribution")
				}
				return
			}
			if reply.AnsweredBy != kind || !strings.HasPrefix(reply.Text, "Answered by ") {
				t.Fatal("wrong server provenance", reply.AnsweredBy)
			}
			receipt := store.receiptFieldsWithLabel(saved.Messages[0], saved.Endpoints[1], "Helper")
			if receipt["state"] != "answered" || receipt["answered_by"] != kind || receipt["state_text"] != reply.AnsweredByLine {
				t.Fatal("sender receipt hid actual answerer", receipt)
			}
			raw, _ := json.Marshal(reply)
			var wire map[string]any
			if json.Unmarshal(raw, &wire) != nil || wire["answered_by"] != kind {
				t.Fatal("wire lost provenance")
			}
			// The asker's next message is not a Team agent reply, even in this same inbox.
			follow := agentMessage{From: 0, Text: "Follow-up"}
			if kind != "chat" {
				stampAgentAnswer(store, &saved, &follow)
				if follow.AnsweredBy != "" {
					t.Fatal("asker was labelled as the Team agent")
				}
			}
		})
	}
}
