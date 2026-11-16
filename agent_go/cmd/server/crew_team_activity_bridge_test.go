package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #2817: a public preview is a send-time Team decision, never a retrospective
// inference from a newly shared chat. A thread remains endpoint-owner-only.
func TestCrewTeamActivityBridgePreviewSeenAndOwnerThread(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","products":["work"]},{"id":"other","username":"other","role":"editor","products":["work"]},{"id":"observer","username":"observer","role":"editor","products":["work"]}]}`)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}, {UserID: "observer"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"left": true}, "other": {"right": true}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	team, _ = readCrewTeam("team")
	if !crewTeamReachOpen(team, "observer") {
		t.Fatal("fixture observer must be a current active member")
	}
	c := agentConversation{ID: "inbox-shared", Endpoints: [2]agentMessageEndpoint{{UserID: "owner", Kind: triggerCallerCrew, Profile: crewProfileID, ID: "team", ChatKey: "team:chat:left"}, {UserID: "other", Kind: triggerCallerCrew, Profile: crewProfileID, ID: "team", ChatKey: "team:chat:right"}}}
	stamp := crewTeamActivityStampFor(c)
	if stamp == nil {
		t.Fatal("matching current In-team agents must capture a stamp")
	}
	at := time.Now().UTC()
	c.Messages = []agentMessage{{ID: "public", From: 0, Text: strings.Repeat("界", 70) + "OWNER_THREAD_TAIL", SentAt: at, TeamActivity: stamp}, {ID: "private-before-share", From: 1, Text: "PRIVATE_SENTINEL", SentAt: at.Add(-time.Minute)}}
	// Same endpoint while Only me: no stamp; sharing it later must not backfill that message.
	right := team.Agents[stamp.Agents[1]]
	right.InTeam = false
	team.Agents[right.ID] = right
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if crewTeamActivityStampFor(c) != nil {
		t.Fatal("Only-me source/target captured activity")
	}
	right.InTeam = true
	team.Agents[right.ID] = right
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	team, _ = readCrewTeam("team")
	outsider := c
	outsider.ID = "inbox-outside"
	outsider.Endpoints[0] = agentMessageEndpoint{UserID: "observer", Kind: triggerCallerUser, External: true, ID: "observer"}
	if crewTeamActivityStampFor(outsider) != nil {
		t.Fatal("outside/user sender captured activity")
	}
	if err := saveAgentMessageStore(t.Context(), agentMessageStore{Conversations: []agentConversation{c, outsider}}); err != nil {
		t.Fatal(err)
	}
	request := func(person, method, suffix, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.api.handleCrewTeamActivity(w, adminRequest(method, "/api/crew-teams/team/activity"+suffix, body, &UserClaims{UserID: person, Username: person}, map[string]string{"project": "team"}))
		return w
	}
	read := request("observer", http.MethodGet, "", "")
	if read.Code != 200 {
		t.Fatalf("allowed current member: %d %s", read.Code, read.Body.String())
	}
	var view crewTeamActivityView
	if err := json.Unmarshal(read.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Feed) != 1 || view.Feed[0].Bridge == nil || view.Feed[0].Bridge.CanOpen || view.Unseen != 1 || len([]rune(view.Feed[0].Line)) != 61 {
		t.Fatalf("bounded line-only preview: %s", read.Body.String())
	}
	if strings.Contains(read.Body.String(), "PRIVATE_SENTINEL") || strings.Contains(read.Body.String(), "OWNER_THREAD_TAIL") {
		t.Fatal("private or full message reached preview")
	}
	if w := request("observer", http.MethodGet, "?thread=inbox-shared", ""); w.Code != 403 {
		t.Fatalf("non-owner thread read: %d %s", w.Code, w.Body.String())
	}
	for _, person := range []string{"owner", "other"} {
		w := request(person, http.MethodGet, "?thread=inbox-shared", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "OWNER_THREAD_TAIL") || strings.Contains(w.Body.String(), "PRIVATE_SENTINEL") {
			t.Fatalf("admitted endpoint owner thread: %d %s", w.Code, w.Body.String())
		}
	}
	if w := request("observer", http.MethodPost, "", `{"seen_through":"message:public"}`); w.Code != 200 {
		t.Fatalf("mark current admitted feed: %d %s", w.Code, w.Body.String())
	}
	if w := request("observer", http.MethodGet, "", ""); !strings.Contains(w.Body.String(), `"unseen":0`) {
		t.Fatal("mark did not affect own count")
	}
	if w := request("owner", http.MethodGet, "", ""); !strings.Contains(w.Body.String(), `"unseen":1`) {
		t.Fatal("one person's mark cleared another's count")
	}
	if w := request("observer", http.MethodPost, "", `{"seen_through":"made-up"}`); w.Code != 409 {
		t.Fatal("invented mark was accepted")
	}
	right.InTeam = false
	team.Agents[right.ID] = right
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if w := request("owner", http.MethodGet, "?thread=inbox-shared", ""); w.Code != 403 {
		t.Fatal("unsharing failed to revoke the full thread")
	}
	if w := request("observer", http.MethodGet, "", ""); strings.Contains(w.Body.String(), "message:public") {
		t.Fatal("unsharing failed to revoke preview")
	}
}
