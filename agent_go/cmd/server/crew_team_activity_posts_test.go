package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
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
// #2871: the work log binds every entry to the caller's own session agent,
// refuses a task another agent started, and keeps Only-me entries private.
func TestCrewWorkLogBindsOwnAgentClaimsTasksAndKeepsDecisions(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")

	// #2871: a never-written Mac/personal Crew has no team-folders directory.
	// Its first work_add must create the log, and work_start must see that task.
	scope := crewWorkScope{Project: "fresh-work-log"}
	actor := crewTeamRegisteredAgent{OwnerID: "owner", ChatKey: "fresh-work-log:chat:own", Name: "Helper"}
	_, path, err := crewWorkPath(scope.Project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("fixture already has a directory: %v", err)
	}
	if _, err := crewWorkDo(t.Context(), scope, "owner", actor, crewWorkInput{Op: "work_add", Label: "#first", Title: "First task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := crewWorkDo(t.Context(), scope, "owner", actor, crewWorkInput{Op: "work_start", Task: "#first"}); err != nil {
		t.Fatal(err)
	}
	entries, err := readCrewWorkLog(scope.Project)
	if err != nil || len(entries) != 2 || entries[1].Event != "started" {
		t.Fatalf("first task was not durable: %+v %v", entries, err)
	}
	// Owner, 2026-11-17: one Activity line per task. "added" shows only while
	// the task still waits; a started task shows its "started" line alone.
	if _, err := crewWorkDo(t.Context(), scope, "owner", actor, crewWorkInput{Op: "work_add", Label: "#later", Title: "Queued task"}); err != nil {
		t.Fatal(err)
	}
	_, _, feed := crewWorkActivityViews(scope, "owner")
	lines := map[string]int{}
	for _, item := range feed {
		lines[item.Task+" "+item.Event]++
	}
	if lines["#first added"] != 0 || lines["#first started"] != 1 || lines["#later added"] != 1 {
		t.Fatalf("Activity should show one line per task: %v", lines)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private work file: %v %v", info, err)
	}
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
	call := func(args map[string]any) map[string]any {
		t.Helper()
		args["agent_key"] = foreign // a model-chosen agent is ignored; the session decides
		text, err := reg.exec(ctx, args)
		if err != nil {
			t.Fatalf("work tool %v failed: %v", args["action"], err)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(text), &out)
		return out
	}
	call(map[string]any{"action": "work_add", "label": "#1", "title": "Final report"})
	call(map[string]any{"action": "work_start", "task": "#1", "line": "Checking the shared report"})
	call(map[string]any{"action": "work_update", "task": "#1", "line": "Reviewing the final report"})
	first := call(map[string]any{"action": "work_decide", "line": "Use the draft report", "why": "It was first"})
	call(map[string]any{"action": "work_decide", "line": "Use the reviewed report", "why": "It contains the confirmed totals", "replaces": first["id"]})
	call(map[string]any{"action": "work_decide", "task": "#1", "line": "Keep the source notes", "why": "They explain the result"})
	snapshot, _ := readCrewTeam("team")
	posts := crewTeamActivityPostsFor(snapshot, "owner")
	if len(posts.Doing) != 1 || posts.Doing[own].Line != "#1: Reviewing the final report" || len(posts.Decisions) != 2 || posts.Decisions[0].AgentID != own || posts.Decisions[0].Line != "#1: Keep the source notes" {
		t.Fatalf("live line/identity/replaced decision: %+v", posts)
	}
	// First to start takes a task: another person's agent is refused while it is held.
	otherCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other", Username: "other"})
	otherTool := &singleToolCaptureRegistrar{}
	if err := f.api.registerCrewTeamChatTool(otherTool, newProductToolGate(nil), &UserClaims{UserID: "other", Username: "other"}, false, "session-two", externalCommandCrewRoot, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := otherTool.exec(otherCtx, map[string]any{"action": "work_start", "task": "#1"}); err == nil || !strings.Contains(err.Error(), "already taken") {
		t.Fatalf("a held task was taken by a second agent: %v", err)
	}
	if _, err := otherTool.exec(otherCtx, map[string]any{"action": "work_done", "task": "#1", "line": "Finished"}); err == nil {
		t.Fatal("a second agent finished a task it did not hold")
	}
	// #2853 Needs-you cards: an agent asks one person; only that person answers, only the asker withdraws.
	asked := call(map[string]any{"action": "work_need", "for": "other", "task": "#1", "line": "The report needs a review", "buttons": []any{"Approve", "Not now", "Ask my agent"}})
	needID, _ := asked["id"].(string)
	if cards := crewWorkNeedsView(snapshot, "other"); len(cards) != 1 || !cards[0].ForMe || cards[0].Task != "#1" {
		t.Fatalf("the person asked does not see the card: %+v", cards)
	}
	if cards := crewWorkNeedsView(snapshot, "owner"); len(cards) != 1 || cards[0].ForMe {
		t.Fatalf("the asker's person should see who it waits on, without buttons: %+v", cards)
	}
	if _, err := reg.exec(ctx, map[string]any{"action": "work_answer", "need": needID, "button": "Approve"}); err == nil {
		t.Fatal("someone other than the person asked answered the card")
	}
	if _, err := otherTool.exec(otherCtx, map[string]any{"action": "work_withdraw", "need": needID}); err == nil {
		t.Fatal("an agent that did not ask withdrew the card")
	}
	if _, err := otherTool.exec(otherCtx, map[string]any{"action": "work_answer", "need": needID, "button": "Merge"}); err == nil {
		t.Fatal("an answer that is not one of the card's buttons was accepted")
	}
	if _, err := otherTool.exec(otherCtx, map[string]any{"action": "work_answer", "need": needID, "button": "approve"}); err != nil {
		t.Fatalf("the person asked could not answer: %v", err)
	}
	if cards := crewWorkNeedsView(snapshot, "owner"); len(cards) != 0 {
		t.Fatal("an answered card stayed pinned")
	}
	if _, err := crewWorkDo(ctx, crewWorkScope{Project: "team", Team: &snapshot}, "owner", snapshot.Agents[private], crewWorkInput{Op: "work_need", For: "other", Line: "Private ask", Buttons: []string{"Ok"}}); err == nil {
		t.Fatal("an Only-me agent asked a teammate, who cannot see its card")
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
	if _, err := shared.exec(ctx, map[string]any{"action": "work_update", "task": "#1", "line": "Reviewing the final report"}); err != nil {
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
	brief := call(map[string]any{"action": "work_brief"})
	if yours, _ := brief["yours"].([]any); len(yours) != 1 {
		t.Fatalf("brief did not list the agent's own started task: %v", brief)
	}
	if current, _ := brief["decisions"].([]any); len(current) != 2 {
		t.Fatalf("brief should list only current (not replaced) decisions: %v", brief["decisions"])
	}
	if found := call(map[string]any{"action": "work_find", "query": "draft"}); len(found["decisions"].([]any)) != 1 {
		t.Fatalf("find should still reach a replaced decision: %v", found)
	}
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
		raw, _ := json.Marshal(map[string]any{"op": "work_update", "task": "#1", "agent_id": agent, "line": "Not this agent"})
		w := httptest.NewRecorder()
		f.api.handleCrewWorkLog(w, adminRequest(http.MethodPost, "/api/crew-teams/team/work", string(raw), &UserClaims{UserID: person, Username: person}, map[string]string{"project": "team"}))
		return w
	}
	if w := postHTTP("other", own); w.Code != 403 {
		t.Fatalf("foreign agent admitted: %d %s", w.Code, w.Body.String())
	}
	if w := postHTTP("owner", private); w.Code != 200 {
		t.Fatalf("own Only me agent refused: %d %s", w.Code, w.Body.String())
	}
	if _, err := reg.exec(ctx, map[string]any{"action": "work_update", "task": "#1", "project": "another-crew", "line": "Wrong Crew"}); err == nil {
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
	stored, _ := readCrewWorkLog("team")
	if decided := slices.DeleteFunc(stored, func(e crewWorkEntry) bool { return e.Event != "decided" }); len(decided) != 3 {
		t.Fatal("hiding an agent deleted retained decisions")
	}
	if _, err := crewWorkDo(ctx, crewWorkScope{Project: "team", Team: &saved}, "owner", saved.Agents[private], crewWorkInput{Op: "work_decide", Line: "Private progress", Why: "Owner note"}); err != nil {
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
	reply, err := ownTool.exec(ctx, map[string]any{"action": "work_decide", "line": "New private note", "why": "Keep this to myself"})
	if err != nil || !strings.Contains(reply, "Only you see this") {
		t.Fatalf("unregistered own agent: %s %v", reply, err)
	}
	if got := crewTeamActivityPostsFor(saved, "other"); len(got.Decisions) != 0 {
		t.Fatal("unregistered private post leaked")
	}
	if _, err := shared.exec(ctx, map[string]any{"action": "work_update", "task": "#1", "line": "Must not become private"}); err == nil {
		t.Fatal("shared ask posted private activity after source unshared")
	}

	// A person over MCP (Claude Cowork, ChatGPT) writes as themselves, shared with
	// the Team they are in; nobody may write as another person.
	if _, err := crewWorkDo(ctx, crewWorkScope{Project: "team", Team: &saved}, "other", crewWorkPersonActor("other"), crewWorkInput{Op: "work_decide", Line: "Decided over MCP", Why: "From Cowork"}); err != nil {
		t.Fatal(err)
	}
	if got := crewTeamActivityPostsFor(saved, "owner"); !slices.ContainsFunc(got.Decisions, func(p crewTeamActivityPost) bool { return p.Line == "Decided over MCP" }) {
		t.Fatal("a member's MCP decision was not shared with the Team")
	}
	if _, err := crewWorkDo(ctx, crewWorkScope{Project: "team", Team: &saved}, "owner", crewWorkPersonActor("other"), crewWorkInput{Op: "work_decide", Line: "Forged", Why: "x"}); err == nil {
		t.Fatal("wrote as another person")
	}
	// Team off: the work log is personal; each person sees only their own entries.
	offTeam := saved
	offTeam.On = false
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), offTeam, 0600); err != nil {
		t.Fatal(err)
	}
	if reply, err := reg.exec(ctx, map[string]any{"action": "work_add", "label": "#9", "title": "Personal task"}); err != nil || !strings.Contains(reply, "Team is off") {
		t.Fatalf("Team-off add: %s %v", reply, err)
	}
	if own, err := reg.exec(ctx, map[string]any{"action": "work_brief"}); err != nil || !strings.Contains(own, "Personal task") {
		t.Fatalf("owner lost their own Team-off task: %s %v", own, err)
	}
	if theirs, err := otherTool.exec(otherCtx, map[string]any{"action": "work_brief"}); err != nil || strings.Contains(theirs, "Personal task") || strings.Contains(theirs, "Final report") {
		t.Fatalf("Team-off work log leaked to a teammate: %s %v", theirs, err)
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), saved, 0600); err != nil {
		t.Fatal(err)
	}

	// Refusal bodies never include a storage path or saved post text.
	if w := postHTTP("other", own); strings.Contains(w.Body.String(), "confirmed totals") || strings.Contains(w.Body.String(), "Final report") || strings.Contains(w.Body.String(), dir) {
		t.Fatal("refusal leaked internal post state")
	}
}
