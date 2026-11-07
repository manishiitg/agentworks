package handlers

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/spf13/viper"
)

func TestBrowserRecordingBundleAndIsolation(t *testing.T) {
	root := t.TempDir()
	socket := t.TempDir()
	bin := t.TempDir()
	old := viper.GetString("docs-dir")
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", old)
	t.Setenv("AGENT_BROWSER_SOCKET_DIR", socket)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.MkdirAll(filepath.Join(root, "Workflow", "one"), 0700)
	os.MkdirAll(filepath.Join(root, "Workflow", "two"), 0700)
	os.WriteFile(filepath.Join(socket, "capture-test.stream"), []byte("12345"), 0600)
	script := `#!/bin/sh
[ "$3" = "--user-agent" ] || exit 1
[ "$5" = "--args" ] || exit 1
shift 6
if [ "$1 $2" = "record stop" ] && [ -f "$AGENT_BROWSER_SOCKET_DIR/fail-stop" ]; then echo stop-failed; exit 1; fi
case "$1 $2 $3" in
 "record start "*) printf video > "$3" ;;
 "network har stop") printf '{"log":{"entries":[]}}' > "$4" ;;
esac
printf '{"success":true,"data":{"messages":["test console"]}}'
`
	os.WriteFile(filepath.Join(bin, "agent-browser"), []byte(script), 0700)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:session", BrowserRecording)
	call := func(action, workspace string) (int, browserCapture) {
		payload, _ := json.Marshal(map[string]string{"action": action, "workspace_path": workspace})
		req := httptest.NewRequest("POST", "/capture-test", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		var capture browserCapture
		json.Unmarshal(response.Body.Bytes(), &capture)
		if response.Code != 200 && workspace == "Workflow/one" {
			t.Fatalf("%s: %s", action, response.Body.String())
		}
		return response.Code, capture
	}
	_, started := call("start", "Workflow/one")
	if !started.Recording {
		t.Fatal("not recording")
	}
	_, same := call("start", "Workflow/one")
	if same.Directory != started.Directory {
		t.Fatal("duplicate start forked capture")
	}
	if code, _ := call("stop", "Workflow/two"); code != 409 {
		t.Fatalf("cross-workflow stop: %d", code)
	}
	if code, _ := call("start", "../../outside"); code != 400 {
		t.Fatalf("path traversal: %d", code)
	}
	os.WriteFile(filepath.Join(socket, "fail-stop"), []byte("1"), 0600)
	_, partial := call("stop", "Workflow/one")
	if !partial.Recording || !partial.VideoActive || partial.HARActive {
		t.Fatalf("failed stop must stay retryable: %+v", partial)
	}
	os.Remove(filepath.Join(socket, "fail-stop"))
	_, stopped := call("stop", "Workflow/one")
	if stopped.Recording || len(stopped.Errors) > 0 {
		t.Fatalf("stop failed: %+v", stopped)
	}
	archive, err := zip.OpenReader(filepath.Join(root, stopped.Directory, "capture.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := map[string]bool{}
	for _, file := range archive.File {
		files[file.Name] = true
	}
	for _, name := range []string{"video.webm", "network.har", "console.json", "errors.json", "manifest.json"} {
		if !files[name] {
			t.Errorf("missing %s", name)
		}
	}
	_, again := call("stop", "Workflow/one")
	if again.Directory != stopped.Directory {
		t.Fatal("stop must be idempotent")
	}
	// A finished capture must not reserve the global browser for its workflow.
	if code, state := call("status", "Workflow/two"); code != 200 || state.Recording || state.Directory != "" {
		t.Fatalf("finished foreign capture status: %d %+v", code, state)
	}
	if code, _ := call("start", "Workflow/two"); code != 200 {
		t.Fatalf("finished capture blocked next workflow: %d", code)
	}
	call("stop", "Workflow/two")
	guarded := func(action string, guard *models.FolderGuardConfig) (int, browserCapture) {
		payload, _ := json.Marshal(map[string]interface{}{"action": action, "workspace_path": "Workflow/one", "working_directory": "Workflow/one/runs/step", "folder_guard": guard})
		req := httptest.NewRequest("POST", "/capture-test", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		var state browserCapture
		json.Unmarshal(response.Body.Bytes(), &state)
		return response.Code, state
	}
	for _, guard := range []*models.FolderGuardConfig{
		{Enabled: true, ReadPaths: []string{"Workflow/one"}},
		{Enabled: true, WritePaths: []string{"Workflow/two"}},
		{Enabled: false, WritePaths: []string{"Workflow/one"}},
		{Enabled: true, WritePaths: []string{"Workflow/one"}, BlockedWritePaths: []string{"Workflow/one"}},
	} {
		if code, _ := guarded("start", guard); code != 403 {
			t.Fatalf("guard bypass: %d for %+v", code, guard)
		}
	}
	os.MkdirAll(filepath.Join(root, "Workflow/one/runs/step"), 0700)
	stepGuard := &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"Workflow/one"}, WritePaths: []string{"Workflow/one/runs/step"}}
	code, stepCapture := guarded("start", stepGuard)
	if code != 200 || !strings.HasPrefix(stepCapture.Directory, "Workflow/one/runs/step/browser-recordings/") {
		t.Fatalf("step capture: %d %+v", code, stepCapture)
	}
	if code, _ := guarded("stop", &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"Workflow/one"}}); code != 403 {
		t.Fatalf("read-only stop: %d", code)
	}
	if code, state := guarded("status", &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"Workflow/one"}}); code != 200 || !state.Recording {
		t.Fatalf("read-only status: %d %+v", code, state)
	}
	if code, state := guarded("stop", stepGuard); code != 200 || state.Recording {
		t.Fatalf("step stop: %d %+v", code, state)
	}

	// The same recording and denied-write checks follow a moved workflow.
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	os.MkdirAll(filepath.Join(state, "ownership/crew-executables"), 0700)
	os.MkdirAll(filepath.Join(root, "Crew/example"), 0700)
	os.MkdirAll(filepath.Join(root, "_system/crew-data/crew/workflows"), 0700)
	os.WriteFile(filepath.Join(state, "ownership/projects.json"), []byte(`{"projects":{"work/example":{"product":"work","folder":"example","project_id":"crew","owner_id":"owner"}}}`), 0600)
	binding := crewroot.ExecutableBinding{ID: "one", Root: "Workflow/one", CrewRoot: "Crew/example", CrewID: "crew", Kind: "workflow"}
	raw, _ := json.Marshal(binding)
	key := fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))
	os.WriteFile(filepath.Join(state, "ownership/crew-executables", key), raw, 0600)
	runtime := filepath.Join(root, "_system/crew-data/crew/workflows/one")
	if err := os.Rename(filepath.Join(root, "Workflow/one"), runtime); err != nil {
		t.Fatal(err)
	}
	if code, capture := guarded("start", stepGuard); code != 200 || !capture.Recording {
		t.Fatal("moved recording failed", code, capture)
	}
	denied := &models.FolderGuardConfig{Enabled: true, WritePaths: []string{"Workflow/one"}, BlockedWritePaths: []string{"Workflow/one/runs/step"}}
	if code, _ := guarded("stop", denied); code != 403 {
		t.Fatal("moved blocked path ignored", code)
	}
	if code, capture := guarded("stop", stepGuard); code != 200 || capture.Recording {
		t.Fatal("moved stop failed", code, capture)
	}
	if _, err := os.Stat(filepath.Join(root, "Workflow/one")); !os.IsNotExist(err) {
		t.Fatal("recording recreated the old workflow")
	}

}
