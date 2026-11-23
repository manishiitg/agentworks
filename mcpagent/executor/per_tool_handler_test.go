package executor

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestPerToolSessionIDHeaderOverridesBodyAndStripsArg(t *testing.T) {
	args := map[string]interface{}{
		"session_id": "body-session",
		"query":      "keep",
	}
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/test", nil)
	req.Header.Set("X-Session-ID", "header-session")

	got := perToolSessionIDFromRequest(req, args)
	if got != "header-session" {
		t.Fatalf("session id = %q, want header-session", got)
	}
	if _, exists := args["session_id"]; exists {
		t.Fatal("session_id arg was not stripped")
	}
	if args["query"] != "keep" {
		t.Fatalf("non-session arg changed: %v", args)
	}
}

func TestPerToolSessionIDBodyUsedOnlyWithoutHeader(t *testing.T) {
	args := map[string]interface{}{"session_id": "body-session"}
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/test", nil)

	got := perToolSessionIDFromRequest(req, args)
	if got != "body-session" {
		t.Fatalf("session id = %q, want body-session", got)
	}
}

func TestPerToolCustomUsesTrustedHeaderSession(t *testing.T) {
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	t.Cleanup(func() {
		codeexec.CleanupSession("trusted-session")
		codeexec.CleanupSession("body-session")
	})

	toolName := "session_probe_custom"
	codeexec.InitRegistryForSession("trusted-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return SessionIDFromContext(ctx), nil
		},
	}, logger)
	codeexec.InitRegistryForSession("body-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) { return "body", nil },
	}, logger)

	handler := NewExecutorHandlers("", logger)
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{"session_id":"body-session"}`))
	req.Header.Set("X-Session-ID", "trusted-session")
	rr := httptest.NewRecorder()

	handler.HandlePerToolCustomRequest(rr, req, toolName)

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response failed: %s", resp.Error)
	}
	if resp.Result != "trusted-session" {
		t.Fatalf("trusted context session = %q, want trusted-session", resp.Result)
	}
}

func TestPerToolCustomReturnsStructuredExecutionID(t *testing.T) {
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	const sessionID = "execution-id-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	toolName := "execute_step"
	codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) {
			return "Step started in background.\nexecution_id: \"exec-step-grow-123\"", nil
		},
	}, logger)

	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{"step_id":"step-grow"}`))
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	NewExecutorHandlers("", logger).HandlePerToolCustomRequest(rr, req, toolName)

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response failed: %s", resp.Error)
	}
	if got := resp.Data["execution_id"]; got != "exec-step-grow-123" {
		t.Fatalf("data.execution_id = %q, want exec-step-grow-123", got)
	}
}

func TestPerToolVirtualUsesTrustedHeaderSession(t *testing.T) {
	logger := loggerv2.NewNoop()
	codeexec.InitRegistryWithVirtualTools(nil, nil, nil, nil, logger)
	t.Cleanup(func() {
		codeexec.CleanupSession("trusted-session")
		codeexec.CleanupSession("body-session")
	})

	toolName := "session_probe_virtual"
	codeexec.InitRegistryVirtualToolsForSession("trusted-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) { return "trusted", nil },
	}, logger)
	codeexec.InitRegistryVirtualToolsForSession("body-session", map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(context.Context, map[string]interface{}) (string, error) { return "body", nil },
	}, logger)

	handler := NewExecutorHandlers("", logger)
	req := httptest.NewRequest(http.MethodPost, "/tools/virtual/"+toolName, strings.NewReader(`{"session_id":"body-session"}`))
	req.Header.Set("X-Session-ID", "trusted-session")
	rr := httptest.NewRecorder()

	handler.HandlePerToolVirtualRequest(rr, req, toolName)

	var resp VirtualExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response failed: %s", resp.Error)
	}
	if resp.Result != "trusted" {
		t.Fatalf("result = %q, want trusted", resp.Result)
	}
}

func TestPerToolDelegationCancellationReachesCustomTool(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "")
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	const sessionID = "cancel-custom-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	started := make(chan struct{})
	toolName := "call_sub_agent"
	codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewExecutorHandlers("", logger).HandlePerToolCustomRequest(rr, req, toolName)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("custom tool did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("custom tool remained detached after request cancellation")
	}

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success || !strings.Contains(resp.Error, "layer=custom_tool_handler") || !strings.Contains(resp.Error, "canceled") {
		t.Fatalf("response = %+v, want attributed cancellation", resp)
	}
}

func TestPerToolDelegationCancellationReachesVirtualTool(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "")
	logger := loggerv2.NewNoop()
	codeexec.InitRegistryWithVirtualTools(nil, nil, nil, nil, logger)
	const sessionID = "cancel-virtual-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	started := make(chan struct{})
	toolName := "call_generic_agent"
	codeexec.InitRegistryVirtualToolsForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/tools/virtual/"+toolName, strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewExecutorHandlers("", logger).HandlePerToolVirtualRequest(rr, req, toolName)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("virtual tool did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("virtual tool remained detached after request cancellation")
	}

	var resp VirtualExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success || !strings.Contains(resp.Error, "layer=virtual_tool_handler") || !strings.Contains(resp.Error, "canceled") {
		t.Fatalf("response = %+v, want attributed cancellation", resp)
	}
}

