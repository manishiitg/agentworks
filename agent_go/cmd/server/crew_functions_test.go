package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func muxRouterForCrewFunctions(svc *ProductScheduleService) *mux.Router {
	router := mux.NewRouter()
	ProductWebhookRoutes(router, svc)
	return router
}

func TestCrewFunctionSchemaValidation(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object", "required": []interface{}{"build", "env"},
		"properties": map[string]interface{}{
			"build":   map[string]interface{}{"type": "string"},
			"env":     map[string]interface{}{"type": "string", "enum": []interface{}{"staging", "prod"}},
			"retries": map[string]interface{}{"type": "integer"},
			"tags":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		},
	}
	if err := checkCrewFunctionSchema(schema, "input_schema"); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	for _, bad := range []map[string]interface{}{
		{"type": "date"},
		{"type": "object", "required": []interface{}{"missing"}, "properties": map[string]interface{}{}},
		{"type": "string", "enum": []interface{}{}},
		{"type": "array", "items": "string"},
	} {
		if err := checkCrewFunctionSchema(bad, "s"); err == nil {
			t.Fatalf("invalid schema accepted: %v", bad)
		}
	}
	if problems := validateCrewFunctionValue(schema, map[string]interface{}{"build": "812", "env": "staging", "retries": float64(2), "tags": []interface{}{"a"}}); len(problems) != 0 {
		t.Fatalf("valid value rejected: %v", problems)
	}
	problems := validateCrewFunctionValue(schema, map[string]interface{}{"build": float64(812), "env": "qa", "retries": 1.5, "tags": []interface{}{float64(1)}})
	joined := strings.Join(problems, " | ")
	for _, want := range []string{"$.build: expected string", "$.env: qa is not one of", "$.retries: expected integer", "$.tags[0]: expected string"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("problems %q missing %q", joined, want)
		}
	}
	if problems := validateCrewFunctionValue(schema, map[string]interface{}{"build": "812"}); len(problems) != 1 || !strings.Contains(problems[0], "$.env: required") {
		t.Fatalf("missing required = %v", problems)
	}
}

func TestCrewFunctionToolName(t *testing.T) {
	if got := crewFunctionToolName("RTS Flow Tester", "run_login_flow"); got != "rts_flow_tester__run_login_flow" {
		t.Fatalf("tool name = %q", got)
	}
	long := crewFunctionToolName(strings.Repeat("very long crew name ", 6), "run_login_flow")
	if len(long) > 64 || !strings.HasSuffix(long, "__run_login_flow") {
		t.Fatalf("long tool name = %q", long)
	}
}

type crewFunctionEnv struct {
	triggerLinkEnv
	alpha map[string]recordedTool
	beta  map[string]recordedTool
}

func newCrewFunctionEnv(t *testing.T) crewFunctionEnv {
	t.Helper()
	previousPoll, previousWait := triggerTargetPollInterval, crewFunctionFastWait
	triggerTargetPollInterval = 10 * time.Millisecond
	crewFunctionFastWait = 3 * time.Second
	t.Cleanup(func() { triggerTargetPollInterval, crewFunctionFastWait = previousPoll, previousWait })
	// Calls are process-wide; a call left in flight by an earlier test would
	// otherwise be joined by an identical call here.
	crewFunctionCalls.Lock()
	crewFunctionCalls.m = map[string]*crewFunctionCall{}
	crewFunctionCalls.Unlock()
	env := crewFunctionEnv{triggerLinkEnv: newTriggerLinkEnv(t)}
	env.alpha = env.functionTools(t, linkAlphaPath, "sess-caller", nil)
	env.beta = env.functionTools(t, linkBetaPath, "sess-beta-chat", nil)
	return env
}

func (env crewFunctionEnv) functionTools(t *testing.T, crewPath, sessionID string, contextPaths []string) map[string]recordedTool {
	t.Helper()
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", sessionID, QueryRequest{SelectedFolder: crewPath, WorkflowContextPaths: contextPaths}, crewTriggerLinkCaller(crewPath), nil); err != nil {
		t.Fatal(err)
	}
	return reg.tools
}

