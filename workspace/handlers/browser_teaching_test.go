package handlers

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/browserteach"
	"github.com/spf13/viper"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTeachingRejectsEscapingArtifacts(t *testing.T) {
	root := t.TempDir()
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", "")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := teachBase("escape"); err == nil {
		t.Fatal("outside workspace was accepted")
	}
	os.Mkdir(filepath.Join(root, "project"), 0700)
	os.Symlink(outside, filepath.Join(root, "project", "browser-demonstrations"))
	if _, _, err := teachBase("project"); err == nil {
		t.Fatal("symlinked evidence directory accepted")
	}
	if _, err := loadTeach(root, "../outside"); err == nil {
		t.Fatal("invalid demonstration ID accepted")
	}
}
func TestTeachingRealBrowserCaptureAndReplay(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TEACH_E2E") != "1" {
		t.Skip("set RUN_BROWSER_TEACH_E2E=1 for owned real Chrome test")
	}
	if _, err := exec.LookPath("agent-browser"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", "")
	os.Mkdir(filepath.Join(root, "Workflow"), 0700)
	os.Mkdir(filepath.Join(root, "Workflow", "teach"), 0700)
	socket, err := os.MkdirTemp("/tmp", "awteach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socket)
	t.Setenv("AGENT_BROWSER_SOCKET_DIR", socket)
	const session = "teach-e2e"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run := func(args ...string) []byte {
		t.Helper()
		out, err := runTeachCommand(ctx, socket, session, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	var ambiguous atomic.Bool
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if ambiguous.Load() {
			w.Write([]byte(`<button>Submit</button>`))
		}
		w.Write([]byte(`<label>Customer <input id="customer" name="customer"></label><label>Password <input id="password" type="password"></label><button onclick="document.getElementById('result').textContent='Hello '+document.getElementById('customer').value">Submit</button><p id="result"></p>`))
	}))
	defer fixture.Close()
	run("--profile", filepath.Join(root, "profile"), "open", fixture.URL)
	defer run("close")
	// Native agent-browser already enables its stream on startup.
	run("eval", "localStorage.setItem('signed_in','yes')")
	call := func(payload map[string]any) teachState {
		t.Helper()
		payload["workspace_path"] = "Workflow/teach"
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
		if json.Unmarshal(rec.Body.Bytes(), &state) != nil {
			t.Fatal(rec.Body.String())
		}
		return state
	}
	call(map[string]any{"action": "start", "goal": "Greet a customer"})
	run("click", "#customer")
	run("keyboard", "type", "Alice")
	run("press", "Tab")
	run("keyboard", "type", "do-not-record-this-secret")
	run("click", "button")
	call(map[string]any{"action": "pause"})
	run("click", "#customer")
	run("keyboard", "type", "private-paused-input")
	call(map[string]any{"action": "resume"})
	run("click", "button")
	state := call(map[string]any{"action": "finish"})
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), "do-not-record") || strings.Contains(string(raw), "private-paused") {
		t.Fatal("sensitive/paused input leaked into trace")
	}
	found := false
	for i, a := range state.Actions {
		if a.Kind == "fill" && strings.Contains(a.Target.Selector, "customer") {
			state.Actions[i].Parameter = "customer"
			found = true
		}
	}
	if !found {
		t.Fatalf("No actual field edit in %s", raw)
	}
	call(map[string]any{"action": "save", "id": state.ID, "actions": state.Actions, "check": teachCheck{Kind: "text", Value: "Hello Bob"}})
	state = call(map[string]any{"action": "test", "id": state.ID, "inputs": map[string]string{"customer": "Bob"}})
	if state.Status != "tested" {
		t.Fatalf("Replay failed: %+v", state)
	}
	out := run("eval", "localStorage.getItem('signed_in')")
	if !strings.Contains(string(out), "yes") {
		t.Fatal("Replay lost sign-in state")
	}
	ambiguous.Store(true)
	blocked := call(map[string]any{"action": "test", "id": state.ID, "inputs": map[string]string{"customer": "Bob"}})
	if blocked.Status != "needs_repair" || len(blocked.Errors) == 0 || !strings.Contains(blocked.Errors[0], "ambiguous") {
		t.Fatal("Ambiguous target did not stop replay", blocked)
	}
	ambiguous.Store(false)
	call(map[string]any{"action": "save", "id": state.ID, "actions": state.Actions, "guidance": "Greet the requested customer and confirm the greeting.", "check": teachCheck{Kind: "text", Value: "Hello Dana"}})
	state = call(map[string]any{"action": "test", "id": state.ID, "inputs": map[string]string{"customer": "Dana"}})
	if state.Status != "tested" {
		t.Fatal("Second parameterized replay failed", state)
	}
	state = call(map[string]any{"action": "publish", "id": state.ID})
	if state.Status != "saved" || state.Skill == "" {
		t.Fatal("No reusable procedure saved")
	}
	if index, err := os.ReadFile(filepath.Join(root, "Workflow", "teach", "learnings", "_global", "SKILL.md")); err != nil || !strings.Contains(string(index), state.ID) {
		t.Fatal("Saved procedure is not indexed for future runs", err)
	}
	if _, err := os.Stat(filepath.Join(root, state.Skill)); err != nil {
		t.Fatal(err)
	}
	run("close")
	run("--profile", filepath.Join(root, "profile"), "open", fixture.URL)
	if out := run("eval", "localStorage.getItem('signed_in')"); !strings.Contains(string(out), "yes") {
		t.Fatal("Profile lost sign-in across browser restart")
	}
	// A second session uses the same installed listeners after stop, without
	// inheriting the old demonstration's actions or losing login state.
	call(map[string]any{"action": "start", "goal": "Another greeting"})
	run("click", "#customer")
	run("keyboard", "type", "Carol")
	run("press", "Tab")
	again := call(map[string]any{"action": "finish"})
	if len(again.Actions) < 2 {
		t.Fatal("Second demonstration failed to capture")
	}
	call(map[string]any{"action": "start", "goal": "Capture visible result evidence"})
	run("open", fixture.URL+"/visual")
	run("eval", "document.getElementById('password').remove()")
	run("click", "button")
	var visual teachState
	for i := 0; i < 20; i++ {
		visual = call(map[string]any{"action": "status"})
		found := false
		for _, a := range visual.Actions {
			if a.Screenshot != "" {
				f, err := os.Open(filepath.Join(root, visual.Directory, a.Screenshot))
				if err != nil {
					t.Fatal(err)
				}
				_, err = jpeg.DecodeConfig(f)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	foundShot := false
	for _, a := range visual.Actions {
		if a.Screenshot != "" {
			foundShot = true
		}
	}
	if !foundShot {
		t.Fatalf("No actual visual evidence captured: %+v; script=%s", visual, run("eval", "JSON.stringify({installed:window.__awTeachInstalled,safe:window.__awTeachVisualSafe?.()})"))
	}
	call(map[string]any{"action": "finish"})
	call(map[string]any{"action": "start", "goal": "Paused navigation"})
	call(map[string]any{"action": "pause"})
	run("open", fixture.URL+"/other")
	run("click", "#customer")
	run("keyboard", "type", "paused-after-navigation")
	call(map[string]any{"action": "resume"})
	run("click", "button")
	afterNav := call(map[string]any{"action": "finish"})
	raw, _ = json.Marshal(afterNav)
	if strings.Contains(string(raw), "paused-after-navigation") {
		t.Fatal("Paused navigation leaked input")
	}

}
func TestTeachingSkillUsesVariables(t *testing.T) {
	skill := browserteach.Skill("Export", []browserteach.Action{{ID: 1, Kind: "fill", Parameter: "customer", Value: "private-example"}})
	if !strings.Contains(skill, "customer") || strings.Contains(skill, "private-example") {
		t.Fatal(skill)
	}
}

func TestTeachingPublishRequiresActualTestReceipt(t *testing.T) {
	root := t.TempDir()
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", "")
	os.Mkdir(filepath.Join(root, "project"), 0700)
	_, base, err := teachBase("project")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "forged")
	os.Mkdir(dir, 0700)
	state := &teachState{ID: "forged", Workspace: "project", Status: "tested", Goal: "Fake", path: dir, Actions: []browserteach.Action{{ID: 1, Kind: "navigate", URL: "https://example.com"}}, Check: teachCheck{Kind: "text", Value: "Done"}}
	if err := persistTeach(state); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "session", Value: "receipt-test"}}
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"publish","workspace_path":"project","id":"forged"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	BrowserTeaching(c)
	if rec.Code != 409 {
		t.Fatalf("Forged tested state published: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTeachingArtifactsAllowOwningAccountSlotReview(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", "")
	os.Mkdir(filepath.Join(root, "project"), 0700)
	_, base, err := teachBase("project")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "review")
	if err = makeTeachDirectory(dir); err != nil {
		t.Fatal(err)
	}
	state := &teachState{ID: "review", Workspace: "project", path: dir, Status: "draft"}
	if err = persistTeach(state); err != nil {
		t.Fatal(err)
	}
	if err = writeTeachText(filepath.Join(dir, "draft.md"), "Review this"); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(root, "project", "skills", "browser-review")
	if err = makeTeachDirectory(skill); err != nil {
		t.Fatal(err)
	}
	if err = writeTeachText(filepath.Join(skill, "SKILL.md"), "Tested procedure"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, dir, filepath.Dir(skill), skill} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0070 != 0070 || info.Mode()&os.ModeSetgid == 0 {
			t.Fatalf("slot cannot access teaching directory %s: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(dir, "manifest.json"), filepath.Join(dir, "actions.jsonl"), filepath.Join(dir, "draft.md"), filepath.Join(skill, "SKILL.md")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0660 {
			t.Fatalf("slot cannot review teaching file %s: %v", path, err)
		}
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Symlink(outside, filepath.Join(root, "project", "redirect"))
	if err := makeTeachDirectory(filepath.Join(root, "project", "redirect", "child")); err == nil {
		t.Fatal("symlinked learning directory accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "child")); !os.IsNotExist(err) {
		t.Fatal("outside directory created")
	}
}
