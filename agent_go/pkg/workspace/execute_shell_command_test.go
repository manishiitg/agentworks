package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestRedactShellCommandForLog(t *testing.T) {
	command := `python3 <<'PY'
KEY = "sk-api-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
headers = {"Authorization": "Bearer abcdefghijklmnopqrstuvwxyz1234567890"}
api_key = "AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"
PY`

	redacted := redactShellCommandForLog(command)
	for _, forbidden := range []string{"sk-api-abcdefghijklmnopqrstuvwxyz", "Bearer abcdefghijklmnopqrstuvwxyz", "AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ"} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("expected secret fragment %q to be redacted from %q", forbidden, redacted)
		}
	}
	if strings.Count(redacted, "[REDACTED]") < 3 {
		t.Fatalf("expected multiple redactions, got %q", redacted)
	}
}

func TestDetectRawChromeCDPAccessInHeredoc(t *testing.T) {
	command := `python3 <<'PY'
import websocket
tab_id = "abc"
ws = websocket.create_connection(
    f"ws://localhost:9222/devtools/page/{tab_id}",
    header=["Host: localhost"],
)
PY`

	if got := detectRawChromeCDPAccess(command); got == "" {
		t.Fatal("expected raw CDP access to be detected inside heredoc")
	}
}

func TestDetectRawChromeCDPAccessIgnoresUnrelatedWebSocket(t *testing.T) {
	command := `python3 <<'PY'
import websocket
ws = websocket.create_connection("wss://example.com/events")
PY`

	if got := detectRawChromeCDPAccess(command); got != "" {
		t.Fatalf("expected unrelated websocket to pass, got %q", got)
	}
}

func TestContainsAgentBrowserInvocationAllowsReadOnlySkillsDocs(t *testing.T) {
	allowed := []string{
		"agent-browser skills list",
		"agent-browser skills get core",
		"agent-browser skills get core --full",
		"agent-browser skills get electron --full",
	}
	for _, command := range allowed {
		if containsAgentBrowserInvocation(command) {
			t.Fatalf("expected %q to be allowed", command)
		}
	}
}

func TestContainsAgentBrowserInvocationStillBlocksBrowserActions(t *testing.T) {
	blocked := []string{
		"agent-browser open https://example.com",
		"agent-browser snapshot -i",
		"agent-browser skills get core && agent-browser open https://example.com",
		"agent-browser skills get core | cat",
	}
	for _, command := range blocked {
		if !containsAgentBrowserInvocation(command) {
			t.Fatalf("expected %q to be blocked", command)
		}
	}
}

func TestDetectRawChromeCDPAccessAllowsPlanTextMentioningCDPVariable(t *testing.T) {
	command := `cat > /tmp/plan.py <<'PY'
description = """
VAR: TWITTER_CDP_URL
node learnings/step-post-twitter-reply/scripts/post_reply_v2.js $VAR_TWITTER_CDP_URL $tweet_url $payload_path.
"""
PY`

	if got := detectRawChromeCDPAccess(command); got != "" {
		t.Fatalf("expected CDP variable mentions in plan text to pass, got %q", got)
	}
}

func TestDetectRawChromeCDPAccessAllowsDocumentationTextWithEndpoints(t *testing.T) {
	command := `cat > /tmp/browser-docs.md <<'EOF'
Do not use ws://localhost:9222/devtools/page/<id>.
Do not call websocket.create_connection("ws://localhost:9222/devtools/page/<id>").
Do not read http://localhost:9222/json/list directly.
EOF`

	if got := detectRawChromeCDPAccess(command); got != "" {
		t.Fatalf("expected documentation text to pass, got %q", got)
	}
}

func TestDetectRawChromeCDPAccessBlocksCurlJSONEndpoint(t *testing.T) {
	command := `curl -sS http://localhost:9222/json/list`

	if got := detectRawChromeCDPAccess(command); got != "Chrome /json target endpoint" {
		t.Fatalf("expected curl to Chrome JSON endpoint to be blocked, got %q", got)
	}
}

