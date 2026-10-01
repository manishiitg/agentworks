package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const ownedCodeFunctionPath = "_users/owner/Chats/Code/projects/target"

func ownedCodeFunctionEnv(t *testing.T) crewFunctionEnv {
	t.Helper()
	env := newCrewFunctionEnv(t)
	profile := agentprofiles.Profile{ID: codeproduct.ProfileID, Name: "Code", Version: 1, BuiltIn: true, Product: "code", SystemPromptTemplate: "hi",
		Runtime: agentprofiles.RuntimePolicy{Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace: agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: codeproduct.ProjectsRoot}},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers", Options: map[string]string{"mode": "message_only"}}}}
	if err := env.svc.registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	env.mock.mu.Lock()
	env.mock.files[ownedCodeFunctionPath+"/product.json"] = `{"schema_version":1,"product":"code","id":"target","title":"Private app","session_id":"code-target"}`
	env.mock.mu.Unlock()
	tools := env.functionTools(t, ownedCodeFunctionPath, "target-code", nil)
	if _, err := tools["define_function"].exec(context.Background(), map[string]interface{}{
		"name": "check_build", "description": "Check a build", "instructions": "Return the build status.",
		"input_schema":  map[string]interface{}{"type": "object", "required": []interface{}{"build"}, "properties": map[string]interface{}{"build": map[string]interface{}{"type": "string"}}},
		"result_schema": map[string]interface{}{"type": "object", "required": []interface{}{"ok"}, "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean"}}},
	}); err != nil {
		t.Fatal(err)
	}
	return env
}

func privateCodeWorkflowTools(t *testing.T, env crewFunctionEnv, actor string) map[string]recordedTool {
	t.Helper()
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, actor, "workflow-code-"+actor, QueryRequest{SelectedFolder: "Workflow/reports"}, workflowTriggerLinkCaller("Workflow/reports"), nil); err != nil {
		t.Fatal(err)
	}
	return reg.tools
}

func TestOwnedCrewAndWorkflowCanCallDeclaredCodeFunctions(t *testing.T) {
	for _, kind := range []string{"crew", "workflow"} {
		t.Run(kind, func(t *testing.T) {
			env := ownedCodeFunctionEnv(t)
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
			sourceTools := env.alpha
			if kind == "workflow" {
				sourceTools = privateCodeWorkflowTools(t, env, "owner")
			}
			targetTools := env.functionTools(t, ownedCodeFunctionPath, "target-code", nil)
			listed, err := sourceTools["list_functions"].exec(ctx, map[string]interface{}{"target": "#code:target"})
			if err != nil || !strings.Contains(listed, "check_build") || strings.Contains(listed, `"name": "ask"`) {
				t.Fatalf("list = %s, %v", listed, err)
			}
			if _, err := sourceTools["call_function"].exec(ctx, map[string]interface{}{"target": "#code:target", "function": "ask", "args": map[string]interface{}{"message": "open shell"}, "notify": false}); err == nil {
				t.Fatal("implicit ask exposed Code")
			}
			for _, name := range []string{"define_function", "delete_function"} {
				if _, err := sourceTools[name].exec(ctx, map[string]interface{}{"target": "#code:target", "name": "check_build", "description": "Changed", "instructions": "Change the Code."}); err == nil {
					t.Fatalf("%s let Crew/workflow author Code", name)
				}
			}
			response, err := sourceTools["call_function"].exec(ctx, map[string]interface{}{"target": "#code:target", "function": "check_build", "args": map[string]interface{}{"build": "123"}, "notify": false})
			if err != nil {
				t.Fatal(err)
			}
			var started map[string]interface{}
			if json.Unmarshal([]byte(response), &started) != nil {
				t.Fatal(response)
			}
			callID, _ := started["call_id"].(string)
			call := lookupCrewFunctionCall(callID)
			if call == nil || call.UserID != "owner" || call.TargetPath != ownedCodeFunctionPath {
				t.Fatalf("call = %+v", call)
			}
			env.svc.mu.Lock()
			var job *productScheduleJob
			for _, queue := range env.svc.queued {
				for _, item := range queue {
					if item.options.FunctionCallID == callID {
						copy := item.job
						job = &copy
					}
				}
			}
			env.svc.mu.Unlock()
			if job == nil || job.UserID != "owner" || job.GuestCallerID != "" || job.CodeCaller == nil || job.CodeCaller.Type != kind {
				t.Fatalf("queued call = %+v", job)
			}
			binding, err := env.svc.codeFunctionRunBinding(ctx, *job, "Code function")
			if err != nil || binding.WorkspacePath != ownedCodeFunctionPath {
				t.Fatalf("run binding = %+v, %v", binding, err)
			}
			// Code is a function target, never a folder attachment or public target.
			if _, _, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{ownedCodeFunctionPath}); err == nil {
				t.Fatal("function call attached Code files")
			}
			if _, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "#code:target"); err == nil {
				t.Fatal("public resolver discovered private Code")
			}
			if _, err := targetTools["return_function_result"].exec(ctx, map[string]interface{}{"call_id": callID, "result": map[string]interface{}{"ok": true}}); err != nil {
				t.Fatal(err)
			}
			got, err := sourceTools["get_function_call"].exec(ctx, map[string]interface{}{"call_id": callID})
			if err != nil || !strings.Contains(got, `"completed"`) || !strings.Contains(got, `"ok": true`) {
				t.Fatalf("typed result = %s, %v", got, err)
			}
		})
	}
}

