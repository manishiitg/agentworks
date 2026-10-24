package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func playwrightTestServer(t *testing.T) (*StreamingAPI, *httptest.Server) {
	t.Helper()
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"run": {UserID: "alice", WorkspacePath: "Workflow/test"}}}
	router := mux.NewRouter()
	tool := router.PathPrefix("/s/{session_id}/tools").Subrouter()
	tool.Use(executor.AuthMiddleware("producer-secret"))
	tool.HandleFunc("/browser/live", api.handlePlaywrightPublisher)
	tool.HandleFunc("/browser/packages/{package}", api.handlePlaywrightPackage)
	router.HandleFunc("/api/browser/live/sessions", api.handleLiveBrowserSessions)
	router.HandleFunc("/api/browser/live/{session}/stream", api.handleLiveBrowserStream)
	router.HandleFunc("/api/browser/live/{session}/recording", api.handleBrowserRecording)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("X-Test-User")
		router.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user})))
	}))
	t.Cleanup(func() {
		server.Close()
		api.playwrightLive.Lock()
		records := []*playwrightRecording{}
		for _, r := range api.playwrightLive.recordings {
			records = append(records, r)
		}
		api.playwrightLive.Unlock()
		for _, r := range records {
			api.deletePlaywrightRecording(r)
		}
	})
	return api, server
}

func readPlaywrightType(t *testing.T, c *websocket.Conn, kind string) map[string]interface{} {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		var m map[string]interface{}
		if err := c.ReadJSON(&m); err != nil {
			t.Fatal(err)
		}
		if m["type"] == kind {
			return m
		}
	}
}