func TestDetectRawChromeCDPAccessBlocksCDPMethodWithVariableInExecutableHeredoc(t *testing.T) {
	command := `python3 <<'PY'
import os
method = "Target.createTarget"
url = os.environ["TWITTER_CDP_URL"]
print(method, url)
PY`

	if got := detectRawChromeCDPAccess(command); got != "Chrome CDP method call" {
		t.Fatalf("expected CDP method with CDP URL variable to be blocked, got %q", got)
	}
}

func TestExecuteShellCommand_BlocksRawChromeCDPAccess(t *testing.T) {
	client := NewClient("http://127.0.0.1:1")
	result, err := client.ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{
		Command: `python3 -c 'import urllib.request; print(urllib.request.urlopen("http://localhost:9222/json/list").read())'`,
	})
	if err != nil {
		t.Fatalf("ExecuteShellCommand returned Go error: %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Raw Chrome CDP access") || !strings.Contains(result.Stderr, "agent_browser") {
		t.Fatalf("expected actionable raw CDP error, got %q", result.Stderr)
	}
}

func TestExecuteShellCommandPreservesTimedOutProcessOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		_, _ = w.Write([]byte(`{"error":"Command exceeded timeout of 3600 seconds","data":{"stdout":"CASE login: starting\n","stderr":"TIMEOUT: Command killed after 3600 seconds\n","exit_code":-1,"execution_time_ms":3600000}}`))
	}))
	defer server.Close()

	result, err := NewClient(server.URL).ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{Command: "python3 main.py"})
	if err != nil {
		t.Fatalf("structured timeout must be inspectable as a result: %v", err)
	}
	if !result.TimedOut || result.ExitCode != -1 {
		t.Fatalf("timeout classification lost: %#v", result)
	}
	if !strings.Contains(result.Stdout, "CASE login: starting") || !strings.Contains(result.Stderr, "Command killed") {
		t.Fatalf("partial process evidence lost: %#v", result)
	}
}

func TestExecuteShellCommandBlocksRelativeGitBundleDestination(t *testing.T) {
	client := NewClient("http://127.0.0.1:1")
	_, err := client.ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{
		Command:          `git bundle create backup/wf-backup.bundle --all`,
		WorkingDirectory: "/tmp/workflow",
	})
	if err == nil {
		t.Fatal("expected an in-repository relative bundle destination to be blocked")
	}
	if !strings.Contains(err.Error(), "outside the source repository") {
		t.Fatalf("expected actionable containment error, got %v", err)
	}
}

func TestExecuteShellCommandBlocksAbsoluteBundleInsideCanonicalRepo(t *testing.T) {
	repoRoot := t.TempDir()
	destination := filepath.Join(repoRoot, "backup", "workflow.bundle")
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params ExecuteShellCommandParams
		_ = json.NewDecoder(r.Body).Decode(&params)
		stdout := "ok"
		if strings.Contains(params.Command, "rev-parse --show-toplevel") {
			stdout = repoRoot
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": stdout, "exit_code": 0},
		})
	}))
	defer server.Close()

	_, err := NewClient(server.URL).ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{
		Command:          fmt.Sprintf("git bundle create %q --all", destination),
		WorkingDirectory: repoRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "inside source repository") {
		t.Fatalf("absolute in-repository bundle should be blocked, got %v", err)
	}
}

func TestBlockAbsoluteHostPaths_AllowsAbsoluteWorkspacePathOutsideGuardForSandboxEnforcement(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")

	guard := &FolderGuardConfig{
		Enabled:    true,
		ReadPaths:  []string{"Workflow/testing/runs/iteration-0/test-group/execution"},
		WritePaths: []string{"Workflow/testing/runs/iteration-0/test-group/execution/math-solver"},
	}

	err := blockAbsoluteHostPaths(
		`cat '/Users/mipl/ai-work/coding-agent-loop/workspace-docs/Workflow/testing/workflow.json'`,
		guard,
	)
	if err != nil {
		t.Fatalf("expected absolute workspace-docs path to pass through to sandbox enforcement, got: %v", err)
	}
}

