package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestWorkflowBrowserRuntimeRefreshesPersistentTool(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	mode := "none"
	readErr := error(nil)
	exec := workflowBrowserExecutors("browser-refresh-test", "test-workflow", func(context.Context, string) (*WorkflowManifest, bool, error) {
		return &WorkflowManifest{Capabilities: WorkflowCapabilities{BrowserMode: mode}}, true, readErr
	})["agent_browser"]
	for _, configured := range []string{"none", "auto", "headless", "cdp", "none", ""} {
		mode = configured
		result, err := exec(context.Background(), map[string]interface{}{"command": "status"})
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			EffectiveMode string `json:"effective_mode"`
		}
		if err := json.Unmarshal([]byte(result), &status); err != nil {
			t.Fatal(err)
		}
		want := "headless"

		if status.EffectiveMode != want {
			t.Fatalf("mode %q: got %q, want %q", configured, status.EffectiveMode, want)
		}
	}

	readErr = errors.New("manifest unavailable")
	if _, err := exec(context.Background(), map[string]interface{}{"command": "status"}); !errors.Is(err, readErr) {
		t.Fatalf("manifest failure must fail closed: %v", err)
	}
}

// Exercise the real HTTP executor and per-step registry with a fresh request
// context, as used by scripted/CLI steps. A connected relay socket stands in
// for Chrome; status/tab-list need no page commands or model invocation.
func TestWorkflowStepBrowserHTTPBridgeKeepsAccountExtension(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "true")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	m, err := browserrelay.NewPersistent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := browserrelay.Default
	browserrelay.Default = m
	t.Cleanup(func() { m.Close(); browserrelay.Default = previous })
	parent := "extension-http-parent-" + time.Now().Format("150405.000000000")
	workspace := "Workflow/extension-http"
	common.BindSessionBrowserIsolationForWorkflow(parent, workspace)
	common.SetSessionFolderGuard(parent, []string{workspace}, []string{workspace})
	t.Cleanup(func() {
		common.ClearSessionShellConfig(parent)
		mcpclient.GetSessionRegistry().CloseHTTPSession(parent)
	})
	scope := common.SandboxBrowserSession(parent)
	token, err := m.PairForProfile("alice", scope, workspace, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	relay := httptest.NewServer(http.HandlerFunc(m.ServeExtension))
	defer relay.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(relay.URL, "http", "ws", 1), http.Header{"Origin": {"chrome-extension://fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteJSON(map[string]string{"type": "pair", "token": token, "scope": scope}); err != nil {
		t.Fatal(err)
	}
	var paired map[string]interface{}
	if err := conn.ReadJSON(&paired); err != nil || paired["type"] != "paired" {
		t.Fatalf("pair failed: %v", err)
	}

	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner(parent, "alice")
	requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Provider: "local"})
	executors := api.bindWorkflowBrowserExecutors(requestCtx, parent, QueryRequest{}, false,
		workflowBrowserExecutors(parent, workspace, func(context.Context, string) (*WorkflowManifest, bool, error) {
			return &WorkflowManifest{Capabilities: WorkflowCapabilities{BrowserMode: "cdp", CDPPorts: []int{9222}}}, true, nil
		}))
	logger := loggerv2.NewNoop()
	handlers := executor.NewExecutorHandlers("", logger)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Same trusted session injection as /s/{session}/tools/custom/{tool}.
		session := r.Header.Get("X-Session-ID")
		r = r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, session))
		handlers.HandlePerToolCustomRequest(w, r, "agent_browser")
	}))
	defer bridge.Close()
	call := func(session, command string) executor.CustomExecuteResponse {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"command": command})
		r, _ := http.NewRequest(http.MethodPost, bridge.URL, bytes.NewReader(body))
		r.Header.Set("X-Session-ID", session)
		response, err := bridge.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result executor.CustomExecuteResponse
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, child := range []string{parent + "-execution", parent + "-message-sequence"} {
		mcpclient.GetSessionRegistry().RegisterHTTPSession(parent, child)
		common.SetSessionBrowserNamespace(child, common.GetSessionShellConfig(parent).BrowserSessionNamespace)
		output := workspace + "/runs/iteration-0/default/execution/" + child
		common.SetSessionWorkingDir(child, output)
		common.SetSessionFolderGuard(child, []string{workspace}, []string{output})
		t.Cleanup(func() { common.ClearSessionShellConfig(child); codeexec.CleanupSession(child) })
		codeexec.InitRegistryForSession(child, executors, logger)
		result := call(child, "status")
		var status struct {
			EffectiveMode string   `json:"effective_mode"`
			Connected     bool     `json:"connected"`
			WritePaths    []string `json:"screenshot_write_paths"`
		}
		if !result.Success || json.Unmarshal([]byte(result.Result), &status) != nil || status.EffectiveMode != "extension" || !status.Connected || !slices.Equal(status.WritePaths, []string{output}) {
			t.Fatalf("step lost extension or borrowed parent write access: %+v %+v", result, status)
		}
		if result := call(child, "tab"); !result.Success || !strings.Contains(result.Result, `"tabs":[]`) {
			t.Fatalf("zero-tab connection did not use extension: %+v", result)
		}
	}
	child := parent + "-execution"
	// Both steps used the parent's controller, rather than claiming ownership
	// independently. Also prove conflicting identities and unregistered sessions
	// cannot borrow this account's extension.
	if _, release, err := m.Lookup("alice", scope).AcquireFor(context.Background(), parent); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	conflict := context.WithValue(context.Background(), common.UserIDKey, "bob")
	conflict = context.WithValue(conflict, common.ChatSessionIDKey, child)
	if _, err := executors["agent_browser"](conflict, map[string]interface{}{"command": "status"}); err == nil {
		t.Fatal("conflicting owner admitted")
	}
	unknown := parent + "-unregistered"
	codeexec.InitRegistryForSession(unknown, executors, logger)
	t.Cleanup(func() { codeexec.CleanupSession(unknown) })
	if result := call(unknown, "status"); result.Success {
		t.Fatal("unregistered child admitted")
	}

	// Socket loss retains selection: no silent local-CDP fallback.
	conn.Close()
	deadline := time.Now().Add(time.Second)
	for m.Status("alice", scope).Connected {
		if time.Now().After(deadline) {
			t.Fatal("relay did not notice socket loss")
		}
		time.Sleep(time.Millisecond)
	}
	result := call(child, "status")
	if !result.Success || !strings.Contains(result.Result, `"effective_mode":"extension"`) || !strings.Contains(result.Result, `"connected":false`) {
		t.Fatalf("disconnection fell through to CDP: %+v", result)
	}
	if result := call(child, "snapshot"); result.Success || !strings.Contains(result.Error, "CHROME_EXTENSION_DISCONNECTED") {
		t.Fatalf("disconnected action did not stop: %+v", result)
	}
}