func TestInternalFunctionCallPendingInputAndReplyAcrossCallerKinds(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	workflowCaller, err := workflowTriggerLinkCaller("Workflow/reports")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workflowTarget, err := workflowTriggerLinkCaller("Workflow/pipeline")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", "workflow-caller", QueryRequest{SelectedFolder: "Workflow/reports"}, workflowTriggerLinkCaller("Workflow/reports"), nil); err != nil {
		t.Fatal(err)
	}
	store := virtualtools.GetHumanFeedbackStore()
	for _, tc := range []struct {
		name          string
		callID        string
		requestID     string
		callerKind    string
		callerID      string
		targetKind    string
		targetID      string
		targetPath    string
		targetProfile string
		tools         map[string]recordedTool
	}{
		{"crew-to-crew", "fn-internal-crew-crew", "request-internal-crew-crew", triggerCallerCrew, "alpha", triggerCallerCrew, "beta", linkBetaPath, "work", env.alpha},
		{"crew-to-workflow", "fn-internal-crew-workflow", "request-internal-crew-workflow", triggerCallerCrew, "alpha", triggerCallerWorkflow, workflowCaller.Stamp.ID, "Workflow/reports", "", env.alpha},
		{"workflow-to-crew", "fn-internal-workflow-crew", "request-internal-workflow-crew", triggerCallerWorkflow, workflowCaller.Stamp.ID, triggerCallerCrew, "beta", linkBetaPath, "work", reg.tools},
		{"workflow-to-workflow", "fn-internal-workflow-workflow", "request-internal-workflow-workflow", triggerCallerWorkflow, workflowCaller.Stamp.ID, triggerCallerWorkflow, workflowTarget.Stamp.ID, "Workflow/pipeline", "", reg.tools},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := &crewFunctionCall{ID: tc.callID, UserID: "owner", CallerKind: tc.callerKind, CallerID: tc.callerID, TargetKind: tc.targetKind, TargetID: tc.targetID, TargetPath: tc.targetPath, TargetProfileID: tc.targetProfile, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
			crewFunctionCalls.Lock()
			crewFunctionCalls.m[call.ID] = call
			crewFunctionCalls.Unlock()
			t.Cleanup(func() {
				crewFunctionCalls.Lock()
				delete(crewFunctionCalls.m, call.ID)
				crewFunctionCalls.Unlock()
				store.WithdrawOperation(call.ID)
			})
			if err := store.CreatePendingRequest(tc.requestID, "Which branch?", "", "shared-chat", []string{"main", "release"}, false, time.Minute, tc.callID); err != nil {
				t.Fatal(err)
			}
			foreignRequestID := tc.requestID + "-foreign"
			if err := store.CreatePendingRequest(foreignRequestID, "Unrelated question", "", "shared-chat", nil, true, time.Minute, "fn-other-call"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.WithdrawOperation("fn-other-call") })
			polled, err := tc.tools["get_function_call"].exec(ctx, map[string]interface{}{"call_id": call.ID})
			if err != nil || !strings.Contains(polled, tc.requestID) || strings.Contains(polled, foreignRequestID) {
				t.Fatalf("pending input missing: %s, %v", polled, err)
			}
			args := map[string]interface{}{"call_id": call.ID, "request_id": tc.requestID, "response": "main"}
			if _, err := env.beta["reply_function_call"].exec(ctx, args); err == nil {
				t.Fatal("target Crew answered the caller's pending input")
			}
			args["request_id"] = foreignRequestID
			if _, err := tc.tools["reply_function_call"].exec(ctx, args); err == nil {
				t.Fatal("another call's question was answered")
			}
			args["request_id"] = tc.requestID
			args["response"] = "wrong"
			if _, err := tc.tools["reply_function_call"].exec(ctx, args); err == nil {
				t.Fatal("invalid choice accepted")
			}
			// A stray space or newline around an exact choice does not matter,
			// whichever tool answers.
			args["response"] = " main\n"
			if _, err := tc.tools["reply_function_call"].exec(ctx, args); err != nil {
				t.Fatalf("valid answer refused: %v", err)
			}
			if _, err := tc.tools["reply_function_call"].exec(ctx, args); err == nil {
				t.Fatal("duplicate answer accepted")
			}
		})
	}
}

func TestInternalFunctionReplyRechecksTargetAccess(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", "workflow-caller", QueryRequest{SelectedFolder: "Workflow/reports"}, workflowTriggerLinkCaller("Workflow/reports"), nil); err != nil {
		t.Fatal(err)
	}
	const targetPath = "Workflow/revocable"
	manifest := NewWorkflowManifest("Revocable")
	manifest.CreatedBy = "other"
	manifest.Access = &WorkflowAccess{Owners: []string{"other"}, Editors: []string{"owner"}}
	writeManifest := func() {
		t.Helper()
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		env.mock.mu.Lock()
		env.mock.files[manifestPath(targetPath)] = string(raw)
		env.mock.mu.Unlock()
	}
	writeManifest()
	caller, err := workflowTriggerLinkCaller("Workflow/reports")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveFunctionTarget(ctx, &UserClaims{UserID: "owner"}, caller, targetPath)
	if err != nil {
		t.Fatalf("initial editor access: %v", err)
	}
	call := &crewFunctionCall{ID: "fn-revoked-target", UserID: "owner", CallerKind: caller.Stamp.Type, CallerID: caller.Stamp.ID,
		TargetKind: target.Kind, TargetID: target.stampID(), TargetPath: targetPath, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	store := virtualtools.GetHumanFeedbackStore()
	t.Cleanup(func() { store.WithdrawOperation(call.ID) })
	if err := store.CreatePendingRequest("revoked-target-request", "Proceed?", "", "shared-chat", []string{"yes", "no"}, false, time.Minute, call.ID); err != nil {
		t.Fatal(err)
	}
	manifest.Access.Editors = nil
	writeManifest()
	if _, err := reg.tools["reply_function_call"].exec(ctx, map[string]interface{}{
		"call_id": call.ID, "request_id": "revoked-target-request", "response": "yes",
	}); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("revoked editor answered target question: %v", err)
	}
}

