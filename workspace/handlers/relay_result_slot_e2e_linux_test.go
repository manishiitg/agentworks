//go:build linux

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/spf13/viper"
)

// Opt-in production runner test. Uses only a throwaway directory in the slot
// run area. No live Relay, package install, credentials or service is changed.
func TestRelayScriptWritesJSONAsItsSlotE2E(t *testing.T) {
	user := os.Getenv("AGENTWORKS_RELAY_SLOT_USER")
	if user == "" {
		t.Skip("set AGENTWORKS_RELAY_SLOT_USER on a configured Linux host")
	}
	slot, on, err := slots.For(user)
	if !on || err != nil || slot == "" {
		t.Fatalf("slot unavailable: %v", err)
	}
	runRoot, err := slots.RunDirFor(slot)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := os.MkdirTemp(runRoot, "relay-result-check-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(docs) })
	if err := os.Chmod(docs, 0770|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	outputRel := "Workflow/test/runs/iteration-0/default/execution/extract"
	output := filepath.Join(docs, outputRel)
	if err := os.MkdirAll(output, 0750); err != nil {
		t.Fatal(err)
	}
	// Same group-readable but non-writable output mode found in Excellence.
	if err := os.Chmod(output, 0750|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(docs, "ungranted.txt")
	if err := os.WriteFile(filepath.Join(output, "step_helper.py"), []byte("VALUE = True\n"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("UNGRANTED"), 0660); err != nil {
		t.Fatal(err)
	}
	viper.Set("docs-dir", docs)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/execute", ExecuteShellCommand)
	helperCode := `import agentworks_output, step_helper
agentworks_output.set_output({"ok": step_helper.VALUE})
try:
    with open(agentworks_output.__file__, "w") as file:
        file.write("tampered")
except PermissionError:
    pass
else:
    raise AssertionError("platform helper was writable")
`
	command := "set -eu; id -un; python3 -B -c " + shellQuote(helperCode) + "; python3 -m pip list --format=json >/dev/null; if cat " + shellQuote(outside) + " >/dev/null 2>&1; then echo UNGRANTED_VISIBLE; exit 1; fi; echo RESULT_WRITTEN_PIP_OK"
	body, _ := json.Marshal(map[string]any{"command": command, "working_directory": outputRel, "timeout": 30, "use_shell": true,
		"extra_env":    map[string]string{"STEP_OUTPUT_DIR": output, "PYTHONPATH": output},
		"folder_guard": map[string]any{"enabled": true, "read_paths": []string{outputRel}, "write_paths": []string{outputRel}}})
	req := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", user)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var response struct {
		Data struct {
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			ExitCode int    `json:"exit_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	t.Logf("status=%d exit=%d stdout=%q stderr=%q", w.Code, response.Data.ExitCode, response.Data.Stdout, response.Data.Stderr)
	if w.Code != 200 || response.Data.ExitCode != 0 || !strings.HasPrefix(response.Data.Stdout, slot+"\n") || !strings.Contains(response.Data.Stdout, "RESULT_WRITTEN_PIP_OK") {
		t.Fatal("slot output or pip failed")
	}
	raw, err := os.ReadFile(filepath.Join(output, "result.json"))
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("missing JSON result: %q %v", raw, err)
	}
}
