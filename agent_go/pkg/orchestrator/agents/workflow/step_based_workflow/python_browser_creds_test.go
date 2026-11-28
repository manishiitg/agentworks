package step_based_workflow

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPythonAgentBrowserSecretsValidationAndRedaction(t *testing.T) {
	// 1. Valid keys (plain and SECRET_-prefixed) normalize cleanly.
	parsed, err := parsePythonAgentBrowserSecrets(map[string]interface{}{
		"browser_secrets": map[string]interface{}{
			"SITE_PASSWORD":        "pw-alpha-123",
			"SECRET_SECONDARY_PIN": "pin-456",
		},
	})
	if err != nil {
		t.Fatalf("parsePythonAgentBrowserSecrets valid: %v", err)
	}
	if parsed["SECRET_SITE_PASSWORD"] != "pw-alpha-123" || parsed["SECRET_SECONDARY_PIN"] != "pin-456" {
		t.Fatalf("unexpected parsed map: %#v", parsed)
	}

	// 2. Invalid names, reserved prefix, non-strings, empties, and conflicts fail plainly.
	for _, tc := range []struct {
		name string
		raw  interface{}
		want string
	}{
		{"non-object", []string{"bad"}, "must be an object"},
		{"bad-chars", map[string]interface{}{"BAD-KEY": "val"}, "Invalid browser_secrets key"},
		{"empty-key", map[string]interface{}{"SECRET_": "val"}, "Invalid browser_secrets key"},
		{"reserved-prefix", map[string]interface{}{"AGENTWORKS_BROWSER_INPUT_0": "val"}, "Invalid browser_secrets key"},
		{"empty-value", map[string]interface{}{"SITE_PASSWORD": ""}, "non-empty string"},
		{"non-string-value", map[string]interface{}{"SITE_PASSWORD": 123}, "non-empty string"},
		{"duplicate-conflict", map[string]interface{}{"SITE_PASSWORD": "one", "SECRET_SITE_PASSWORD": "two"}, "Duplicate browser_secrets key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePythonAgentBrowserSecrets(map[string]interface{}{"browser_secrets": tc.raw})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("case %s: err=%v, want substring %q", tc.name, err, tc.want)
			}
		})
	}

	// 3. sanitizePythonHelperArguments strips values without mutating caller map.
	orig := map[string]interface{}{
		"name":            "Login",
		"browser":         true,
		"browser_secrets": map[string]interface{}{"SITE_PASSWORD": "super-secret-value-xyz"},
	}
	sanitized := sanitizePythonHelperArguments("agent", orig)
	encodedSanitized, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedSanitized), "super-secret-value-xyz") {
		t.Fatalf("sanitized arguments leaked secret: %s", encodedSanitized)
	}
	if !strings.Contains(string(encodedSanitized), "[REDACTED]") {
		t.Fatalf("sanitized arguments missing [REDACTED]: %s", encodedSanitized)
	}
	if orig["browser_secrets"].(map[string]interface{})["SITE_PASSWORD"] != "super-secret-value-xyz" {
		t.Fatal("sanitizePythonHelperArguments mutated caller map")
	}

	// 4. redactPythonAgentText removes both raw and JSON-escaped secret forms.
	secretWithQuotes := `p@ss"word\123`
	redactVals := pythonBrowserSecretRedactValues(map[string]string{"SECRET_PW": secretWithQuotes})
	rawOut := redactPythonAgentText("raw="+secretWithQuotes+` json=p@ss\"word\\123`, redactVals)
	if strings.Contains(rawOut, "p@ss") {
		t.Fatalf("redactPythonAgentText leaked secret: %q", rawOut)
	}
}