func TestPrivateCodeCallerIsSeparateFromCrewWithSameProjectID(t *testing.T) {
	env := newCrewFunctionEnv(t)
	const codePath = "_users/owner/Chats/Code/projects/private-alpha"
	env.mock.mu.Lock()
	env.mock.files[codePath+"/product.json"] = `{"schema_version":1,"product":"code","id":"alpha","title":"Private acquisition project","session_id":"code-alpha"}`
	env.mock.mu.Unlock()
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	codeCaller, err := crewTriggerLinkCaller(codePath)(ctx)
	if err != nil || codeCaller.Stamp.ProfileID != "code" || codeCaller.Label != "private Code workspace" {
		t.Fatalf("private Code caller = %+v, %v", codeCaller, err)
	}
	if codeCaller.isTarget(triggerTarget{Kind: triggerCallerCrew, CrewID: "alpha", CrewProfile: "work"}) {
		t.Fatal("Code was mistaken for a Crew with the same project ID")
	}
	if target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, codePath); err == nil {
		t.Fatalf("private Code became a callable target: %+v", target)
	}
	codeReg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(codeReg, "owner", "code-caller", QueryRequest{SelectedFolder: codePath}, crewTriggerLinkCaller(codePath), nil); err != nil {
		t.Fatal(err)
	}
	if listed, err := codeReg.tools["list_functions"].exec(ctx, map[string]interface{}{}); err != nil || !strings.Contains(listed, `"ask"`) {
		t.Fatalf("Code could not list its own callable functions: %s, %v", listed, err)
	}
	store := virtualtools.GetHumanFeedbackStore()
	for _, profileID := range []string{"work", "code"} {
		id := "fn-same-project-" + profileID
		call := &crewFunctionCall{ID: id, UserID: "owner", CallerKind: triggerCallerCrew, CallerID: "alpha", CallerProfileID: profileID,
			CallerPath: map[string]string{"work": linkAlphaPath, "code": codePath}[profileID],
			TargetKind: triggerCallerCrew, TargetID: "beta", TargetProfileID: "work", TargetPath: linkBetaPath, Function: "ask", ArgumentsKey: crewFunctionArgumentsFingerprint("{}"),
			Status: "running", SubmissionID: "retry-same-id", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
		crewFunctionCalls.Lock()
		crewFunctionCalls.m[id] = call
		crewFunctionCalls.Unlock()
		t.Cleanup(func() {
			crewFunctionCalls.Lock()
			delete(crewFunctionCalls.m, id)
			crewFunctionCalls.Unlock()
			store.WithdrawOperation(id)
		})
	}
	if _, err := codeReg.tools["get_function_call"].exec(ctx, map[string]interface{}{"call_id": "fn-same-project-work"}); err == nil {
		t.Fatal("Code read the Crew's call with the same project ID")
	}
	if _, err := env.alpha["get_function_call"].exec(ctx, map[string]interface{}{"call_id": "fn-same-project-code"}); err == nil {
		t.Fatal("Crew read the private Code's call with the same project ID")
	}
	if err := store.CreatePendingRequest("private-code-question", "Which branch?", "", "private-code-chat", nil, true, time.Minute, "fn-same-project-code"); err != nil {
		t.Fatal(err)
	}
	if out, err := codeReg.tools["get_function_call"].exec(ctx, map[string]interface{}{"call_id": "fn-same-project-code"}); err != nil || !strings.Contains(out, "private-code-question") {
		t.Fatalf("Code cannot read its own pending input: %s, %v", out, err)
	}
	if _, err := codeReg.tools["reply_function_call"].exec(ctx, map[string]interface{}{"call_id": "fn-same-project-work", "request_id": "private-code-question", "response": "main"}); err == nil {
		t.Fatal("Code replied to the Crew's call")
	}
	if _, err := codeReg.tools["reply_function_call"].exec(ctx, map[string]interface{}{"call_id": "fn-same-project-code", "request_id": "private-code-question", "response": "main"}); err != nil {
		t.Fatalf("Code could not reply to its own call: %v", err)
	}
	targetCall := &crewFunctionCall{ID: "fn-target-work-alpha", UserID: "owner", CallerKind: triggerCallerCrew, CallerID: "beta", TargetKind: triggerCallerCrew, TargetID: "alpha", Status: "running", done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[targetCall.ID] = targetCall
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, targetCall.ID)
		crewFunctionCalls.Unlock()
	})
	if _, err := codeReg.tools["report_function_progress"].exec(ctx, map[string]interface{}{"call_id": targetCall.ID, "message": "done"}); err == nil {
		t.Fatal("private Code impersonated a Crew target with the same ID")
	}
	crewFunctionCalls.Lock()
	retried, found, err := inMemoryCrewFunctionSubmissionLocked("owner", codeCaller.Stamp, codePath, "retry-same-id", triggerTarget{Kind: triggerCallerCrew, Path: linkBetaPath, CrewID: "beta", CrewProfile: "work"}, "ask", "{}")
	crewFunctionCalls.Unlock()
	if err != nil || !found || retried.ID != "fn-same-project-code" {
		t.Fatalf("Code's submission ID crossed into Crew: call=%+v found=%v err=%v", retried, found, err)
	}
	if crewFunctionSubmissionPath("owner", codeCaller.Stamp, "retry-same-id", codePath) == crewFunctionSubmissionPath("owner", codeCaller.Stamp, "retry-same-id", "_users/another/Chats/Code/projects/private-alpha") {
		t.Fatal("same-ID Codes under different owners shared a submission index")
	}
}

