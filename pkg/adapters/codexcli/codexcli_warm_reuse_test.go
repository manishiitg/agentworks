package codexcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/instructions"
)

type warmThreadWriter struct {
	t     *testing.T
	rpc   *codexRPC
	loads *[]map[string]any
}

func (*warmThreadWriter) Close() error { return nil }
func (w *warmThreadWriter) Write(raw []byte) (int, error) {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params map[string]any  `json:"params"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return 0, err
	}
	var result any = map[string]any{}
	if request.Method == "thread/start" || request.Method == "thread/resume" {
		request.Params["method"] = request.Method
		*w.loads = append(*w.loads, request.Params)
		result = map[string]any{"thread": map[string]any{"id": "thread-existing"}, "model": "test"}
	}
	response, _ := json.Marshal(result)
	w.rpc.mu.Lock()
	ch := w.rpc.pending[string(request.ID)]
	w.rpc.mu.Unlock()
	ch <- codexRPCMessage{Result: response}
	return len(raw), nil
}

// PLAT-746-941: changing bridge credentials/instructions used to kill the
// app-server. Exercise actual setup RPCs; only the child process is a fixture.
func TestCodexWarmConfigRefreshResumesThreadWithoutRelaunch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", t.TempDir())
	prior := startCodexAppServer
	t.Cleanup(func() { startCodexAppServer = prior; closeCodexAppSession("warm-regression") })
	launches := 0
	var loads []map[string]any
	startCodexAppServer = func(_ *CodexCLIAdapter, ctx context.Context, _ *llmtypes.CallOptions, _ string) (*codexRPC, error) {
		launches++
		r := &codexRPC{pending: map[string]chan codexRPCMessage{}, done: make(chan struct{}), exited: make(chan struct{}), stop: func() {}}
		close(r.exited)
		r.stdin = &warmThreadWriter{t: t, rpc: r, loads: &loads}
		// The real launch initializes exactly once; verify the reuse path never
		// crosses this process boundary when only thread settings change.
		if err := r.call(ctx, "initialize", map[string]any{}, nil); err != nil {
			return nil, err
		}
		return r, nil
	}
	adapter := NewCodexCLIAdapter("account-one", "test", &MockLogger{})
	opts := &llmtypes.CallOptions{}
	for _, option := range []llmtypes.CallOption{WithInteractiveSessionID("warm-regression"), WithPersistentInteractiveSession(true), WithProjectDirID(t.TempDir()), llmtypes.WithCodingProviderLaunchOnly(), WithMCPServers(`{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"https://example.com/one"}}}`)} {
		option(opts)
	}
	run := func(system string) {
		t.Helper()
		if _, err := adapter.generateContentAppServer(context.Background(), []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, system)}, opts); err != nil {
			t.Fatal(err)
		}
	}
	run("trusted first")
	WithMCPServers(`{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"https://example.com/two"}}}`)(opts)
	run("trusted second")
	run("trusted second")
	if launches != 1 || len(loads) != 2 || loads[0]["method"] != "thread/start" || loads[1]["method"] != "thread/resume" || loads[1]["developerInstructions"] != "trusted second" {
		t.Fatalf("launches=%d loads=%d: thread refresh/reuse failed", launches, len(loads))
	}
	config := loads[1]["config"].(map[string]any)
	servers := config["mcp_servers"].(map[string]any)
	if servers["api-bridge"].(map[string]any)["env"].(map[string]any)["MCP_API_URL"] != "https://example.com/two" {
		t.Fatal("thread resumed with stale MCP config")
	}
	// #3190: changed instructions alone keep the thread; they go with the
	// next turn, and a later reload carries the current ones.
	run("trusted third")
	if launches != 1 || len(loads) != 2 {
		t.Fatalf("an instructions change reloaded the thread: loads=%d", len(loads))
	}
	session := codexAppSessionForOwner("warm-regression")
	pending, _ := session.instructions.Next("trusted third")
	entry := codexInstructionsContext(pending)["agentworks_instructions"].(map[string]any)
	if pending.Kind == instructions.None || entry["kind"] != "application" || !strings.HasSuffix(entry["value"].(string), "trusted third") {
		t.Fatalf("pending instructions = %+v, context = %+v", pending, entry)
	}
	// Bridge credentials belong to thread configuration, not the child env.
	WithMCPServers(`{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"https://example.com/two","MCP_API_TOKEN":"renewed-bridge-token"}}}`)(opts)
	run("trusted second")
	if launches != 1 || len(loads) != 3 {
		t.Fatal("bridge token renewal relaunched the process")
	}
	renewed := loads[2]["config"].(map[string]any)["mcp_servers"].(map[string]any)["api-bridge"].(map[string]any)["env"].(map[string]any)
	if renewed["MCP_API_TOKEN"] != "renewed-bridge-token" {
		t.Fatal("renewed bridge token was not passed to thread/resume")
	}
	if loads[2]["developerInstructions"] != "trusted second" || !session.instructions.Holds("trusted second") {
		t.Fatal("a reload must carry the current instructions and need nothing more")
	}
	adapter.apiKey = "account-two"
	run("trusted second")
	if launches != 2 {
		t.Fatal("account change reused the child process")
	}
}