func TestPythonAutomationHandlerRedactsBrowserSecretsOnDisk(t *testing.T) {
	docs, _ := workflowHTTPTestWorkspace(t)
	const root = "Workflow/browser-secrets-redaction"
	runRoot := filepath.Join(docs, root, "runs/run-one")
	sourceDir := filepath.Join(runRoot, "execution/frozen/code")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte("print('ok')\n"), 0600); err != nil {
		t.Fatal(err)
	}

	c := newAgentFactoryTestOrchestrator(t)
	c.SetWorkspacePath(root)
	c.selectedRunFolder = "run-one"
	parent := &ScriptedPlanStep{
		CommonStepFields: CommonStepFields{ID: "parent"},
		AgentConfigs:     &AgentConfigs{ExecutionLLM: &AgentLLMConfig{Provider: "codex-cli", ModelID: "fixture"}},
	}

	ctx := context.Background()
	handler, finish := c.pythonAutomationHandler(ctx, parent, sourceDir, runRoot)
	defer finish(false)

	const sentinel = "sheet-secret-sentinel-987654"
	rawReply, err := handler(ctx, map[string]interface{}{
		"action":       "agent",
		"key":          "login-without-browser",
		"explicit_key": true,
		"order":        float64(1),
		"arguments": map[string]interface{}{
			"name":            "Portal Login",
			"messages":        []string{"Log in with $SECRET_SITE_PASSWORD"},
			"browser":         false,
			"browser_secrets": map[string]interface{}{"SITE_PASSWORD": sentinel},
		},
	})
	if err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}
	var reply pythonHelperReply
	if err := json.Unmarshal([]byte(rawReply), &reply); err != nil {
		t.Fatalf("unmarshal reply: %v (%s)", err, rawReply)
	}
	if reply.OK || !strings.Contains(reply.Error, "Use browser=True when passing browser_secrets") {
		t.Fatalf("expected browser=True refusal, got %+v", reply)
	}
	if strings.Contains(rawReply, sentinel) {
		t.Fatalf("helper reply leaked secret value: %s", rawReply)
	}

	// Verify no file written anywhere in the run folder contains the secret,
	// and the helper request log recorded [REDACTED] instead.
	sawRedacted := false
	err = filepath.WalkDir(runRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), sentinel) {
			t.Errorf("run artifact %s leaked browser_secrets value", path)
		}
		if strings.Contains(path, filepath.Join("logs", "helpers")) && strings.Contains(string(data), "[REDACTED]") {
			sawRedacted = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawRedacted {
		t.Fatal("expected helper request log to contain [REDACTED] placeholder")
	}
}

func TestAgentworksPythonClientBrowserSecrets(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file path")
	}
	runtimeDir := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..", "..", "workspace", "security", "runtime"))
	if _, err := os.Stat(filepath.Join(runtimeDir, "agentworks", "__init__.py")); err != nil {
		t.Fatalf("agentworks runtime not found at %s: %v", runtimeDir, err)
	}

	var received map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  map[string]interface{}{"ok": true, "text": "done", "conversation_id": "python-agent-00000000-0000-4000-8000-000000000001"},
		})
	}))
	defer srv.Close()

	script := `
import agentworks
try:
    agentworks.agent("login", "do it", browser=False, browser_secrets={"SITE_PASSWORD": "pw"})
    raise AssertionError("expected ValueError when browser=False")
except ValueError as exc:
    assert "browser=True" in str(exc), str(exc)

try:
    agentworks.agent("login", "do it", browser=True, browser_secrets={"SITE_PASSWORD": ""})
    raise AssertionError("expected ValueError for empty secret value")
except ValueError as exc:
    assert "non-empty" in str(exc), str(exc)

reply = agentworks.agent("login", "type $SECRET_SITE_PASSWORD", browser=True, browser_secrets={"SITE_PASSWORD": "sheet-pw-42"})
assert reply.ok and reply.text == "done", reply
`
	cmd := exec.Command(python, "-c", script)
	cmd.Env = append(os.Environ(),
		"PYTHONPATH="+runtimeDir,
		"WORKFLOW_RUN_DIR="+t.TempDir(),
		"MCP_CUSTOM="+srv.URL,
		"MCP_AUTH=Authorization:Bearer test-token",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python client check failed: %v\n%s", err, out)
	}
	args, _ := received["arguments"].(map[string]interface{})
	secrets, _ := args["browser_secrets"].(map[string]interface{})
	if secrets["SITE_PASSWORD"] != "sheet-pw-42" {
		t.Fatalf("python client did not forward browser_secrets: %#v", received)
	}
}
