package server

import (
	"context"
	"testing"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/mcpagent/executor"
)

// A real registered Builder tool must persist the person's choice, while a
// retained tool cannot answer from Run, an unattended turn, or another Crew.
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
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "former-code", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAudience("code", "former-code", false, nil); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: internalevents.NewEventStore(20), lastQueryRequests: map[string]QueryRequest{}, activeSessions: map[string]*ActiveSessionInfo{}}
	previousAPI := pulsePlatformAPI
	pulsePlatformAPI = api
	t.Cleanup(func() { pulsePlatformAPI = previousAPI })
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
		if _, ok := reg.tools["answer_human_input_request"]; ok && !gate.Allows("answer_human_input_request") {
			t.Fatal("the product allowlist would drop the answer tool")
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
		{"owner", "_users/owner/Chats/Code/projects/former-code", "code"},
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
		db.Close()
		if err != nil || status != "answered" || answeredAt == "" || answeredBy != test.actor || option != "approve" || consumed != "" || actor != test.actor || kind != "human_via_chat" || channel != "agent_chat" {
			t.Fatalf("saved answer: status=%s at=%s by=%s option=%s consumed=%s audit=%s/%s/%s err=%v", status, answeredAt, answeredBy, option, consumed, actor, kind, channel, err)
		}
	}
	pendingID := create(rootA, "still-pending")
	_, executors, _ := createReportHumanInputTools()
	answer := executors[name].(func(context.Context, map[string]interface{}) (string, error))
	args := map[string]interface{}{"workspace_path": rootA, "input_id": pendingID, "selected_option_id": "approve"}
	_, ownerCtx := register("identity-check", "owner", rootA, "work", false, nil)
	for _, claims := range []*UserClaims{{UserID: "co"}, {UserID: "owner", ExternalBuilderOperationID: "mcp-operation"}} {
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
		if _, ok := reg.tools[name]; ok {
			t.Fatalf("%s turn received the answer tool", label)
		}
		if _, err := answer(ctx, args); err == nil {
			t.Fatalf("%s turn answered through a retained tool", label)
		}
	}
	reg, ctx := register("reader", "reader", rootA, "work", true, nil)
	if _, ok := reg.tools[name]; ok {
		t.Fatal("Run person received the answer tool")
	}
	if _, err := answer(ctx, args); err == nil {
		t.Fatal("Run person answered through a retained tool")
	}
	_, otherCtx := register("other-crew", "owner", rootB, "work", false, nil)
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
	if _, err := answer(coCtx, args); err == nil {
		t.Fatal("removed co-owner answered through a retained tool")
	}
	pending, err := listReportHumanInputs(contextFor("owner", "check"), rootA, "pending", "")
	if err != nil || len(pending) != 1 || pending[0].ID != pendingID {
		t.Fatalf("refused calls changed the pending input: %+v %v", pending, err)
	}
}
