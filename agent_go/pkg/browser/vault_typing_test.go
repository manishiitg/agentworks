package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Regression for #4456: fill and type must substitute this turn's selected
// $SECRET_<NAME> via ExtraEnv expansion without putting the secret value into
// the shell command, ExecuteShellResponse.Command, tool results, or errors.
func TestBrowserSecretTypingRealShellExecutionAndRedaction(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("NATIVE_WORKSPACE", "true")

	// Install a stub agent-browser executable on PATH that records its exact argv
	// for fill/type and echoes the typed value in stdout/stderr so we can verify
	// both exact delivery and redaction.
	binDir := t.TempDir()
	tmpDir := t.TempDir()
	argvLog := filepath.Join(tmpDir, "argv.json")
	failMarker := filepath.Join(tmpDir, "fail")
	stubPath := filepath.Join(binDir, "agent-browser")
	stubScript := fmt.Sprintf(`#!/bin/sh
python3 - %q %q "$@" <<'PY'
import json, os, sys
argv_path, fail_path = sys.argv[1], sys.argv[2]
args = sys.argv[3:]
if "fill" not in args and "type" not in args:
    print(json.dumps({"success": True, "data": {"tabs": [{"id": "t1", "url": "about:blank", "active": True}]}}))
    sys.exit(0)
with open(argv_path, "w") as f:
    json.dump(args, f)
typed = args[-2] if len(args) >= 2 else ""
if os.path.exists(fail_path):
    sys.stderr.write("boom typed=" + typed + "\n")
    sys.exit(1)
print(json.dumps({"success": True, "data": {"typed": typed}}))
PY
`, argvLog, failMarker)
	if err := os.WriteFile(stubPath, []byte(stubScript), 0755); err != nil {
		t.Fatalf("write stub agent-browser: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	oldDocs := viper.GetString("docs-dir")
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", oldDocs)

	gin.SetMode(gin.TestMode)
	var lastExecutedCommand string
	var lastResponseCommand string
	inspectRouter := gin.New()
	inspectRouter.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBytes, _ := io.ReadAll(r.Body)
		var req struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(rawBytes, &req)
		lastExecutedCommand = req.Command

		fwdReq := r.Clone(r.Context())
		fwdReq.Body = io.NopCloser(strings.NewReader(string(rawBytes)))
		rec := httptest.NewRecorder()
		inspectRouter.ServeHTTP(rec, fwdReq)

		var resp APIResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		lastResponseCommand = resp.Data.Command

		for k, vals := range rec.Header() {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer server.Close()

	secretValue := "p@ss'w\"ord $HOME `id` \\ line1\nline2"
	sessionID := "secret-typing-real-shell"
	common.SetSessionWorkingDir(sessionID, ".")
	common.SetSessionShellEnv(sessionID, map[string]string{
		"SECRET_SITE_PASSWORD": secretValue,
	})
	defer common.ClearSessionShellConfig(sessionID)

	exec := NewExecutor(NewClient(server.URL), WithBrowserRuntimeConfig(NewBrowserRuntimeConfig("headless", nil)))
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)

	// 1. fill with $SECRET_SITE_PASSWORD delivers the exact value to agent-browser
	//    while keeping the secret out of the shell command and tool output.
	out, err := exec.HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "fill",
		"args":    []interface{}{"@e3", "$SECRET_SITE_PASSWORD"},
		"session": "default",
	})
	if err != nil {
		t.Fatalf("fill failed: %v", err)
	}
	if strings.Contains(lastExecutedCommand, secretValue) || strings.Contains(lastExecutedCommand, "p@ss") {
		t.Fatalf("shell command leaked secret value: %q", lastExecutedCommand)
	}
	if !strings.Contains(lastExecutedCommand, `"$SECRET_AGENTWORKS_BROWSER_INPUT_0"`) {
		t.Fatalf("shell command did not use double-quoted env ref: %q", lastExecutedCommand)
	}
	if strings.Contains(lastResponseCommand, secretValue) || strings.Contains(lastResponseCommand, "p@ss") {
		t.Fatalf("workspace response Command leaked secret value: %q", lastResponseCommand)
	}
	if strings.Contains(out, secretValue) || strings.Contains(out, "p@ss") {
		t.Fatalf("tool result leaked secret value: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected echoed secret in stdout to be redacted, got: %q", out)
	}

	rawArgv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read stub argv: %v", err)
	}
	var gotArgv []string
	if err := json.Unmarshal(rawArgv, &gotArgv); err != nil {
		t.Fatalf("unmarshal stub argv: %v", err)
	}
	foundExact := false
	for _, a := range gotArgv {
		if a == secretValue {
			foundExact = true
			break
		}
	}
	if !foundExact {
		t.Fatalf("agent-browser did not receive exact secret argv; got %#v", gotArgv)
	}

	// 2. type with embedded ${SECRET_SITE_PASSWORD} and failing command redacts stderr error.
	if err := os.WriteFile(failMarker, []byte("1"), 0600); err != nil {
		t.Fatalf("write fail marker: %v", err)
	}
	_, err = exec.HandleAgentBrowser(ctx, map[string]interface{}{
		"command": "type",
		"args":    []interface{}{"@e4", "prefix-${SECRET_SITE_PASSWORD}-suffix"},
		"session": "default",
	})
	if err == nil {
		t.Fatalf("expected error from failing stub")
	}
	if strings.Contains(err.Error(), secretValue) || strings.Contains(err.Error(), "p@ss") {
		t.Fatalf("error message leaked secret value: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected echoed secret in error to be redacted, got: %v", err)
	}
}

