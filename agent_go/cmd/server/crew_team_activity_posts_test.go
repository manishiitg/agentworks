package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"path/filepath"
)

// PLAT-746-904: the registered chat/MCP tool and the HTTP route share one writer;
// an In-team agent posts as its actual session, not a selected name or another person.
func TestCrewTeamActivityPostsBindOwnSharedAgentAndKeepDecisions(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"one": true, "another": true, "private": false}, "other": {"two": true}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	for _, person := range []string{"owner", "other"} {
		entries := map[string]ProductConversationRecord{}
		for _, agent := range team.Agents {
			if agent.OwnerID == person {
				entries[productConversationRegistryEntryKey(crewProfileID, agent.ChatKey)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: agent.ChatKey, SessionID: "session-" + agent.Tab, WorkspacePath: externalCommandCrewRoot}
			}
		}
		if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath(person), productConversationRegistryDocument{Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	var own, foreign, private string
	for _, agent := range team.Agents {
		switch {
		case agent.OwnerID == "owner" && agent.Tab == "one":
			own = agent.ID
		case agent.OwnerID == "other":
			foreign = agent.ID
		case !agent.InTeam:
			private = agent.ID
		}
	}
	reg := &singleToolCaptureRegistrar{}
	if err := f.api.registerCrewTeamChatTool(reg, newProductToolGate(nil), &UserClaims{UserID: "owner", Username: "owner"}, false, "session-one", externalCommandCrewRoot, "", ""); err != nil {
		t.Fatal(err)
	}
	call := func(action, line, why string) {
		t.Helper()
		_, err := reg.exec(ctx, map[string]any{"action": action, "line": line, "why": why, "agent_key": foreign})
		if err != nil {
			t.Fatalf("shared agent tool %s failed: %v", action, err)
		}
	}
	call("doing", "Checking the shared report", "")
	call("doing", "Reviewing the final report", "")
	call("decision", "Use the reviewed report", "It contains the confirmed totals")
	call("decision", "Keep the source notes", "They explain the result")
	posts, err := readCrewTeamActivityPosts("team")
	if err != nil || len(posts.Doing) != 1 || posts.Doing[own].Line != "Reviewing the final report" || len(posts.Decisions) != 2 || posts.Decisions[0].AgentID != own {
		t.Fatalf("post replacement/identity/retention: %+v %v", posts, err)
	}
	// A shared/standby thread is the same agent, with its source provided by
	// trusted ask provenance. It cannot choose another person's source chat.
	ownerDoc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath("owner"))
	if err != nil {
		t.Fatal(err)
	}
	thread := sharedThreadKey("team", "one", "other")
	ownerDoc.Entries[productConversationRegistryEntryKey(crewProfileID, thread)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: thread, SessionID: "session-thread", WorkspacePath: externalCommandCrewRoot}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), ownerDoc); err != nil {
		t.Fatal(err)
	}
	shared := &singleToolCaptureRegistrar{}
	if err := f.api.registerCrewTeamChatTool(shared, newProductToolGate(nil), &UserClaims{UserID: "owner", Username: "owner"}, true, "session-thread", externalCommandCrewRoot, "other", "team:chat:one"); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.exec(ctx, map[string]any{"action": "doing", "line": "Reviewing the final report"}); err != nil {
		t.Fatalf("admitted shared-thread agent could not post: %v", err)
	}
	if _, err := f.api.crewTeamActivityAgentForSession(ctx, &UserClaims{UserID: "owner"}, nil, "team", "team", "session-thread", "team:chat:two"); err == nil {
		t.Fatal("foreign source record admitted for a shared thread")
	}
	// PLAT-856-564: a finding is accepted only while a schedule started the
	// session's turn; the server supplies the schedule name and the agent.
	if _, err := reg.exec(ctx, map[string]any{"action": "finding", "line": "One checked item"}); err == nil || !strings.Contains(err.Error(), "Findings come from scheduled runs") {
		t.Fatalf("finding outside a schedule turn was not refused: %v", err)
	}
	end := beginCrewScheduleTurn("session-one", productScheduleJob{ProjectID: "team", UserID: "owner", Profile: agentprofiles.Profile{ID: crewProfileID}, Schedule: productschedule.Schedule{Name: "Daily report"}})
	if _, err := reg.exec(ctx, map[string]any{"action": "finding", "line": "One checked item"}); err != nil {
		t.Fatalf("finding in a schedule turn was refused: %v", err)
	}
	end()
	if posts, _ := readCrewTeamActivityPosts("team"); len(posts.Findings) != 1 || posts.Findings[0].Schedule != "Daily report" || posts.Findings[0].AgentID != own {
		t.Fatalf("finding identity: %+v", posts.Findings)
	}
	text, err := reg.exec(ctx, map[string]any{"action": "decisions"})
	if err != nil {
		t.Fatal(err)
	}
	var recent map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &recent) != nil || len(recent) != 1 || recent["decisions"] == nil {
		t.Fatalf("recent-decisions read was not short and specific: %s", text)
	}
	snapshot, _ := readCrewTeam("team")
	public := crewTeamActivityPostsFor(snapshot)
	public.Decisions[0].At = time.Now().Add(-25 * time.Hour)
	facts := crewAccessFacts{TeamKnown: true, ServerCrew: true, Team: &snapshot, Posts: public, Viewer: "owner"}
	live := 0
	for _, row := range crewAccessTree(facts) {
		if row.Kind == "team_live" {
			live++
		}
	}
	if live != 2 {
		t.Fatalf("card should have one recent decision and one finding, not the old decision: %d", live)
	}
	facts.SuppressLive = true
	for _, row := range crewAccessTree(facts) {
		if row.Kind == "team_live" {
			t.Fatal("retained post text made a stale live claim")
		}
	}
	postHTTP := func(person, agent string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"kind": "doing", "agent_id": agent, "line": "Not this agent"})
		w := httptest.NewRecorder()
		f.api.handleCrewTeamActivityPosts(w, adminRequest(http.MethodPost, "/api/crew-teams/team/activity-posts", string(raw), &UserClaims{UserID: person, Username: person}, map[string]string{"project": "team"}))
		return w
	}
	if w := postHTTP("other", own); w.Code != 403 {
		t.Fatalf("foreign agent admitted: %d %s", w.Code, w.Body.String())
	}
	if w := postHTTP("owner", private); w.Code != 403 {
		t.Fatalf("Only me agent admitted: %d %s", w.Code, w.Body.String())
	}
	if _, err := reg.exec(ctx, map[string]any{"action": "doing", "project": "another-crew", "line": "Wrong Crew"}); err == nil {
		t.Fatal("tool posted across Crews")
	}
	forged, _ := json.Marshal(map[string]any{"kind": "finding", "agent_id": own, "line": "Not a schedule run", "schedule": "Forged schedule"})
	w := httptest.NewRecorder()
	f.api.handleCrewTeamActivityPosts(w, adminRequest(http.MethodPost, "/api/crew-teams/team/activity-posts", string(forged), &UserClaims{UserID: "owner", Username: "owner"}, map[string]string{"project": "team"}))
	if w.Code != 400 {
		t.Fatal("agent-facing route forged an automatic schedule finding")
	}
	// Explicit posts remain stored, but do not bypass a later Only me choice.
	saved, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	agent := saved.Agents[own]
	agent.InTeam = false
	saved.Agents[own] = agent
	dir, _ := crewTeamDir("team")
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), saved, 0600); err != nil {
		t.Fatal(err)
	}
	if visible := crewTeamActivityPostsFor(saved); len(visible.Doing) != 0 || len(visible.Decisions) != 0 {
		t.Fatal("retained post bypassed Only me")
	}
	stored, _ := readCrewTeamActivityPosts("team")
	if len(stored.Decisions) != 2 {
		t.Fatal("hiding an agent deleted retained decisions")
	}
	if _, err := writeCrewTeamActivityPost(ctx, "team", "owner", private, "doing", "Private progress", "", ""); err == nil {
		t.Fatal("shared writer admitted Only me")
	}
	// Refusal bodies never include a storage path or saved post text.
	if w := postHTTP("other", own); strings.Contains(w.Body.String(), "confirmed totals") || strings.Contains(w.Body.String(), dir) {
		t.Fatal("refusal leaked internal post state")
	}
}
