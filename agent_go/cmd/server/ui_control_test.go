package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	platformskills "github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUIControlHTTPPlanReceiptRoundTrip(t *testing.T) {
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		"Workflow/test/workflow.json": `{"id":"wf-test","label":"Test","created_by":"user-a","access":{"owners":["user-a"]}}`,
	}})
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"s": {SessionID: "s", UserID: "user-a"}}}
	b := api.uiBroker()
	b.setScope("s", "Workflow/test")
	call := func(body map[string]interface{}) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := mux.SetURLVars(requestWithUserForSessionAccess("user-a"), map[string]string{"session_id": "s"})
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		w := httptest.NewRecorder()
		api.handleUIControl(w, r)
		return w
	}
	w := call(map[string]interface{}{"operation": "bind", "version": 1})
	if w.Code != 200 {
		t.Fatalf("bind: %d %s", w.Code, w.Body.String())
	}
	var binding map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	request := func(op string, extra map[string]interface{}) *httptest.ResponseRecorder {
		body := map[string]interface{}{"operation": op, "version": 1, "binding": binding["binding"], "token": binding["token"]}
		for k, v := range extra {
			body[k] = v
		}
		return call(body)
	}
	a, _, err := b.submit("s", "flow", "open", "livekit-quality", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	w = request("sync", map[string]interface{}{"state": uiSnapshot{View: "flow", Revision: 1, Visible: true}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), a.RequestID) {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	w = request("ack", map[string]interface{}{"request_id": a.RequestID, "status": "applied", "state": uiSnapshot{View: "flow", Revision: 2, Visible: true}})
	if w.Code == 200 {
		t.Fatal("view shell alone must not acknowledge selected step")
	}
	w = request("ack", map[string]interface{}{"request_id": a.RequestID, "status": "applied", "state": uiSnapshot{View: "flow", Target: "livekit-quality", Revision: 2, Visible: true}})
	if w.Code != 200 {
		t.Fatalf("ack: %d %s", w.Code, w.Body.String())
	}
	receipt, err := b.result("s", a.RequestID)
	if err != nil || receipt.Status != "applied" || !receipt.Visible {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
}

func TestUIControlHTTPRejectsForeignAndOwnerlessSessions(t *testing.T) {
	for _, owner := range []string{"other-user", ""} {
		api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"s": {SessionID: "s", UserID: owner}}}
		r := mux.SetURLVars(requestWithUserForSessionAccess("user-a"), map[string]string{"session_id": "s"})
		w := httptest.NewRecorder()
		api.handleUIControl(w, r)
		if w.Code != 403 {
			t.Fatalf("owner %q: %d", owner, w.Code)
		}
	}
}

func TestUIControlHTTPRestoresCrewScopeAndAcceptsCrewViews(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"work:project:crew-1": {
			SessionID:     "work:project:crew-1",
			UserID:        "user-a",
			WorkspacePath: "_users/user-a/Chats/Work/projects/crew-1",
		},
	}}
	call := func(body map[string]interface{}) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := mux.SetURLVars(requestWithUserForSessionAccess("user-a"), map[string]string{"session_id": "work:project:crew-1"})
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		w := httptest.NewRecorder()
		api.handleUIControl(w, r)
		return w
	}

	w := call(map[string]interface{}{"operation": "bind", "version": 1})
	if w.Code != 200 {
		t.Fatalf("bind: %d %s", w.Code, w.Body.String())
	}
	var binding map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	if binding["workspace"] != "Chats/Work/projects/crew-1" {
		t.Fatalf("restored workspace = %#v", binding["workspace"])
	}

	a, _, err := api.uiBroker().submitForContract("work:project:crew-1", workUIControlContract, "memory", "open", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	w = call(map[string]interface{}{
		"operation": "sync", "version": 1, "binding": binding["binding"], "token": binding["token"],
		"state": map[string]interface{}{"view": "memory", "revision": 1, "visible": true},
	})
	if w.Code != 200 || !strings.Contains(w.Body.String(), a.RequestID) {
		t.Fatalf("memory sync: %d %s", w.Code, w.Body.String())
	}
}

func TestUIControlHTTPDoesNotRestoreAutomatedCrewScope(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"work:project:crew-1": {
			SessionID: "work:project:crew-1", UserID: "user-a",
			WorkspacePath: "Chats/Work/projects/crew-1", TriggeredBy: "cron",
		},
	}}
	raw, _ := json.Marshal(map[string]interface{}{"operation": "bind", "version": 1})
	r := mux.SetURLVars(requestWithUserForSessionAccess("user-a"), map[string]string{"session_id": "work:project:crew-1"})
	r.Body = io.NopCloser(strings.NewReader(string(raw)))
	w := httptest.NewRecorder()
	api.handleUIControl(w, r)
	if w.Code != 409 || strings.TrimSpace(w.Body.String()) != "unsupported_surface" {
		t.Fatalf("automated Crew bind: %d %s", w.Code, w.Body.String())
	}
}

