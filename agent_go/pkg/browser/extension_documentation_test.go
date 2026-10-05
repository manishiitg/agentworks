package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestExtensionDocumentationWithoutConnectionOrTabs(t *testing.T) {
	stateRoot := t.TempDir()
	m, err := browserrelay.NewPersistent(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	previous := browserrelay.Default
	browserrelay.Default = m
	defer func() { m.Close(); browserrelay.Default = previous }()
	session, workspace := "extension-documentation-step", "Workflow/docs-test"
	common.BindSessionBrowserIsolationForWorkflow(session, workspace)
	common.SetSessionWorkingDir(session, workspace)
	common.SetSessionFolderGuard(session, []string{workspace}, []string{workspace + "/step-output"})
	defer common.ClearSessionShellConfig(session)
	if _, err := m.PairForProfile("alice", common.SandboxBrowserSession(session), workspace, "workflow"); err != nil {
		t.Fatal(err)
	}
	// Restore an explicitly selected extension after server restart, offline.
	selected, _ := json.Marshal(map[string]string{"alice\x00" + common.SandboxBrowserSession(session): workspace})
	if err := os.WriteFile(filepath.Join(stateRoot, "selected.json"), selected, 0600); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = browserrelay.NewPersistent(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	browserrelay.Default = m
	calls := 0
	shell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if r.Header.Get("X-User-ID") != "alice" || request.WorkingDirectory != workspace || request.FolderGuard == nil || len(request.FolderGuard.WritePaths) != 1 || request.FolderGuard.WritePaths[0] != workspace+"/step-output" {
			t.Errorf("documentation lost trusted account or step grants: %+v", request)
		}
		if !strings.HasPrefix(request.Command, "agent-browser skills ") || !strings.HasSuffix(request.Command, " --json") || strings.Contains(request.Command, "--cdp") || strings.Contains(request.Command, "--session") {
			t.Errorf("documentation must not attach to a browser: %s", request.Command)
		}
		json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: `{"success":true,"data":[{"name":"core","content":"# Core\nRead a snapshot first."}]}`}})
	}))
	defer shell.Close()
	executor := NewExecutor(NewClient(shell.URL), WithCdpPort(9222))
	ctx := context.WithValue(context.Background(), common.UserIDKey, "alice")
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, session)
	for _, args := range [][]string{{"list"}, {"get", "core"}, {"get", "core", "--full"}} {
		output, err := executor.HandleAgentBrowser(ctx, map[string]interface{}{"command": "skills", "args": args})
		if err != nil || !strings.Contains(output, "in extension mode, omit --cdp") || !strings.Contains(output, "Read a snapshot first") {
			t.Fatalf("skills %v: %s, %v", args, output, err)
		}
	}
	for _, args := range [][]string{{"get", "core", "--cdp", "http://localhost:9222"}, {"get", "../core"}, {"get", "--config"}, {"list", "--session", "other"}, {"open", "https://example.com"}} {
		if _, err := executor.HandleAgentBrowser(ctx, map[string]interface{}{"command": "skills", "args": args}); err == nil {
			t.Fatalf("accepted non-documentation args: %v", args)
		}
	}
	if calls != 3 || m.Status("alice", common.SandboxBrowserSession(session)).Connected {
		t.Fatal("documentation used a browser connection or dispatched invalid args")
	}
	if _, err := executor.HandleAgentBrowser(ctx, map[string]interface{}{"command": "snapshot"}); err == nil || !strings.Contains(err.Error(), "CHROME_EXTENSION_DISCONNECTED") {
		t.Fatalf("offline page action did not fail closed: %v", err)
	}
}
