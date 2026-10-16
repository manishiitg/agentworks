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
	if w.Code != 200 || strings.Contains(w.Body.String(), `"id":"invoices"`) {
		t.Fatalf("another account inherited state: %d %s", w.Code, w.Body)
	}
	if w = request("owner", http.MethodPut, body); w.Code != 409 {
		t.Fatalf("stale browser overwrote newer save: %d %s", w.Code, w.Body)
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

func TestHomeMCPUsesSameVisibility(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	w := f.call(t, "owner", "home", map[string]any{"action": "recent"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Invoice processing") || strings.Contains(w.Body.String(), "Private research") {
		t.Fatalf("MCP home: %d %s", w.Code, w.Body)
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
