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
		case strings.HasSuffix(r.URL.Path, "/api/skills/project/delete"):
			var req map[string]string
			_ = json.Unmarshal(body, &req)
			deletedPaths = append(deletedPaths, req["target_dir"]+"|"+req["name"])
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
	if status := verdict("application/json", `{"source":"owner/repo@skill"}`); status != 0 {
		t.Fatalf("a shared-library install was refused: %d", status)
	}
}
