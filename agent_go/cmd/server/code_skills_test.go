package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// A Code's skill tools write only its own skills/ folder.
func TestCodeProjectSkillCallbacksStayInTheProject(t *testing.T) {
	var mu sync.Mutex
	var installTargets, deletedPaths []string
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/skills/cli/install"):
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			installTargets = append(installTargets, req["target_dir"])
			_, _ = w.Write([]byte(`{"installed_skills":["demo"]}`))
		case strings.HasSuffix(r.URL.Path, "/api/skills/workspace/delete"):
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			deletedPaths = append(deletedPaths, req["workspace_path"]+"/skills|"+req["name"])
			_, _ = w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodDelete:
			// The generic file API follows links; a Code must never use it.
			deletedPaths = append(deletedPaths, "GENERIC:"+r.URL.String())
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		}
	}))
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	dir := "_users/owner/Chats/Code/projects/app-c0de0001/skills"
	scoped := projectSkillCallbacks(&todo_creation_human.SkillCallbacks{}, dir, "code")
	if out, err := scoped.InstallSkill(context.Background(), "owner/repo@demo"); err != nil || !strings.Contains(out, "private skills/") {
		t.Fatalf("install = %q %v", out, err)
	}
	if err := scoped.DeleteSkill(context.Background(), "demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, bad := range []string{"../other", "a/b", ""} {
		if err := scoped.DeleteSkill(context.Background(), bad); err == nil {
			t.Fatalf("deleted outside the project with %q", bad)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(installTargets) != 1 || installTargets[0] != dir {
		t.Fatalf("install targets = %v, want the project's skills folder", installTargets)
	}
	if len(deletedPaths) != 1 || deletedPaths[0] != dir+"|demo" {
		t.Fatalf("deletes = %v, want one link-safe project delete", deletedPaths)
	}
}

// Only the agent server may point a skill install at a project's private
// skills folder: a browser request carrying target_dir is refused whatever
// its Content-Type (the workspace binds the body as JSON regardless), and
// whoever's project it names -- including the caller's own.
func TestWorkspaceProxyRefusesSkillInstallTargetDir(t *testing.T) {
	verdict := func(contentType, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/wp/api/skills/cli/install", strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		status, _, cleanup := workspaceProxyCrossUserBlock(req, "bob")
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
		return status
	}
	for _, target := range []string{"_users/alice/Chats/Code/projects/app-1/skills", "_users/alice/Chats/Work/projects/c-1/skills", "_users/bob/Chats/Code/projects/mine/skills"} {
		body := `{"source":"attacker/repo@x","target_dir":"` + target + `"}`
		for _, contentType := range []string{"application/json", "text/plain", ""} {
			if status := verdict(contentType, body); status != http.StatusForbidden {
				t.Fatalf("target_dir %s with Content-Type %q = %d, want 403", target, contentType, status)
			}
		}
	}
	if status := verdict("application/json", `{"source":"owner/repo@skill"}`); status != http.StatusForbidden {
		t.Fatalf("unscoped installs must be refused: %d", status)
	}
}

// The workspace binds JSON whatever Content-Type is claimed, so the proxy
// vets any JSON-looking body the same way, and refuses one that looks like
// JSON but does not parse.
func TestWorkspaceProxyVetsJSONBodiesWhateverTheirType(t *testing.T) {
	verdict := func(contentType, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/wp/api/folders", strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		status, _, cleanup := workspaceProxyCrossUserBlock(req, "bob")
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
		return status
	}
	crossUser := `{"folder_path":"_users/alice/Chats/Code/projects/app-1/code/x"}`
	for _, contentType := range []string{"application/json", "text/plain", "application/octet-stream", ""} {
		if status := verdict(contentType, crossUser); status != http.StatusForbidden {
			t.Fatalf("cross-user path as %q = %d, want 403", contentType, status)
		}
		if status := verdict(contentType, strings.Repeat(" ", 2048)+crossUser); status != http.StatusForbidden {
			t.Fatalf("padded cross-user path as %q = %d, want 403", contentType, status)
		}
	}
	// A JSON value labelled multipart is a preamble to the multipart walk,
	// but a JSON endpoint still binds it.
	multipartSmuggle := crossUser + "\r\n--x--\r\n"
	if status := verdict("multipart/form-data; boundary=x", multipartSmuggle); status != http.StatusForbidden {
		t.Fatalf("cross-user path in a multipart preamble = %d, want 403", status)
	}
	installSmuggle := `{"target_dir":"_users/alice/Chats/Code/projects/p/skills","source":"x/y@z"}` + "\r\n--x--\r\n"
	req := httptest.NewRequest(http.MethodPost, "/api/wp/api/skills/cli/install", strings.NewReader(installSmuggle))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if status, _, cleanup := workspaceProxyCrossUserBlock(req, "bob"); status != http.StatusForbidden {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("target_dir in a multipart preamble = %d, want 403", status)
	}
	if status := verdict("text/plain", `{"folder_path": "_users/alice/x"`); status != http.StatusBadRequest {
		t.Fatalf("truncated JSON-looking body = %d, want 400", status)
	}
	if status := verdict("text/plain", `{"folder_path":"_users/bob/Chats/Code/projects/mine/code"}`); status != 0 {
		t.Fatalf("own path refused: %d", status)
	}
}

// Code sharing and the admin audit log are written only by the server:
// through the proxy not even an admin may write them, and the audit append
// route is unreachable from a browser.
func TestWorkspaceProxyProtectsServerOwnedFilesFromAdmins(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"boss","username":"boss","admin":true}]}`)
	call := func(method, target, body string) int {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "boss", Username: "boss"}))
		req.Header.Set("Content-Type", "application/json")
		status, _, cleanup := workspaceProxyCrossUserBlock(req, "boss")
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
		return status
	}
	for _, write := range []struct{ method, target, body string }{
		{http.MethodPut, "/api/wp/api/documents/config/code-shares.json", `{"content":"{}"}`},
		{http.MethodDelete, "/api/wp/api/documents/config/code-shares.json", ``},
		{http.MethodPut, "/api/wp/api/documents/config/code-admin-audit/2026-09.jsonl", `{"content":""}`},
		{http.MethodDelete, "/api/wp/api/folders/config", ``},
		{http.MethodPost, "/api/wp/api/audit/code-admin/append", `{"month":"2026-09","entry":"{}"}`},
	} {
		if status := call(write.method, write.target, write.body); status != http.StatusForbidden {
			t.Fatalf("admin %s %s = %d, want 403", write.method, write.target, status)
		}
	}
	// Reading them, and writing the rest of config/, stays admin-allowed.
	if status := call(http.MethodGet, "/api/wp/api/documents/config/code-shares.json", ""); status != 0 {
		t.Fatalf("admin read of shares refused: %d", status)
	}
	if status := call(http.MethodPut, "/api/wp/api/documents/config/scheduler.json", `{"content":"{}"}`); status != 0 {
		t.Fatalf("admin write of other config refused: %d", status)
	}
}