func TestBlockAbsoluteHostPaths_DeniesAbsoluteHostPathOutsideWorkspaceDocs(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")

	guard := &FolderGuardConfig{
		Enabled:    true,
		ReadPaths:  []string{"Workflow/testing/runs/iteration-0/test-group/execution"},
		WritePaths: []string{"Workflow/testing/runs/iteration-0/test-group/execution/math-solver"},
	}

	err := blockAbsoluteHostPaths(
		`cat '/Users/mipl/.ssh/id_rsa'`,
		guard,
	)
	if err == nil {
		t.Fatal("expected absolute host path outside workspace-docs to be rejected")
	}
	if !strings.Contains(err.Error(), "absolute host path") {
		t.Fatalf("expected absolute host path error, got: %v", err)
	}
}

func TestBlockAbsoluteHostPaths_AllowsExplicitReadOnlyHostDownloadsGrant(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")

	guard := &FolderGuardConfig{
		Enabled:           true,
		ReadPaths:         []string{"/Users/mipl/Downloads"},
		WritePaths:        []string{"Workflow/testing/runs/iteration-0/test-group/execution/Downloads"},
		BlockedWritePaths: []string{"/Users/mipl/Downloads"},
	}

	err := blockAbsoluteHostPaths(
		`cp '/Users/mipl/Downloads/statement.pdf' Workflow/testing/runs/iteration-0/test-group/execution/Downloads/statement.pdf`,
		guard,
	)
	if err != nil {
		t.Fatalf("expected explicit read-only host Downloads grant to pass, got: %v", err)
	}
}

func TestBlockAbsoluteHostPaths_DeniesHostReadPathWithoutWriteBlock(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")

	guard := &FolderGuardConfig{
		Enabled:    true,
		ReadPaths:  []string{"/Users/mipl/Downloads"},
		WritePaths: []string{"Workflow/testing/runs/iteration-0/test-group/execution/Downloads"},
	}

	err := blockAbsoluteHostPaths(
		`cp '/Users/mipl/Downloads/statement.pdf' Workflow/testing/runs/iteration-0/test-group/execution/Downloads/statement.pdf`,
		guard,
	)
	if err == nil {
		t.Fatal("expected host read path without blocked-write protection to be rejected")
	}
}

func TestBlockAbsoluteHostPaths_AllowsAbsoluteWorkspacePathInsideGuardAndIgnoresHeredocData(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")

	guard := &FolderGuardConfig{
		Enabled:    true,
		ReadPaths:  []string{"Workflow/testing/runs/iteration-0/test-group/execution"},
		WritePaths: []string{"Workflow/testing/runs/iteration-0/test-group/execution/math-solver"},
	}

	command := `cat '/Users/mipl/ai-work/coding-agent-loop/workspace-docs/Workflow/testing/runs/iteration-0/test-group/execution/prepare-test-fixtures/test_fixtures.json'
cat > '/Users/mipl/ai-work/coding-agent-loop/workspace-docs/Workflow/testing/runs/iteration-0/test-group/execution/math-solver/math_probe.json' <<'EOF'
{
  "attempted_path": "/Users/mipl/ai-work/coding-agent-loop/workspace-docs/Workflow/testing/workflow.json"
}
EOF`

	if err := blockAbsoluteHostPaths(command, guard); err != nil {
		t.Fatalf("expected allowed absolute execution paths to pass, got: %v", err)
	}
}

