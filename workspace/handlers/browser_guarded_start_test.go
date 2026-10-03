package handlers

import (
	"context"
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
	"github.com/manishiitg/coding-agent-loop/workspace/browserteach"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/spf13/viper"
)

// Qualifies the actual guarded shell path, not just direct CLI execution. Each
// browser/profile/socket belongs to this temporary fixture and is removed.
func TestBrowserRealGuardedStartup(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TEACH_E2E") != "1" {
		t.Skip("set RUN_BROWSER_TEACH_E2E=1 for owned Chrome")
	}
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv(slots.EnvEnabled, "off")
	root := t.TempDir()
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", "")
	profileBase, err := os.MkdirTemp("/tmp", "aw-guard-profile-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(profileBase)
	if executable := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; func() bool { _, err := os.Stat(executable); return err == nil }() {
		t.Setenv("AGENT_BROWSER_EXECUTABLE_PATH", executable)
	}
	t.Setenv(browserconfig.ProfileRootEnv, filepath.Join(profileBase, "profile"))
	previous, had := os.LookupEnv(browserconfig.ProfileEnv)
	os.Unsetenv(browserconfig.ProfileEnv)
	defer func() {
		if had {
			os.Setenv(browserconfig.ProfileEnv, previous)
		} else {
			os.Unsetenv(browserconfig.ProfileEnv)
		}
	}()
	session := fmt.Sprintf("workflow-%016x--browser", time.Now().UnixNano())
	workspace := "Workflow/" + session
	if err := os.MkdirAll(filepath.Join(root, workspace), 0700); err != nil {
		t.Fatal(err)
	}
	launch := browserconfig.HeadlessArgsForSession(session)
	run := func(args ...string) []byte {
		t.Helper()
		argv := append(append(append([]string{}, launch...), "--session", session), args...)
		argv = append(argv, "--json")
		quoted := []string{}
		for _, arg := range argv {
			quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\\''")+"'")
		}
		body, _ := json.Marshal(map[string]any{"command": "agent-browser " + strings.Join(quoted, " "), "working_directory": workspace, "timeout": 30, "folder_guard": map[string]any{"enabled": true, "write_paths": []string{workspace}, "browser_session": session}})
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/api/execute", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-User-ID", "guarded-browser-fixture")
		ExecuteShellCommand(c)
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Stdout string `json:"stdout"`
				Code   int    `json:"exit_code"`
			} `json:"data"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &response) != nil || !response.Success || response.Data.Code != 0 {
			t.Fatalf("Guarded %v: HTTP %d %s", args, rec.Code, rec.Body.String())
		}
		var command struct {
			Success bool `json:"success"`
		}
		if json.Unmarshal([]byte(response.Data.Stdout), &command) != nil || !command.Success {
			t.Fatalf("Guarded %v: %s", args, response.Data.Stdout)
		}
		return []byte(response.Data.Stdout)
	}
	// Cleanup remains scoped to this fixture even if the guarded launch fails.
	defer func() {
		closeArgs := append([]string{"close"}, launch...)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = runTeachCommand(ctx, browserconfig.SocketDirForSession(session), session, closeArgs...)
		os.RemoveAll(browserconfig.SocketDirForSession(session))
	}()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<title>Guarded fixture</title><h1>Browser ready</h1>`))
	}))
	defer fixture.Close()
	run("tab")
	run("open", fixture.URL)
	run("eval", "localStorage.setItem('signed_in_fixture','yes')")
	run("tab") // Start/reuse lists tabs; it must not reset the signed-in page.
	if !strings.Contains(string(run("get", "url")), fixture.URL) {
		t.Fatal("Repeated startup lost the page")
	}
	if !strings.Contains(string(run("eval", "localStorage.getItem('signed_in_fixture')")), `"yes"`) {
		t.Fatal("Repeated startup lost sign-in")
	}
	// Teaching must attach to the guarded daemon without changing its process.
	_, socket, err := browserLiveEndpoint(session)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(socket, session+".pid"))
	if err != nil {
		t.Fatal(err)
	}
	teachBody, _ := json.Marshal(map[string]any{"action": "start", "goal": "Guarded teaching fixture", "workspace_path": workspace, "launch_args": launch})
	teachResponse := httptest.NewRecorder()
	teachContext, _ := gin.CreateTestContext(teachResponse)
	teachContext.Params = gin.Params{{Key: "session", Value: session}}
	teachContext.Request = httptest.NewRequest("POST", "/", strings.NewReader(string(teachBody)))
	teachContext.Request.Header.Set("Content-Type", "application/json")
	BrowserTeaching(teachContext)
	if teachResponse.Code != 200 {
		t.Fatalf("Guarded teaching: HTTP %d %s", teachResponse.Code, teachResponse.Body.String())
	}
	after, err := os.ReadFile(filepath.Join(socket, session+".pid"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("Teaching restarted the browser: before=%s after=%s err=%v", before, after, err)
	}
	if value, ok := teachingActive.Load(session); ok {
		stopTeach(value.(*teachState), "cancelled")
	}
	if err := replayTeach(context.Background(), socket, session, &teachState{Actions: []browserteach.Action{{ID: 1, Kind: "navigate", Page: "root", URL: fixture.URL}}, Check: teachCheck{Kind: "text", Value: "Browser ready"}}, nil); err != nil {
		t.Fatalf("Guarded replay: %v", err)
	}
	after, err = os.ReadFile(filepath.Join(socket, session+".pid"))
	if err != nil || string(before) != string(after) {
		t.Fatal("Replay restarted the browser", err)
	}
	for _, args := range [][]string{{"network", "har", "start", "--content", "none"}, {"console", "--clear"}, {"errors", "--clear"}, {"network", "har", "stop", os.DevNull}} {
		if _, err := runCaptureCommand(context.Background(), socket, session, args...); err != nil {
			t.Fatalf("Managed capture %v: %v", args, err)
		}
	}
	after, err = os.ReadFile(filepath.Join(socket, session+".pid"))
	if err != nil || string(before) != string(after) {
		t.Fatal("Capture restarted browser", err)
	}
	// Qualify viewer paste against the daemon started by the guarded shell.
	run("eval", "(() => { const field = document.createElement('textarea'); field.id = 'viewer-paste'; document.body.append(field); field.focus(); })()")
	text := "first line\n日本語🙂 \"quotes\" $(not-a-command)"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := insertBrowserViewerText(ctx, session, text); err != nil {
		t.Fatalf("Viewer paste: %v", err)
	}
	var pasted struct {
		Data struct {
			Result string `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(run("eval", "document.querySelector('#viewer-paste').value"), &pasted); err != nil || pasted.Data.Result != text {
		t.Fatalf("Viewer paste did not preserve multiline Unicode text: %v", err)
	}
	// The headless shell has no native Sessions files: persist and reopen its
	// actual tab strip, including duplicate URLs and the selected tab.
	run("tab", "new", fixture.URL+"/second")
	run("tab", "new", fixture.URL+"/removed")
	run("tab", "close")
	run("tab", "new", fixture.URL)
	if err := saveBrowserTabs(context.Background(), socket, session); err != nil {
		t.Fatal(err)
	}
	statePath, err := browserTabsPath(session)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("Tab state must be private", err)
	}
	run("close")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(socket, session+".pid")); os.IsNotExist(err) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	run("tab")
	if err := restoreBrowserTabs(context.Background(), session); err != nil {
		t.Fatalf("Restore tabs: %v", err)
	}
	reopened, err := currentBrowserTabs(context.Background(), socket, session)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	active := ""
	for _, tab := range reopened {
		counts[tab.URL]++
		if tab.Active {
			active = tab.URL
		}
	}
	if len(reopened) != 3 || counts[fixture.URL+"/"] != 2 || counts[fixture.URL+"/second"] != 1 || active != fixture.URL+"/" {
		t.Fatalf("Wrong restored tabs: %+v", reopened)
	}
	if err := restoreBrowserTabs(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if again, err := currentBrowserTabs(context.Background(), socket, session); err != nil || len(again) != 3 {
		t.Fatal("Repeated restore duplicated tabs", again, err)
	}
	if !strings.Contains(string(run("eval", "localStorage.getItem('signed_in_fixture')")), `"yes"`) {
		t.Fatal("Restored tabs lost sign-in")
	}

}
