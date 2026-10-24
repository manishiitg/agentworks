package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

// Real workspace HTTP and persisted registry/history, with only the paid model
// turn replaced. Pin the shared-tab decision across native chat, website and MCP.
func TestCrewSharedAgentsKeepExistingConversationAndAuthority(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	f.api.productSchedules = NewProductScheduleService(f.api, f.api.agentProfiles)
	const callerRoot = "Crew/calling-team"
	f.write(t, callerRoot+"/product.json", `{"schema_version":1,"product":"work","id":"calling","title":"Calling Team","session_id":"session-calling"}`)
	f.write(t, callerRoot+"/workflow.json", `{"id":"calling","label":"Calling Team","created_by":"other"}`)
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
	if _, err := reg.tools["share_dashboard"].exec(ctx, map[string]any{"mode": "private", "shared_agents": []string{coach.ConversationKey}, "agent_purposes": map[string]string{coach.ConversationKey: "Review a situation and suggest practical next steps."}}); err != nil {
		t.Fatal(err)
	}
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if got := crewSharedAgentKeys(externalCommandCrewRoot); len(got) != 1 || got[0] != coach.ConversationKey {
		t.Fatalf("native selection: %v", got)
	}
	reader := &UserClaims{UserID: "other", Username: "Colleague", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:run", "dashboards:read"}, CrewIDs: []string{"team"}}}
	call := func(action string, extra map[string]any, website bool) *httptest.ResponseRecorder {
		t.Helper()
		args := map[string]any{"action": action, "workspace_id": "work:team"}
		for k, v := range extra {
			args[k] = v
		}
		if _, byRoot := args["workspace"]; byRoot {
			delete(args, "workspace_id")
		}
		w := httptest.NewRecorder()
		if website {
			body, _ := json.Marshal(args)
			req := httptest.NewRequest(http.MethodPost, "/api/crew-agents", strings.NewReader(string(body)))
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "other", Username: "Colleague"}))
			f.api.handleCrewSharedAgents(w, req)
		} else {
			body, _ := json.Marshal(map[string]any{"name": "workspace", "arguments": args})
			req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(string(body)))
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, reader))
			f.api.handleExternalCall(w, req)
		}
		return w
	}
	for _, web := range []bool{false, true} {
		w := call("chats", nil, web)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Coach") || !strings.Contains(w.Body.String(), "practical next steps") || strings.Contains(w.Body.String(), "Private planning") || strings.Contains(w.Body.String(), "provider_connections") {
			t.Fatalf("filtered list web=%t: %d %s", web, w.Code, w.Body.String())
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
	transcript := fmt.Sprintf(`{"ui_events":[{"sequence":1,"timestamp":%q,"type":"user_message","data":{"data":{"content":"Private earlier context"}}},{"sequence":2,"timestamp":%q,"type":"user_message","data":{"data":{"content":"Shared owner context"}}},{"sequence":3,"timestamp":%q,"type":"user_message","data":{"data":{"content":"internal envelope","metadata":{"source":"agent_message","sender_label":"Colleague","display_content":"Please review this"}}}}]}`, since.Add(-time.Hour).Format(time.RFC3339Nano), since.Format(time.RFC3339Nano), since.Format(time.RFC3339Nano))
	f.write(t, externalCommandCrewRoot+"/builder/conversation/2026-10-24/session-"+coach.SessionID+"-conversation.json", transcript)
	for _, web := range []bool{false, true} {
		w := call("read_chat", map[string]any{"chat_id": coach.ConversationKey}, web)
		if w.Code != 200 || strings.Contains(w.Body.String(), "Private earlier context") || !strings.Contains(w.Body.String(), "Shared owner context") || !strings.Contains(w.Body.String(), `"sender":"owner"`) || strings.Contains(w.Body.String(), `"role":"you"`) || !strings.Contains(w.Body.String(), `"sender":"Colleague"`) || strings.Contains(w.Body.String(), "internal envelope") {
			t.Fatalf("shared transcript web=%t: %d %s", web, w.Code, w.Body.String())
		}
		w = call("read_chat", map[string]any{"chat_id": private.ConversationKey}, web)
		if w.Code < 400 {
			t.Fatalf("unselected history web=%t: %s", web, w.Body.String())
		}
	}
	if w := call("read_chat", map[string]any{"workspace": externalCommandCrewRoot, "chat_id": coach.ConversationKey}, true); w.Code != 200 || !strings.Contains(w.Body.String(), "Shared owner context") || strings.Contains(w.Body.String(), "Private earlier context") {
		t.Fatalf("standalone dashboard reads the same shared conversation: %d %s", w.Code, w.Body.String())
	}
	if w := call("read_chat", map[string]any{"session_id": coach.SessionID}, false); w.Code != 200 || strings.Contains(w.Body.String(), "Private earlier context") || !strings.Contains(w.Body.String(), "Shared owner context") {
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
	for _, action := range []string{"stop", "close_chat", "rename_chat", "set_chat_model"} {
		w := call(action, map[string]any{"chat_id": coach.ConversationKey, "name": "Takeover"}, false)
		if w.Code < 400 {
			t.Fatalf("shared recipient acquired %s: %s", action, w.Body.String())
		}
	}
	// Read/run consent can use agents but cannot edit even the caller's own project.
	reader.UserID = "owner"
	for _, action := range []string{"open_chat", "rename_chat", "set_chat_model", "stop", "chat_tool", "update"} {
		w := call(action, map[string]any{"chat_id": "coach", "name": "New name", "tool_name": "perform_ui_action", "changes": map[string]any{"memory_enabled": false}}, false)
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
	for _, web := range []bool{true, false} {
		w := call("ask", map[string]any{"chat_id": coach.ConversationKey, "message": "Please help with this feedback.", "submission_id": fmt.Sprintf("shared-agent-message-%t", web)}, web)
		if w.Code != 200 {
			t.Fatalf("send web=%t: %d %s", web, w.Code, w.Body.String())
		}
		select {
		case <-delivered:
		case <-time.After(5 * time.Second):
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
	case <-time.After(5 * time.Second):
		t.Fatal("hosted Crew did not reach the same existing tab")
	}
	var contact agentConversation
	deadline := time.Now().Add(5 * time.Second)
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
		if w := call(action, map[string]any{"chat_id": coach.ConversationKey, "message": "old contact"}, false); w.Code < 400 {
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
	if w := call("read_chat", map[string]any{"chat_id": coach.ConversationKey}, true); w.Code != 200 || strings.Contains(w.Body.String(), "Shared owner context") {
		t.Fatalf("re-shared history: %d %s", w.Code, w.Body.String())
	}
	if got := f.read(t, externalCommandCrewRoot+"/builder/conversation/2026-10-24/session-"+coach.SessionID+"-conversation.json"); !strings.Contains(got, "Private earlier context") {
		t.Fatal("sharing changed the original agent context")
	}
	// A saved dashboard URL does not retain access after its Crew becomes private.
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, nil); err != nil {
		t.Fatal(err)
	}
	if w := call("read_chat", map[string]any{"workspace": externalCommandCrewRoot, "chat_id": coach.ConversationKey}, true); w.Code < 400 {
		t.Fatalf("dashboard URL retained private Crew access: %s", w.Body.String())
	}

}