func TestPlaywrightRunCannotSendInput(t *testing.T) {
	fx := newCrewRunModeFixture(t)
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "browser-a1", OwnerID: "owner", CoOwners: []string{"coowner"}, Shared: true}); err != nil {
		t.Fatal(err)
	}
	const root = "Crew/browser-a1"
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"reader","username":"reader","products":["work"]},{"id":"coowner","username":"coowner","products":["work"]}]}`)
	api := fx.api
	s := &playwrightLiveSession{id: "pw-access", owner: "owner", workspace: playwrightScope("owner", root), latest: map[string][]byte{}, watchers: map[chan []byte]struct{}{}, input: make(chan []byte, 8), done: make(chan struct{})}
	api.playwrightLive.sessions = map[string]*playwrightLiveSession{s.id: s}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: r.Header.Get("X-Test-User")}))
		api.handlePlaywrightViewer(w, r, s.id)
	}))
	defer server.Close()
	for _, user := range []string{"owner", "coowner", "reader"} {
		viewer, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/?workspace_path="+root, http.Header{"X-Test-User": []string{user}})
		if err != nil {
			t.Fatal(err)
		}
		ready := readPlaywrightType(t, viewer, "viewer_ready")
		if ready["interactive"] != (user != "reader") {
			t.Fatalf("wrong input authority: %s %v", user, ready)
		}
		for _, command := range []map[string]any{
			{"type": "input_mouse", "eventType": "mousePressed", "button": "left", "x": 10, "y": 20},
			{"type": "input_keyboard", "eventType": "keyDown", "key": "x", "code": "KeyX", "text": "x"},
			{"type": "navigate", "url": "https://example.com"},
		} {
			if err := viewer.WriteJSON(command); err != nil {
				t.Fatal(err)
			}
			if user == "reader" {
				readPlaywrightType(t, viewer, "viewer_error")
				select {
				case <-s.input:
					t.Fatal("Run user drove browser")
				default:
				}
			} else {
				select {
				case <-s.input:
				case <-time.After(time.Second):
					t.Fatal("owner input missing")
				}
			}
		}
		viewer.Close()
	}
}

func TestPlaywrightLiveOwnerInputAndCleanup(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	api, server := playwrightTestServer(t)
	url := strings.Replace(server.URL, "http", "ws", 1)
	for _, path := range []string{"/s/run/tools/browser/live", "/s/missing/tools/browser/live"} {
		headers := http.Header{}
		if strings.Contains(path, "missing") {
			headers.Set("Authorization", "Bearer producer-secret")
		}
		c, response, err := websocket.DefaultDialer.Dial(url+path, headers)
		if c != nil {
			c.Close()
		}
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("unauthenticated or unknown run registered")
		}
	}
	producer, _, err := websocket.DefaultDialer.Dial(url+"/s/run/tools/browser/live?label=Checkout", http.Header{"Authorization": []string{"Bearer producer-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	id := readPlaywrightType(t, producer, "registered")["browser_session"].(string)
	for _, scope := range []struct {
		user, workspace string
		want            int
	}{{"alice", "Workflow/test", 1}, {"bob", "Workflow/test", 0}, {"alice", "Workflow/other", 0}, {"", "Workflow/test", 0}} {
		req := httptest.NewRequest("GET", "/?workspace_path="+scope.workspace, nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: scope.user}))
		if got := len(api.liveBrowserSessions(req)); got != scope.want {
			t.Fatalf("scope %+v got %d", scope, got)
		}
	}
	for _, scope := range []struct{ user, workspace string }{{"bob", "Workflow/test"}, {"alice", "Workflow/other"}, {"", "Workflow/test"}} {
		c, response, err := websocket.DefaultDialer.Dial(url+"/api/browser/live/"+id+"/stream?workspace_path="+scope.workspace, http.Header{"X-Test-User": []string{scope.user}})
		if c != nil {
			c.Close()
		}
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatalf("viewer crossed ownership boundary: %+v", scope)
		}
	}
	viewer, _, err := websocket.DefaultDialer.Dial(url+"/api/browser/live/"+id+"/stream?workspace_path=Workflow/test", http.Header{"X-Test-User": []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	if readPlaywrightType(t, viewer, "viewer_ready")["interactive"] != true {
		t.Fatal("builder input unavailable")
	}
	_ = viewer.WriteJSON(map[string]any{"type": "input_mouse", "eventType": "mousePressed", "x": 12, "y": 20, "button": "left", "clickCount": 1, "secret": "must-not-forward"})
	command := readPlaywrightType(t, producer, "input_mouse")
	if command["x"] != float64(12) || command["secret"] != nil {
		t.Fatalf("invalid input relay: %v", command)
	}
	_ = viewer.WriteJSON(map[string]any{"type": "navigate", "url": "file:///private"})
	_ = readPlaywrightType(t, viewer, "viewer_error")
	// Only viewport fields survive the relay; unrelated producer payload is dropped.
	// Screencast geometry (where the page sits inside the captured surface)
	// survives too, so viewers can map clicks through it (PLAT-776).
	_ = producer.WriteJSON(map[string]interface{}{"type": "frame", "data": "/9j/", "metadata": map[string]interface{}{"deviceWidth": 640, "deviceHeight": 480, "offsetTop": 120.5, "pageScaleFactor": 2, "scrollOffsetY": 33}, "secret": "must-not-forward"})
	frame := readPlaywrightType(t, viewer, "frame")
	if frame["data"] != "/9j/" || frame["secret"] != nil {
		t.Fatalf("invalid relay: %v", frame)
	}
	geometry, _ := frame["metadata"].(map[string]interface{})
	if geometry["offsetTop"] != 120.5 || geometry["pageScaleFactor"] != float64(2) || geometry["scrollOffsetY"] != float64(33) {
		t.Fatalf("screencast geometry dropped: %v", frame)
	}
	req, _ := http.NewRequest("POST", server.URL+"/api/browser/live/"+id+"/recording?workspace_path=Workflow/test", strings.NewReader(`{"action":"start"}`))
	req.Header.Set("X-Test-User", "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("recording spawned browser: %d", resp.StatusCode)
	}
	producer.Close()
	_ = viewer.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := viewer.ReadMessage(); err == nil {
		t.Fatal("viewer survived producer cleanup")
	}
	if got := api.playwrightSessions("alice", "Workflow/test"); len(got) != 1 || got[0]["state"] != "completed" {
		t.Fatalf("completed replay missing: %v", got)
	}
}

func TestPlaywrightLiveRejectsMalformedFrames(t *testing.T) {
	_, server := playwrightTestServer(t)
	for _, data := range []string{"not-base64", "aGVsbG8="} {
		c, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/s/run/tools/browser/live", http.Header{"Authorization": []string{"Bearer producer-secret"}})
		if err != nil {
			t.Fatal(err)
		}
		readPlaywrightType(t, c, "registered")
		payload, _ := json.Marshal(map[string]interface{}{"type": "frame", "data": data, "metadata": map[string]int{"deviceWidth": 640, "deviceHeight": 480}})
		_ = c.WriteMessage(websocket.TextMessage, payload)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err := c.ReadMessage(); err == nil {
			t.Fatal("malformed image accepted")
		}
		c.Close()
	}
}

// Opt-in integration: real user-style Playwright test, fixture, JPEG relay and
// video attachment. All services are in-process and use test-only credentials.
func TestPlaywrightFixtureLive(t *testing.T) {
	if os.Getenv("AGENTWORKS_PLAYWRIGHT_LIVE_TEST") != "1" {
		t.Skip("set AGENTWORKS_PLAYWRIGHT_LIVE_TEST=1 after installing the fixture dependencies and Chromium")
	}
	testPlaywrightFixtureLive(t, "node")
}

func TestPythonPlaywrightFixtureLive(t *testing.T) {
	if os.Getenv("AGENTWORKS_PLAYWRIGHT_LIVE_TEST") != "1" {
		t.Skip("opt-in real Chromium integration")
	}
	for _, runtime := range []string{"sync", "async", "pytest", "pytest-failure"} {
		t.Run(runtime, func(t *testing.T) { testPlaywrightFixtureLive(t, runtime) })
	}
}

func testPlaywrightFixtureLive(t *testing.T, runtime string) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	api, server := playwrightTestServer(t)
	pkg, err := filepath.Abs("../../../packages/playwright")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	output := t.TempDir()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(pkg, "node_modules/@playwright/test/cli.js"), "test", "--config", filepath.Join(pkg, "test/playwright.config.mjs"))
	if runtime != "node" {
		pkg, err = filepath.Abs("../../../packages/playwright-python")
		if err != nil {
			t.Fatal(err)
		}
		python := filepath.Join(pkg, ".venv/bin/python")
		if configured := os.Getenv("AGENTWORKS_TEST_PYTHON"); configured != "" {
			python = configured
		}
		if strings.HasPrefix(runtime, "pytest") {
			cmd = exec.CommandContext(ctx, python, "-m", "pytest", filepath.Join(pkg, "tests/live_pytest.py"), "--video=on", "--output="+output, "-q")
		} else {
			cmd = exec.CommandContext(ctx, python, filepath.Join(pkg, "tests/live_demo.py"), runtime)
		}
	}
	cmd.Dir = pkg
	cmd.Env = append(os.Environ(), "MCP_API_URL="+server.URL+"/s/run", "MCP_API_TOKEN=producer-secret", "AGENTWORKS_TEST_OUTPUT="+output)
	if runtime == "pytest-failure" {
		cmd.Env = append(cmd.Env, "AGENTWORKS_TEST_FAILURE=1")
	}
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait(); close(finished) }()
	defer func() { cancel(); <-finished }()
	var id string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		sessions := api.playwrightSessions("alice", "Workflow/test")
		if len(sessions) > 0 {
			id = sessions[0]["browser_session"]
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("fixture exited before registering: %v\n%s", err, log.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if id == "" {
		cancel()
		<-finished
		t.Fatalf("no fixture session: %s", log.String())
	}
	viewer, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/api/browser/live/"+id+"/stream?workspace_path=Workflow/test", http.Header{"X-Test-User": []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	// Observe interaction, a new tab, then the original tab after it closes.
	wantColors := []string{"blue", "green", "yellow", "green"}
	observed := 0
	var config image.Config
	for observed < len(wantColors) {
		var frame map[string]any
		_ = viewer.SetReadDeadline(time.Now().Add(12 * time.Second))
		if err := viewer.ReadJSON(&frame); err != nil {
			cancel()
			fixtureErr := <-finished
			t.Fatalf("viewer ended after %d/%d page states; wanted %s: %v; fixture: %v\n%s", observed, len(wantColors), wantColors[observed], err, fixtureErr, log.String())
		}
		if frame["type"] != "frame" {
			continue
		}
		jpegData, err := base64.StdEncoding.DecodeString(frame["data"].(string))
		if err != nil {
			t.Fatal(err)
		}
		config, err = jpeg.DecodeConfig(bytes.NewReader(jpegData))
		if err != nil || config.Width != 640 || config.Height != 480 {
			t.Fatalf("not a real viewport JPEG: %+v %v", config, err)
		}
		picture, err := jpeg.Decode(bytes.NewReader(jpegData))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := picture.At(10, 450).RGBA()
		color := ""
		switch {
		case r>>8 < 30 && g>>8 > 40 && b>>8 > 70:
			color = "blue"
		case r>>8 > 150 && g>>8 > 120 && b>>8 < 60:
			color = "yellow"
		case r>>8 > 30 && g>>8 > 70 && b>>8 < 65:
			color = "green"
		}
		t.Logf("viewer page state=%s wanted=%s", color, wantColors[observed])
		if color == wantColors[observed] {
			if observed == 0 && !strings.HasPrefix(runtime, "pytest") {
				for _, message := range []map[string]any{
					{"type": "input_mouse", "eventType": "mousePressed", "x": 30, "y": 90, "button": "left", "clickCount": 1},
					{"type": "input_mouse", "eventType": "mouseReleased", "x": 30, "y": 90, "button": "left", "clickCount": 1},
					{"type": "input_text", "text": "manual"},
					{"type": "input_keyboard", "eventType": "keyDown", "key": "x", "code": "KeyX", "text": "x", "windowsVirtualKeyCode": 88},
					{"type": "input_keyboard", "eventType": "keyUp", "key": "x", "code": "KeyX", "text": "", "windowsVirtualKeyCode": 88},
				} {
					if err := viewer.WriteJSON(message); err != nil {
						t.Fatal(err)
					}
				}
			}
			observed++
		}
	}
	if err := <-finished; (err != nil) != (runtime == "pytest-failure") {
		t.Fatalf("Playwright unexpected exit: %v\n%s", err, log.String())
	}
	t.Log(log.String())
	deadline = time.Now().Add(15 * time.Second)
	for {
		sessions := api.playwrightSessions("alice", "Workflow/test")
		if len(sessions) == 1 && sessions[0]["state"] == "completed" && sessions[0]["recording_state"] == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("finished test replay not ready: %v", sessions)
		}
		time.Sleep(20 * time.Millisecond)
	}
	api.playwrightLive.Lock()
	replay := api.playwrightLive.recordings[id]
	api.playwrightLive.Unlock()
	if info, err := os.Stat(filepath.Join(replay.dir, "replay.mp4")); err != nil || info.Size() == 0 {
		t.Fatalf("missing temporary replay: %v", err)
	}
	videos, _ := filepath.Glob(filepath.Join(output, "*", "*.webm"))
	if len(videos) == 0 {
		t.Fatalf("Playwright did not save the test recording under %s", output)
	}
	info, err := os.Stat(videos[0])
	if err != nil || info.Size() == 0 {
		t.Fatal("empty recording")
	}
	t.Logf("Observed Chromium %dx%d JPEG through viewer and saved %d-byte recording", config.Width, config.Height, info.Size())
}

func TestPlaywrightPackagesUseAuthenticatedSessionAndFixedFiles(t *testing.T) {
	_, server := playwrightTestServer(t)
	directory := t.TempDir()
	t.Setenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR", directory)
	if err := os.WriteFile(filepath.Join(directory, "agentworks-playwright-python.zip"), []byte("fixture archive"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		run, name, token string
		status           int
	}{
		{"run", "python", "producer-secret", 200},
		{"run", "python", "", 401},
		{"missing", "python", "producer-secret", 404},
		{"run", "unknown", "producer-secret", 404},
		{"run", "node", "producer-secret", 404},
	} {
		req, _ := http.NewRequest("GET", server.URL+"/s/"+tc.run+"/tools/browser/packages/"+tc.name, nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%+v: got %d", tc, resp.StatusCode)
		}
	}
}

func TestPlaywrightSavedStepSessionResolvesActiveParent(t *testing.T) {
	api, server := playwrightTestServer(t)
	dir := t.TempDir()
	t.Setenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "agentworks-playwright-python.zip"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, parent, user, workspace string
		allowed                       bool
	}{
		{"registered-child", "run", "", "Workflow/test", true},
		{"matching-user", "run", "alice", "Workflow/test/", true},
		{"wrong-user", "run", "bob", "Workflow/test", false},
		{"wrong-workflow", "run", "", "Workflow/other", false},
		{"missing-workflow", "run", "", "", false},
		{"ended-parent", "missing", "alice", "Workflow/test", false},
		{"unregistered", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := "session-group-" + tc.name
			virtualtools.RegisterParentChat(child, &virtualtools.ParentChatContext{SessionID: tc.parent, UserID: tc.user, WorkflowPath: tc.workspace})
			defer virtualtools.UnregisterParentChat(child)
			req, _ := http.NewRequest("GET", server.URL+"/s/"+child+"/tools/browser/packages/python", nil)
			req.Header.Set("Authorization", "Bearer producer-secret")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			want := http.StatusNotFound
			if tc.allowed {
				want = http.StatusOK
			}
			if resp.StatusCode != want {
				t.Fatalf("package status=%d want=%d", resp.StatusCode, want)
			}
			conn, resp, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/s/"+child+"/tools/browser/live", http.Header{"Authorization": []string{"Bearer producer-secret"}})
			if resp != nil {
				resp.Body.Close()
			}
			if !tc.allowed {
				if conn != nil {
					conn.Close()
				}
				if err == nil {
					t.Fatal("unauthorized child registered")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			id := readPlaywrightType(t, conn, "registered")["browser_session"]
			found := false
			for _, item := range api.playwrightSessions("alice", "Workflow/test") {
				if item["browser_session"] == id && item["workflow_session"] == child {
					found = true
				}
			}
			if !found || len(api.playwrightSessions("bob", "Workflow/test")) != 0 {
				t.Fatal("child browser lost owner/workflow isolation")
			}
		})
	}
}

func TestPlaywrightScheduledGroupUsesActiveRunRegistration(t *testing.T) {
	api, server := playwrightTestServer(t)
	registry := mcpclient.GetSessionRegistry()
	for _, tc := range []struct {
		name, parent string
		allowed      bool
	}{
		{"scheduled", "run", true}, {"webhook", "run", true}, {"ended", "missing", false}, {"unregistered", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := "scheduled-live-test-" + tc.name
			registry.RegisterHTTPSession(tc.parent, child)
			defer registry.CloseHTTPSession(tc.parent)
			conn, resp, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1)+"/s/"+child+"/tools/browser/live", http.Header{"Authorization": []string{"Bearer producer-secret"}})
			if !tc.allowed {
				if conn != nil {
					conn.Close()
					t.Fatal("unauthorized group registered")
				}
				if err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
					t.Fatalf("expected 404, got %v / %v", resp, err)
				}
				resp.Body.Close()
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			readPlaywrightType(t, conn, "registered")
			if len(api.playwrightSessions("alice", "Workflow/test")) == 0 || len(api.playwrightSessions("bob", "Workflow/test")) != 0 {
				t.Fatal("incorrect owner visibility")
			}
			api.activeSessionsMux.Lock()
			saved := api.activeSessions["run"]
			delete(api.activeSessions, "run")
			api.activeSessionsMux.Unlock()
			owner, _ := api.playwrightWorkflowOwner(child)
			if owner != "" {
				t.Fatal("ended run still authorizes registration")
			}
			api.activeSessionsMux.Lock()
			api.activeSessions["run"] = saved
			api.activeSessionsMux.Unlock()
		})
	}
}