func TestBrowserSecretTypingAccessAndScopeRules(t *testing.T) {
	var called int
	var lastReq ShellExecuteRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		_ = json.NewDecoder(r.Body).Decode(&lastReq)
		_ = json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data:    ShellExecuteResponse{Stdout: `{"success":true}`, ExitCode: 0},
		})
	}))
	defer server.Close()

	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("AGENT_BROWSER_MANAGED_ENABLED", "true")

	parentSession := "secret-typing-parent"
	childRunSession := "secret-typing-run-ask"
	common.SetSessionShellEnv(parentSession, map[string]string{
		"SECRET_KNOWN_KEY": "super-secret-value-99",
	})
	common.SetSessionCrewAskReadOnly(childRunSession, true)
	defer common.ClearSessionShellConfig(parentSession)
	defer common.ClearSessionShellConfig(childRunSession)

	exec := NewExecutor(NewClient(server.URL), WithBrowserRuntimeConfig(NewBrowserRuntimeConfig("headless", nil)))
	parentCtx := context.WithValue(context.Background(), common.ChatSessionIDKey, parentSession)

	// Unknown/unselected secret fails with a plain message and never calls workspace-api.
	_, err := exec.HandleAgentBrowser(parentCtx, map[string]interface{}{
		"command": "fill",
		"args":    []interface{}{"@e1", "$SECRET_MISSING_KEY"},
		"session": "default",
	})
	if err == nil || !strings.Contains(err.Error(), "MISSING_KEY") || !strings.Contains(err.Error(), "not selected") {
		t.Fatalf("expected plain missing-secret error, got: %v", err)
	}
	if called != 0 {
		t.Fatalf("workspace-api must not be called for unknown secret, calls=%d", called)
	}

	// Malformed empty secret reference fails explicitly instead of typing literally.
	_, err = exec.HandleAgentBrowser(parentCtx, map[string]interface{}{
		"command": "type",
		"args":    []interface{}{"@e1", "$SECRET_"},
		"session": "default",
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid secret reference") {
		t.Fatalf("expected malformed secret reference error, got: %v", err)
	}
	if called != 0 {
		t.Fatalf("workspace-api must not be called for malformed secret ref, calls=%d", called)
	}

	// Run/Ask turn (CrewAskReadOnly) must not inherit secrets from parent workflow session.
	runCtx := context.WithValue(context.Background(), common.ChatSessionIDKey, childRunSession)
	runCtx = context.WithValue(runCtx, common.WorkflowSessionIDKey, parentSession)
	_, err = exec.HandleAgentBrowser(runCtx, map[string]interface{}{
		"command": "fill",
		"args":    []interface{}{"@e1", "$SECRET_KNOWN_KEY"},
		"session": "default",
	})
	if err == nil || !strings.Contains(err.Error(), "KNOWN_KEY") {
		t.Fatalf("expected Run/Ask turn to be refused access to parent secret, got: %v", err)
	}
	if called != 0 {
		t.Fatalf("workspace-api must not be called on refused Run/Ask secret, calls=%d", called)
	}

	// Non-typing commands (eval) must NOT expand $SECRET_KNOWN_KEY.
	_, err = exec.HandleAgentBrowser(parentCtx, map[string]interface{}{
		"command": "eval",
		"args":    []interface{}{"console.log('$SECRET_KNOWN_KEY')"},
		"session": "default",
	})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if len(lastReq.ExtraEnv) != 0 {
		t.Fatalf("eval must not attach ExtraEnv secrets: %#v", lastReq.ExtraEnv)
	}
	if strings.Contains(lastReq.Command, "super-secret-value-99") {
		t.Fatalf("eval must not expand secret value: %q", lastReq.Command)
	}

	// Shared prepareBrowserSecretArgs path (used by extension, headless, and CDP modes).
	plan, err := prepareBrowserSecretArgs("type", []string{"@e2", "$SECRET_KNOWN_KEY"}, sessionSecretsForBrowser(parentSession, parentSession))
	if err != nil || plan == nil {
		t.Fatalf("prepareBrowserSecretArgs(type) = %v, %v", plan, err)
	}
	if plan.ExtraEnv["SECRET_AGENTWORKS_BROWSER_INPUT_0"] != "super-secret-value-99" {
		t.Fatalf("unexpected ExtraEnv: %#v", plan.ExtraEnv)
	}
	if plan.RewrittenArgs[1] != "$SECRET_AGENTWORKS_BROWSER_INPUT_0" {
		t.Fatalf("unexpected RewrittenArgs: %#v", plan.RewrittenArgs)
	}
}