func TestPerToolCustomTimeoutIdentifiesLayerAndTool(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "20ms")
	logger := loggerv2.NewNoop()
	codeexec.InitRegistry(nil, nil, nil, logger)
	const sessionID = "timeout-custom-session"
	t.Cleanup(func() { codeexec.CleanupSession(sessionID) })

	toolName := "silent_tool"
	codeexec.InitRegistryForSession(sessionID, map[string]func(context.Context, map[string]interface{}) (string, error){
		toolName: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}, logger)

	req := httptest.NewRequest(http.MethodPost, "/tools/custom/"+toolName, strings.NewReader(`{}`))
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()
	NewExecutorHandlers("", logger).HandlePerToolCustomRequest(rr, req, toolName)

	var resp CustomExecuteResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{"timed out", "layer=custom_tool_handler", "tool=" + toolName, "session=" + sessionID, "timeout=20ms"} {
		if !strings.Contains(resp.Error, want) {
			t.Fatalf("error %q missing %q", resp.Error, want)
		}
	}
}

// Every bridge tool handler must report failures the same way, because two
// consumers key off that one format: `grep '[TOOL_ERROR]'` in the backend logs,
// and the frontend's harness-error detector, which requires the `layer=` token.
//
// The MCP handler previously logged a bare "Tool execution failed" and built its
// own error string without `layer=`, so every MCP server tool failure was
// invisible to both. This pins all three layers together so a fourth handler
// cannot quietly opt out.
func TestToolExecutionErrorEnvelopeCoversEveryHandlerLayer(t *testing.T) {
	// The frontend detector, copied verbatim from
	// frontend/src/utils/toolCallFormatting.ts (HARNESS_TOOL_ERROR).
	uiDetector := regexp.MustCompile(`tool execution (?:failed|canceled|timed out): layer=`)

	layers := []string{"mcp_tool_handler", "custom_tool_handler", "virtual_tool_handler"}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "failed", err: errors.New("boom"), want: "tool execution failed"},
		{name: "canceled", err: context.Canceled, want: "tool execution canceled"},
		{name: "timed out", err: context.DeadlineExceeded, want: "tool execution timed out"},
	}

	for _, layer := range layers {
		for _, tc := range cases {
			got := toolExecutionError(layer, "some_tool", "some-session", 20*time.Millisecond, tc.err)
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("layer %s / %s: got %q, want prefix %q", layer, tc.name, got, tc.want)
			}
			if !strings.Contains(got, "layer="+layer) {
				t.Errorf("layer %s / %s: got %q, missing layer attribution", layer, tc.name, got)
			}
			if !uiDetector.MatchString(got) {
				t.Errorf("layer %s / %s: got %q, which the frontend HARNESS_TOOL_ERROR detector does not match", layer, tc.name, got)
			}
		}
	}
}

// The format test above would still pass if a handler stopped calling
// toolExecutionError, because it exercises the function directly. This pins the
// wiring: every bridge execute handler must actually use the shared envelope.
// That is the property that failed before — HandleMCPExecute built its own
// error string and was the only handler no test covered.
func TestEveryExecuteHandlerUsesTheSharedErrorEnvelope(t *testing.T) {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "handlers.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		"HandleMCPExecute":     false,
		"HandleCustomExecute":  false,
		"HandleVirtualExecute": false,
	}

	for _, declaration := range parsed.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == "toolExecutionError" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}

	for name, found := range want {
		if !found {
			t.Errorf("%s does not call toolExecutionError; its failures will carry no layer= "+
				"attribution, so neither the [TOOL_ERROR] log grep nor the frontend "+
				"harness-error detector will see them", name)
		}
	}
}

// The API spec and the per-tool routes print sanitized tool names; a server with hyphenated tools
// (notion-fetch) must still be called by its own name.
func TestRealToolNameMapsSanitizedNameBackToTheServersName(t *testing.T) {
	tools := []mcp.Tool{{Name: "notion-fetch"}, {Name: "notion-search"}, {Name: "plain_tool"}}
	if got, ok := realToolName(tools, "notion_fetch"); !ok || got != "notion-fetch" {
		t.Fatalf("notion_fetch = %q, %v; want notion-fetch", got, ok)
	}
	if _, ok := realToolName(tools, "plain_tool"); ok {
		t.Fatal("a listed name must be called as is")
	}
	if _, ok := realToolName(append(tools, mcp.Tool{Name: "notion_fetch"}), "notion_fetch"); ok {
		t.Fatal("an exact listed name wins over a sanitized match")
	}
	if _, ok := realToolName(tools, "missing"); ok {
		t.Fatal("unknown name must stay unknown")
	}
}
