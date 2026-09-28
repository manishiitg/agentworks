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
		case r.Method == http.MethodDelete:
			deletedPaths = append(deletedPaths, r.URL.String())
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
	if len(deletedPaths) != 1 || !strings.Contains(deletedPaths[0], "app-c0de0001") {
		t.Fatalf("deletes = %v, want only inside the project", deletedPaths)
	}
}