func TestUIControlScopeChangeInvalidatesBindings(t *testing.T) {
	b, c := boundUI(t)
	b.setScope(c.session, "Workflow/first")
	a, _, _ := b.submit(c.session, "flow", "open", "", "", nil)
	b.setScope(c.session, "Workflow/second")
	if _, err := b.syncClient(c.session, c.id, c.token, uiSnapshot{}); err == nil {
		t.Fatal("old workspace binding survived")
	}
	r, _ := b.result(c.session, a.RequestID)
	if r.Status != "cancelled" {
		t.Fatal(r)
	}
}

func boundUI(t *testing.T) (*uiControlBroker, *uiBinding) {
	t.Helper()
	b := newUIControlBroker()
	c, err := b.bind("session-a")
	if err != nil {
		t.Fatal(err)
	}
	return b, c
}
func TestUIControlOnlyAdvertisesActualActions(t *testing.T) {
	// 25 views before the knowledge/bots/llm consolidation; 18 after it, with
	// workflow upgrades inside Identity rather than a standalone view; 19 with
	// the Human actions view (decisions and suggestions waiting for a person).
	if len(uiControlContract.Views) != 19 {
		t.Fatal("registry coverage changed")
	}
	for _, v := range uiControlContract.Views {
		if err := validateUIAction(v.ID, "open", ""); err != nil {
			t.Fatal(err)
		}
		if err := validateUIAction(v.ID, "delete", ""); err == nil {
			t.Fatal("mutation advertised")
		}
		if err := validateUIAction(v.ID, "refresh", ""); err != nil {
			t.Fatalf("refresh missing for %s: %v", v.ID, err)
		}
		if err := validateUIAction(v.ID, "open", "arbitrary target"); err == nil && v.ID != "flow" && v.ID != "report" && v.ID != "files" {
			t.Fatal("ignored target")
		}
	}
	if validateUIAction("files", "open", "code/shared/helpers.py") != nil {
		t.Fatal("workspace-relative file target missing")
	}
	for _, section := range []string{"schedules", "webhooks"} {
		if validateUIAction("schedules", "open", section) != nil {
			t.Fatalf("schedule section missing: %q", section)
		}
	}
	if validateUIAction("schedules", "open", "guessed") == nil {
		t.Fatal("unknown schedule section accepted")
	}
	for _, section := range []string{"chats", "schedules", "triggers", "bots"} {
		if validateUIAction("workshop", "open", section) != nil {
			t.Fatalf("automation section missing: %q", section)
		}
	}
	if validateUIAction("workshop", "open", "guessed") == nil {
		t.Fatal("unknown automation section accepted")
	}
	for _, tab := range []string{"learnings", "knowledgebase", "database"} {
		if validateUIAction("knowledge", "open", tab) != nil {
			t.Fatalf("knowledge tab missing: %q", tab)
		}
	}
	if validateUIAction("knowledge", "open", "guessed") == nil {
		t.Fatal("unknown knowledge tab accepted")
	}
	for _, invalid := range []string{"/etc/passwd", "../other/file", `code\\file.py`} {
		if err := validateUIAction("files", "open", invalid); err == nil {
			t.Fatalf("unsafe file target accepted: %q", invalid)
		}
	}
	if validateUIAction("notify", "expand", "pulse_review") != nil {
		t.Fatal("notify disclosure missing")
	}
}

