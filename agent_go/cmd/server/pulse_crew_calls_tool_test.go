package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Only this workflow's own Crew step records under runs/ can be named.
func TestCrewStepRecordPathMustBeThisWorkflowsRunRecord(t *testing.T) {
	for _, bad := range []string{
		"soul/soul.md",
		"runs/iteration-1/default/crew-run.json/../../../workflow.json",
		"runs/iteration-1/default/notes.json",
		"../other-workflow/runs/iteration-1/crew-run.json",
		"/runs/iteration-1/crew-run.json",
	} {
		if _, err := crewStepRecordRef(context.Background(), "Workflow/w", bad); err == nil {
			t.Errorf("record_path %q must be refused", bad)
		}
	}
}

// A function call counts only when this workflow made it to a Crew.
func TestFunctionCallRefOnlyForThisWorkflowsCrewCalls(t *testing.T) {
	now := time.Now()
	mine := &crewFunctionCall{ID: "fn-1", CallerKind: triggerCallerWorkflow, CallerID: "wf-sales", TargetKind: triggerCallerCrew,
		TargetID: "crew-7", TargetLabel: "Research Crew", TriggerID: "tr-1", RunIDs: []string{"run-a", "run-b"}, Function: "research", Status: "completed", UserID: "u1", UpdatedAt: now}
	ref, ok := functionCallRef(mine, "wf-sales")
	if !ok || ref.RunID != "run-b" || ref.CrewProjectID != "crew-7" || ref.userID != "u1" {
		t.Fatalf("this workflow's call must resolve to its latest run, got %+v %v", ref, ok)
	}
	if _, ok := functionCallRef(mine, "wf-other"); ok {
		t.Fatal("another workflow's call must not be readable")
	}
	fromCrew := &crewFunctionCall{ID: "fn-2", CallerKind: triggerCallerCrew, CallerID: "wf-sales", TargetKind: triggerCallerCrew}
	if _, ok := functionCallRef(fromCrew, "wf-sales"); ok {
		t.Fatal("a call made by a Crew is not this workflow's call")
	}
	toWorkflow := &crewFunctionCall{ID: "fn-3", CallerKind: triggerCallerWorkflow, CallerID: "wf-sales", TargetKind: triggerCallerWorkflow}
	if _, ok := functionCallRef(toWorkflow, "wf-sales"); ok {
		t.Fatal("a call to a workflow is not a Crew call")
	}
}

// read with a call_id refuses a call this workflow did not make.
func TestReadCrewCallRefusesAnotherWorkflowsCall(t *testing.T) {
	crewFunctionCalls.Lock()
	crewFunctionCalls.m["fn-other"] = &crewFunctionCall{ID: "fn-other", CallerKind: triggerCallerWorkflow, CallerID: "wf-other", TargetKind: triggerCallerCrew}
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, "fn-other")
		crewFunctionCalls.Unlock()
	})
	_, err := resolveWorkflowCrewCall(context.Background(), "Workflow/w", "wf-sales", "u1", map[string]interface{}{"call_id": "fn-other"})
	if err == nil || !strings.Contains(err.Error(), "not made by this workflow") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if _, err := resolveWorkflowCrewCall(context.Background(), "Workflow/w", "wf-sales", "u1", map[string]interface{}{}); err == nil {
		t.Fatal("read needs a call_id or record_path")
	}
}

// The conversation is shown without the system prompt, newest messages
// first-kept, with tool calls and results clipped.
func TestRenderCrewConversationKeepsTheCallAndClipsNoise(t *testing.T) {
	long := strings.Repeat("x", crewCallsToolChars+50)
	doc := map[string]interface{}{"conversation_history": []interface{}{
		map[string]interface{}{"Role": "system", "Parts": []interface{}{map[string]interface{}{"Text": "SECRET SYSTEM PROMPT"}}},
		map[string]interface{}{"Role": "human", "Parts": []interface{}{map[string]interface{}{"Text": "Research acme.com"}}},
		map[string]interface{}{"Role": "ai", "Parts": []interface{}{
			map[string]interface{}{"Text": "Looking it up."},
			map[string]interface{}{"FunctionCall": map[string]interface{}{"Name": "web_search", "Arguments": `{"q":"acme"}`}},
		}},
		map[string]interface{}{"Role": "tool", "Parts": []interface{}{map[string]interface{}{"Name": "web_search", "Content": long, "IsError": true}}},
		map[string]interface{}{"Role": "ai", "Parts": []interface{}{map[string]interface{}{"Text": "No results; the site blocks crawlers."}}},
	}}
	raw, _ := json.Marshal(doc)
	out := renderCrewConversation(raw, 3)
	encoded, _ := json.Marshal(out)
	text := string(encoded)
	if strings.Contains(text, "SECRET SYSTEM PROMPT") {
		t.Fatal("the system prompt must not be shown")
	}
	if out["messages_total"] != 4 || out["messages_shown"] != 3 {
		t.Fatalf("want the newest 3 of 4 messages, got %v", out)
	}
	if strings.Contains(text, "Research acme.com") {
		t.Fatal("older messages beyond the limit must be dropped")
	}
	for _, want := range []string{`web_search {\"q\":\"acme\"}`, `"tool_error":true`, "blocks crawlers", "…"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered conversation missing %q: %s", want, text)
		}
	}
}
