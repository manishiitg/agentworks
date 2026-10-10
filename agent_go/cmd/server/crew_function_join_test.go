package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCrewFunctionSubmissionIDSurvivesCompletionAndRestart(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env.svc.heldConversation = nil
	env.svc.automationTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: `{ "passed": true }`}, nil
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := env.alpha["define_function"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	functions, err := callableFunctions(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	fn, _ := findCrewFunction(functions, "run_login_flow")
	start := func(args map[string]interface{}) (*crewFunctionCall, error) {
		return env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, args, time.Minute, "retry-42")
	}
	first, err := start(map[string]interface{}{"build": "7"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.done:
	case <-time.After(3 * time.Second):
		t.Fatal("isolated submission never completed")
	}
	second, err := start(map[string]interface{}{"build": "7"})
	if err != nil || second.ID != first.ID {
		t.Fatalf("completed retry started another call: %v %+v", err, second)
	}
	crewFunctionCalls.Lock()
	delete(crewFunctionCalls.m, first.ID)
	crewFunctionCalls.Unlock()
	third, err := start(map[string]interface{}{"build": "7"})
	if err != nil || third.ID != first.ID {
		t.Fatalf("restart retry started another call: %v %+v", err, third)
	}
	if _, err := start(map[string]interface{}{"build": "8"}); err == nil {
		t.Fatal("conflicting reuse of submission_id was accepted")
	}
}

func TestInternalCallFunctionPassesSubmissionID(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env.svc.heldConversation = nil
	env.svc.automationTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: `{ "passed": true }`}, nil
	}
	if _, err := env.alpha["define_function"].exec(context.Background(), loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	args := map[string]interface{}{"target": "Beta", "function": "run_login_flow", "args": map[string]interface{}{"build": "7"}, "submission_id": "crew-retry-7", "notify": false}
	firstRaw, err := env.alpha["call_function"].exec(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	firstID, _ := decodeToolJSON(t, firstRaw)["call_id"].(string)
	if firstID == "" {
		t.Fatalf("call_function omitted call_id: %s", firstRaw)
	}
	call := lookupCrewFunctionCall(firstID)
	if call == nil {
		t.Fatal("accepted submission has no call record")
	}
	select {
	case <-call.done:
	case <-time.After(3 * time.Second):
		t.Fatal("isolated submission never completed")
	}
	if call.snapshot()["status"] != "completed" {
		t.Fatalf("function did not complete: %v", call.snapshot())
	}
	secondRaw, err := env.alpha["call_function"].exec(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if secondID, _ := decodeToolJSON(t, secondRaw)["call_id"].(string); secondID != firstID {
		t.Fatalf("completed retry started a new call: %s vs %s", secondID, firstID)
	}
	args["args"] = map[string]interface{}{"build": "8"}
	if _, err := env.alpha["call_function"].exec(context.Background(), args); err == nil {
		t.Fatal("conflicting reuse of submission_id was accepted")
	}
}

// PLAT-756: an owner can ask for a call to run in Run mode (MCP run_mode), to see how it behaves for anyone else. The
// call records it and the delivery to the Crew carries no trace of the flag, so a function cannot see or depend on it.
func TestCrewFunctionRunModeIsRecordedAndKeptOutOfTheDelivery(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env.svc.heldConversation = nil
	env.svc.automationTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: `{ "passed": true }`}, nil
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := env.alpha["define_function"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	functions, err := callableFunctions(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	fn, _ := findCrewFunction(functions, "run_login_flow")
	plain, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, map[string]interface{}{"build": "1"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := env.api.startCrewFunctionCall(withCrewRunMode(ctx), "owner", caller, target, fn, map[string]interface{}{"build": "2"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if plain.RunMode || !pinned.RunMode {
		t.Fatalf("run mode recorded wrongly: plain=%v pinned=%v", plain.RunMode, pinned.RunMode)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		env.mock.mu.Lock()
		delivered := false
		for path, content := range env.mock.files {
			if strings.Contains(path, "/triggers/deliveries/") && strings.Contains(content, pinned.ID) {
				delivered = true
				if strings.Contains(content, "run_mode") || strings.Contains(content, "run_as_owner") {
					env.mock.mu.Unlock()
					t.Fatalf("the delivery carries the run-mode flag: %s", content)
				}
			}
		}
		env.mock.mu.Unlock()
		if delivered {
			for _, call := range []*crewFunctionCall{plain, pinned} {
				select {
				case <-call.done:
				case <-time.After(3 * time.Second):
					t.Fatal("function did not complete")
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pinned call was never delivered")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
