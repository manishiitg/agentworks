package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestWorkflowRunAskPublishedFunctionBoundary(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	const root = "Workflow/shared" // owned by other; owner is a Run member here.
	manifest, _, err := ReadWorkflowManifest(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Schedules = []WorkflowSchedule{reviewPRTrigger()}
	save := func() {
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		env.mock.mu.Lock()
		env.mock.files[manifestPath(root)] = string(raw)
		env.mock.mu.Unlock()
		if err := saveWorkflowEntryActions(ctx, root, manifest); err != nil {
			t.Fatal(err)
		}
	}
	save()
	reg := &recordingRegistrar{}
	if err := env.api.registerWorkflowPublishedFunctionTools(ctx, reg, "owner", "member-chat", root); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 4 {
		t.Fatalf("expected only four published-function tools: %v", reg.tools)
	}
	if got, err := reg.tools["project_functions_list"].exec(ctx, nil); err != nil || !strings.Contains(got, "review_pr") {
		t.Fatalf("list: %s %v", got, err)
	}
	if _, err := reg.tools["project_functions_call"].exec(ctx, map[string]any{"function": "review_pr"}); err == nil || !strings.Contains(err.Error(), "missing required input") {
		t.Fatalf("Run must reach input validation: %v", err)
	}
	if _, err := reg.tools["project_functions_call"].exec(ctx, map[string]any{"function": "review_pr", "input": "wrong"}); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("malformed args: %v", err)
	}

	// A retry from Ask reuses an already completed typed call, not a chat turn
	// or a second execution. The real submission lookup and response paths run.
	args := map[string]any{"GITHUB_OWNER": "example", "GITHUB_REPO": "app", "PR_NUMBER": float64(42)}
	argsJSON, _ := json.Marshal(args)
	call := &crewFunctionCall{ID: "fn-workflow-ask-completed", Function: "review_pr", UserID: "owner", CallerKind: triggerCallerUser, CallerID: "owner", TargetKind: triggerCallerWorkflow, TargetID: manifest.ID, TargetPath: root, Status: "completed", Result: map[string]any{"status": "completed", "steps": []any{}}, SubmissionID: "review-42", ArgumentsKey: crewFunctionArgumentsFingerprint(string(argsJSON)), argsKey: string(argsJSON), CreatedAt: time.Now(), closed: true, done: make(chan struct{})}
	close(call.done)
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	t.Cleanup(func() { crewFunctionCalls.Lock(); delete(crewFunctionCalls.m, call.ID); crewFunctionCalls.Unlock() })
	input := map[string]any{"function": "review_pr", "input": args, "submission_id": "review-42"}
	got, err := reg.tools["project_functions_call"].exec(ctx, input)
	if err != nil || !strings.Contains(got, call.ID) || !strings.Contains(got, "completed") {
		t.Fatalf("completed retry: %s %v", got, err)
	}
	got, err = reg.tools["project_functions_get_call"].exec(ctx, map[string]any{"call_id": call.ID})
	if err != nil || !strings.Contains(got, call.ID) {
		t.Fatalf("own result: %s %v", got, err)
	}
	args["PR_NUMBER"] = float64(43)
	if _, err := reg.tools["project_functions_call"].exec(ctx, input); err == nil {
		t.Fatal("conflicting submission ID accepted")
	}
	call.mu.Lock()
	call.UserID = "other"
	call.mu.Unlock()
	if _, err := reg.tools["project_functions_reply"].exec(ctx, map[string]any{"call_id": call.ID, "request_id": "question", "response": "yes"}); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("another person's input: %v", err)
	}

	// Shared people may run published functions regardless of person entries
	// in AllowedCallers (the decisions log); non-person callers stay restricted.
	call.mu.Lock()
	call.UserID = "owner"
	call.mu.Unlock()
	args["PR_NUMBER"] = float64(42)
	manifest.Schedules[0].Function.AllowedCallers = []triggerCaller{{Type: triggerCallerUser, ID: "other"}}
	save()
	if got, err := reg.tools["project_functions_call"].exec(ctx, input); err != nil || !strings.Contains(got, call.ID) || !strings.Contains(got, "completed") {
		t.Fatalf("shared person retry: %s %v", got, err)
	}
	manifest.Schedules[0].Enabled = false
	save()
	if _, err := reg.tools["project_functions_call"].exec(ctx, input); err == nil || !strings.Contains(err.Error(), "no function") {
		t.Fatalf("disabled function: %v", err)
	}
	manifest.Access.Readers = nil
	save()
	if _, err := reg.tools["project_functions_list"].exec(ctx, nil); err == nil || !strings.Contains(err.Error(), "no longer use") {
		t.Fatalf("revoked share: %v", err)
	}

	// The same external entry also accepts Relay Run members. Even with a
	// callable function in the draft, no publication means no execution.
	manifest.Access.Readers = []string{"owner"}
	manifest.Kind = "relay"
	manifest.Schedules[0].Enabled = true
	manifest.Schedules[0].Function.AllowedCallers = nil
	save()
	if _, err := reg.tools["project_functions_list"].exec(ctx, nil); err == nil {
		t.Fatal("native workflow tools must not acquire a Relay target")
	}
	req := httptest.NewRequest("POST", "/api/external/call", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	env.api.externalWorkflowFunctionCall(rec, req, "project_functions_call", map[string]any{"function": "review_pr", "input": args, "submission_id": "unpublished-relay"}, DiscoveredWorkflow{WorkspacePath: root, Manifest: manifest}, WorkflowAccessRead)
	var reply map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || reply["status"] != "failed" || !strings.Contains(rec.Body.String(), "no published version") {
		t.Fatalf("draft-only Relay must be refused: %d %s", rec.Code, rec.Body.String())
	}
	if id, _ := reply["call_id"].(string); id != "" {
		relayCall := lookupCrewFunctionCall(id)
		if relayCall == nil || relayCall.RunID != "" || relayCall.UserID != "owner" {
			t.Fatalf("draft-only Relay started a run or lost caller attribution: %v", relayCall)
		}
		t.Cleanup(func() { crewFunctionCalls.Lock(); delete(crewFunctionCalls.m, id); crewFunctionCalls.Unlock() })
	}
}

func TestWorkflowRunAskPreservesTokenAuthority(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "workflow-ask-test-signing-secret")
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	token, _, err := store.Issue(context.Background(), accesstokens.Token{Name: "Run", UserID: "owner", Scopes: []string{"runs:execute", "workflows:read"}, AllWorkflows: true, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "owner", AccessToken: &token}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	reg := &recordingRegistrar{}
	if err := env.api.registerWorkflowPublishedFunctionTools(ctx, reg, "owner", "pat-ask", "Workflow/shared"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["project_functions_list"].exec(context.Background(), nil); err != nil {
		t.Fatalf("live token refused: %v", err)
	}
	if err := store.Revoke(context.Background(), token.ID, "owner", now); err != nil {
		t.Fatal(err)
	}
	// A later full-account tool context cannot lift the captured token's limits.
	fullAccount := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := reg.tools["project_functions_list"].exec(fullAccount, nil); err == nil {
		t.Fatal("revoked token upgraded to a full account")
	}
	token.Scopes = []string{"workflows:read"}
	reg = &recordingRegistrar{}
	if err := env.api.registerWorkflowPublishedFunctionTools(ctx, reg, "owner", "pat-ask-no-run", "Workflow/shared"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["project_functions_call"].exec(fullAccount, map[string]any{"function": "review_pr"}); err == nil || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("token without run consent: %v", err)
	}
}
