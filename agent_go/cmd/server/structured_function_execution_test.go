package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Drive real binding, trigger admission, isolated worker, saved terminal run,
// supervisor and post-restart call read; only the model turn is replaced.
func TestStructuredFunctionsIsolateParallelRunsAndKeepOutputsAfterRestart(t *testing.T) {
	env := newCrewFunctionEnv(t)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	env.svc.heldConversation = nil
	started := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	env.svc.automationTurnRunner = func(ctx context.Context, req map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		vars := common.GetSessionShellEnv(session)
		if vars["FUNCTION_CALL_ID"] == "" || !strings.HasPrefix(vars["FUNCTION_RESULT_FILE"], docs) {
			return internalSessionTurnResult{}, context.Canceled
		}
		started <- session
		select {
		case <-release:
		case <-ctx.Done():
			return internalSessionTurnResult{}, ctx.Err()
		}
		if err := os.MkdirAll(vars["FUNCTION_OUTPUT_DIR"], 0700); err != nil {
			return internalSessionTurnResult{}, err
		}
		if err := os.WriteFile(filepath.Join(vars["FUNCTION_OUTPUT_DIR"], "report.pdf"), []byte("%PDF-1.0\noutput"), 0600); err != nil {
			return internalSessionTurnResult{}, err
		}
		return internalSessionTurnResult{FinalResponse: "Finished report."}, nil
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	fn := crewFunction{Name: "report", Instructions: "Prepare a report."}
	calls := make([]*crewFunctionCall, 0, 3)
	sessions := map[string]bool{}
	for i := 0; i < 3; i++ {
		call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, map[string]interface{}{}, time.Minute, string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
		select {
		case sid := <-started:
			if sessions[sid] || sid == "sess-beta" {
				t.Fatalf("function reused session %s", sid)
			}
			sessions[sid] = true
		case <-time.After(3 * time.Second):
			t.Fatal("isolated trigger did not reach model boundary")
		}
	}
	retry, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, map[string]interface{}{}, time.Minute, "a")
	if err != nil || retry != calls[0] {
		t.Fatalf("accepted retry did not bypass capacity: %v %v", retry, err)
	}
	if call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, map[string]interface{}{}, time.Minute, "d"); call != nil || err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("full Crew accepted/queued another call: %v %v", call, err)
	}
	env.svc.mu.Lock()
	queued := len(env.svc.queued)
	env.svc.mu.Unlock()
	if queued != 0 {
		t.Fatalf("function calls entered a queue: %d", queued)
	}
	releaseOnce.Do(func() { close(release) })
	for _, call := range calls {
		select {
		case <-call.done:
		case <-time.After(3 * time.Second):
			t.Fatal("function did not settle")
		}
		if call.snapshot()["status"] != "completed" || call.snapshot()["answer"] != "Finished report." {
			t.Fatalf("terminal call = %v", call.snapshot())
		}
	}
	page, err := readCrewFunctionOutput(ctx, calls[0], "report.pdf", 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(page["content_base64"].(string))
	if err != nil || string(raw) != "%PDF-" || page["has_more"] != true {
		t.Fatalf("binary page = %v %v", page, err)
	}
	if _, err := readCrewFunctionOutput(ctx, calls[0], "../"+calls[1].ID+"/report.pdf", 0, 5); err == nil {
		t.Fatal("read a different call's output")
	}
	if err := os.Symlink(filepath.Join(docs, "outside"), filepath.Join(docs, filepath.FromSlash(calls[0].outputFolder()), "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := readCrewFunctionOutput(ctx, calls[0], "escape", 0, 5); err == nil {
		t.Fatal("read a symlink output")
	}
	crewFunctionCalls.Lock()
	delete(crewFunctionCalls.m, calls[0].ID)
	crewFunctionCalls.Unlock()
	if !strings.HasPrefix(calls[0].recordPath(), "_system/function_call_records/") {
		t.Fatal("call permissions are stored in model-writable project files")
	}
	saved := lookupCrewFunctionCall(calls[0].ID)
	if saved == nil || saved.snapshot()["answer"] != "Finished report." {
		t.Fatalf("restart lost call result: %+v", saved)
	}
	out := saved.snapshot()
	env.api.addCrewFunctionReadDetails(ctx, out, saved, -1, 1)
	if len(out["messages"].([]structuredFunctionMessage)) != 1 || out["has_more_messages"] != true {
		t.Fatalf("saved message page = %v", out)
	}
}

func TestStructuredFunctionFileCorrectionUsesSameExecutionAndRejectsInvalidResult(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env.svc.heldConversation = nil
	var mu sync.Mutex
	turns := map[string]int{}
	sessions := map[string]string{}
	env.svc.automationTurnRunner = func(ctx context.Context, req map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		vars := common.GetSessionShellEnv(session)
		id := vars["FUNCTION_CALL_ID"]
		mu.Lock()
		turns[id]++
		n := turns[id]
		if old := sessions[id]; old != "" && old != session {
			t.Error("correction changed session")
		}
		sessions[id] = session
		mu.Unlock()
		if n == 1 {
			return internalSessionTurnResult{FinalResponse: "Done, but result file omitted."}, nil
		}
		if err := os.MkdirAll(vars["FUNCTION_OUTPUT_DIR"], 0700); err != nil {
			return internalSessionTurnResult{}, err
		}
		call := lookupCrewFunctionCall(id)
		if call.Function == "valid" {
			if err := os.WriteFile(vars["FUNCTION_RESULT_FILE"], []byte(`{"passed":true}`), 0600); err != nil {
				return internalSessionTurnResult{}, err
			}
			return internalSessionTurnResult{FinalResponse: "Report completed."}, nil
		}
		if call.Function == "fallback" {
			return internalSessionTurnResult{FinalResponse: "Result:\n```json\n{\"passed\":true}\n```"}, nil
		}
		return internalSessionTurnResult{FinalResponse: "Still no valid output."}, nil
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Gamma")
	if err != nil {
		t.Fatal(err)
	}
	schema := map[string]interface{}{"type": "object", "required": []interface{}{"passed"}, "properties": map[string]interface{}{"passed": map[string]interface{}{"type": "boolean"}}}
	for _, name := range []string{"valid", "fallback", "invalid"} {
		call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, crewFunction{Name: name, Instructions: "Check the report.", ResultSchema: schema}, nil, time.Minute, name)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-call.done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s never settled", name)
		}
		out := call.snapshot()
		mu.Lock()
		n := turns[call.ID]
		mu.Unlock()
		if n != 2 || len(call.RunIDs) != 1 {
			t.Fatalf("%s used %d turns / %d runs", name, n, len(call.RunIDs))
		}
		if name == "invalid" {
			if out["status"] != "failed" || out["final_reply"] != "Still no valid output." {
				t.Fatalf("invalid result succeeded/lost partial answer: %v", out)
			}
		} else {
			if out["status"] != "completed" {
				t.Fatalf("valid result failed: %v", out)
			}
			encoded, _ := json.Marshal(out["result"])
			if string(encoded) != `{"passed":true}` {
				t.Fatalf("wrong structured result: %s", encoded)
			}
		}
	}
	declaredAsk := crewFunction{Name: "ask", Instructions: "Run the declared ask function."}
	if isBuiltinConversationalFunction(target, declaredAsk) {
		t.Fatal("owner-declared ask was treated as messaging")
	}
	if _, exists := env.beta["return_function_result"]; exists {
		t.Fatal("retired result-return tool is still exposed")
	}
	functions, err := callableFunctions(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range functions {
		if isBuiltinConversationalFunction(target, fn) {
			t.Fatal("builtin ask offered as a structured trigger")
		}
	}
}
