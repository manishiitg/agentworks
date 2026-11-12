package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

// Real workspace HTTP and persisted registry/history, with only the paid model
// turn replaced. Pin the shared-tab decision across native chat and MCP.
func TestCrewSharedAgentsKeepExistingConversationAndAuthority(t *testing.T) {
	// PLAT-746-820: real workspace preparation exceeded five seconds on the
	// build host, letting cleanup remove the model stub before delivery reached it.
	const deliveryTimeout = 30 * time.Second
	f, _ := newExternalCrewCommandFixture(t)
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	const callerRoot = "Crew/calling-team"
	if err := os.MkdirAll(filepath.Join(f.docs, callerRoot), 0700); err != nil {
		t.Fatal(err)
	}
	// A Crew's records live in its .agentworks/settings (PLAT-746-924).
	const callerState = callerRoot + "/.agentworks/settings"
	f.write(t, callerState+"/product.json", `{"schema_version":1,"product":"work","id":"calling","title":"Calling Team","session_id":"session-calling"}`)
	f.write(t, callerState+"/workflow.json", `{"id":"calling","label":"Calling Team","created_by":"other"}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "calling-team", ProjectID: "calling", OwnerID: "other", Shared: true, Private: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTH_SECRET", "shared-agent-fixture-signing-key")
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	create := func(person, key, name, purpose string) ProductConversationRecord {
		t.Helper()
		personCtx := internalBotRequestContext(t.Context(), person)
		binding, _, err := resolveConversationBindingForUser(personCtx, person, profile, key)
		if err != nil {
			t.Fatal(err)
		}
		record, err := defaultProductConversationRegistryStore().resolveOrCreate(personCtx, person, profile, binding, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = defaultProductConversationRegistryStore().setAgentIdentity(personCtx, person, "work", key, &name, &purpose, person); err != nil {
			t.Fatal(err)
		}
		return record
	}
	coach := create("owner", "team:chat:coach", "Coach", "Help the team prepare useful feedback.")
	private := create("owner", "team:chat:private", "Private planning", "Owner planning only.")
	reg := &recordingRegistrar{}
	owner := &UserClaims{UserID: "owner", Username: "Owner"}
	if err := f.api.registerDashboardTools(reg, "owner", externalCommandCrewRoot, owner); err != nil {
		t.Fatal(err)
	}
	projectRecordedCatalog(t, reg)
	if _, err := reg.tools["share"].exec(ctx, map[string]any{"action": "set", "mode": "private", "shared_agents": []any{coach.ConversationKey}, "agent_purposes": map[string]any{coach.ConversationKey: "Review a situation and suggest practical next steps."}}); err != nil {
		t.Fatal(err)
	}
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if got := crewSharedAgentKeys(externalCommandCrewRoot); len(got) != 1 || got[0] != coach.ConversationKey {
		t.Fatalf("native selection: %v", got)
	}
	reader := &UserClaims{UserID: "other", Username: "Colleague", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:run", "dashboards:read"}, CrewIDs: []string{"team"}}}
	call := func(action string, extra map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		args := map[string]any{"action": action, "workspace_id": "work:team"}
		for k, v := range extra {
			args[k] = v
		}
		w := httptest.NewRecorder()
		body, _ := json.Marshal(map[string]any{"name": "workspace", "arguments": args})
		req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(string(body)))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, reader))
		f.api.handleExternalCall(w, req)
		return w
	}
	{
		w := call("chats", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Coach") || !strings.Contains(w.Body.String(), "practical next steps") || strings.Contains(w.Body.String(), "Private planning") || strings.Contains(w.Body.String(), "provider_connections") {
			t.Fatalf("filtered list: %d %s", w.Code, w.Body.String())
		}
	}
	// The agent retains old context; shared readers see only the current sharing period.
	since := crewSharedAgentSince(externalCommandCrewRoot, coach.ConversationKey)
	if since.IsZero() {
		t.Fatal("sharing did not establish a history boundary")
	}
	if err := defaultProjectOwners().SetAudience("work", "test-team", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := crewSharedAgentSince(externalCommandCrewRoot, coach.ConversationKey); !got.Equal(since) {
		t.Fatal("audience edit reset the continuously shared history")
	}
	// PLAT-856-509: a sender reads only their own exchange with the shared agent: not the owner's words in that tab,
	// not another sender's message.
	transcript := fmt.Sprintf(`{"ui_events":[{"sequence":1,"timestamp":%q,"type":"user_message","data":{"data":{"content":"Private earlier context"}}},{"sequence":2,"timestamp":%q,"type":"user_message","data":{"data":{"content":"Shared owner context"}}},{"sequence":3,"timestamp":%q,"type":"user_message","data":{"data":{"content":"internal envelope","metadata":{"turn_id":"t-other","source":"agent_message","sender_label":"Colleague","sender_user_id":"other","display_content":"Please review this"}}}},{"sequence":4,"timestamp":%q,"type":"unified_completion","data":{"data":{"final_result":"Reviewed for you","metadata":{"turn_id":"t-other"}}}},{"sequence":5,"timestamp":%q,"type":"user_message","data":{"data":{"content":"third envelope","metadata":{"turn_id":"t-third","source":"agent_message","sender_label":"Third","sender_user_id":"third","display_content":"Third person's note"}}}}]}`, since.Add(-time.Hour).Format(time.RFC3339Nano), since.Format(time.RFC3339Nano), since.Format(time.RFC3339Nano), since.Format(time.RFC3339Nano), since.Format(time.RFC3339Nano))
	f.write(t, externalCommandCrewState+"/builder/conversation/2026-10-24/session-"+coach.SessionID+"-conversation.json", transcript)
	{
		w := call("read_chat", map[string]any{"chat_id": coach.ConversationKey})
		body := w.Body.String()
		if w.Code != 200 || strings.Contains(body, "Private earlier context") || strings.Contains(body, "Shared owner context") || strings.Contains(body, "Third person's note") || strings.Contains(body, `"role":"you"`) || !strings.Contains(body, `"sender":"Colleague"`) || !strings.Contains(body, "Please review this") || !strings.Contains(body, "Reviewed for you") || strings.Contains(body, "internal envelope") {
			t.Fatalf("shared transcript: %d %s", w.Code, w.Body.String())
		}
		w = call("read_chat", map[string]any{"chat_id": private.ConversationKey})
		if w.Code < 400 {
			t.Fatalf("unselected history: %s", w.Body.String())
		}
	}
	if w := call("read_chat", map[string]any{"session_id": coach.SessionID}); w.Code != 200 || strings.Contains(w.Body.String(), "Shared owner context") || !strings.Contains(w.Body.String(), "Please review this") {
		t.Fatalf("session read must use the same shared transcript: %d %s", w.Code, w.Body.String())
	}
	otherCtx := internalBotRequestContext(t.Context(), "other")
	address := "workspace:work:team/agent/" + coach.ConversationKey
	peer, err := resolveTriggerTarget(otherCtx, GetUserFromContext(otherCtx), address)
	if err != nil || peer.Chat == nil || peer.Chat.SessionID != coach.SessionID {
		t.Fatalf("Builder agent address did not resolve the same existing tab: %+v %v", peer.Chat, err)
	}
	directory, err := listAgentDirectory(otherCtx, GetUserFromContext(otherCtx))
	if err != nil {
		t.Fatal(err)
	}
	directoryJSON, _ := json.Marshal(directory)
	if !strings.Contains(string(directoryJSON), address) || strings.Contains(string(directoryJSON), "Private planning") {
		t.Fatalf("Builder agent directory: %s", directoryJSON)
	}
	// The list says how each Crew relates to the person: shared with them, and "this Crew" when asked from inside it (owner, 7 Nov).
	relationOf := func(c context.Context) string {
		d, err := listAgentDirectory(c, GetUserFromContext(c))
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range d["agents"].([]map[string]interface{}) {
			if row["kind"] == "crew" && row["crew_id"] == "team" {
				relation, _ := row["relation"].(string)
				return relation
			}
		}
		return ""
	}
	if got, here := relationOf(otherCtx), relationOf(withDirectoryThisCrew(otherCtx, externalCommandCrewRoot)); got != "shared" || here != "this_crew" {
		t.Fatalf("directory relation: shared=%q this=%q", got, here)
	}
	privatePeer := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerCrew, ID: "team", Profile: "work", Path: externalCommandCrewRoot, ChatKey: private.ConversationKey, Session: private.SessionID}
	for _, send := range []bool{false, true} {
		if err := f.api.externalAuthorizeMessagePeer(context.WithValue(otherCtx, UserContextKey, reader), reader, privatePeer, send); err == nil {
			t.Fatalf("unselected owner tab admitted send=%t", send)
		}
	}
	for _, action := range []string{"stop", "close_chat", "rename_chat"} {
		w := call(action, map[string]any{"chat_id": coach.ConversationKey, "name": "Takeover"})
		if w.Code < 400 {
			t.Fatalf("shared recipient acquired %s: %s", action, w.Body.String())
		}
	}
	modelArgs := map[string]any{"workspace_id": "work:team", "action": "set", "scope": "tab", "chat_id": coach.ConversationKey, "engine": ""}
	if response := codeReviewCall(f.api, reader, "models", modelArgs); response.Code != http.StatusForbidden {
		t.Fatalf("shared recipient changed a model: %d %s", response.Code, response.Body)
	}
	// Read/run consent can use agents but cannot edit even the caller's own project.
	reader.UserID = "owner"
	for _, action := range []string{"open_chat", "rename_chat", "stop", "chat_tool", "update"} {
		w := call(action, map[string]any{"chat_id": "coach", "name": "New name", "tool_name": "perform_ui_action", "changes": map[string]any{"purpose": "Changed"}})
		if w.Code < 400 {
			t.Fatalf("run-only connection acquired owner action %s: %s", action, w.Body.String())
		}
	}
	body, _ := json.Marshal(map[string]any{"workspace": externalCommandCrewRoot, "action": "create", "dashboard_id": "unauthorized", "title": "Unauthorized", "files": map[string]string{"index.tsx": "export default function Page(){return null}"}})
	dashboardRequest := httptest.NewRequest(http.MethodPost, "/api/dashboards", strings.NewReader(string(body)))
	dashboardRequest = dashboardRequest.WithContext(context.WithValue(dashboardRequest.Context(), UserContextKey, reader))
	dashboardResponse := httptest.NewRecorder()
	f.api.handleDashboards(dashboardResponse, dashboardRequest)
	if dashboardResponse.Code != 403 {
		t.Fatalf("run-only connection acquired dashboard authoring: %d %s", dashboardResponse.Code, dashboardResponse.Body.String())
	}
	reader.UserID = "other"
	delivered := make(chan string, 2)
	old := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, ctx context.Context, req map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		if session != coach.SessionID || user != "owner" {
			return internalSessionTurnResult{}, fmt.Errorf("wrong existing tab: %s %s", session, user)
		}
		if restricted, _ := req["crew_ask_read_only"].(bool); restricted {
			return internalSessionTurnResult{}, fmt.Errorf("selected Builder agent demoted")
		}
		actor := costobserver.AttributionFromContext(ctx).ActorID
		if actor != "other" {
			return internalSessionTurnResult{}, fmt.Errorf("wrong caller cost attribution %q", actor)
		}
		delivered <- session
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { agentMessageTurn = old })
	var receipt map[string]any
	for n := range 2 {
		w := call("ask", map[string]any{"chat_id": coach.ConversationKey, "message": "Please help with this feedback.", "submission_id": fmt.Sprintf("shared-agent-message-%d", n)})
		if w.Code != 200 {
			t.Fatalf("send %d: %d %s", n, w.Code, w.Body.String())
		}
		select {
		case <-delivered:
		case <-time.After(deliveryTimeout):
			t.Fatal("message did not reach existing owner tab")
		}
		_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	}
	// A different hosted Crew reaches the same agent through the native path.
	caller, err := crewTriggerLinkCaller(callerRoot)(otherCtx)
	if err != nil {
		t.Fatal(err)
	}
	callerTab := create("other", "calling:chat:builder", "Private caller", "Prepare our own work.")
	caller.Chat = &codeChat{OwnerID: "other", Key: callerTab.ConversationKey, ID: callerTab.ConversationKey, SessionID: callerTab.SessionID}
	contactPair := agentConversation{Endpoints: [2]agentMessageEndpoint{callerMessageEndpoint("other", caller), targetMessageEndpoint("other", peer)}}
	if err := f.api.recheckAgentMessageAdmission(ctx, contactPair, agentMessage{From: 1}); err != nil {
		t.Fatalf("shared agent could not reply to its private caller: %v", err)
	}
	if _, err := f.api.sendAgentMessage(otherCtx, "other", caller, peer, "Please review our feedback.", "", "hosted-crew-shared-agent"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
	case <-time.After(deliveryTimeout):
		t.Fatal("hosted Crew did not reach the same existing tab")
	}
	var contact agentConversation
	deadline := time.Now().Add(deliveryTimeout)
	for time.Now().Before(deadline) {
		agentMessageMu.Lock()
		store, err := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range store.Conversations {
			if c.ID == receipt["inbox_id"] {
				contact = c
			}
		}
		if len(contact.Messages) > 0 && contact.Messages[0].Delivery == "delivered" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(contact.Messages) == 0 || contact.Endpoints[0].UserID != "other" || contact.Endpoints[1].Session != coach.SessionID {
		t.Fatalf("wrong attributed contact: %+v", contact)
	}
	// Removing the selected agent invalidates old reply addresses and pending delivery.
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, []string{"other"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.api.recheckAgentMessageAdmission(ctx, contactPair, agentMessage{From: 1}); err == nil {
		t.Fatal("shared agent retained reply access after unsharing")
	}
	if err := f.api.recheckAgentMessageAdmission(ctx, contact, contact.Messages[0]); err == nil {
		t.Fatal("queued work retained revoked agent grant")
	}
	for _, action := range []string{"ask", "read_chat"} {
		if w := call(action, map[string]any{"chat_id": coach.ConversationKey, "message": "old contact"}); w.Code < 400 {
			t.Fatalf("revoked %s: %s", action, w.Body.String())
		}
	}
	if _, err := f.api.sendAgentMessage(internalBotRequestContext(ctx, "other"), "other", externalCrewCaller(&UserClaims{UserID: "other", Username: "Colleague"}), triggerTarget{}, "old inbox", contact.ID, "revoked-reply"); err == nil {
		t.Fatal("old inbox retained selected-tab authority")
	}
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, []string{"other"}, []string{coach.ConversationKey}); err != nil {
		t.Fatal(err)
	}
	if got := crewSharedAgentSince(externalCommandCrewRoot, coach.ConversationKey); !got.After(since) {
		t.Fatal("re-sharing did not start a fresh history boundary")
	}
	if w := call("read_chat", map[string]any{"chat_id": coach.ConversationKey}); w.Code != 200 || strings.Contains(w.Body.String(), "Shared owner context") {
		t.Fatalf("re-shared history: %d %s", w.Code, w.Body.String())
	}
	if got := f.read(t, externalCommandCrewState+"/builder/conversation/2026-10-24/session-"+coach.SessionID+"-conversation.json"); !strings.Contains(got, "Private earlier context") {
		t.Fatal("sharing changed the original agent context")
	}

	// PLAT-746-820: network-visible Team agents are reachable from an unrelated
	// private Crew chat, with replies bound to that exact originating chat.
	// Keep the fixture's state root: it holds both registered Crew owners.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}}
	team.Agents = crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {coach.ConversationKey: true, private.ConversationKey: false}})
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	team = mustReadCrewTeam(t, "team")
	shared, _ := team.registeredAgent("owner", coach.ConversationKey)
	privateAgent, _ := team.registeredAgent("owner", private.ConversationKey)
	directoryHas := func(id string) bool {
		t.Helper()
		out, err := listAgentDirectory(withDirectoryThisCrew(otherCtx, callerRoot), GetUserFromContext(otherCtx))
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range out["agents"].([]map[string]interface{}) {
			if row["chat_id"] == id {
				return true
			}
		}
		return false
	}
	if !directoryHas(shared.ID) || directoryHas(privateAgent.ID) {
		t.Fatal("directory visibility differs from Team privacy")
	}
	target, err := f.api.resolveAgentChatTarget(otherCtx, triggerTarget{Kind: triggerCallerCrew, CrewID: "team", CrewProfile: crewProfileID, CrewOwner: "owner", Path: externalCommandCrewRoot}, shared.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.resolveTeamAgentTarget(otherCtx, target, privateAgent.ID); err == nil {
		t.Fatal("private Team agent was admitted")
	}
	turns := make(chan string, 4)
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, _ map[string]interface{}, session, _ string) (internalSessionTurnResult, error) {
		turns <- session
		return internalSessionTurnResult{}, nil
	}
	result, err := f.api.sendAgentMessage(otherCtx, "other", caller, target, "Ask across the network", "", "team-cross-crew")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case session := <-turns:
		if session != coach.SessionID {
			t.Fatalf("wrong target session: %s", session)
		}
	case <-time.After(deliveryTimeout):
		t.Fatal("Team delivery did not reach target")
	}
	inbox := result["inbox_id"].(string)
	receiver := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ProfileID: crewProfileID, ID: "team"}, Path: externalCommandCrewRoot, Chat: &codeChat{OwnerID: "owner", Key: coach.ConversationKey, SessionID: coach.SessionID}}
	if _, err := f.api.sendAgentMessage(ctx, "owner", receiver, triggerTarget{}, "Reply to the private caller", inbox, "team-reply"); err != nil {
		t.Fatal(err)
	}
	select {
	case session := <-turns:
		if session != callerTab.SessionID {
			t.Fatalf("reply went to %s instead of original chat", session)
		}
	case <-time.After(deliveryTimeout):
		t.Fatal("reply did not reach original chat")
	}
	wrong := caller
	wrong.Chat = &codeChat{Key: "calling:chat:someone-else", SessionID: "another-session"}
	if _, err := f.api.readAgentMessages(otherCtx, "other", wrong, inbox, 0, 30, 0); err == nil {
		t.Fatal("another chat read the private reply inbox")
	}
	// Hiding an agent invalidates both existing replies and queued delivery,
	// even though the caller remains a member of the Team.
	pair := agentConversation{Endpoints: [2]agentMessageEndpoint{callerMessageEndpoint("other", caller), targetMessageEndpoint("other", target)}}
	if err := f.api.recheckAgentMessageAdmission(otherCtx, pair, agentMessage{From: 0}); err != nil {
		t.Fatalf("allowed queued Team message: %v", err)
	}
	if err := updateCrewTeamRegisteredAgent("team", "owner", shared.ID, func(a *crewTeamRegisteredAgent) { a.InTeam = false }); err != nil {
		t.Fatal(err)
	}
	if directoryHas(shared.ID) {
		t.Fatal("hidden Team agent remained listed")
	}
	if err := requireLinkedOwnCrew(otherCtx, "other", caller, target); err == nil {
		t.Fatal("stale target bypassed visibility at link admission")
	}
	if err := f.api.recheckAgentMessageAdmission(otherCtx, pair, agentMessage{From: 0}); err == nil {
		t.Fatal("queued send bypassed agent privacy")
	}
	if _, err := f.api.sendAgentMessage(ctx, "owner", receiver, triggerTarget{}, "Hidden reply", inbox, "team-hidden"); err == nil {
		t.Fatal("existing reply bypassed agent privacy")
	}
	if err := updateCrewTeamRegisteredAgent("team", "owner", shared.ID, func(a *crewTeamRegisteredAgent) { a.InTeam = true }); err != nil {
		t.Fatal(err)
	}
	// Membership loss removes discovery and invalidates existing reply contacts.
	team.People = nil
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if directoryHas(shared.ID) {
		t.Fatal("non-member directory retained Team agent")
	}
	if _, err := f.api.resolveTeamAgentTarget(otherCtx, target, shared.ID); err == nil {
		t.Fatal("removed member could send")
	}
	if _, err := f.api.sendAgentMessage(ctx, "owner", receiver, triggerTarget{}, "Late reply", inbox, "team-revoked"); err == nil {
		t.Fatal("reply bypassed membership revocation")
	}

}
