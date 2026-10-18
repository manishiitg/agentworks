package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestHomeUsesAuthoritativeVisibleQuestionsAndDecisions(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	f.api.activeSessions = map[string]*ActiveSessionInfo{"owner-question": {SessionID: "owner-question", UserID: "owner", WorkspacePath: "Workflow/invoices"}}
	store := virtualtools.GetHumanFeedbackStore()
	id := "home-test-question"
	if err := store.CreatePendingRequest(id, "Approve invoice?", "", "owner-question", []string{"Yes", "No"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.SubmitResponse(id, "No") })
	req := adminRequest("GET", "/api/home", "", &UserClaims{UserID: "owner"}, nil)
	decision, err := createReportHumanInput(req.Context(), "Workflow/invoices", ReportHumanInputCreateRequest{Question: "Choose invoice policy", Source: "pulse", Priority: "high"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.api.handleHome(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) || !strings.Contains(w.Body.String(), decision.ID) || strings.Contains(w.Body.String(), "Private research") {
		t.Fatalf("owner home: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	f.api.handleHome(w, adminRequest("GET", "/api/home", "", &UserClaims{UserID: "outsider"}, nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), decision.ID) || strings.Contains(w.Body.String(), "Invoice processing") {
		t.Fatalf("cross-user home leaked: %d %s", w.Code, w.Body)
	}
	if err := store.SubmitResponse(id, "Yes"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	f.api.handleHome(w, req)
	if strings.Contains(w.Body.String(), id) {
		t.Fatal("resolved question remained on Home")
	}
}

func TestWorkspaceStateUserIsolationRevisionAndRevokedAccess(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	request := func(user, method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.api.handleWorkspaceReturnState(w, adminRequest(method, "/api/workspace-state", body, &UserClaims{UserID: user}, nil))
		return w
	}
	body := `{"version":1,"revision":0,"last":{"surface":"agentworks","id":"invoices","view":"human-actions","layout":{"chat_open":true,"panel_open":true,"split_ratio":0.4,"navigation_mode":"auto-hide"}}}`
	w := request("owner", http.MethodPut, body)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w = request("owner", http.MethodGet, "")
	var saved workspaceReturnState
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Revision != 1 || saved.Last == nil || saved.Last.Layout.SplitRatio != 0.4 {
		t.Fatalf("restored: %d %s (%v)", w.Code, w.Body, err)
	}
	w = request("reader", http.MethodGet, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"last":null`) || !strings.Contains(w.Body.String(), `"revision":0`) {
		t.Fatalf("another account inherited state: %d %s", w.Code, w.Body)
	}
	if w = request("owner", http.MethodPut, body); w.Code != 200 {
		t.Fatalf("another browser tab could not save its preference: %d %s", w.Code, w.Body)
	}
	if json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Revision != 2 {
		t.Fatalf("latest save did not advance the server revision: %s", w.Body)
	}
	if w = request("owner", http.MethodPut, strings.ReplaceAll(body, `"revision":0`, `"revision":1`)); w.Code != 200 {
		t.Fatalf("next save: %d %s", w.Code, w.Body)
	}
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"outsider","access":{"owners":["outsider"]}}`)
	noteWorkspaceMutation("Workflow/invoices/workflow.json", false)
	w = request("owner", http.MethodGet, "")
	if w.Code != 200 {
		t.Fatalf("revocation restore: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Last != nil || saved.Note == "" || len(saved.Recent) != 0 {
		t.Fatalf("revoked workspace reopened: %s (%v)", w.Body, err)
	}
}

func TestWorkspaceStateRejectsForgedTargetAndForeignSession(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	f.api.activeSessions = map[string]*ActiveSessionInfo{"foreign-chat": {SessionID: "foreign-chat", UserID: "outsider", WorkspacePath: "Workflow/invoices"}}
	work := []homeWork{{Surface: "agentworks", ID: "invoices", WorkspacePath: "Workflow/invoices"}}
	req := adminRequest("GET", "/api/workspace-state", "", &UserClaims{UserID: "owner"}, nil)
	if d, ok := f.api.validateWorkspaceReturn(req, &workspaceDestination{Surface: "agentworks", ID: "secret"}, work); ok || d != nil {
		t.Fatal("forged workspace admitted")
	}
	d, ok := f.api.validateWorkspaceReturn(req, &workspaceDestination{Surface: "agentworks", ID: "invoices", SessionID: "foreign-chat"}, work)
	if !ok || d.SessionID != "" {
		t.Fatalf("foreign chat restored: %+v", d)
	}
	for _, id := range []string{"../escape", ".", "..", "a/b", "%2f"} {
		if validWorkspaceDestination(&workspaceDestination{Surface: "work", ID: id}) {
			t.Fatalf("unsafe resource accepted: %s", id)
		}
	}
}

func TestRemovedHomeMCPIsNotDiscoverable(t *testing.T) {
	tokenTestSetup(t)
	if _, exists := externalCatalogTool("home"); exists {
		t.Fatal("duplicate attention tool exposed")
	}
}

func TestHomeRunningExcludesIdleAndCompletedChats(t *testing.T) {
	for _, phase := range []RuntimePhase{runtimePhaseIdle, runtimePhaseCompleted, runtimePhaseCanceled, runtimePhaseFailed} {
		if homeSessionRunning(&ActiveSessionInfo{Status: "running", RuntimeState: &RuntimeSnapshot{Phase: phase}}) {
			t.Fatalf("settled phase %s shown as running", phase)
		}
	}
	if !homeSessionRunning(&ActiveSessionInfo{RuntimeState: &RuntimeSnapshot{Phase: runtimePhaseRunning}}) {
		t.Fatal("live work hidden")
	}
	if homeSessionRunning(&ActiveSessionInfo{ParentSessionID: "parent", Status: "running"}) {
		t.Fatal("child duplicated as top-level work")
	}
}

func TestWorkspaceStateCorruptPreferenceRecoversToHome(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	for _, raw := range []string{`{broken`, `{"version":99,"last":{"surface":"agentworks","id":"invoices"}}`, strings.Repeat("x", 32769)} {
		f.write(t, workspaceReturnPath("owner"), raw)
		w := httptest.NewRecorder()
		f.api.handleWorkspaceReturnState(w, adminRequest("GET", "/api/workspace-state", "", &UserClaims{UserID: "owner"}, nil))
		var state workspaceReturnState
		if err := json.Unmarshal(w.Body.Bytes(), &state); w.Code != 200 || err != nil || state.Last != nil || state.Revision != 0 || state.Note == "" {
			t.Fatalf("corrupt state blocked navigation: %d %s (%v)", w.Code, w.Body, err)
		}
	}
	w := httptest.NewRecorder()
	f.api.handleWorkspaceReturnState(w, adminRequest("PUT", "/api/workspace-state", `{"version":1,"revision":0,"last":{"surface":"agentworks","id":"invoices"}}`, &UserClaims{UserID: "owner"}, nil))
	if w.Code != 200 {
		t.Fatalf("could not replace corrupt preference: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	f.api.handleWorkspaceReturnState(w, adminRequest("GET", "/api/workspace-state", "", &UserClaims{UserID: "owner"}, nil))
	var state workspaceReturnState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.Last == nil || state.Last.ID != "invoices" || state.Note != "" {
		t.Fatalf("recovery was not durable: %s (%v)", w.Body, err)
	}
}

func TestHomeInventoryRespectsOneCrewToken(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENT_PRODUCTS", "work")
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	req := adminRequest("GET", "/api/home", "", claims, nil)
	items, err := env.api.homeInventory(req.Context())
	if err != nil || len(items) != 1 || items[0].Surface != "work" || items[0].ID != "beta" {
		t.Fatalf("Home escaped token Crew bound: %+v (%v)", items, err)
	}
}

func TestHomeAndNeedsYouShareAnsweredDecisionProjection(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	req := adminRequest("GET", "/api/home", "", &UserClaims{UserID: "owner"}, nil)
	input, err := createReportHumanInput(req.Context(), "Workflow/invoices", ReportHumanInputCreateRequest{Question: "Use approved invoice rule", Source: "pulse", Options: []ReportHumanInputOption{{ID: "approve", Title: "Approve"}}, ApplyContract: ReportHumanInputApplyContract{Mode: "direct_apply"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := answerReportHumanInput(req.Context(), "Workflow/invoices", input.ID, ReportHumanInputAnswerRequest{SelectedOptionID: "approve", AnsweredBy: "owner"}); err != nil {
		t.Fatal(err)
	}
	home, err := f.api.collectHome(req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range home.Attention {
		if item.ID == input.ID && item.Kind == "apply_decision" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Home omitted unapplied decision: %+v", home.Attention)
	}
	w := f.call(t, "owner", "list_needs_you", map[string]any{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), input.ID) || !strings.Contains(w.Body.String(), `"type":"apply_decision"`) || !strings.Contains(w.Body.String(), `"apply_message"`) {
		t.Fatalf("MCP disagrees with Home: %d %s", w.Code, w.Body)
	}
}

func TestWorkspaceStateDerivesAuthorityAndEntryOnGet(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	f.api.activeSessions = map[string]*ActiveSessionInfo{"schedule-cron--owned": {SessionID: "schedule-cron--owned", UserID: "owner", WorkspacePath: "Workflow/invoices", Title: "Server schedule", WorkshopMode: "run", TriggeredBy: "cron"}}
	put := httptest.NewRecorder()
	f.api.handleWorkspaceReturnState(put, adminRequest("PUT", "/api/workspace-state", `{"version":1,"revision":0,"last":{"surface":"agentworks","id":"invoices","session_id":"schedule-cron--owned","chat":{"name":"My saved label","read_only":false,"execution_run":false,"scheduled_run":false,"workshop_mode":"workshop","bot_platform":"slack","view_mode":"tree"}}}`, &UserClaims{UserID: "owner"}, nil))
	if put.Code != 200 {
		t.Fatal(put.Code, put.Body)
	}
	var saved workspaceReturnState
	if json.Unmarshal(put.Body.Bytes(), &saved) != nil || saved.Last == nil || saved.Last.SessionID != "" || saved.Last.Chat != nil {
		t.Fatal("save acknowledgement exposed unverified restore data", put.Body)
	}
	raw, _, err := readFileFromWorkspace(t.Context(), workspaceReturnPath("owner"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forged := range []string{"workshop_mode", "bot_platform", "execution_run", "scheduled_run"} {
		if strings.Contains(raw, forged) {
			t.Fatal("persisted authority", raw)
		}
	}
	get := httptest.NewRecorder()
	f.api.handleWorkspaceReturnState(get, adminRequest("GET", "/api/workspace-state", "", &UserClaims{UserID: "owner"}, nil))
	var state workspaceReturnState
	if json.Unmarshal(get.Body.Bytes(), &state) != nil || state.Last == nil || state.Last.Chat == nil {
		t.Fatal(get.Code, get.Body)
	}
	chat := state.Last.Chat
	if !chat.ReadOnly || !chat.ExecutionRun || !chat.ScheduledRun || chat.WorkshopMode != "run" || chat.BotPlatform != "" || chat.Name != "My saved label" || chat.ViewMode != "tree" {
		t.Fatalf("browser mode trusted: %+v", chat)
	}
	if state.Destination == nil || state.Destination.ID != "invoices" || state.FirstVisit {
		t.Fatal("entry not server resolved", get.Body)
	}
	if f.questionRegistryReads.Load() != 0 {
		t.Fatal("restore loaded attention inventory")
	}
}

func TestWorkspaceAccessUsesCurrentProjectRole(t *testing.T) {
	f := newExternalToolsFixture(t)
	for _, item := range []struct {
		user     string
		canBuild bool
	}{{"owner", true}, {"reader", false}} {
		rec := httptest.NewRecorder()
		f.api.handleWorkspaceAccess(rec, adminRequest("GET", "/api/workspace-access?workspace=Workflow%2Finvoices", "", &UserClaims{UserID: item.user}, nil))
		var got struct {
			CanBuild bool `json:"can_build"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.CanBuild != item.canBuild {
			t.Fatal("account role replaced project authority", item.user, rec.Code, rec.Body)
		}
	}
}

func TestWorkspaceStateResolvesConversationKeyFromRegistry(t *testing.T) {
	env, _, root := conditionCodeFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks,work,code")
	env.api.activeSessions = map[string]*ActiveSessionInfo{"own-code": {SessionID: "own-code", UserID: "owner", WorkspacePath: root}}
	document := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{"code/checks:chat:side": {ProfileID: "code", ConversationKey: "checks:chat:side", SessionID: "own-code", WorkspacePath: root}}}
	raw, _ := json.Marshal(document)
	env.mock.mu.Lock()
	env.mock.files[productConversationRegistryPath("owner")] = string(raw)
	env.mock.mu.Unlock()
	request := adminRequest("GET", "/api/workspace-state", "", &UserClaims{UserID: "owner"}, nil)
	got, ok := env.api.validateWorkspaceReturn(request, &workspaceDestination{Surface: "code", ID: "checks", SessionID: "own-code", Chat: &workspaceChat{ConversationKey: "checks:chat:forged", WorkshopMode: "run"}}, []homeWork{{Surface: "code", ID: "checks", WorkspacePath: root}})
	if !ok || got.Chat == nil || got.Chat.ConversationKey != "checks:chat:side" || got.Chat.WorkshopMode == "run" {
		t.Fatalf("browser slot or mode used: %+v", got)
	}
}

func TestWorkspaceStateRestoresEmptyRegisteredSideChat(t *testing.T) {
	env, _, root := conditionCodeFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks,work,code")
	document := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{"code/checks:chat:side": {ProfileID: "code", ConversationKey: "checks:chat:side", SessionID: "empty-side", WorkspacePath: root}}}
	raw, _ := json.Marshal(document)
	env.mock.mu.Lock()
	env.mock.files[productConversationRegistryPath("owner")] = string(raw)
	env.mock.mu.Unlock()
	request := adminRequest("GET", "/api/workspace-state", "", &UserClaims{UserID: "owner"}, nil)
	destination := &workspaceDestination{Surface: "code", ID: "checks", SessionID: "empty-side", Chat: &workspaceChat{Name: "Chat 2", ConversationKey: "checks:chat:forged"}}
	work := []homeWork{{Surface: "code", ID: "checks", WorkspacePath: root}}
	got, ok := env.api.validateWorkspaceReturn(request, destination, work)
	if !ok || got.SessionID != "empty-side" || got.Chat == nil || got.Chat.ConversationKey != "checks:chat:side" || got.Chat.ReadOnly || got.Chat.Name != "Chat 2" {
		t.Fatalf("empty registered side chat lost: %+v", got)
	}
	destination.SessionID = "foreign"
	got, ok = env.api.validateWorkspaceReturn(request, destination, work)
	if !ok || got.SessionID != "" || got.Chat != nil {
		t.Fatalf("unregistered session restored: %+v", got)
	}
}