// TestExecuteShellCommand_InjectsSessionEnv proves that per-session shell env
// (e.g. DB_PATH set by the workflow orchestrator) reaches the bridge request,
// and that an explicit per-call extra_env overrides the session value.
func TestExecuteShellCommand_InjectsSessionEnv(t *testing.T) {
	t.Setenv("WORKSPACE_API_TOKEN", "server-only-token")
	sessionID := "test-session-env"
	common.SetSessionShellEnv(sessionID, map[string]string{
		"DB_PATH":            "/abs/workflow/db/db.sqlite",
		"STEP_OUTPUT_DIR":    "/abs/workspace-docs/Workflow/test-workflow/runs/iteration-0/default/execution/step-score",
		"WORKFLOW_DB_ACCESS": "read",
	})
	common.SetSessionWorkingDir(sessionID, "Workflow/test-workflow/runs/iteration-0/default/execution")
	defer ClearSessionShellConfig(sessionID)

	var got ExecuteShellCommandParams
	var gotWorkspaceToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWorkspaceToken = r.Header.Get("X-Workspace-Token")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": "ok", "exit_code": 0},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)

	// Per-call extra_env sets DB_PATH explicitly — it must win over the session value.
	if _, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{
		Command:  `sqlite3 "$DB_PATH" "select 1"`,
		ExtraEnv: map[string]string{"DB_PATH": "/per/call/override.sqlite"},
	}); err != nil {
		t.Fatalf("ExecuteShellCommand error: %v", err)
	}

	if got.ExtraEnv["DB_PATH"] != "/per/call/override.sqlite" {
		t.Fatalf("per-call extra_env should win: DB_PATH=%q", got.ExtraEnv["DB_PATH"])
	}
	if gotWorkspaceToken != "server-only-token" {
		t.Fatalf("workspace token header = %q", gotWorkspaceToken)
	}
	if got.ExtraEnv["STEP_OUTPUT_DIR"] != "/abs/workspace-docs/Workflow/test-workflow/runs/iteration-0/default/execution/step-score" {
		t.Fatalf("session STEP_OUTPUT_DIR not injected: %q", got.ExtraEnv["STEP_OUTPUT_DIR"])
	}
	if !got.DBReadSnapshot {
		t.Fatal("read-only scripted shell request did not ask the workspace service for a DB snapshot")
	}
	if got.WorkingDirectory != "Workflow/test-workflow/runs/iteration-0/default/execution" {
		t.Fatalf("session working directory not injected: %q", got.WorkingDirectory)
	}
	if got.ExtraEnv["RUNLOOP_OWNER"] != "workflow" {
		t.Fatalf("RUNLOOP_OWNER not inferred: %q", got.ExtraEnv["RUNLOOP_OWNER"])
	}
	if got.ExtraEnv["RUNLOOP_WORKFLOW_ID"] != "test-workflow" {
		t.Fatalf("RUNLOOP_WORKFLOW_ID not inferred: %q", got.ExtraEnv["RUNLOOP_WORKFLOW_ID"])
	}
	if got.ExtraEnv["RUNLOOP_RUN_ID"] != "iteration-0/default" {
		t.Fatalf("RUNLOOP_RUN_ID not inferred: %q", got.ExtraEnv["RUNLOOP_RUN_ID"])
	}
	if got.ExtraEnv["RUNLOOP_STEP_ID"] != "step-score" {
		t.Fatalf("RUNLOOP_STEP_ID not inferred: %q", got.ExtraEnv["RUNLOOP_STEP_ID"])
	}
	if got.ExtraEnv["RUNLOOP_SESSION_ID"] != sessionID {
		t.Fatalf("RUNLOOP_SESSION_ID not set: %q", got.ExtraEnv["RUNLOOP_SESSION_ID"])
	}

	// Without a per-call override, the session DB_PATH must be injected.
	got = ExecuteShellCommandParams{}
	if _, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{Command: `sqlite3 "$DB_PATH" "select 1"`}); err != nil {
		t.Fatalf("ExecuteShellCommand error: %v", err)
	}
	if got.ExtraEnv["DB_PATH"] != "/abs/workflow/db/db.sqlite" {
		t.Fatalf("session DB_PATH not injected: %q", got.ExtraEnv["DB_PATH"])
	}
}

