package picli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildRealMCPBridgeBinary compiles the actual mcpbridge binary from the
// mcpagent sibling checkout (the same binary a real workflow launches --
// /Users/.../go/bin/mcpbridge in production, per coding_agents_bridge.go)
// instead of standing in a hand-written Node MCP-protocol stub. Building it
// fresh, rather than trusting whatever happens to be on PATH, keeps this test
// self-contained and honest about which source it's actually exercising.
func buildRealMCPBridgeBinary(t *testing.T) string {
	t.Helper()
	sourceDir := os.Getenv("MCPAGENT_SOURCE_DIR")
	if sourceDir == "" {
		t.Fatal("set MCPAGENT_SOURCE_DIR to an owned SDK worktree")
	}
	mcpagentDir, err := filepath.Abs(sourceDir)
	if err != nil {
		t.Fatalf("resolve mcpagent sibling checkout path: %v", err)
	}
	if _, statErr := exec.LookPath("go"); statErr != nil {
		t.Skipf("go toolchain not available to build the real mcpbridge binary: %v", statErr)
	}
	entrypoint := filepath.Join(mcpagentDir, "cmd", "mcpbridge", "main.go")
	if _, statErr := os.Stat(entrypoint); statErr != nil {
		t.Skipf("mcpagent sibling checkout not found at %s (expected for the real-bridge P0 test): %v", mcpagentDir, statErr)
	}
	binPath := filepath.Join(t.TempDir(), "mcpbridge-under-test")
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", binPath, "./cmd/mcpbridge")
	cmd.Dir = mcpagentDir
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("go build ./cmd/mcpbridge (from %s): %v\n%s", mcpagentDir, buildErr, out)
	}
	return binPath
}

// fakeBridgeAPIServer reproduces mcpbridge's real HTTP contract
// (POST {url}/tools/custom/{name} or /tools/mcp/{server}/{name}, bearer
// auth, {"success":bool,"result":string,"error":string} response envelope --
// confirmed by reading cmd/mcpbridge/main.go directly) so the real mcpbridge
// binary under test has something real to forward to, without depending on
// (or risking interference with) the live desktop app's own server.
type fakeBridgeAPIServer struct {
	*httptest.Server
	token string

	mu    sync.Mutex
	calls []string
}

func newFakeBridgeAPIServer(t *testing.T, token string, results map[string]string) *fakeBridgeAPIServer {
	t.Helper()
	f := &fakeBridgeAPIServer{token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("/tools/custom/", func(w http.ResponseWriter, r *http.Request) {
		f.handleToolCall(w, r, strings.TrimPrefix(r.URL.Path, "/tools/custom/"), results)
	})
	mux.HandleFunc("/tools/mcp/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/tools/mcp/"), "/", 2)
		name := ""
		if len(parts) == 2 {
			name = parts[1]
		}
		f.handleToolCall(w, r, name, results)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeBridgeAPIServer) handleToolCall(w http.ResponseWriter, r *http.Request, toolName string, results map[string]string) {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"error":"unauthorized"}`))
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, toolName)
	f.mu.Unlock()

	result, ok := results[toolName]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"error":"no such tool on fake bridge API server"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}

func (f *fakeBridgeAPIServer) sawCall(toolName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == toolName {
			return true
		}
	}
	return false
}

// realMCPBridgeToolDefsJSON builds the exact MCP_TOOLS shape mcpbridge's
// ToolDef expects (name/description/input_schema/server/type -- confirmed by
// reading cmd/mcpbridge/main.go's ToolDef struct directly).
func realMCPBridgeToolDefsJSON(names ...string) string {
	type toolDef struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema any    `json:"input_schema"`
		Server      string `json:"server"`
		Type        string `json:"type"`
	}
	defs := make([]toolDef, 0, len(names))
	for _, name := range names {
		defs = append(defs, toolDef{
			Name:        name,
			Description: fmt.Sprintf("test tool %s", name),
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Type:        "custom",
		})
	}
	body, _ := json.Marshal(defs)
	return string(body)
}
