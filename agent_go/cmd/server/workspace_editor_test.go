package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-1002: editor opening cannot turn a local project/file grant into
// another person's paths, an escaped symlink, a command URL or a remote path.
func TestWorkspaceEditorAdmitsOnlyLocalOwnedProjectTargets(t *testing.T) {
	_, repo := gitTestEnv(t)
	workspace, _ := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	old := workspaceEditorLocalAvailable
	workspaceEditorLocalAvailable = func() bool { return true }
	t.Cleanup(func() { workspaceEditorLocalAvailable = old })
	call := func(user string, body map[string]any, limited bool) (int, map[string]any) {
		t.Helper()
		req := sharedSecretsRequest(http.MethodPost, "/api/workspace-editor", user, body)
		if limited {
			req = req.WithContext(context.WithValue(req.Context(), externalFileGuardKey{}, &wf.FolderGuard{}))
		}
		w := httptest.NewRecorder()
		(&StreamingAPI{}).handleWorkspaceEditor(w, req)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return w.Code, out
	}
	body := map[string]any{"workspace_path": gitTestProject, "op": "resolve", "editor": "vscode", "repo": "app", "file": "a.txt", "line": 9, "remember": true}
	code, out := call("alice", body, false)
	if code != 200 {
		t.Fatalf("allowed file: %d %v", code, out)
	}
	launch := out["launch"].(map[string]any)
	uri, err := url.Parse(launch["url"].(string))
	if err != nil || uri.Scheme != "vscode" || uri.Host != "file" || uri.Path != filepath.ToSlash(filepath.Join(repo, "a.txt"))+":9" {
		t.Fatalf("wrong target: %v", out)
	}
	// Omission resolves the caller's saved preference; no app/process is started.
	code, out = call("alice", map[string]any{"workspace_path": gitTestProject, "op": "resolve"}, false)
	if code != 200 || out["editor"] != "vscode" {
		t.Fatalf("saved choice: %d %v", code, out)
	}
	code, out = call("alice", map[string]any{"workspace_path": gitTestProject, "op": "resolve", "editor": "jetbrains", "repo": "app", "file": "a.txt", "line": 7}, false)
	if code != 200 {
		t.Fatalf("IDEA target: %d %v", code, out)
	}
	idea := out["launch"].(map[string]any)["jetbrains"].(map[string]any)
	if idea["path"] != filepath.Join(repo, "a.txt") || idea["line"] != float64(7) {
		t.Fatalf("IDEA target: %v", idea)
	}
	if code, _ = call("bob", body, false); code != 403 {
		t.Fatalf("foreign project: %d", code)
	}
	if code, _ = call("alice", body, true); code != 403 {
		t.Fatalf("limited token: %d", code)
	}
	for _, file := range []string{"../escape", "/etc/passwd", ".git/config", "schedule-runs.json", "a\n.txt"} {
		body["file"] = file
		if code, _ = call("alice", body, false); code < 400 {
			t.Fatalf("accepted %q", file)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(repo, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	body["file"] = "escape.txt"
	if code, _ = call("alice", body, false); code != 404 {
		t.Fatalf("escaped symlink: %d", code)
	}
	body["file"] = "a.txt"
	body["editor"] = "command:install"
	if code, _ = call("alice", body, false); code != 400 {
		t.Fatalf("command editor: %d", code)
	}
	body["editor"] = "vscode"
	workspaceEditorLocalAvailable = func() bool { return false }
	if code, out = call("alice", body, false); code != 409 || out["launch"] != nil {
		t.Fatalf("remote path exposed: %d %v", code, out)
	}
}

func TestWorkspaceEditorURLEncodesFileCharacters(t *testing.T) {
	for _, editor := range []string{"vscode", "cursor", "zed"} {
		raw := workspaceEditorURL(editor, "/Users/example/project/a #?.ts", 23)
		u, err := url.Parse(raw)
		if err != nil || u.Path != "/Users/example/project/a #?.ts:23" || u.RawQuery != "" || u.Fragment != "" || !strings.Contains(raw, "%23%3F") {
			t.Fatalf("ambiguous editor URL: %s", raw)
		}
	}
}