func TestExecuteShellCommand_ConcurrentRequestsUseIsolatedEnv(t *testing.T) {
	const requestCount = 64

	received := make(chan ExecuteShellCommandParams, requestCount)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params ExecuteShellCommandParams
		if err := json.NewDecoder(r.Body).Decode(&params); err == nil {
			received <- params
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": "ok", "exit_code": 0},
		})
	}))
	defer server.Close()

	sourceEnv := map[string]string{
		"MCP_API_URL":   "http://127.0.0.1:45678/s/shared-session",
		"MCP_API_TOKEN": "test-token",
	}
	client := NewClient(server.URL, WithExtraEnv(sourceEnv))
	// WithExtraEnv must own a copy rather than retain the caller's mutable map.
	sourceEnv["MCP_API_URL"] = "http://invalid.example"

	var wg sync.WaitGroup
	errors := make(chan error, requestCount)
	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callEnv := map[string]string{"REQUEST_LOCAL": "present"}
			_, err := client.ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{
				Command:  "printf ok",
				ExtraEnv: callEnv,
			})
			if err != nil {
				errors <- err
			}
			if len(callEnv) != 1 || callEnv["REQUEST_LOCAL"] != "present" {
				errors <- fmt.Errorf("caller env was mutated: %#v", callEnv)
			}
		}()
	}
	wg.Wait()
	close(errors)
	close(received)
	for err := range errors {
		t.Error(err)
	}

	receivedCount := 0
	for params := range received {
		receivedCount++
		if got := params.ExtraEnv["MCP_API_URL"]; got != "http://127.0.0.1:45678/s/shared-session" {
			t.Fatalf("client env was not isolated from its source map: %q", got)
		}
		if params.ExtraEnv["MCP_MCP"] == "" || params.ExtraEnv["MCP_CUSTOM"] == "" || params.ExtraEnv["MCP_AUTH"] == "" {
			t.Fatalf("derived MCP environment is incomplete: %#v", params.ExtraEnv)
		}
		if params.ExtraEnv["REQUEST_LOCAL"] != "present" {
			t.Fatalf("per-call environment missing: %#v", params.ExtraEnv)
		}
	}
	if receivedCount != requestCount {
		t.Fatalf("received %d requests, want %d", receivedCount, requestCount)
	}

	for _, derivedKey := range []string{"MCP_MCP", "MCP_CUSTOM", "MCP_VIRTUAL", "MCP_AUTH", "RUNLOOP_SESSION_ID"} {
		if _, exists := client.ExtraEnv[derivedKey]; exists {
			t.Fatalf("shared client env was mutated with %s: %#v", derivedKey, client.ExtraEnv)
		}
	}
}

func TestExecuteShellCommand_PassesCDPHostDownloadsReadWriteGuard(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")
	t.Setenv("PI_HOST_DOWNLOADS_PATH", "/Users/mipl/Downloads")

	sessionID := "test-cdp-downloads"
	common.SetSessionFolderGuard(
		sessionID,
		[]string{"Workflow/testing/runs/iteration-0/test-group/execution"},
		[]string{"Workflow/testing/runs/iteration-0/test-group/execution/Downloads"},
	)
	common.GrantSessionCDPHostDownloadsReadWrite(sessionID, "cdp")
	defer ClearSessionShellConfig(sessionID)

	var got ExecuteShellCommandParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": "ok", "exit_code": 0},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	if _, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{
		Command: `cp '/Users/mipl/Downloads/statement.pdf' Workflow/testing/runs/iteration-0/test-group/execution/Downloads/statement.pdf`,
	}); err != nil {
		t.Fatalf("ExecuteShellCommand error: %v", err)
	}

	if got.FolderGuard == nil {
		t.Fatal("expected folder guard in execute request")
	}
	if !containsString(got.FolderGuard.ReadPaths, "/Users/mipl/Downloads") {
		t.Fatalf("expected host Downloads in read paths, got %v", got.FolderGuard.ReadPaths)
	}
	if !containsString(got.FolderGuard.WritePaths, "/Users/mipl/Downloads") {
		t.Fatalf("expected host Downloads in write paths, got %v", got.FolderGuard.WritePaths)
	}
	if containsString(got.FolderGuard.BlockedWritePaths, "/Users/mipl/Downloads") {
		t.Fatalf("host Downloads must not remain write-blocked, got %v", got.FolderGuard.BlockedWritePaths)
	}
}

func TestExecuteShellCommand_UsesClientSessionIDForCDPHostDownloadsGuard(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")
	t.Setenv("PI_HOST_DOWNLOADS_PATH", "/Users/mipl/Downloads")

	sessionID := "test-cdp-downloads-client-session"
	common.SetSessionFolderGuard(
		sessionID,
		[]string{"Workflow/testing"},
		[]string{"Workflow/testing/Downloads"},
	)
	common.GrantSessionCDPHostDownloadsReadWrite(sessionID, "cdp")
	defer ClearSessionShellConfig(sessionID)

	var got ExecuteShellCommandParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": "ok", "exit_code": 0},
		})
	}))
	defer server.Close()

	// This reproduces the code-execution bridge path: the session-aware client
	// has MCP_SESSION_ID, but an HTTP intermediary supplied no context value.
	client := NewClient(server.URL, WithExtraEnv(map[string]string{"MCP_SESSION_ID": sessionID}))
	if _, err := client.ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{
		Command: `cat '/Users/mipl/Downloads/statement.pdf'`,
	}); err != nil {
		t.Fatalf("ExecuteShellCommand error: %v", err)
	}

	if got.FolderGuard == nil {
		t.Fatal("expected session folder guard in execute request")
	}
	if !containsString(got.FolderGuard.ReadPaths, "/Users/mipl/Downloads") {
		t.Fatalf("expected host Downloads in read paths, got %v", got.FolderGuard.ReadPaths)
	}
	if !containsString(got.FolderGuard.WritePaths, "/Users/mipl/Downloads") {
		t.Fatalf("expected host Downloads in write paths, got %v", got.FolderGuard.WritePaths)
	}
	if containsString(got.FolderGuard.BlockedWritePaths, "/Users/mipl/Downloads") {
		t.Fatalf("host Downloads must not remain write-blocked, got %v", got.FolderGuard.BlockedWritePaths)
	}
}

