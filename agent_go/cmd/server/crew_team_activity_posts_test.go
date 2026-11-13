package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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
	// Web-only regression: a real pending question makes this registered server
	// chat waiting without transferring its private prompt into the Team payload.
	pending := virtualtools.GetHumanFeedbackStore()
	questionID := t.Name() + "-web-wait"
	if err := pending.CreatePendingRequest(questionID, "private owner question", "", "session-one", nil, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	defer pending.SubmitResponse(questionID, "done")
	foundWaiting := false
	for _, agent := range memberTeamAgentsWithMain(ctx, team, "owner", "other", "") {
		if agent.Tab == "one" {
			foundWaiting = agent.Activity == "waiting" && !agent.waitingSince.IsZero() && !strings.Contains(agent.StatusLabel, "private owner question")
		}
	}
	if !foundWaiting {
		t.Fatal("server-only registered chat with a pending owner question did not show waiting")
	}
	// PLAT-746-909: a server-only Crew with Team off still returns array-valued
	// live lists. Null lists crashed the website sidebar after the initial rollout.
	off := team
	off.On = false
	offView := crewTeamTabFor(ctx, off, "owner")
	if offView.Live.Agents == nil || offView.Live.Idle == nil {
		t.Fatal("off-Team live lists must be empty arrays, not null")
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
	public := crewTeamActivityPostsFor(snapshot, "owner")
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
	if w := postHTTP("owner", private); w.Code != 200 {
		t.Fatalf("own Only me agent refused: %d %s", w.Code, w.Body.String())
	}
	if _, err := reg.exec(ctx, map[string]any{"action": "doing", "project": "another-crew", "line": "Wrong Crew"}); err == nil {
		t.Fatal("tool posted across Crews")
	}
	forged, _ := json.Marshal(map[string]any{"kind": "finding", "agent_id": own, "line": "Mac checked item", "schedule": "Mac schedule"})
	w := httptest.NewRecorder()
	f.api.handleCrewTeamActivityPosts(w, adminRequest(http.MethodPost, "/api/crew-teams/team/activity-posts", string(forged), &UserClaims{UserID: "owner", Username: "owner"}, map[string]string{"project": "team"}))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Findings come from scheduled runs") {
		t.Fatalf("agent-facing route forged a schedule finding: %d %s", w.Code, w.Body.String())
	}
	// A Mac Crew's schedule run posts through this route with the Crew's pair header (PLAT-856-564).
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: "one", Name: "One", Audience: "team"}}); err != nil {
		t.Fatal(err)
	}
	macPost := func(pair string) *httptest.ResponseRecorder {
		r := adminRequest(http.MethodPost, "/api/crew-teams/team/activity-posts", string(forged), &UserClaims{UserID: "owner", Username: "owner"}, map[string]string{"project": "team"})
		r.Header.Set(crewMacPairHeader, pair)
		w := httptest.NewRecorder()
		f.api.handleCrewTeamActivityPosts(w, r)
		return w
	}
	if w := macPost("wrong-pair"); w.Code != 403 {
		t.Fatalf("finding with a wrong pair header admitted: %d", w.Code)
	}
	if w := macPost("owner-pair"); w.Code != 200 {
		t.Fatalf("Mac schedule finding refused: %d %s", w.Code, w.Body.String())
	}
	if posts, _ := readCrewTeamActivityPosts("team"); len(posts.Findings) != 2 || posts.Findings[1].Schedule != "Mac schedule" {
		t.Fatalf("Mac finding not stored with its schedule: %+v", posts.Findings)
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
	if visible := crewTeamActivityPostsFor(saved, "other"); len(visible.Doing) != 0 || len(visible.Decisions) != 0 {
		t.Fatal("retained post bypassed Only me")
	}
	stored, _ := readCrewTeamActivityPosts("team")
	if len(stored.Decisions) != 2 {
		t.Fatal("hiding an agent deleted retained decisions")
	}
	if _, err := writeCrewTeamActivityPost(ctx, "team", "owner", private, "decision", "Private progress", "Owner note", ""); err != nil {
		t.Fatal(err)
	}
	ownPosts := crewTeamActivityPostsFor(saved, "owner")
	otherPosts := crewTeamActivityPostsFor(saved, "other")
	if len(ownPosts.Decisions) != 1 || !ownPosts.Decisions[0].Private || ownPosts.Decisions[0].ChatKey == "" || len(otherPosts.Decisions) != 0 {
		t.Fatal("private activity was hidden from owner or disclosed to teammate")
	}
	// Unregistered own sessions also post privately; no model-supplied agent key.
	doc, _ := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath("owner"))
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, "team:chat:unregistered")] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: "team:chat:unregistered", SessionID: "session-unregistered", WorkspacePath: externalCommandCrewRoot, Title: "New agent"}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), doc); err != nil {
		t.Fatal(err)
	}
	ownTool := &singleToolCaptureRegistrar{}
	if err := f.api.registerCrewTeamChatTool(ownTool, newProductToolGate(nil), &UserClaims{UserID: "owner", Username: "owner"}, false, "session-unregistered", externalCommandCrewRoot, "", ""); err != nil {
		t.Fatal(err)
	}
	reply, err := ownTool.exec(ctx, map[string]any{"action": "decision", "line": "New private note", "why": "Keep this to myself"})
	if err != nil || !strings.Contains(reply, "Only you see it") {
		t.Fatalf("unregistered own agent: %s %v", reply, err)
	}
	if got := crewTeamActivityPostsFor(saved, "other"); len(got.Decisions) != 0 {
		t.Fatal("unregistered private post leaked")
	}
	if _, err := shared.exec(ctx, map[string]any{"action": "doing", "line": "Must not become private"}); err == nil {
		t.Fatal("shared ask posted private activity after source unshared")
	}

	// Refusal bodies never include a storage path or saved post text.
	if w := postHTTP("other", own); strings.Contains(w.Body.String(), "confirmed totals") || strings.Contains(w.Body.String(), dir) {
		t.Fatal("refusal leaked internal post state")
	}
}
