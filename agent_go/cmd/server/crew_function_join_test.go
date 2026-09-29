package server

import (
	"context"
	"testing"
	"time"
)

// A caller that repeats a call it believes failed (a shell curl that timed
// out while call_function was still waiting) joins the running call instead
// of starting another run; different arguments still start their own run.
func TestCrewFunctionIdenticalInFlightCallIsJoined(t *testing.T) {
	env := newCrewFunctionEnv(t)
	first := startLoginFlowCall(t, env, time.Minute)
	second := startLoginFlowCall(t, env, time.Minute)
	if second.ID != first.ID {
		t.Fatalf("identical in-flight call started a new run: %s vs %s", second.ID, first.ID)
	}
	if snap := second.snapshot(); snap["joined"] != 1 || snap["note"] == nil {
		t.Fatalf("joined call must say it is the running one: %v", snap)
	}

	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
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
	other, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, map[string]interface{}{"build": "8"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID {
		t.Fatal("a call with different arguments must start its own run")
	}
}

func TestCrewFunctionSubmissionIDSurvivesCompletionAndRestart(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
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
	first.finish("completed", map[string]interface{}{"ok": true}, "")
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