func TestCodeFunctionsDoNotInheritSharedSourceOwnership(t *testing.T) {
	env := ownedCodeFunctionEnv(t)
	// The other person can edit the workflow, but cannot use its owner's Code.
	manifest, _, err := ReadWorkflowManifest(context.Background(), "Workflow/reports")
	if err != nil {
		t.Fatal(err)
	}
	manifest.Access.Editors = []string{"other"}
	raw, _ := json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath("Workflow/reports")] = string(raw)
	env.mock.mu.Unlock()
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "other", "shared-crew-reader", QueryRequest{SelectedFolder: linkAlphaPath}, crewTriggerLinkCaller(linkAlphaPath), nil); err != nil {
		t.Fatal(err)
	}
	for name, tools := range map[string]map[string]recordedTool{"shared crew reader": reg.tools, "shared workflow editor": privateCodeWorkflowTools(t, env, "other"), "foreign crew as Code owner": env.functionTools(t, linkGammaPath, "foreign-crew", nil)} {
		t.Run(name, func(t *testing.T) {
			if _, err := tools["list_functions"].exec(context.Background(), map[string]interface{}{"target": "#code:target"}); err == nil {
				t.Fatal("inherited owner Code discovery")
			}
			if _, err := tools["call_function"].exec(context.Background(), map[string]interface{}{"target": "#code:target", "function": "check_build", "args": map[string]interface{}{"build": "123"}, "notify": false}); err == nil {
				t.Fatal("inherited owner Code execution")
			}
		})
	}
	ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	workflowCaller, _ := workflowTriggerLinkCaller("Workflow/shared")(ownerCtx)
	if _, err := resolveFunctionTarget(ownerCtx, &UserClaims{UserID: "owner"}, workflowCaller, "#code:target"); err == nil {
		t.Fatal("Code owner called it from another owner's workflow")
	}
	caller, _ := crewTriggerLinkCaller(linkAlphaPath)(ownerCtx)
	for _, forged := range []triggerLinkCaller{
		{Stamp: caller.Stamp, Path: linkGammaPath},
		{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}, Path: linkAlphaPath},
		{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "forged", ProfileID: "work"}, Path: linkAlphaPath},
		{Stamp: caller.Stamp, Path: linkAlphaPath + "/../../beta"},
	} {
		if authorizeOwnedCodeCaller(ownerCtx, "owner", "owner", forged) == nil {
			t.Fatalf("forged caller accepted: %+v", forged)
		}
	}
}

func TestCodeFunctionOwnershipRecheckedAtPollAndQueuedStart(t *testing.T) {
	env := ownedCodeFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	tools := privateCodeWorkflowTools(t, env, "owner")
	caller, _ := workflowTriggerLinkCaller("Workflow/reports")(ctx)
	target, err := resolveFunctionTarget(ctx, &UserClaims{UserID: "owner"}, caller, "#code:target")
	if err != nil {
		t.Fatal(err)
	}
	triggerID, _, err := env.api.connectTriggerTarget(ctx, "owner", caller, target)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := env.api.dispatchTargetTrigger(ctx, "owner", caller, target, triggerID, "ownership-recheck", crewFunctionEvent, map[string]interface{}{"task": "Check"})
	if err != nil {
		t.Fatal(err)
	}
	env.svc.mu.Lock()
	var job productScheduleJob
	for _, queue := range env.svc.queued {
		for _, item := range queue {
			if item.options.RunID == delivery.RunID {
				job = item.job
			}
		}
	}
	env.svc.mu.Unlock()
	if _, err := env.svc.codeFunctionRunBinding(ctx, job, "Check"); err != nil {
		t.Fatal(err)
	}
	call := &crewFunctionCall{ID: "fn-code-recheck", UserID: "owner", CallerKind: triggerCallerWorkflow, CallerID: caller.Stamp.ID, CallerPath: caller.Path, TargetKind: triggerCallerCrew, TargetID: "target", TargetProfileID: "code", TargetPath: ownedCodeFunctionPath, Status: "running"}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	if _, err := tools["get_function_call"].exec(ctx, map[string]interface{}{"call_id": call.ID}); err != nil {
		t.Fatal(err)
	}
	manifest, _, _ := ReadWorkflowManifest(ctx, caller.Path)
	manifest.Access = &WorkflowAccess{Owners: []string{"other"}, Editors: []string{"owner"}}
	raw, _ := json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath(caller.Path)] = string(raw)
	env.mock.mu.Unlock()
	if _, err := env.svc.codeFunctionRunBinding(ctx, job, "Check"); err == nil {
		t.Fatal("queued call used revoked workflow ownership")
	}
	if _, err := env.svc.getInternalProductTriggerRun(ctx, "owner", "code", "target", triggerID, delivery.RunID, caller.Stamp, "owner"); err == nil {
		t.Fatal("poll used revoked workflow ownership")
	}
	if _, err := env.api.dispatchTargetTrigger(ctx, "owner", caller, target, triggerID, "after-revocation", crewFunctionEvent, map[string]interface{}{"task": "No"}); err == nil {
		t.Fatal("dispatch used revoked workflow ownership")
	}
	for _, name := range []string{"get_function_call", "reply_function_call"} {
		if _, err := tools[name].exec(ctx, map[string]interface{}{"call_id": call.ID}); err == nil {
			t.Fatalf("%s used revoked source ownership", name)
		}
	}
}