func TestProductUIControlSkillsCoverTheirOwnContracts(t *testing.T) {
	if err := workproduct.RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	crewSkills := platformskills.LoadAttachable("", []string{"work-ui-control"})
	if len(crewSkills) != 1 {
		t.Fatalf("Crew UI skills = %d, want 1", len(crewSkills))
	}

	var workflowSkill *llmtypes.Skill
	if err := guidance.AttachConfiguredReferenceSurface("workshop", true, []string{"workflow-ui-control"}, func(skill *llmtypes.Skill) error {
		if workflowSkill != nil {
			return fmt.Errorf("multiple workflow UI skills")
		}
		workflowSkill = skill
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if workflowSkill == nil {
		t.Fatal("workflow-ui-control was not materialized")
	}

	for _, tc := range []struct {
		name     string
		contract uiContract
		content  string
	}{
		{name: "Crew", contract: workUIControlContract, content: crewSkills[0].Content},
		{name: "Workflow", contract: uiControlContract, content: workflowSkill.Content},
	} {
		for _, view := range tc.contract.Views {
			if !strings.Contains(tc.content, "`"+view.ID+"`") {
				t.Errorf("%s UI skill does not document contract view %q", tc.name, view.ID)
			}
		}
	}

	workflowReference := guidance.MaterializeReferenceSkill("workshop")
	if workflowReference == nil {
		t.Fatal("builder-reference was not materialized")
	}
	for _, file := range workflowReference.SupportingFiles {
		if file.RelPath == "references/workspace-views.md" {
			t.Fatal("Workflow UI guidance is still duplicated inside builder-reference")
		}
	}
}
func TestUIControlDisconnectedAndAmbiguousClients(t *testing.T) {
	b := newUIControlBroker()
	if _, _, err := b.submit("session-a", "flow", "open", "", "", nil); err == nil || err.Error() != "browser_disconnected" {
		t.Fatal(err)
	}
	_, _ = b.bind("session-a")
	_, _ = b.bind("session-a")
	if _, _, err := b.submit("session-a", "flow", "open", "", "", nil); err == nil || err.Error() != "ambiguous_client" {
		t.Fatal(err)
	}
}
func TestUIControlDeduplicationAndReceiptIsolation(t *testing.T) {
	b, c := boundUI(t)
	a, fresh, err := b.submit(c.session, "notify", "expand", "run_summary", "same", nil)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	duplicate, fresh, err := b.submit(c.session, "notify", "expand", "run_summary", "same", nil)
	if err != nil || fresh || duplicate.RequestID != a.RequestID {
		t.Fatal("not idempotent")
	}
	if _, _, err := b.submit(c.session, "flow", "open", "", "same", nil); err == nil {
		t.Fatal("key reused for different action")
	}
	if _, err := b.result("other-session", a.RequestID); err == nil {
		t.Fatal("cross-session result")
	}
	state := uiSnapshot{View: "notify", Revision: 1, Visible: true}
	if _, err := b.syncClient("other-session", c.id, c.token, state); err == nil {
		t.Fatal("cross-session claim")
	}
	if _, err := b.syncClient(c.session, c.id, "wrong-token", state); err == nil {
		t.Fatal("unauthenticated claim")
	}
	if err := b.ack(c.session, c.id, c.token, a.RequestID, "applied", "", state); err == nil {
		t.Fatal("ack before claim")
	}
	commands, err := b.syncClient(c.session, c.id, c.token, state)
	if err != nil || len(commands) != 1 {
		t.Fatal(err, len(commands))
	}
	commands, _ = b.syncClient(c.session, c.id, c.token, state)
	if len(commands) != 0 {
		t.Fatal("duplicate claim on reconnect")
	}
	if err := b.ack(c.session, c.id, c.token, a.RequestID, "applied", "", uiSnapshot{View: "flow", Revision: 1, Visible: true}); err == nil {
		t.Fatal("wrong view accepted")
	}
	if err := b.ack(c.session, c.id, c.token, a.RequestID, "applied", "", state); err != nil {
		t.Fatal(err)
	}
	result, _ := b.result(c.session, a.RequestID)
	if result.Status != "applied" || !result.Visible {
		t.Fatal(result)
	}
	duplicate, _, _ = b.submit(c.session, "notify", "expand", "run_summary", "same", nil)
	if duplicate.Status != "applied" {
		t.Fatal("completed retry executed")
	}
}
func TestUIControlRevisionAndLeaseExpiry(t *testing.T) {
	b, c := boundUI(t)
	now := time.Now()
	b.now = func() time.Time { return now }
	_, _ = b.syncClient(c.session, c.id, c.token, uiSnapshot{View: "flow", Revision: 2})
	old := int64(1)
	if _, _, err := b.submit(c.session, "flow", "open", "", "", &old); err == nil || err.Error() != "stale_state" {
		t.Fatal(err)
	}
	current := int64(2)
	a, _, _ := b.submit(c.session, "flow", "open", "", "", &current)
	commands, err := b.syncClient(c.session, c.id, c.token, uiSnapshot{View: "flow", Revision: 3})
	if err != nil || len(commands) != 0 {
		t.Fatal(err)
	}
	r, _ := b.result(c.session, a.RequestID)
	if r.Code != "stale_state" {
		t.Fatal(r)
	}
	a, _, _ = b.submit(c.session, "flow", "open", "", "", nil)
	now = now.Add(11 * time.Second)
	r, _ = b.result(c.session, a.RequestID)
	if r.Status != "expired" || r.Visible {
		t.Fatal(r)
	}
	now = now.Add(10 * time.Minute)
	if _, err := b.result(c.session, a.RequestID); err == nil {
		t.Fatal("unbounded retention")
	}
	if _, err := b.snapshot(c.session); err == nil {
		t.Fatal("expired browser")
	}
}
func TestUIControlUnbindCancelsWithoutReplay(t *testing.T) {
	b, c := boundUI(t)
	a, _, _ := b.submit(c.session, "flow", "open", "", "", nil)
	if err := b.unbind(c.session, c.id, c.token); err != nil {
		t.Fatal(err)
	}
	r, _ := b.result(c.session, a.RequestID)
	if r.Status != "cancelled" {
		t.Fatal(r)
	}
	newClient, _ := b.bind(c.session)
	commands, _ := b.syncClient(c.session, newClient.id, newClient.token, uiSnapshot{})
	if len(commands) != 0 {
		t.Fatal("replayed old binding")
	}
}
func TestUIControlStateIsBoundedAndRedacted(t *testing.T) {
	b, c := boundUI(t)
	if _, err := b.syncClient(c.session, c.id, c.token, uiSnapshot{View: "SECRET_VALUE"}); err == nil {
		t.Fatal("unbounded state")
	}
	a, _, _ := b.submit(c.session, "secrets", "open", "", "", nil)
	text := uiJSON(a)
	if strings.Contains(text, c.token) || strings.Contains(text, c.id) || strings.Contains(text, c.session) {
		t.Fatal("private binding leaked")
	}
}

func TestPerformUIActionCancellationReturnsTerminalReceipt(t *testing.T) {
	api := &StreamingAPI{}
	b := api.uiBroker()
	b.setScope("s", "Workflow/test")
	if _, err := b.bind("s"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := map[string]interface{}{"view": "flow", "action": "open", "idempotency_key": "cancel-test"}
	out, err := api.performUIAction(ctx, "s", "Workflow/test", args)
	if err != nil {
		t.Fatal(err)
	}
	var receipt uiAction
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "expired" || receipt.Code != "request_cancelled" {
		t.Fatalf("expected terminal cancellation receipt, got %s", out)
	}
	again, err := api.performUIAction(context.Background(), "s", "Workflow/test", args)
	if err != nil {
		t.Fatal(err)
	}
	var retained uiAction
	if err := json.Unmarshal([]byte(again), &retained); err != nil {
		t.Fatal(err)
	}
	if retained.RequestID != receipt.RequestID || retained.Status != receipt.Status {
		t.Fatalf("same key created a new action: %s", again)
	}
}

// PLAT-434: a project under _users/<id>/ (every multi-user server) got the
// workflow contract, so the Code page's views were refused as invalid_state
// and the agent saw "browser disconnected".
func TestUIContractForScopeIgnoresUserPrefix(t *testing.T) {
	for _, scope := range []string{"Chats/Code/projects/hi", "_users/manish/Chats/Code/projects/hi"} {
		if !validUIViewForContract(uiContractForScope(scope), "database") {
			t.Errorf("%s: a Code project must accept the Code-only view \"database\"", scope)
		}
	}
	for _, scope := range []string{"Chats/Work/projects/site", "_users/manish/Chats/Work/projects/site"} {
		if !validUIViewForContract(uiContractForScope(scope), "memory") {
			t.Errorf("%s: a Crew project must accept the Crew view \"memory\"", scope)
		}
	}
	if validUIViewForContract(uiContractForScope("Workflow/w"), "database") {
		t.Error("a workflow must keep the workflow contract")
	}
}

// PLAT-434: the Code page reports the tab it shows. Terminal, Plan and Suggestions are not views the agent can open, but a page showing one must still register
// (it was refused as invalid_state, so the agent saw "browser_disconnected").
func TestObservedUIViewIsAcceptedEvenWhenTheAgentCannotOpenIt(t *testing.T) {
	for _, view := range []string{"", "report", "shell", "plan", "suggestions", "execution-logs"} {
		if !validObservedUIView(view) {
			t.Errorf("view %q must be accepted as an observation", view)
		}
	}
	for _, view := range []string{"../x", "Shell", "a b", "x;y", "9lives", strings.Repeat("a", 33), "<script>"} {
		if validObservedUIView(view) {
			t.Errorf("view %q is not a well-formed view name", view)
		}
	}
	// Opening is still limited to the contract: the Code contract has no terminal view to open.
	if validUIViewForContract(uiContractForScope("_users/manish/Chats/Code/projects/hi"), "shell") {
		t.Error("an action must not be able to open a view the contract does not list")
	}
}
