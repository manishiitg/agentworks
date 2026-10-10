package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestServerLogContextAlwaysIncludesUsernameAndWorkflow(t *testing.T) {
	fallback := serverLogContext{}.Prefix()
	if !strings.Contains(fallback, "username=-") || !strings.Contains(fallback, "workflow=-") {
		t.Fatalf("fallback prefix = %q, want stable username/workflow fields", fallback)
	}

	actual := newServerLogContext("Workflow/customer support", "", "workflow_phase", "user-1", "customer", "session-1").Prefix()
	for _, want := range []string{`username=customer`, `workflow="customer support"`, `user=customer`, `user_id=user-1`, `session=session-1`} {
		if !strings.Contains(actual, want) {
			t.Fatalf("prefix = %q, missing %q", actual, want)
		}
	}
}

func TestServerLogContextCarriesDiagnosticMetadata(t *testing.T) {
	ctx := newServerLogContext("Workflow/testing", "", "workflow", "u1", "customer", "s1").Context(context.Background())
	if got := ctx.Value(common.UsernameKey); got != "customer" {
		t.Fatalf("username context = %v, want customer", got)
	}
	if got := ctx.Value(common.WorkflowNameKey); got != "testing" {
		t.Fatalf("workflow context = %v, want testing", got)
	}
}

func TestServerLogContextWriterEnrichesSessionAndChildLines(t *testing.T) {
	logCtx := newServerLogContext("Workflow/testing", "", "workflow", "u1", "customer", "session-1")
	registerServerLogContext(logCtx, "log-context-test-session-1", "log-context-test-query-1")

	var output bytes.Buffer
	writer := newServerLogContextWriter(&output)
	input := "[BROWSER] command failed session=\"log-context-test-session-1:child:worker\"\n[LLM] query_id=log-context-test-query-1 failed\n"
	n, err := writer.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(input) {
		t.Fatalf("Write returned %d bytes, want %d", n, len(input))
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("output lines = %d, want 2: %q", len(lines), output.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, "username=customer") || !strings.Contains(line, "workflow=testing") {
			t.Fatalf("enriched line = %q", line)
		}
	}
}

func TestServerLogContextWriterUsesFallbackAndAvoidsDuplicates(t *testing.T) {
	if got := enrichServerLogLine("[STARTUP] ready"); got != "[STARTUP] ready username=- workflow=-" {
		t.Fatalf("fallback enrichment = %q", got)
	}
	legacy := enrichServerLogLine("[QUERY] user=customer session=log-context-test-unknown")
	if !strings.Contains(legacy, "username=customer") || !strings.Contains(legacy, "workflow=-") {
		t.Fatalf("legacy enrichment = %q", legacy)
	}
	alreadyScoped := "[username=customer workflow=testing] complete"
	if got := enrichServerLogLine(alreadyScoped); got != alreadyScoped {
		t.Fatalf("already-scoped line = %q, want unchanged", got)
	}
}

func TestHTTPRequestLogContextUsesAuthenticatedUserAndActiveWorkflow(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"session-1": {
			SessionID:     "session-1",
			UserID:        "u1",
			Username:      "customer",
			WorkspacePath: "Workflow/testing",
			AgentMode:     "workflow_phase",
		},
	}}
	req := httptest.NewRequest("GET", "/api/events?session_id=session-1", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "u1", Username: "customer"}))

	logCtx := api.httpRequestLogContext(req)
	if logCtx.Username != "customer" || logCtx.Workflow != "testing" || logCtx.Session != "session-1" {
		t.Fatalf("http log context = %+v", logCtx)
	}
}
