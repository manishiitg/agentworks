package mcpagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestSearchToolsHTTPSpecAndSessionIsolation(t *testing.T) {
	logger := loggerv2.NewDefault()
	codeexec.InitRegistry(nil, nil, nil, logger)
	a := codeExecutionPromptAgent()
	a.sessionID, a.apiBaseURL = "http-discovery-a-"+t.Name(), "http://bridge.test"
	b := codeExecutionPromptAgent()
	b.sessionID = "http-discovery-b-" + t.Name()
	for _, name := range []string{"execute_shell_command", "agent_browser", "read_visible", "hidden_mutation"} {
		addDirectToolFixture(t, a, directToolFixture(name, "workspace"))
	}
	addDirectToolFixture(t, b, directToolFixture("foreign_private_tool", "private"))
	a.setToolAccess([]string{"search_tools", "execute_shell_command", "agent_browser", "read_visible"})
	for _, agent := range []*Agent{a, b} {
		codeexec.InitRegistryVirtualToolsForSession(agent.virtualToolScopeID(), map[string]func(context.Context, map[string]interface{}) (string, error){"search_tools": agent.handleSearchTools}, logger)
		t.Cleanup(func() { codeexec.CleanupSession(agent.sessionID) })
	}

	// The exact batch from the live incident must now return all three schemas,
	// with discovery on virtual and real actions on custom routes.
	spec, err := a.handleGetAPISpec(context.Background(), map[string]interface{}{
		"tool_name": []interface{}{"execute_shell_command", "agent_browser", "search_tools"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"base: http://bridge.test/s/" + a.sessionID,
		"POST /tools/virtual/search_tools", "POST /tools/custom/execute_shell_command",
		"POST /tools/custom/agent_browser", "query:", "group:", "server_name:", "offset:", "limit:",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("missing HTTP contract %q: %s", want, spec)
		}
	}
	if strings.Contains(spec, "POST /tools/custom/search_tools") {
		t.Fatal("discovery was routed to the wrong executor")
	}

	handlers := executor.NewExecutorHandlers("", logger)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /s/{session}/tools/virtual/search_tools", func(w http.ResponseWriter, r *http.Request) {
		// Same trusted header injection as AgentWorks' session-scoped router.
		r.Header.Set("X-Session-ID", r.PathValue("session"))
		handlers.HandlePerToolVirtualRequest(w, r, "search_tools")
	})
	bridge := executor.AuthMiddleware("test-discovery-token")(mux)
	call := func(session, body, auth string) (int, executor.VirtualExecuteResponse) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "http://bridge.test/s/"+session+"/tools/virtual/search_tools", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		out := httptest.NewRecorder()
		bridge.ServeHTTP(out, req)
		var response executor.VirtualExecuteResponse
		if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
			t.Fatalf("invalid HTTP response: %s", out.Body.String())
		}
		return out.Code, response
	}
	for _, auth := range []string{"", "Bearer wrong-token"} {
		status, _ := call(a.sessionID, `{}`, auth)
		if status != http.StatusUnauthorized {
			t.Fatal("HTTP discovery bypassed bearer authentication")
		}
	}
	status, result := call(a.sessionID, `{"session_id":"`+b.sessionID+`"}`, "Bearer test-discovery-token")
	if status != http.StatusOK || !result.Success || !strings.Contains(result.Result, "read_visible") || strings.Contains(result.Result, "hidden_mutation") || strings.Contains(result.Result, "foreign_private_tool") {
		t.Fatalf("session discovery leaked tools or accepted the forged body session: %+v", result)
	}
	_, foreign := call(b.sessionID, `{}`, "Bearer test-discovery-token")
	if !foreign.Success || !strings.Contains(foreign.Result, "foreign_private_tool") || strings.Contains(foreign.Result, "read_visible") {
		t.Fatalf("second session did not retain its own registry: %+v", foreign)
	}
	_, unknown := call("unregistered-session", `{}`, "Bearer test-discovery-token")
	if unknown.Success {
		t.Fatal("unregistered session fell back to another session's discovery")
	}

	// Discovery stays live after metadata was cached. Revoking a tool removes
	// it from HTTP results; revoking discovery also prevents a cached spec hit.
	a.setToolAccess([]string{"search_tools", "agent_browser"})
	_, refreshed := call(a.sessionID, `{}`, "Bearer test-discovery-token")
	if !refreshed.Success || strings.Contains(refreshed.Result, "read_visible") || !strings.Contains(refreshed.Result, "agent_browser") {
		t.Fatalf("HTTP discovery ignored a live permission change: %+v", refreshed)
	}
	if _, err := a.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": "search_tools"}); err != nil {
		t.Fatal(err)
	}
	a.setToolAccess([]string{"agent_browser"})
	if _, err := a.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": "search_tools"}); err == nil || !strings.Contains(err.Error(), "not_allowed=[search_tools]") {
		t.Fatalf("cached discovery schema bypassed revoked permission: %v", err)
	}
}