func TestExecuteShellCommandPreservesReadOnlySessionGuard(t *testing.T) {
	sessionID := "test-read-only-shell-guard"
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo"}, nil)
	defer ClearSessionShellConfig(sessionID)

	var got ExecuteShellCommandParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": "ok", "exit_code": 0},
		})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	if _, err := NewClient(server.URL).ExecuteShellCommand(ctx, ExecuteShellCommandParams{
		Command: "cat Workflow/demo/report.html",
	}); err != nil {
		t.Fatalf("read-only shell command failed: %v", err)
	}
	if got.FolderGuard == nil || len(got.FolderGuard.ReadPaths) != 1 || len(got.FolderGuard.WritePaths) != 0 {
		t.Fatalf("read-only guard was not preserved: %#v", got.FolderGuard)
	}
}

func TestExecuteShellCommandSessionEnvOverridesParentMCPRoute(t *testing.T) {
	const sessionID = "pulse-fixer-child-session"
	common.SetSessionFolderGuard(sessionID, []string{"Workflow/demo"}, []string{"Workflow/demo/db"})
	common.SetSessionShellEnv(sessionID, map[string]string{
		"MCP_SESSION_ID": sessionID,
		"MCP_API_URL":    "http://127.0.0.1:45678/s/" + sessionID,
	})
	defer ClearSessionShellConfig(sessionID)

	var got ExecuteShellCommandParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": "ok", "exit_code": 0},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, WithExtraEnv(map[string]string{
		"MCP_SESSION_ID": "parent-workshop-session",
		"MCP_API_URL":    "http://127.0.0.1:45678/s/parent-workshop-session",
		"MCP_API_TOKEN":  "test-token",
	}))
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	if _, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{Command: "printf ok"}); err != nil {
		t.Fatalf("ExecuteShellCommand error: %v", err)
	}
	for key, want := range map[string]string{
		"MCP_SESSION_ID": sessionID,
		"MCP_API_URL":    "http://127.0.0.1:45678/s/" + sessionID,
		"MCP_CUSTOM":     "http://127.0.0.1:45678/s/" + sessionID + "/tools/custom",
		"MCP_AUTH":       "Authorization: Bearer test-token",
	} {
		if value := got.ExtraEnv[key]; value != want {
			t.Fatalf("%s = %q, want %q (env=%#v)", key, value, want, got.ExtraEnv)
		}
	}
}

