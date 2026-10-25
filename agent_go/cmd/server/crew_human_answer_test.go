package server

import (
	"context"
	"maps"
	"strings"
	"testing"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/mcpagent/executor"
)

// Real registered Builder tools persist the answer and checked completion.
// Retained tools cannot cross Run, unattended, revoked or Crew boundaries.
func TestCrewBuilderAnswerSavesThePersonsDecision(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work","code"]},{"id":"co","role":"editor","products":["work"]},{"id":"reader","role":"editor","products":["work","code"]}]}`)
	registry := newTestRegistry(t)
	previousRegistry := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() {
		defaultProjectOwnersOverride = previousRegistry
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	})
	const rootA, rootB = "Crew/approval-a", "Crew/approval-b"
	for _, folder := range []string{"approval-a", "approval-b"} {
		if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: "owner", Shared: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.AddCoOwner("work", "approval-a", "co"); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: internalevents.NewEventStore(20), lastQueryRequests: map[string]QueryRequest{}, activeSessions: map[string]*ActiveSessionInfo{}}
	previousAPI := workflowToolsAPI
	workflowToolsAPI = api
	t.Cleanup(func() { workflowToolsAPI = previousAPI })
	contextFor := func(actor, session string) context.Context {
		return executor.WithSessionID(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: actor}), session)
	}
	register := func(session, actor, root, profile string, readOnly bool, change func(*QueryRequest)) (*recordingRegistrar, context.Context) {
		t.Helper()
		req := QueryRequest{userID: actor, AgentProfileID: profile, SelectedFolder: root}
		if change != nil {
			change(&req)
		}
		api.lastQueryRequests[session] = req
		api.eventStore.SetSessionOwner(session, actor)
		api.activeSessions[session] = &ActiveSessionInfo{UserID: actor, WorkspacePath: root}
		resolved := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: profile, ToolPolicy: agentprofiles.ToolPolicy{Mode: agentprofiles.ToolPolicyModeAllowlist}}}
		gate := newProductToolGate(resolved)
		reg := &recordingRegistrar{}
		if err := api.registerAgentProfileTools(reg, gate, resolved, actor, session, root, readOnly, req); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"answer_human_input_request", "get_human_input_request", "mark_human_input_consumed"} {
			if _, ok := reg.tools[name]; ok && !gate.Allows(name) {
				t.Fatalf("the product allowlist would drop %s", name)
			}
		}
		return reg, contextFor(actor, session)
	}
	create := func(root, id string) string {
		t.Helper()
		input, err := submitCrewSuggestion(contextFor("reader", "seed-"+id), &UserClaims{UserID: "reader"}, root, "seed-"+id, "Suggestion "+id, "", "")
		if err != nil {
			t.Fatalf("submit suggestion for %s: %v", root, err)
		}
		return input.ID
	}
	const name = "answer_human_input_request"
	for _, test := range []struct{ actor, root, profile string }{
		{"owner", rootA, "work"}, {"co", rootA, "work"},
	} {
		id := create(test.root, "answer-"+test.actor+"-"+test.profile)
		session := "chat-" + test.actor + "-" + test.profile
		reg, ctx := register(session, test.actor, test.root, test.profile, false, nil)
		tool, ok := reg.tools[name]
		if !ok {
			t.Fatalf("%s/%s Builder has no answer tool", test.actor, test.profile)
		}
		if _, err := tool.exec(ctx, map[string]interface{}{"workspace_path": test.root, "input_id": id, "selected_option_id": "approve", "answered_by": "forged"}); err != nil {
			t.Fatal(err)
		}
		_, db, err := openReportHumanInputDB(ctx, test.root, false)
		if err != nil {
			t.Fatal(err)
		}
		var status, answeredAt, answeredBy, option, consumed, actor, kind, channel string
		err = db.QueryRow(`SELECT status,answered_at,answered_by,selected_option_id,consumed_at FROM report_human_inputs WHERE id=?`, id).Scan(&status, &answeredAt, &answeredBy, &option, &consumed)
		if err == nil {
			err = db.QueryRow(`SELECT actor_id,actor_kind,channel FROM report_human_input_events WHERE input_id=? AND event_type='answered'`, id).Scan(&actor, &kind, &channel)
		}
		if err != nil || status != "answered" || answeredAt == "" || answeredBy != test.actor || option != "approve" || consumed != "" || actor != test.actor || kind != "human_via_chat" || channel != "agent_chat" {
			t.Fatalf("saved answer: status=%s at=%s by=%s option=%s consumed=%s audit=%s/%s/%s err=%v", status, answeredAt, answeredBy, option, consumed, actor, kind, channel, err)
		}
		read, hasRead := reg.tools["get_human_input_request"]
		complete, hasComplete := reg.tools["mark_human_input_consumed"]
		if !hasRead || !hasComplete {
			t.Fatal("Builder cannot read and complete its saved answer")
		}
		result, err := read.exec(ctx, map[string]interface{}{"workspace_path": test.root, "input_id": id})
		if err != nil || !strings.Contains(result, id) || !strings.Contains(result, "approve") {
			t.Fatalf("read saved decision: %s %v", result, err)
		}
		receipt := map[string]interface{}{"workspace_path": test.root, "input_id": id, "outcome_summary": "Checked this no-change suggestion"}
		if _, err := complete.exec(ctx, receipt); err == nil {
			t.Fatal("completion without checked evidence was accepted")
		}
		if err := db.QueryRow(`SELECT status FROM report_human_inputs WHERE id=?`, id).Scan(&status); err != nil || status != "answered" {
			t.Fatalf("missing evidence changed the saved decision: %s %v", status, err)
		}
		receipt["outcome_evidence"] = "Read the saved test suggestion and confirmed it requests no project change."
		if _, err := complete.exec(ctx, receipt); err != nil {
			t.Fatal(err)
		}
		var outcome, auditDetails, auditSession string
		if err := db.QueryRow(`SELECT status,consumed_at,outcome_summary FROM report_human_inputs WHERE id=?`, id).Scan(&status, &consumed, &outcome); err != nil || status != "consumed" || consumed == "" || !strings.Contains(outcome, receipt["outcome_evidence"].(string)) {
			t.Fatalf("saved completion: %s %s %s %v", status, consumed, outcome, err)
		}
		if err := db.QueryRow(`SELECT details,session_id FROM report_human_input_events WHERE input_id=? AND event_type='consumed'`, id).Scan(&auditDetails, &auditSession); err != nil || auditSession != session || !strings.Contains(auditDetails, receipt["outcome_evidence"].(string)) {
			t.Fatalf("completion audit: %s %s %v", auditSession, auditDetails, err)
		}
		db.Close()
	}
	pendingID := create(rootA, "still-pending")
	_, executors, _ := createReportHumanInputTools()
	answer := executors[name].(func(context.Context, map[string]interface{}) (string, error))
	args := map[string]interface{}{"workspace_path": rootA, "input_id": pendingID, "selected_option_id": "approve"}
	retained, retainedCtx := register("retained-owner", "owner", rootA, "work", false, nil)
	answeredID := create(rootA, "answered-still-open")
	if _, err := retained.tools[name].exec(retainedCtx, map[string]interface{}{"workspace_path": rootA, "input_id": answeredID, "selected_option_id": "approve"}); err != nil {
		t.Fatal(err)
	}
	refuseLifecycle := func(ctx context.Context, args map[string]interface{}) {
		t.Helper()
		for _, name := range []string{"get_human_input_request", "mark_human_input_consumed"} {
			scoped := maps.Clone(args)
			if name == "mark_human_input_consumed" {
				scoped["input_id"] = answeredID
				scoped["outcome_summary"] = "Checked result"
				scoped["outcome_evidence"] = "Evidence supplied to exercise authorization before lifecycle completion."
			}
			if _, err := retained.tools[name].exec(ctx, scoped); err == nil {
				t.Fatalf("%s accepted a retained tool outside its Builder scope", name)
			}
		}
	}
	_, ownerCtx := register("identity-check", "owner", rootA, "work", false, nil)
	for _, claims := range []*UserClaims{{UserID: "co"}, {UserID: "owner", ExternalBuilderOperationID: "mcp-operation"}} {
		refuseLifecycle(context.WithValue(ownerCtx, UserContextKey, claims), args)
		if _, err := answer(context.WithValue(ownerCtx, UserContextKey, claims), args); err == nil {
			t.Fatal("a mismatched or external caller answered through the website's tool")
		}
	}
	for label, change := range map[string]func(*QueryRequest){
		"run":          func(r *QueryRequest) { r.PinRunMode = true },
		"schedule":     func(r *QueryRequest) { r.TriggeredBy = "schedule" },
		"channel":      func(r *QueryRequest) { r.BotPlatform = "slack" },
		"background":   func(r *QueryRequest) { r.ParentSessionID = "parent" },
		"notification": func(r *QueryRequest) { r.IsAutoNotification = true },
		"external":     func(r *QueryRequest) { r.ExternalBuilderOperationID = "mcp-operation" },
	} {
		reg, ctx := register("blocked-"+label, "owner", rootA, "work", label == "run", change)
		for _, tool := range []string{name, "get_human_input_request", "mark_human_input_consumed"} {
			if _, ok := reg.tools[tool]; ok {
				t.Fatalf("%s turn received %s", label, tool)
			}
		}
		refuseLifecycle(ctx, args)
		if _, err := answer(ctx, args); err == nil {
			t.Fatalf("%s turn answered through a retained tool", label)
		}
	}
	reg, ctx := register("reader", "reader", rootA, "work", true, nil)
	for _, tool := range []string{name, "get_human_input_request", "mark_human_input_consumed"} {
		if _, ok := reg.tools[tool]; ok {
			t.Fatalf("Run person received %s", tool)
		}
	}
	refuseLifecycle(ctx, args)
	if _, err := answer(ctx, args); err == nil {
		t.Fatal("Run person answered through a retained tool")
	}
	_, otherCtx := register("other-crew", "owner", rootB, "work", false, nil)
	refuseLifecycle(otherCtx, args)
	refuseLifecycle(otherCtx, map[string]interface{}{"workspace_path": rootB, "input_id": pendingID})
	if _, err := answer(otherCtx, args); err == nil {
		t.Fatal("Crew B's Builder answered Crew A's decision")
	}
	if _, err := answer(otherCtx, map[string]interface{}{"workspace_path": rootB, "input_id": pendingID, "selected_option_id": "approve"}); err == nil {
		t.Fatal("Crew A's input ID was answered inside Crew B")
	}
	_, coCtx := register("revoked-co", "co", rootA, "work", false, nil)
	if _, err := registry.RemoveCoOwner("work", "approval-a", "co"); err != nil {
		t.Fatal(err)
	}
	refuseLifecycle(coCtx, args)
	if _, err := answer(coCtx, args); err == nil {
		t.Fatal("removed co-owner answered through a retained tool")
	}
	pending, err := listReportHumanInputs(contextFor("owner", "check"), rootA, "pending", "")
	if err != nil || len(pending) != 1 || pending[0].ID != pendingID {
		t.Fatalf("refused calls changed the pending input: %+v %v", pending, err)
	}
	answered, err := listReportHumanInputs(retainedCtx, rootA, "answered", "")
	if err != nil || len(answered) != 1 || answered[0].ID != answeredID {
		t.Fatalf("refused calls changed the answered input: %+v %v", answered, err)
	}
}
