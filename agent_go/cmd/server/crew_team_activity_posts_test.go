package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
// The shared team_sync entry point binds every call to the caller's own session
// agent and keeps Only-me agents private; a request to a person is a Needs-you card.
func TestCrewTeamTasksBindOwnAgentAndFollowRequestThreads(t *testing.T) {
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
	snapshot, _ := readCrewTeam("team")
	otherCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other", Username: "other"})
	otherTool := &singleToolCaptureRegistrar{}
	if err := f.api.registerCrewTeamChatTool(otherTool, newProductToolGate(nil), &UserClaims{UserID: "other", Username: "other"}, false, "session-two", externalCommandCrewRoot, "", ""); err != nil {
		t.Fatal(err)
	}
	// #2853: a request to a person is their Needs-you card and their click is the
	// reply in its thread.
	oldAPI, oldTurn := runningServerAPI, agentMessageTurn
	runningServerAPI = f.api
	agentMessageTurn = func(*StreamingAPI, context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { runningServerAPI, agentMessageTurn = oldAPI, oldTurn })
	chatOf := func(id string) triggerLinkCaller {
		row := snapshot.Agents[id]
		return triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "team", ProfileID: crewProfileID}, Label: row.Tab, Chat: &codeChat{Key: row.ChatKey, SessionID: "session-" + row.Tab}}
	}
	threadOf := func(inbox string) agentConversation {
		t.Helper()
		agentMessageMu.Lock()
		s, err := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range s.Conversations {
			if c.ID == inbox {
				return c
			}
		}
		t.Fatalf("no thread %s", inbox)
		return agentConversation{}
	}
	stateOf := func(inbox string) string {
		c := threadOf(inbox)
		state, _ := agentTaskState(c, c.Messages[0], time.Now())
		return state
	}
	ask, err := f.api.sendAgentTeamRequest(ctx, "owner", chatOf(own), "person:other", "Please approve the release", "ask-other", map[string]any{"ref": "#2965", "buttons": []any{"Approve", "Not now"}, "files": []any{"https://example.com/notes", "notes/local.md"}, "attachments": []any{map[string]any{"file_name": "notes.md", "file_base64": base64.StdEncoding.EncodeToString([]byte("# Notes"))}}})
	if err != nil {
		t.Fatal(err)
	}
	askID, askInbox := ask["message_id"].(string), ask["inbox_id"].(string)
	if !slices.ContainsFunc(crewWorkNeedsView(snapshot, "other"), func(n *crewWorkNeed) bool { return n.ID == askID && n.ForMe && n.Link == "#2965" }) {
		t.Fatal("the person asked does not see the request as a card")
	}
	// A card is only for the person asked: the asker and other members do not see it (owner, 19 Nov).
	if slices.ContainsFunc(crewWorkNeedsView(snapshot, "owner"), func(n *crewWorkNeed) bool { return n.ID == askID }) {
		t.Fatal("a card shows to someone who was not asked")
	}
	// Tracking lives in the project's issue tracker (owner, 19 Nov): there is no Team or self target.
	for _, target := range []string{"team", "self"} {
		if _, handled, _ := f.api.agentTeamToolSend(ctx, "owner", chatOf(own), map[string]interface{}{"target": target, "message": "Do this"}); handled {
			t.Fatalf("target %s still makes a request", target)
		}
	}
	if err := agentTeamThreadRefusal(ctx, "owner", chatOf(own), askInbox); err == nil || !strings.Contains(err.Error(), "answers this from the Team panel") {
		t.Fatalf("the asker wrote into the person's card: %v", err)
	}
	// A file sent with the request opens for whoever sees the card, and only by that request.
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := crewTeamTasks(ctx, snapshot, "other", store)
	card := seen[slices.IndexFunc(seen, func(task crewTeamTask) bool { return task.ID == askID })]
	if len(card.Links) != 1 || card.Links[0] != "https://example.com/notes" || len(card.Files) != 1 || card.Files[0].Name != "notes.md" {
		t.Fatalf("card links and files: %+v %+v", card.Links, card.Files)
	}
	if rec, code, _ := crewTeamTaskFileFor(ctx, seen, askID, card.Files[0].ID); code != 200 || rec.Name != "notes.md" {
		t.Fatalf("the person asked can't open the card's file: %d", code)
	}
	if _, code, _ := crewTeamTaskFileFor(ctx, seen, "msg-other", card.Files[0].ID); code != 404 {
		t.Fatalf("a file opened through another request: %d", code)
	}
	if _, err := reg.exec(ctx, map[string]any{"action": "work_answer", "need": askID, "button": "Approve"}); err == nil {
		t.Fatal("someone other than the person asked answered the request")
	}
	if _, err := otherTool.exec(otherCtx, map[string]any{"action": "work_answer", "need": askID, "button": "approve", "comment": "Ship it after the notes"}); err != nil {
		t.Fatalf("the person asked could not answer: %v", err)
	}
	if reply := threadOf(askInbox).Messages[1]; stateOf(askInbox) != "done" || reply.From != 1 || !strings.Contains(reply.Text, "Approve") || !strings.Contains(reply.Text, "Ship it after the notes") {
		t.Fatalf("the click is not the reply that closes the request: %+v", reply)
	}
	// The answer is the one Activity line a request makes.
	views := crewTeamActivityPostsFor(snapshot, "owner")
	if !slices.ContainsFunc(views.Work, func(p crewTeamActivityPost) bool { return p.ID == "request:"+askID && p.Event == "answered" }) || len(views.Doing) != 0 || len(views.Decisions) != 0 {
		t.Fatalf("Activity from a request thread: %+v", views)
	}
	// #3469: another person's ask runs in the agent's own chat, with its source from trusted ask provenance being that
	// very chat. It cannot choose another chat as its source.
	shared := &singleToolCaptureRegistrar{}
	if err := f.api.registerCrewTeamChatTool(shared, newProductToolGate(nil), &UserClaims{UserID: "owner", Username: "owner"}, true, "session-one", externalCommandCrewRoot, "other", "team:chat:one"); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.exec(ctx, map[string]any{"action": "work_brief"}); err != nil {
		t.Fatalf("another person's ask in the agent's own chat could not read its brief: %v", err)
	}
	if _, err := f.api.crewTeamActivityAgentForSession(ctx, &UserClaims{UserID: "owner"}, nil, "team", "team", "session-one", "team:chat:one"); err != nil {
		t.Fatalf("another person's ask in the agent's own chat lost its agent: %v", err)
	}
	if _, err := f.api.crewTeamActivityAgentForSession(ctx, &UserClaims{UserID: "owner"}, nil, "team", "team", "session-one", "team:chat:two"); err == nil {
		t.Fatal("foreign source record admitted for another person's ask")
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
	if brief := call(map[string]any{"action": "work_brief"}); brief["hint"] == nil {
		t.Fatalf("brief: %v", brief)
	}
	public := crewTeamActivityPostsFor(snapshot, "owner")
	facts := crewAccessFacts{TeamKnown: true, ServerCrew: true, Team: &snapshot, Posts: public, Viewer: "owner"}
	finding := false
	for _, row := range crewAccessTree(facts) {
		finding = finding || row.Kind == "team_live" && row.Detail == "Daily report: One checked item"
	}
	if !finding {
		t.Fatal("the card lost the recent finding")
	}
	facts.SuppressLive = true
	for _, row := range crewAccessTree(facts) {
		if row.Kind == "team_live" {
			t.Fatal("retained post text made a stale live claim")
		}
	}
	postHTTP := func(person, agent string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.api.handleCrewWorkLog(w, adminRequest(http.MethodGet, "/api/crew-teams/team/work?op=work_brief&agent_id="+agent, "", &UserClaims{UserID: person, Username: person}, map[string]string{"project": "team"}))
		return w
	}
	if w := postHTTP("other", own); w.Code != 403 {
		t.Fatalf("foreign agent admitted: %d %s", w.Code, w.Body.String())
	}
	if w := postHTTP("owner", private); w.Code != 200 {
		t.Fatalf("own Only me agent refused: %d %s", w.Code, w.Body.String())
	}
	if _, err := reg.exec(ctx, map[string]any{"action": "work_brief", "project": "another-crew"}); err == nil {
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
	if _, err := shared.exec(ctx, map[string]any{"action": "work_brief"}); err == nil {
		t.Fatal("shared ask read Team tasks after its source was unshared")
	}

	// A person over MCP (Claude Cowork, ChatGPT) acts as themselves; nobody may act as another person.
	if _, err := crewWorkDo(ctx, crewWorkScope{Project: "team", Team: &saved}, "other", crewWorkPersonActor("other"), crewWorkInput{Op: "work_brief"}); err != nil {
		t.Fatal(err)
	}
	if _, err := crewWorkDo(ctx, crewWorkScope{Project: "team", Team: &saved}, "owner", crewWorkPersonActor("other"), crewWorkInput{Op: "work_brief"}); err == nil {
		t.Fatal("acted as another person")
	}
	// Team off: the brief is the person's own agents' asks.
	offTeam := saved
	offTeam.On = false
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), offTeam, 0600); err != nil {
		t.Fatal(err)
	}
	if brief, err := reg.exec(ctx, map[string]any{"action": "work_brief"}); err != nil || !strings.Contains(brief, "my_tasks") {
		t.Fatalf("Team-off brief: %s %v", brief, err)
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), saved, 0600); err != nil {
		t.Fatal(err)
	}

	// Refusal bodies never include a storage path or saved post text.
	if w := postHTTP("other", own); strings.Contains(w.Body.String(), dir) {
		t.Fatal("refusal leaked internal post state")
	}
}
