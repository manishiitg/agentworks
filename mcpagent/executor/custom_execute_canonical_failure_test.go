package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// TestHandleCustomExecuteAppliesCanonicalFailure pins the fix for the one path
// that never applied it. agent/conversation.go, agent/parallel_tool_execution.go
// and agent/llm_generation.go all call CanonicalFailureForTool wherever they
// call SuspiciousForTool; HandleCustomExecute — the endpoint every workflow
// step's custom/bridge tool call goes through, execute_shell_command chief
// among them — only ever called the latter. Success was unconditionally true
// regardless of payload content unless the Go call itself errored, so a shell
// command's own real nonzero exit_code was reported identically to a clean
// one. Measured live: 868 SuspiciousForTool hits from this one layer in 8
// hours, none able to change what the caller saw.
//
// The payload below is captured verbatim from a live occurrence
// (2026-08-10T15:39:42+05:30, tool=execute_shell_command, signal=exit_code=2)
// rather than constructed, so this proves the fix against the real shape, not
// an idealized one.
func TestHandleCustomExecuteAppliesCanonicalFailure(t *testing.T) {
	logger := loggerv2.NewNoop()
	toolName := "canonical_failure_probe"
	sessionID := "canonical-failure-session"

	const capturedShellFailure = `{"stdout":"","stderr":"sh: -c: line 8: syntax error near unexpected token ')'\nsh: -c: line 8: \u0027  | split(\"\\n\") | to_entries[] | select(.key < 60) | \"\\(.key+1): \\(.value)\"\u0027","exit_code":2,"execution_time_ms":14}`

	codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) {
			return capturedShellFailure, nil
		},
	}, logger)
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	handler := NewExecutorHandlers("", logger)
	body, err := json.Marshal(CustomExecuteRequest{
		Tool:      toolName,
		SessionID: sessionID,
		Args:      map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(string(body)))
	rr := httptest.NewRecorder()

	handler.HandleCustomExecute(rr, req)

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatalf("Success = true for a captured exit_code:2 payload, want false. Error=%q Result=%q",
			resp.Error, resp.Result)
	}
	if !strings.Contains(resp.Error, "exit_code=2") {
		t.Fatalf("Error = %q, want it to name the exit_code=2 signal", resp.Error)
	}
	// The raw payload is preserved, not replaced. A caller that inspects the
	// shell's own stdout/stderr for diagnosis must still be able to.
	if resp.Result != capturedShellFailure {
		t.Fatalf("Result was altered:\n got:  %s\n want: %s", resp.Result, capturedShellFailure)
	}
}

// TestHandleCustomExecuteStillSuppressesContentBearingTools proves the fix
// does not widen the blast radius. CanonicalFailureForTool carries the same
// problemReportingTools suppression used everywhere else it is called, so a
// tool whose normal payload legitimately contains failure-shaped text (a
// Pulse backlog listing "status":"failed" findings) must still report success.
func TestHandleCustomExecuteStillSuppressesContentBearingTools(t *testing.T) {
	logger := loggerv2.NewNoop()
	sessionID := "canonical-failure-suppressed-session"

	for _, tc := range []struct{ name, payload string }{
		{"query_workflow_db", `{"rows":[{"status":"failed","note":"a finding describing a bug, not a tool failure"}]}`},
		// A failed check was recorded successfully. Keep its typed failed result
		// and completed log receipt inside an ordinary success envelope.
		{"python_automation", `{"ok":false,"error":"expected failed check","log_file":"/example/run/check.log","log_complete":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
				tc.name: func(context.Context, map[string]interface{}) (string, error) { return tc.payload, nil },
			}, logger)
			t.Cleanup(func() { codeexec.CleanupSession(sessionID) })
			handler := NewExecutorHandlers("", logger)
			req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+tc.name, strings.NewReader(`{}`))
			req.Header.Set("X-Session-ID", sessionID)
			rr := httptest.NewRecorder()
			handler.HandlePerToolCustomRequest(rr, req, tc.name)
			var reply CustomExecuteResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if !reply.Success || reply.Result != tc.payload {
				t.Fatalf("domain receipt lost: %+v", reply)
			}
			codeexec.CleanupSession(sessionID)
			codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){}, logger)
			rr = httptest.NewRecorder()
			req = httptest.NewRequest(http.MethodPost, "/tools/custom/"+tc.name, strings.NewReader(`{}`))
			req.Header.Set("X-Session-ID", sessionID)
			handler.HandlePerToolCustomRequest(rr, req, tc.name)
			if err := json.Unmarshal(rr.Body.Bytes(), &reply); err != nil || reply.Success || !strings.Contains(reply.Error, "not registered") {
				t.Fatalf("missing handler reported success: %+v %v", reply, err)
			}
		})
	}
}