func TestExecuteShellCommandRejectsExplicitEmptySessionGuard(t *testing.T) {
	sessionID := "test-empty-shell-guard"
	common.SetSessionFolderGuard(sessionID, []string{}, []string{})
	defer ClearSessionShellConfig(sessionID)

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	_, err := NewClient("http://127.0.0.1:1").ExecuteShellCommand(ctx, ExecuteShellCommandParams{
		Command: "pwd",
	})
	if err == nil || !strings.Contains(err.Error(), "no granted workspace paths") {
		t.Fatalf("explicit empty guard should fail before HTTP, got %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestIsGitPushCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"git push", true},
		{"git push origin main", true},
		{"cd repo && git pull --no-edit && git push", true},
		{`git -C /x push`, true},
		{"git status", false},
		{"git pull", false},
		{`echo "git push" >> notes.txt`, false},      // quoted data, not executable
		{"python3 -c 'print(\"git push\")'", false},  // inside quotes
		{"git commit -m 'do git push later'", false}, // mentioned in message
	}
	for _, c := range cases {
		if got := isGitPushCommand(c.cmd); got != c.want {
			t.Errorf("isGitPushCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestIsWorkflowSecretPath(t *testing.T) {
	secret := []string{"secrets.json", "db/secrets.json", "workflow_secrets/x", "workflow_secrets/a/b.txt",
		"a/workflow_secrets/c", "key.pem", "id_rsa.key", "gh.token", ".env", ".env.production", "credentials", "credentials.json"}
	notSecret := []string{"plan.json", "planning/plan.json", "knowledgebase/notes/x.md", "report.html",
		"keyboard.md", "credentialspolicy.md", "db/db.sqlite", "env.go"}
	for _, p := range secret {
		if !isWorkflowSecretPath(p) {
			t.Errorf("isWorkflowSecretPath(%q) = false, want true", p)
		}
	}
	for _, p := range notSecret {
		if isWorkflowSecretPath(p) {
			t.Errorf("isWorkflowSecretPath(%q) = true, want false", p)
		}
	}
}

func TestParseGitHubOwnerRepo(t *testing.T) {
	cases := []struct {
		url, owner, repo string
		ok               bool
	}{
		{"https://github.com/manishiitg/coding-agent-loop.git", "manishiitg", "coding-agent-loop", true},
		{"git@github.com:owner/repo.git", "owner", "repo", true},
		{"ssh://git@github.com/owner/repo", "owner", "repo", true},
		{"https://github.com/owner/repo", "owner", "repo", true},
		{"https://gitlab.com/owner/repo.git", "", "", false},
		{"https://example.com/x/y", "", "", false},
	}
	for _, c := range cases {
		o, r, ok := parseGitHubOwnerRepo(c.url)
		if ok != c.ok || o != c.owner || r != c.repo {
			t.Errorf("parseGitHubOwnerRepo(%q) = (%q,%q,%v), want (%q,%q,%v)", c.url, o, r, ok, c.owner, c.repo, c.ok)
		}
	}
}

// The 2026-09-28 regression: a scripted step's shell runs under the group
// session, but its script saves through the bridge as the step session (the
// one granted DB write). Binding the bridge token to the group session made
// every save "mutation denied". A trusted caller names the bridge session.
func TestExecuteShellCommandUsesTheTrustedBridgeSession(t *testing.T) {
	common.SetBridgeTokenSecret("server-secret")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	const group, step = "session-group-usa-engineering-ops-1", "step-send-email-outreach-session"

	var got ExecuteShellCommandParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"stdout": "ok", "exit_code": 0}})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, group)
	if _, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{
		Command:         "python3 main.py",
		ExtraEnv:        map[string]string{"MCP_SESSION_ID": step, "MCP_API_URL": "http://h/s/" + step},
		BridgeSessionID: step,
	}); err != nil {
		t.Fatalf("ExecuteShellCommand: %v", err)
	}
	if got.ExtraEnv["MCP_SESSION_ID"] != step || got.ExtraEnv["MCP_API_TOKEN"] != common.BridgeTokenForSession(step) {
		t.Fatalf("the script must call the bridge as its step session: %#v", got.ExtraEnv)
	}

	// Without a trusted bridge session, a model-supplied session is still ignored.
	if _, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{
		Command:  "echo $MCP_API_TOKEN",
		ExtraEnv: map[string]string{"MCP_SESSION_ID": "victim"},
	}); err != nil {
		t.Fatalf("ExecuteShellCommand: %v", err)
	}
	if got.ExtraEnv["MCP_SESSION_ID"] != group || got.ExtraEnv["MCP_API_TOKEN"] != common.BridgeTokenForSession(group) {
		t.Fatalf("model extra_env must not choose the bridge session: %#v", got.ExtraEnv)
	}
	// BridgeSessionID is not settable from tool arguments.
	var decoded ExecuteShellCommandParams
	_ = json.Unmarshal([]byte(`{"command":"x","BridgeSessionID":"victim","bridge_session_id":"victim"}`), &decoded)
	if decoded.BridgeSessionID != "" {
		t.Fatal("BridgeSessionID must not decode from tool arguments")
	}
}
