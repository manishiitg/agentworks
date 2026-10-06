//go:build linux

package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/spf13/viper"
)

// Reproduce the persistent browser writer's original private mount namespace,
// then finalize through the real HTTP handler with a later request's guard.
// The writer stands in for agent-browser's file write; Chrome capture itself is
// exercised by TestChromeExtensionToolRealE2E, rather than mocked here.
func TestBrowserArtifactSurvivesPersistentPrivateTmp(t *testing.T) {
	buildLandlockRunner(t)
	root := t.TempDir()
	session := "session-0123456789abc575--browser"
	other := "session-fedcba9876543575--browser"
	socket := browserconfig.SocketDirForSession(session)
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	work := filepath.Join(root, "code", "test")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	writer := filepath.Join(work, "writer.py")
	script := `import json, os, sys, time
socket = sys.argv[1]
with open(socket + '/writer.pid', 'w') as f: f.write(str(os.getpid()))
while True:
    try:
        with open(socket + '/request.json') as f: path = json.load(f)
    except FileNotFoundError:
        time.sleep(.02)
        continue
    os.unlink(socket + '/request.json')
    try:
        with open(path, 'wb') as f: f.write(b'\x89PNG\r\n\x1a\nimage-data')
        result = 'saved'
    except OSError as e: result = str(e)
    with open(socket + '/response.json', 'w') as f: json.dump(result, f)
`
	if err := os.WriteFile(writer, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	oldDocs := viper.GetString("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	guard := &models.FolderGuardConfig{Enabled: true, ReadPaths: []string{"code/test"}, WritePaths: []string{"code/test"}, BrowserSession: session}
	run := func(command string, transfer *models.BrowserArtifactTransfer) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(models.ExecuteShellRequest{Command: command, WorkingDirectory: "code/test", FolderGuard: guard, ArtifactTransfer: transfer})
		if err != nil {
			t.Fatal(err)
		}
		gin.SetMode(gin.ReleaseMode)
		r := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(r)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/execute", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ExecuteShellCommand(ctx)
		return r
	}
	response := run(fmt.Sprintf("python3 %q %q >/dev/null 2>&1 &", writer, socket), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("start persistent writer: %s", response.Body.String())
	}
	waitFile := func(path string) []byte {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
				return data
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("persistent writer did not produce %s", path)
		return nil
	}
	var pid int
	if _, err := fmt.Sscanf(string(waitFile(filepath.Join(socket, "writer.pid"))), "%d", &pid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	write := func(path string) string {
		t.Helper()
		_ = os.Remove(filepath.Join(socket, "response.json"))
		data, _ := json.Marshal(path)
		if err := os.WriteFile(filepath.Join(socket, "request.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		var result string
		if err := json.Unmarshal(waitFile(filepath.Join(socket, "response.json")), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	legacy := filepath.Join(security.BrowserArtifactStagingDir(), "private-tmp-test.png")
	if err := security.PrepareBrowserArtifactStaging(legacy); err != nil {
		t.Fatal(err)
	}
	if result := write(legacy); !strings.Contains(result, "No such file or directory") {
		t.Fatalf("expected original private-/tmp ENOENT, got %q", result)
	}
	t.Log("reproduced original ENOENT from persistent private /tmp")
	staged := filepath.Join(browserconfig.ArtifactDirForSession(session), "capture.png")
	if err := security.PrepareBrowserArtifactStaging(staged, session); err != nil {
		t.Fatal(err)
	}
	if result := write(staged); result != "saved" {
		t.Fatalf("scoped capture failed: %s", result)
	}
	transfer := &models.BrowserArtifactTransfer{SourcePath: staged, DestinationPath: "code/test/evidence/capture.png", Kind: "screenshot", Finalize: true}
	response = run("true", transfer)
	if response.Code != http.StatusOK {
		t.Fatalf("finalize screenshot: %s", response.Body.String())
	}
	if data, err := os.ReadFile(filepath.Join(root, transfer.DestinationPath)); err != nil || string(data) != "\x89PNG\r\n\x1a\nimage-data" {
		t.Fatalf("workspace image=%q err=%v", data, err)
	}
	if result := write(staged); result != "saved" {
		t.Fatal(result)
	}
	transfer.DestinationPath = "code/other/stolen.png"
	response = run("true", transfer)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "not covered") {
		t.Fatalf("unauthorized destination accepted: %s", response.Body.String())
	}
	transfer.SourcePath = filepath.Join(browserconfig.ArtifactDirForSession(other), "capture.png")
	transfer.DestinationPath = "code/test/evidence/other.png"
	response = run("true", transfer)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "this browser session") {
		t.Fatalf("other browser's staging accepted: %s", response.Body.String())
	}
	t.Log("scoped staging survives later requests; other scopes and unauthorized outputs are rejected")
}
