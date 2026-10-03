package handlers

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/browserteach"
	"github.com/spf13/viper"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTeachingTabTraceRejectsUnboundOrClosedPages(t *testing.T) {
	traces := [][]browserteach.Action{
		{{Kind: "navigate", Page: "root"}, {Kind: "click", Page: "unknown"}},
		{{Kind: "navigate", Page: "root"}, {Kind: "tab_open", Page: "child", Opener: "unknown", Observed: true}},
		{{Kind: "navigate", Page: "root"}, {Kind: "tab_close", Page: "root"}, {Kind: "click", Page: "root"}},
		{{Kind: "navigate", Page: "root"}, {Kind: "tab_open", Page: "root"}},
	}
	for _, trace := range traces {
		if validateTeachingTabs(trace) == nil {
			t.Fatalf("Invalid trace accepted: %+v", trace)
		}
	}
	if err := validateTeachingTabs([]browserteach.Action{{Kind: "navigate", Page: "root"}, {Kind: "tab_open", Page: "child", Opener: "root", Observed: true}, {Kind: "tab_switch", Page: "root"}, {Kind: "tab_close", Page: "child"}}); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"file:///etc/passwd", "https://user:secret@example.com", "javascript:alert(1)", "http:"} {
		if validTeachingAddress(address) {
			t.Fatal("Unsafe navigation", address)
		}
	}
}
func TestTeachingRealMultiTabReplay(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TEACH_E2E") != "1" {
		t.Skip("set RUN_BROWSER_TEACH_E2E=1 for owned Chrome")
	}
	root := t.TempDir()
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", "")
	if err := os.MkdirAll(filepath.Join(root, "Workflow", "tabs"), 0700); err != nil {
		t.Fatal(err)
	}
	socket, err := os.MkdirTemp("/tmp", "awteach-tabs-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socket)
	t.Setenv("AGENT_BROWSER_SOCKET_DIR", socket)
	const session = "teach-tabs-e2e"
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	launchArgs := []string{"--profile", filepath.Join(root, "profile")}
	if args := os.Getenv("AGENT_BROWSER_ARGS"); args != "" {
		launchArgs = append(launchArgs, "--args", args)
	}
	run := func(args ...string) []byte {
		t.Helper()
		out, err := runTeachCommand(ctx, socket, session, append(args, launchArgs...)...)
		if err != nil {
			page, _ := runTeachCommand(ctx, socket, session, append([]string{"eval", "JSON.stringify({url:location.href,body:document.body?.innerText})"}, launchArgs...)...)
			t.Fatalf("%v: %v; page=%s", args, err, page)
		}
		return out
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<label>Customer <input id="customer" name="customer"></label><button id="submit" onclick="document.getElementById('result').textContent='Hello '+document.getElementById('customer').value">Submit</button><button id="popup" onclick="window.open('/popup','_blank')">Open popup</button><p id="result"></p>`))
	}))
	defer fixture.Close()
	run("open", fixture.URL)
	defer run("close")
	run("wait", "#customer")
	var tabs struct {
		Data struct {
			Tabs []teachTab `json:"tabs"`
		} `json:"data"`
	}
	json.Unmarshal(run("tab"), &tabs)
	rootRef := tabs.Data.Tabs[0].Ref
	call := func(payload map[string]any) teachState {
		t.Helper()
		payload["workspace_path"] = "Workflow/tabs"
		payload["launch_args"] = launchArgs
		data, _ := json.Marshal(payload)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Params = gin.Params{{Key: "session", Value: session}}
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(string(data)))
		c.Request.Header.Set("Content-Type", "application/json")
		BrowserTeaching(c)
		if rec.Code != 200 {
			t.Fatalf("%v: HTTP %d %s", payload["action"], rec.Code, rec.Body.String())
		}
		var state teachState
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	call(map[string]any{"action": "start", "goal": "Greet customers across tabs"})
	run("click", "#customer")
	run("keyboard", "type", "Alice")
	run("click", "#submit")
	call(map[string]any{"action": "flush"})
	run("tab", "new", "about:blank")
	call(map[string]any{"action": "select_tab"})
	call(map[string]any{"action": "prepare_navigation"})
	run("open", fixture.URL+"/second")
	run("wait", "#customer")
	run("click", "#customer")
	run("keyboard", "type", "Second")
	run("click", "#submit")
	call(map[string]any{"action": "flush"})
	run("tab", rootRef)
	call(map[string]any{"action": "select_tab"})
	run("click", "#popup")
	time.Sleep(300 * time.Millisecond)
	json.Unmarshal(run("tab"), &tabs)
	popupRef := ""
	for _, tab := range tabs.Data.Tabs {
		if tab.Active {
			popupRef = tab.Ref
		}
	}
	if popupRef == rootRef {
		t.Fatal("Popup did not become active")
	}
	call(map[string]any{"action": "select_tab"})
	run("wait", "#customer")
	run("click", "#customer")
	run("keyboard", "type", "Popup")
	run("click", "#submit")
	call(map[string]any{"action": "flush"})
	call(map[string]any{"action": "prepare_close"})
	run("tab", "close", popupRef)
	run("tab", rootRef)
	call(map[string]any{"action": "select_tab"})
	run("click", "#submit")
	state := call(map[string]any{"action": "finish"})
	opens, switches, closes := 0, 0, 0
	manualNavigation := false
	values := map[string]string{}
	for i, a := range state.Actions {
		if a.Kind == "navigate" && a.URL == fixture.URL+"/second" && !a.Observed {
			manualNavigation = true
		}
		switch a.Kind {
		case "tab_open":
			opens++
		case "tab_switch":
			switches++
		case "tab_close":
			closes++
		}
		if a.Kind == "fill" {
			key := "customer_" + a.Page
			state.Actions[i].Parameter = key
			values[key] = "Bob"
		}
	}
	if !manualNavigation {
		t.Fatal("Address-bar navigation was not recorded as an explicit open")
	}
	if opens != 2 || switches < 3 || closes != 1 {
		t.Fatalf("Incomplete tab capture: %+v", state.Actions)
	}
	call(map[string]any{"action": "save", "id": state.ID, "actions": state.Actions, "check": teachCheck{Kind: "text", Value: "Hello Bob"}})
	for attempt := 0; attempt < 2; attempt++ {
		replay := call(map[string]any{"action": "test", "id": state.ID, "inputs": values})
		if replay.Status != "tested" {
			t.Fatalf("Replay %d failed: errors=%v actions=%+v", attempt, replay.Errors, state.Actions)
		}
	}
	saved := call(map[string]any{"action": "publish", "id": state.ID})
	if saved.Status != "saved" {
		t.Fatal("Multitab procedure could not be saved")
	}
	call(map[string]any{"action": "start", "goal": "Pause across tabs"})
	call(map[string]any{"action": "pause"})
	run("tab", "new", fixture.URL+"/private-paused-tab")
	call(map[string]any{"action": "select_tab"})
	run("wait", "#customer")
	run("click", "#customer")
	run("keyboard", "type", "paused-new-tab-secret")
	run("click", "#submit")
	paused := call(map[string]any{"action": "finish"})
	raw, _ := json.Marshal(paused)
	if strings.Contains(string(raw), "paused-new-tab-secret") || strings.Contains(string(raw), "private-paused-tab") {
		t.Fatal("Paused new tab leaked into trace")
	}
}