func TestCodePeersAllowOwnerAndEditorOnlyWithBothGrants(t *testing.T) {
	env := newCrewFunctionEnv(t)
	profile := agentprofiles.Profile{ID: codeproduct.ProfileID, Name: "Code", Version: 1, BuiltIn: true, Product: "code", SystemPromptTemplate: "hi",
		Runtime: agentprofiles.RuntimePolicy{Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: codeproduct.ProjectsRoot}},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers", Options: map[string]string{"mode": "message_only"}}},
		UIPanels: agentprofiles.UIPanels{Schedules: true}}
	if err := env.svc.registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	const source = "_users/owner/Chats/Code/projects/source"
	const targetPath = "_users/owner/Chats/Code/projects/target"
	env.mock.mu.Lock()
	env.mock.files[source+"/product.json"] = `{"schema_version":1,"product":"code","id":"source","title":"Private source","session_id":"code-source"}`
	env.mock.files[targetPath+"/product.json"] = `{"schema_version":1,"product":"code","id":"target","title":"Private target","session_id":"code-target"}`
	env.mock.files["_users/other/Chats/Code/projects/foreign/product.json"] = `{"schema_version":1,"product":"code","id":"foreign","title":"Foreign","session_id":"code-foreign"}`
	env.mock.files[codeSharesFilePath()] = `{"projects":{"owner/source":{"owner_id":"owner","project_id":"source","grants":{"other":"editor"}},"owner/target":{"owner_id":"owner","project_id":"target","grants":{"other":"editor"}}}}`
	env.mock.mu.Unlock()
	var editorBindingID, editorRunID string
	for _, actor := range []string{"owner", "other"} {
		t.Run(actor, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: actor})
			caller, err := crewTriggerLinkCaller(source)(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target, err := resolveFunctionTarget(ctx, &UserClaims{UserID: actor}, caller, "#code:target")
			if err != nil || target.CrewOwner != "owner" || target.CrewProfile != "code" {
				t.Fatalf("target = %+v, %v", target, err)
			}
			if _, err := resolveFunctionTarget(ctx, &UserClaims{UserID: actor}, caller, "#code:foreign"); err == nil {
				t.Fatal("cross-owner Code was callable")
			}
			bindingID, created, err := env.api.connectTriggerTarget(ctx, actor, caller, target)
			if err != nil || bindingID == "" {
				t.Fatalf("connect = %q, %v, %v", bindingID, created, err)
			}
			if actor == "owner" && !created {
				t.Fatal("owner binding was not created")
			}
			if actor == "other" && created {
				t.Fatal("editor did not reuse the binding")
			}
			delivery, err := env.api.dispatchTargetTrigger(ctx, actor, caller, target, bindingID, "code-test-"+actor, crewFunctionEvent, map[string]interface{}{"task": "Check the project"})
			if err != nil || delivery.RunID == "" {
				t.Fatalf("dispatch = %+v, %v", delivery, err)
			}
			if actor == "other" {
				editorBindingID, editorRunID = bindingID, delivery.RunID
			}
			if got := codePeerPrivateRunsWorkspace(actor, targetPath, "target"); !strings.HasPrefix(got, "_users/"+actor+"/chat_history/") {
				t.Fatalf("Code run history is not private to %s: %s", actor, got)
			}
			if actor == "other" {
				binding, err := codePeerRunBinding(ctx, actor, profile, "target", targetPath, bindingID, "Peer call")
				if err != nil || binding.ManifestPath != "" || binding.WorkspacePath != targetPath {
					t.Fatalf("editor chat binding = %+v, %v", binding, err)
				}
			}
			if _, err := env.svc.getInternalProductTriggerRun(ctx, actor, "code", "target", bindingID, delivery.RunID, caller.Stamp, "owner"); err != nil {
				t.Fatalf("poll = %v", err)
			}
		})
	}
	// A typed result must be returned by the target Code's own tool surface.
	// The caller and target are different Code paths even though both run as
	// the same editor, which previously made callRecord reject the target.
	editorCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	sourceTools := env.functionTools(t, source, "code-peer-source-editor", nil)
	targetTools := env.functionTools(t, targetPath, "code-peer-target-editor", nil)
	if _, err := targetTools["define_function"].exec(editorCtx, map[string]interface{}{
		"name": "review_changes", "description": "Review a change", "instructions": "Return whether it passed.",
		"input_schema":  map[string]interface{}{"type": "object", "required": []interface{}{"change"}, "properties": map[string]interface{}{"change": map[string]interface{}{"type": "string"}}},
		"result_schema": map[string]interface{}{"type": "object", "required": []interface{}{"ok"}, "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean"}}},
	}); err != nil {
		t.Fatalf("define Code function: %v", err)
	}
	started, err := sourceTools["call_function"].exec(editorCtx, map[string]interface{}{"target": "#code:target", "function": "review_changes", "args": map[string]interface{}{"change": "docs"}, "notify": false})
	if err != nil {
		t.Fatalf("call Code function: %v", err)
	}
	var startedCall map[string]interface{}
	if json.Unmarshal([]byte(started), &startedCall) != nil {
		t.Fatalf("call response: %s", started)
	}
	callID, _ := startedCall["call_id"].(string)
	if callID == "" {
		t.Fatalf("call response omitted call_id: %s", started)
	}
	callRecord := lookupCrewFunctionCall(callID)
	env.mock.mu.Lock()
	targetRecord := env.mock.files[callRecord.recordPath()]
	privateIndex := env.mock.files[crewFunctionCallIndexPath(callID)]
	env.mock.mu.Unlock()
	if strings.Contains(targetRecord, source) || strings.Contains(targetRecord, `"caller_path"`) || !strings.Contains(privateIndex, source) {
		t.Fatal("Code source path leaked into the target call record or was lost from the private index")
	}
	if _, err := targetTools["report_function_progress"].exec(editorCtx, map[string]interface{}{"call_id": callID, "message": "checking"}); err != nil {
		t.Fatalf("target Code could not report progress: %v", err)
	}
	if _, err := targetTools["return_function_result"].exec(editorCtx, map[string]interface{}{"call_id": callID, "result": map[string]interface{}{"ok": true}}); err != nil {
		t.Fatalf("target Code could not return typed result: %v", err)
	}
	if got, err := sourceTools["get_function_call"].exec(editorCtx, map[string]interface{}{"call_id": callID}); err != nil || !strings.Contains(got, `"completed"`) {
		t.Fatalf("caller did not receive typed result: %s, %v", got, err)
	}
	// Public Code webhooks still use the owner-run Automation path. The
	// hidden peer binding must not appear in the owner's Automation panel.
	publicTrigger := productWebhookTrigger{ID: "11111111-1111-4111-8111-111111111111", Name: "Owner webhook", Enabled: true, Message: "Handle the event"}
	env.mock.mu.Lock()
	var targetManifest productProjectManifest
	_ = json.Unmarshal([]byte(env.mock.files[targetPath+"/product.json"]), &targetManifest)
	targetManifest.Triggers = append(targetManifest.Triggers, publicTrigger)
	encodedManifest, _ := json.Marshal(targetManifest)
	env.mock.files[targetPath+"/product.json"] = string(encodedManifest)
	env.mock.mu.Unlock()
	ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	request := httptest.NewRequest(http.MethodGet, "/api/product/triggers?profile_id=code&project_id=target", nil).WithContext(ownerCtx)
	recorder := httptest.NewRecorder()
	env.svc.listProductWebhooks(recorder, request)
	var listed struct {
		Triggers []productWebhookResponse `json:"triggers"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &listed) != nil || len(listed.Triggers) != 1 || listed.Triggers[0].ID != publicTrigger.ID {
		t.Fatalf("Code Automation panel exposed peer binding: %d %s", recorder.Code, recorder.Body.String())
	}
	publicDelivery, err := env.svc.deliverProductTrigger(ownerCtx, &productWebhookMatch{UserID: "owner", Profile: profile,
		Binding: productConversationBinding{WorkspacePath: targetPath}, Manifest: targetManifest, Trigger: publicTrigger},
		"public-code-webhook", "", []byte(`{"event":"build"}`), "Authenticated webhook.", nil)
	if err != nil || publicDelivery.RunID == "" {
		t.Fatalf("public Code webhook = %+v, %v", publicDelivery, err)
	}
	if _, err := FindScheduleRun(ownerCtx, targetPath, publicDelivery.RunID); err != nil {
		t.Fatalf("public Code run history moved: %v", err)
	}
	env.svc.mu.Lock()
	publicQueued := false
	for _, queue := range env.svc.queued {
		for _, item := range queue {
			if item.options.RunID == publicDelivery.RunID && item.job.PeerSourceID == "" && strings.Contains(item.job.Schedule.Messages[0], "untrusted data") {
				publicQueued = true
			}
		}
	}
	env.svc.mu.Unlock()
	if !publicQueued {
		t.Fatal("public Code webhook was treated as a private peer call")
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	caller, _ := crewTriggerLinkCaller(source)(ctx)
	target, _ := resolveFunctionTarget(ctx, &UserClaims{UserID: "other"}, caller, "#code:target")
	env.mock.mu.Lock()
	env.mock.files[codeSharesFilePath()] = `{"projects":{"owner/source":{"owner_id":"owner","project_id":"source","grants":{"other":"editor"}}}}`
	env.mock.mu.Unlock()
	if _, err := resolveFunctionTarget(ctx, &UserClaims{UserID: "other"}, caller, "#code:target"); err == nil {
		t.Fatal("editor without target grant could resolve target")
	}
	if _, err := env.svc.getInternalProductTriggerRun(ctx, "other", "code", "target", editorBindingID, editorRunID, caller.Stamp, "owner"); err == nil {
		t.Fatal("revoked editor read a private Code run")
	}
	if _, _, err := env.api.connectTriggerTarget(ctx, "other", caller, target); err == nil {
		t.Fatal("revoked editor reconnected")
	}
	if _, err := env.api.dispatchTargetTrigger(ctx, "other", caller, target, "existing", "revoked", crewFunctionEvent, map[string]interface{}{"task": "No"}); err == nil {
		t.Fatal("revoked editor dispatched")
	}
	crewCaller, _ := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if _, err := resolveFunctionTarget(ctx, &UserClaims{UserID: "other"}, crewCaller, "#code:target"); err == nil {
		t.Fatal("Crew resolved private Code")
	}
	if _, _, err := env.svc.saveProductWebhookConfig(ctx, "owner", productWebhookRequest{ProfileID: "code", ProjectID: "target", Kind: triggerKindInternal, Caller: &caller.Stamp}, ""); err == nil {
		t.Fatal("public trigger API enabled for Code")
	}
	call := &crewFunctionCall{ID: "fn-private-code-record", UserID: "other", TargetProfileID: "code", TargetPath: targetPath}
	if got := call.recordPath(); strings.HasPrefix(got, targetPath) || !strings.HasPrefix(got, "_users/other/chat_history/") {
		t.Fatalf("Code call record was shared: %s", got)
	}
}

func TestFunctionSubmissionSaveFailureSettlesConcurrentRetry(t *testing.T) {
	env := newCrewFunctionEnv(t)
	blocked := make(chan struct{}, 1)
	release := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/function_submissions/") {
			select {
			case blocked <- struct{}{}:
			default:
			}
			<-release
			http.Error(w, "write failed", http.StatusServiceUnavailable)
			return
		}
		env.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("WORKSPACE_API_URL", proxy.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	firstErr := make(chan error, 1)
	start := func() (*crewFunctionCall, error) {
		return env.api.startCrewFunctionCall(ctx, "owner", caller, target, defaultAskCrewFunction(), map[string]interface{}{"message": "status"}, time.Minute, "same-submission")
	}
	go func() { _, err := start(); firstErr <- err }()
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("first call never reached submission save")
	}
	retry, err := start()
	if err != nil || retry == nil {
		close(release)
		t.Fatalf("concurrent retry did not join: %+v, %v", retry, err)
	}
	close(release)
	if err := <-firstErr; err == nil {
		t.Fatal("failed submission save was reported as success")
	}
	select {
	case <-retry.done:
	case <-time.After(3 * time.Second):
		t.Fatal("joined retry never settled")
	}
	if status, _ := retry.snapshot()["status"].(string); status != "failed" {
		t.Fatalf("joined retry status = %q", status)
	}
	if lookupCrewFunctionCall(retry.ID) != retry {
		t.Fatal("joined call disappeared before retry could poll it")
	}
}

var loginFlowArgs = map[string]interface{}{
	"target": "Beta", "name": "run_login_flow", "description": "Run the login flow against a build.",
	"instructions": "Run tests/login.py with the build and report the failing step.",
	"input_schema": map[string]interface{}{"type": "object", "required": []interface{}{"build"}, "properties": map[string]interface{}{
		"build": map[string]interface{}{"type": "string"}, "env": map[string]interface{}{"type": "string", "enum": []interface{}{"staging", "prod"}},
	}},
	"result_schema": map[string]interface{}{"type": "object", "required": []interface{}{"passed"}, "properties": map[string]interface{}{
		"passed": map[string]interface{}{"type": "boolean"}, "failed_step": map[string]interface{}{"type": "integer"},
	}},
}

// waitForCall returns the in-flight call of function from the registry.
func waitForCall(t *testing.T, function string) *crewFunctionCall {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		crewFunctionCalls.Lock()
		for _, call := range crewFunctionCalls.m {
			call.mu.Lock()
			match := call.Function == function && !call.terminalLocked() && call.RunID != ""
			call.mu.Unlock()
			if match {
				crewFunctionCalls.Unlock()
				return call
			}
		}
		crewFunctionCalls.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no in-flight call of %s", function)
	return nil
}

// Alpha declares a function on Beta, calls it, Beta reports progress and
// returns a valid result within the fast window: the caller's tool call
// returns the validated result directly.
func TestCrewFunctionFastPathReturnsValidatedResult(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.Background()
	defined, err := env.alpha["define_function"].exec(ctx, loginFlowArgs)
	if err != nil {
		t.Fatalf("define: %v", err)
	}
	if got := decodeToolJSON(t, defined); got["tool_name_for_callers"] != "beta__run_login_flow" {
		t.Fatalf("define = %v", got)
	}
	env.mock.mu.Lock()
	stored := env.mock.files[agentProfileRuntimeWorkspace("owner", linkBetaPath)+"/functions.json"]
	env.mock.mu.Unlock()
	if !strings.Contains(stored, `"run_login_flow"`) || !strings.Contains(stored, `"created_by": "crew:alpha (Alpha Bot)"`) {
		t.Fatalf("functions.json = %s", stored)
	}
	listed, err := env.alpha["list_functions"].exec(ctx, map[string]interface{}{"target": "#crew:Beta"})
	if err != nil || !strings.Contains(listed, "run_login_flow") {
		t.Fatalf("list = %s, %v", listed, err)
	}

	if _, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "run_login_flow", "args": map[string]interface{}{"env": "qa"}}); err == nil || !strings.Contains(err.Error(), "$.build: required") {
		t.Fatalf("invalid args: err = %v", err)
	}

	go func() {
		call := waitForCall(t, "run_login_flow")
		if _, err := env.alpha["return_function_result"].exec(ctx, map[string]interface{}{"call_id": call.ID, "result": map[string]interface{}{"passed": true}}); err == nil {
			t.Error("the caller must not be able to return the target's result")
		}
		if _, err := env.beta["report_function_progress"].exec(ctx, map[string]interface{}{"call_id": call.ID, "message": "login page loaded", "percent": float64(40)}); err != nil {
			t.Errorf("progress: %v", err)
		}
		if _, err := env.beta["return_function_result"].exec(ctx, map[string]interface{}{"call_id": call.ID, "result": map[string]interface{}{"passed": "yes"}}); err == nil || !strings.Contains(err.Error(), "$.passed: expected boolean") {
			t.Errorf("invalid result: err = %v", err)
		}
		if _, err := env.beta["return_function_result"].exec(ctx, map[string]interface{}{"call_id": call.ID, "result": map[string]interface{}{"passed": false, "failed_step": float64(3)}}); err != nil {
			t.Errorf("valid result: %v", err)
		}
	}()
	out, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "run_login_flow", "args": map[string]interface{}{"build": "812", "env": "staging"}, "wait_seconds": 3})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	result := decodeToolJSON(t, out)
	if result["status"] != "completed" {
		t.Fatalf("call = %v", result)
	}
	if got, _ := json.Marshal(result["result"]); string(got) != `{"failed_step":3,"passed":false}` {
		t.Fatalf("result = %s", got)
	}
	progress, _ := result["progress"].([]interface{})
	if len(progress) != 1 || !strings.Contains(out, "login page loaded") {
		t.Fatalf("progress = %v", result["progress"])
	}
	// The delivery carried the call ID, the validated args and the contract.
	callID, _ := result["call_id"].(string)
	env.mock.mu.Lock()
	var delivery string
	for path, content := range env.mock.files {
		if strings.HasPrefix(path, linkBetaPath+"/triggers/deliveries/") && strings.Contains(content, callID) {
			delivery = content
		}
	}
	env.mock.mu.Unlock()
	for _, want := range []string{`"build":"812"`, "return_function_result", "report_function_progress"} {
		if !strings.Contains(delivery, want) {
			t.Fatalf("delivery missing %q: %s", want, delivery)
		}
	}
}

// A call slower than the fast window returns running and resumes the
// caller's chat with the result; get_function_call shows progress while it
// runs; a Crew target accepts update questions only once its run started.
func TestCrewFunctionSlowPathAutoNotifiesAndReportsProgress(t *testing.T) {
	env := newCrewFunctionEnv(t)
	crewFunctionFastWait = 20 * time.Millisecond
	ctx := context.Background()
	if _, err := env.alpha["define_function"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	out, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "run_login_flow", "args": map[string]interface{}{"build": "900"}})
	if err != nil {
		t.Fatal(err)
	}
	running := decodeToolJSON(t, out)
	callID, _ := running["call_id"].(string)
	notify, ok := running["auto_notification"].(map[string]interface{})
	if running["status"] != "running" || callID == "" || !ok {
		t.Fatalf("slow call = %v", running)
	}
	executionID, _ := notify["execution_id"].(string)

	if _, err := env.alpha["ask_function_update"].exec(ctx, map[string]interface{}{"call_id": callID, "question": "how far along?"}); err == nil || !strings.Contains(err.Error(), "has not started yet") {
		t.Fatalf("update before start: err = %v", err)
	}
	if _, err := env.beta["report_function_progress"].exec(ctx, map[string]interface{}{"call_id": callID, "message": "step 2 of 5"}); err != nil {
		t.Fatal(err)
	}
	polled, err := env.alpha["get_function_call"].exec(ctx, map[string]interface{}{"call_id": callID})
	if err != nil || !strings.Contains(polled, "step 2 of 5") {
		t.Fatalf("poll = %s, %v", polled, err)
	}
	if _, err := env.beta["return_function_result"].exec(ctx, map[string]interface{}{"call_id": callID, "result": map[string]interface{}{"passed": true}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot := env.api.bgAgentRegistry.Get("sess-caller", executionID).GetSnapshot()
		if snapshot.Status == BGAgentCompleted {
			if !strings.Contains(snapshot.Result, `"passed": true`) || !strings.Contains(snapshot.Result, "completed") {
				t.Fatalf("notification = %q", snapshot.Result)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("notification not delivered: %+v", snapshot)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A Crew run that ends without return_function_result gets one retry turn;
// if that also ends without a result the call fails with the final answer.
func TestCrewFunctionRunWithoutResultRetriesThenFails(t *testing.T) {
	env := newCrewFunctionEnv(t)
	crewFunctionFastWait = 20 * time.Millisecond
	ctx := context.Background()
	if _, err := env.alpha["define_function"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	out, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "run_login_flow", "args": map[string]interface{}{"build": "1"}, "notify": false, "wait_seconds": 0.02})
	if err != nil {
		t.Fatal(err)
	}
	call := lookupCrewFunctionCall(decodeToolJSON(t, out)["call_id"].(string))
	runs := agentProfileRuntimeWorkspace("owner", linkBetaPath)
	finishRun := func(runID, answer string) {
		if err := UpdateScheduleRunFinalResponse(ctx, runs, runID, answer); err != nil {
			t.Fatal(err)
		}
		if err := UpdateScheduleRun(ctx, runs, runID, "success", "", nil, "", "sess-beta"); err != nil {
			t.Fatal(err)
		}
	}
	call.mu.Lock()
	first := call.RunID
	call.mu.Unlock()
	finishRun(first, "I ran it, all good.")
	deadline := time.Now().Add(5 * time.Second)
	var retry string
	for retry == "" {
		call.mu.Lock()
		if call.RunID != first {
			retry = call.RunID
		}
		call.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("no retry turn was dispatched")
		}
		time.Sleep(5 * time.Millisecond)
	}
	finishRun(retry, "Still no result, sorry.")
	select {
	case <-call.done:
	case <-time.After(5 * time.Second):
		t.Fatal("call did not fail after the retry")
	}
	snapshot := call.snapshot()
	if snapshot["status"] != "failed" || !strings.Contains(snapshot["error"].(string), "without returning a valid result") || snapshot["final_reply"] != "Still no result, sorry." {
		t.Fatalf("snapshot = %v", snapshot)
	}
}

// Self-calls, cycles and deep chains are refused; mid-run questions are for
// Crew targets only; generated tools appear for tagged Crews.
func TestCrewFunctionGuardsAndGeneratedTools(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.Background()
	if _, err := env.alpha["define_function"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	own := map[string]interface{}{"name": "own_fn", "description": "d", "instructions": "i"}
	if _, err := env.alpha["define_function"].exec(ctx, own); err != nil {
		t.Fatalf("define own: %v", err)
	}
	if _, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Alpha", "function": "own_fn"}); err == nil || !strings.Contains(err.Error(), "cannot call itself") && !strings.Contains(err.Error(), "own function") {
		t.Fatalf("self call: err = %v", err)
	}

	// Alpha is currently running a call that Beta made: Alpha calling Beta
	// back would loop.
	inflight := &crewFunctionCall{ID: "fn-inflight", Function: "x", TargetKind: triggerCallerCrew, TargetID: "alpha", CallerKind: triggerCallerCrew, CallerID: "beta",
		Chain: []string{"crew:work:beta", "crew:work:alpha"}, Root: "fn-inflight", Status: "running", done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[inflight.ID] = inflight
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, inflight.ID)
		crewFunctionCalls.Unlock()
	})
	if _, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "run_login_flow", "args": map[string]interface{}{"build": "1"}}); err == nil || !strings.Contains(err.Error(), "already in this call chain") {
		t.Fatalf("cycle: err = %v", err)
	}
	inflight.mu.Lock()
	inflight.Chain = []string{"crew:work:x1", "crew:work:x2", "crew:work:x3", "crew:work:alpha"}
	inflight.mu.Unlock()
	if _, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "run_login_flow", "args": map[string]interface{}{"build": "1"}}); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("depth: err = %v", err)
	}
	inflight.finish("completed", nil, "")

	// Workflow targets take no mid-run questions.
	wfCall := &crewFunctionCall{ID: "fn-wf", Function: "report", TargetKind: triggerCallerWorkflow, TargetID: "wf", CallerKind: triggerCallerCrew, CallerID: "alpha", Status: "running", done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[wfCall.ID] = wfCall
	crewFunctionCalls.Unlock()
	if _, err := env.alpha["ask_function_update"].exec(ctx, map[string]interface{}{"call_id": "fn-wf", "question": "status?"}); err == nil || !strings.Contains(err.Error(), "workflow runs do not accept") {
		t.Fatalf("workflow update: err = %v", err)
	}
	if _, err := env.beta["ask_function_update"].exec(ctx, map[string]interface{}{"call_id": "fn-wf", "question": "status?"}); err == nil || !strings.Contains(err.Error(), "only the caller") {
		t.Fatalf("non-caller update: err = %v", err)
	}

	tagged := env.functionTools(t, linkAlphaPath, "sess-caller-2", []string{linkBetaPath})
	generated, ok := tagged["beta__run_login_flow"]
	if !ok {
		t.Fatalf("generated tool missing: %v", crewFunctionToolKeys(tagged))
	}
	if _, err := generated.exec(ctx, map[string]interface{}{"env": "prod"}); err == nil || !strings.Contains(err.Error(), "$.build: required") {
		t.Fatalf("generated tool validation: err = %v", err)
	}
}

func crewFunctionToolKeys(tools map[string]recordedTool) []string {
	out := make([]string, 0, len(tools))
	for name := range tools {
		out = append(out, name)
	}
	return out
}

// A Crew that declares no functions is still callable through the implicit
// ask function: it is listed, generated as a tool for tagged Crews, and its
// result is the target's final free-text reply.
func TestCrewFunctionDefaultAskUsesFinalReply(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.Background()
	listed, err := env.alpha["list_functions"].exec(ctx, map[string]interface{}{"target": "Beta"})
	if err != nil || !strings.Contains(listed, `"name": "ask"`) {
		t.Fatalf("list = %s, %v", listed, err)
	}
	tagged := env.functionTools(t, linkAlphaPath, "sess-caller-ask", []string{linkBetaPath})
	if _, ok := tagged["beta__ask"]; !ok {
		t.Fatalf("beta__ask not generated: %v", crewFunctionToolKeys(tagged))
	}
	if _, err := env.alpha["call_function"].exec(ctx, map[string]interface{}{"target": "Beta", "function": "ask", "args": map[string]interface{}{}}); err == nil || !strings.Contains(err.Error(), "$.message: required") {
		t.Fatalf("ask without message: err = %v", err)
	}

	crewFunctionFastWait = 5 * time.Second
	go func() {
		call := waitForCall(t, crewFunctionAskName)
		call.mu.Lock()
		runID, freeText := call.RunID, call.FreeText
		call.mu.Unlock()
		if !freeText {
			t.Error("default ask must be a free-text call")
		}
		runs := agentProfileRuntimeWorkspace("owner", linkBetaPath)
		if err := UpdateScheduleRunFinalResponse(ctx, runs, runID, "Three open bugs: A, B, C."); err != nil {
			t.Error(err)
		}
		if err := UpdateScheduleRun(ctx, runs, runID, "success", "", nil, "", "sess-beta"); err != nil {
			t.Error(err)
		}
	}()
	out, err := tagged["beta__ask"].exec(ctx, map[string]interface{}{"message": "How many open bugs?"})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	// Generated tools return at once; the answer is read from the call.
	result := decodeToolJSON(t, out)
	if call := lookupCrewFunctionCall(result["call_id"].(string)); call != nil {
		select {
		case <-call.done:
		case <-time.After(3 * time.Second):
		}
		result = call.snapshot()
	}
	answer, _ := result["result"].(map[string]interface{})
	if result["status"] != "completed" || answer["answer"] != "Three open bugs: A, B, C." {
		t.Fatalf("ask result = %v", result)
	}
	env.mock.mu.Lock()
	found := false
	for path, content := range env.mock.files {
		if strings.HasPrefix(path, linkBetaPath+"/triggers/deliveries/") && strings.Contains(content, "How many open bugs?") && strings.Contains(content, "exposed as a typed function") {
			found = true
		}
	}
	env.mock.mu.Unlock()
	if !found {
		t.Fatal("ask delivery did not carry the message and the offer-a-function hint")
	}
}

// The Automation panel's Functions tab lists declared functions plus the
// implicit ask and the Crew's recent calls; the owner can delete a function
// but not the built-in ask.
func TestCrewFunctionsHTTPListAndDelete(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.Background()
	if _, err := env.alpha["define_function"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	router := muxRouterForCrewFunctions(env.svc)
	get := httptest.NewRequest(http.MethodGet, "/api/crew-functions?profile_id=work&project_id=beta", nil)
	get = get.WithContext(context.WithValue(get.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", recorder.Code, recorder.Body.String())
	}
	var listed struct {
		Functions []struct {
			Name     string `json:"name"`
			Implicit bool   `json:"implicit"`
		} `json:"functions"`
		Calls []interface{} `json:"calls"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, fn := range listed.Functions {
		names[fn.Name] = fn.Implicit
	}
	if implicit, ok := names["ask"]; !ok || !implicit {
		t.Fatalf("functions = %+v", listed.Functions)
	}
	if implicit, ok := names["run_login_flow"]; !ok || implicit {
		t.Fatalf("functions = %+v", listed.Functions)
	}
	for _, tc := range []struct {
		name string
		code int
	}{{"ask", http.StatusBadRequest}, {"run_login_flow", http.StatusNoContent}, {"run_login_flow", http.StatusNotFound}} {
		del := httptest.NewRequest(http.MethodDelete, "/api/crew-functions/"+tc.name+"?profile_id=work&project_id=beta", nil)
		del = del.WithContext(context.WithValue(del.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, del)
		if recorder.Code != tc.code {
			t.Fatalf("delete %s = %d, want %d: %s", tc.name, recorder.Code, tc.code, recorder.Body.String())
		}
	}
}

func TestCrewFunctionFreeTextAnswerNeverRejectsStructuredAnswers(t *testing.T) {
	schema := map[string]interface{}{"type": "object", "required": []interface{}{"answer"}, "properties": map[string]interface{}{"answer": map[string]interface{}{"type": "string"}}}
	for _, in := range []interface{}{
		map[string]interface{}{"answer": "plain"},
		map[string]interface{}{"answer": map[string]interface{}{"WEB-1764": "PASS", "video": "https://x/v.mp4"}},
		map[string]interface{}{"tickets": []interface{}{"WEB-1764"}},
		"bare text",
	} {
		out := crewFunctionFreeTextAnswer(in)
		if problems := validateCrewFunctionValue(schema, out); len(problems) > 0 {
			t.Fatalf("%v -> %v rejected: %v", in, out, problems)
		}
	}
	got := crewFunctionFreeTextAnswer(map[string]interface{}{"answer": map[string]interface{}{"video": "https://x/v.mp4"}}).(map[string]interface{})["answer"].(string)
	if !strings.Contains(got, "https://x/v.mp4") {
		t.Fatalf("structured answer lost its content: %q", got)
	}
}

func TestCrewFunctionFailureDetailKeepsPartialWork(t *testing.T) {
	detail := crewFunctionFailureDetail(map[string]interface{}{"partial_result": map[string]interface{}{"video": "https://x/v.mp4"}, "final_reply": "1764 PASS"})
	if !strings.Contains(detail, "https://x/v.mp4") || !strings.Contains(detail, "1764 PASS") {
		t.Fatalf("failure detail dropped the target's work: %q", detail)
	}
}

// A private peer call to a Code is listed only for the person who made it:
// the Code's owner never sees an editor's call results (#246 review H1).
func TestRecentCallsHidePrivateCodePeerCallsFromOthers(t *testing.T) {
	crewFunctionCalls.Lock()
	saved := crewFunctionCalls.m
	crewFunctionCalls.m = map[string]*crewFunctionCall{
		"fn-peer": {ID: "fn-peer", UserID: "bob", TargetKind: triggerCallerCrew, TargetID: "code-y", TargetProfileID: codeproduct.ProfileID, Function: "run", Status: "done", Result: map[string]interface{}{"secret": "from bob's MCP"}},
		"fn-crew": {ID: "fn-crew", UserID: "bob", TargetKind: triggerCallerCrew, TargetID: "crew-z", TargetProfileID: "work", Function: "run", Status: "done"},
	}
	crewFunctionCalls.Unlock()
	t.Cleanup(func() { crewFunctionCalls.Lock(); crewFunctionCalls.m = saved; crewFunctionCalls.Unlock() })

	if got := recentCrewFunctionCalls("code-y", "alice"); len(got) != 0 {
		t.Fatalf("owner saw an editor's private peer call: %+v", got)
	}
	if got := recentCrewFunctionCalls("code-y", "bob"); len(got) != 1 || got[0].CallID != "fn-peer" {
		t.Fatalf("the caller must see their own peer call: %+v", got)
	}
	// Calls to a Crew stay visible to whoever can open that Crew.
	if got := recentCrewFunctionCalls("crew-z", "alice"); len(got) != 1 {
		t.Fatalf("Crew calls list: %+v", got)
	}
}
