package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// PLAT-746: automatic turns in an owner's chat have the same handoff as their
// interactive turns. Actual execution scopes and pinned Run turns keep the
// Crew caller and cannot recurse into their own function.
func TestInteractiveBuilderCallsOwnDeclaredFunction(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := env.alpha["project_functions_define"].exec(ctx, map[string]any{"definition": map[string]any{"name": "own_fn", "description": "Own function", "action": map[string]any{"type": "message"}, "input": map[string]any{"messages": []string{"Return the result."}}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, trigger, scope string
		pinRun               bool
	}{
		{name: "function turn in own chat", trigger: "function"},
		{name: "schedule turn in own chat", trigger: "schedule"},
		{name: "notification in own chat", trigger: "notification"},
		{name: "trigger in own chat", trigger: "trigger"},
		{name: "interactive own chat", trigger: "interactive"},
		{name: "workflow execution", trigger: "function", scope: "run-step"},
		{name: "pinned Run", trigger: "interactive", pinRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := &recordingRegistrar{}
			req := QueryRequest{SelectedFolder: linkAlphaPath, AgentProfileID: "work", TriggeredBy: tc.trigger, PinRunMode: tc.pinRun}
			options := crewFunctionToolOptions{InteractiveOwnCalls: publishedBuilderHandoff(&req, false), ExecutionScope: tc.scope}
			if tc.scope != "" {
				options.FunctionTools = agentworksproduct.ExecutionFunctionTools()
			}
			if err := env.api.registerCrewFunctionToolSet(reg, "owner", "sess-builder", req, crewTriggerLinkCaller(linkAlphaPath), nil, options); err != nil {
				t.Fatal(err)
			}
			callTool, admitted := reg.tools["project_functions_call"]
			if !admitted || callTool.exec == nil {
				t.Fatal("fixture did not admit the declared function tool")
			}
			out, err := callTool.exec(ctx, map[string]any{"project": "Alpha", "function": "own_fn", "notify": false})
			if tc.scope != "" || tc.pinRun {
				if err == nil || !strings.Contains(err.Error(), "own function") {
					t.Fatalf("execution self-call guard changed: %s %v", out, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				CallID string `json:"call_id"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			call := lookupCrewFunctionCall(result.CallID)
			if call == nil {
				t.Fatalf("typed call was not recorded: %s", out)
			}
			t.Cleanup(func() { call.settle("cancelled", nil, "test completed") })
			if call.CallerKind != triggerCallerUser || call.CallerID != "owner" || call.UserID != "owner" || !call.IsolatedExecution || call.TargetID != "alpha" {
				t.Fatalf("interactive call lost the real person/isolated target: %+v", call.snapshot())
			}
			if _, err := reg.tools["project_functions_get_call"].exec(ctx, map[string]any{"call_id": call.ID}); err != nil {
				t.Fatalf("Builder cannot follow its own typed call: %v", err)
			}
		})
	}
}

// Exercise activation refresh against real workspace HTTP/disk, including
// source that has report assets but no authored dashboard document.
func TestPublicationRefreshesOnlyGeneratedDashboard(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	claims := GetUserFromContext(ctx)
	record := &publishedProject{Root: externalCommandCrewRoot, WorkspaceID: "team", ProfileID: "work", OwnerID: "owner", PublisherID: "owner", ProjectID: "laptop", Revision: "first-publication", SourcePaths: []string{"functions.json", "db/reports/theme.css"}}
	f.write(t, record.sourceRoot()+"/functions.json", `{"functions":[]}`)
	f.write(t, record.sourceRoot()+"/db/reports/theme.css", "body { color: white; }")
	if err := savePublishedProject(record); err != nil {
		t.Fatal(err)
	}
	target, err := f.api.dashboardTarget(ctx, claims, record.Root)
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		t.Helper()
		if err := f.api.refreshPublicationDashboard(ctx, claims, target); err != nil {
			t.Fatal(err)
		}
	}
	get := func() dashboards.Result {
		t.Helper()
		result, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: record.Root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
		if err != nil || result.Dashboard == nil || result.Snapshot == nil {
			t.Fatalf("published default unavailable: %+v %v", result, err)
		}
		return result
	}
	refresh()
	listedBefore, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: record.Root, Action: "list", IncludeDraft: true})
	if err != nil || len(listedBefore.Dashboards) != 0 {
		t.Fatalf("function publication created a missing dashboard: %+v %v", listedBefore, err)
	}
	// Seed an explicitly requested default; subsequent publication may refresh it.
	if _, _, err := f.api.ensureDefaultDashboard(ctx, claims, target); err != nil {
		t.Fatal(err)
	}
	before := get()
	record.Revision = "second-publication"
	f.write(t, record.sourceRoot()+"/functions.json", `{"functions":[{"id":"qa-ping","name":"qa_ping","enabled":true,"description":"Run the action","action":{"type":"message"},"input":{"messages":["Return the result."]}}]}`)
	f.write(t, record.sourceRoot()+"/db/reports/theme.css", "body { color: white; }")
	if err := savePublishedProject(record); err != nil {
		t.Fatal(err)
	}
	refresh()
	after := get()
	if after.Dashboard.Revision == before.Dashboard.Revision || !strings.Contains(after.Snapshot.Files["index.tsx"], `"name":"qa_ping"`) || !strings.Contains(after.Snapshot.Files["index.tsx"], `<FunctionButton name={action.name}`) || after.Dashboard.Published == "" {
		t.Fatalf("publication did not refresh its function button: %+v", after)
	}
	draft, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: record.Root, Action: "update", ID: defaultDashboardID, ExpectedRevision: after.Dashboard.Revision, IncludeDraft: true, Files: map[string]string{"index.html": "<html><title>Unfinished owner dashboard</title></html>"}})
	if err != nil {
		t.Fatal(err)
	}
	refresh()
	kept := get()
	if kept.Dashboard.Revision != draft.Dashboard.Revision || kept.Dashboard.Published != after.Dashboard.Published {
		t.Fatal("publication changed or published the owner's unfinished dashboard")
	}
	record.Revision = "authored-publication"
	record.SourcePaths = []string{"db/reports/index.html"}
	f.write(t, record.sourceRoot()+"/db/reports/index.html", "<html><title>Authored source</title></html>")
	if err := savePublishedProject(record); err != nil {
		t.Fatal(err)
	}
	refresh()
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: record.Root, Action: "list", IncludeDraft: true})
	if err != nil || len(listed.Dashboards) != 1 || listed.Dashboards[0].Managed || !strings.Contains(f.read(t, record.sourceRoot()+"/db/reports/index.html"), "Authored source") {
		t.Fatalf("authored immutable dashboard was replaced: %+v %v", listed, err)
	}
}
