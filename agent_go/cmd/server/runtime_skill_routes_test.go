package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestRuntimeSkillsUseCurrentBuilderFilesAndKeepAccessBoundaries(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	docs, state := t.TempDir(), t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	workspace := &mockWorkspaceAPI{files: map[string]string{"Workflow/demo/workflow.json": `{"id":"wf","created_by":"alice","access":{"owners":["alice"],"readers":["bob"]}}`}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	api := lifecycleTestAPI()
	project := filepath.Join(docs, "Workflow/demo")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(file, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []string{"old", "current"} {
		dir, err := cliruntime.PrepareLinkedProject(state, docs, "alice", project, session, "codex-cli", "workshop")
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(dir, ".agents/skills", session, "SKILL.md"), "---\nname: "+session+"\ndescription: Actual file\n---\n"+session+" body")
		runtime := ChatHistoryAgentRuntime{Provider: "codex-cli", WorkshopMode: "workshop", WorkspacePath: "Workflow/demo", AgentSessionHandle: &mcpagent.AgentSessionHandle{Provider: llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: dir, NativeSessionID: session}}}
		body, _ := json.Marshal(map[string]any{"session_id": session, "user_id": "alice", "runtime": runtime})
		write(filepath.Join(project, "builder/conversation/users/alice/2026-10-21/session-"+session+"-conversation.json"), string(body))
	}
	call := func(user, query string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/skills/runtime?workspace_path=Workflow/demo"+query, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		workspaceSkillRoute(false, api.runtimeSkillsHandler)(w, r)
		return w
	}
	// Even though an old runtime exists, absence of a current pointer means no inventory.
	if w := call("alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	} else {
		var got runtimeSkillsResponse
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if got.Prepared || got.SessionID != "" || len(got.Skills) != 0 {
			t.Fatal("old runtime substituted", w.Body.String())
		}
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice"})
	if _, err := api.createGoalOwnerTab(ctx, workflowBuilderScope{PresetID: "wf", Workspace: "Workflow/demo"}, "Builder", true, "current"); err != nil {
		t.Fatal(err)
	}
	w := call("alice", "")
	var got runtimeSkillsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !got.Prepared || got.SessionID != "current" || len(got.Skills) != 1 || got.Skills[0].FolderName != "current" {
		t.Fatalf("current inventory: %d %s", w.Code, w.Body.String())
	}
	// Relay uses the same current Builder identity and physical inventory.
	workspace.files["Workflow/demo/workflow.json"] = `{"id":"wf","kind":"relay","created_by":"alice","access":{"owners":["alice"],"readers":["bob"]}}`
	w = call("alice", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !got.Prepared || got.SessionID != "current" {
		t.Fatalf("Relay current inventory: %d %s", w.Code, w.Body.String())
	}
	write(filepath.Join(got.Directory, ".agents/skills/current/SKILL.md"), "---\nname: changed-on-disk\n---\nNew file content")
	w = call("alice", "")
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Skills[0].Frontmatter.Name != "changed-on-disk" {
		t.Fatal("returned snapshot instead of current files", w.Body.String())
	}
	if w := call("alice", "&file=.agents/skills/current/SKILL.md"); w.Code != 200 || !strings.Contains(w.Body.String(), "New file content") {
		t.Fatal("runtime reader did not read the live file", w.Code, w.Body.String())
	}
	if w := call("alice", "&session_id=old"); w.Code != 409 {
		t.Fatal("old session admitted", w.Code, w.Body.String())
	}
	if w := call("mallory", ""); w.Code != 403 {
		t.Fatal("unshared workspace admitted", w.Code, w.Body.String())
	}
	if w := call("bob", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	} else {
		var own runtimeSkillsResponse
		_ = json.Unmarshal(w.Body.Bytes(), &own)
		if own.Prepared || own.SessionID != "" {
			t.Fatal("reader received author's runtime")
		}
	}
	if w := call("alice", "&file=../../workflow.json"); w.Code != 400 {
		t.Fatal("non-skill file admitted", w.Code, w.Body.String())
	}
	if _, err := api.createGoalOwnerTab(ctx, workflowBuilderScope{PresetID: "wf", Workspace: "Workflow/demo"}, "Fresh", false, "new-unprepared"); err != nil {
		t.Fatal(err)
	}
	if w := call("alice", "&session_id=new-unprepared"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	} else {
		var own runtimeSkillsResponse
		_ = json.Unmarshal(w.Body.Bytes(), &own)
		if own.Prepared || len(own.Skills) != 0 {
			t.Fatal("fresh Builder received old projection")
		}
	}
}

func TestRuntimeSkillsCrewRejectsPreviousSlotsAndReaders(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":["work"]},{"id":"bob","username":"bob","products":["work"]}]}`)
	stubCrewLookups(t, map[string]string{"Crew/demo": "alice"}, nil)
	workspace := &mockWorkspaceAPI{files: map[string]string{productConversationRegistryPath("alice"): `{"version":1,"entries":{"work:demo":{"profile_id":"work","session_id":"current","workspace_path":"Crew/demo","provider":"codex-cli"}},"previous":{"work:demo":[{"profile_id":"work","session_id":"old","workspace_path":"Crew/demo"}]}}`}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	ctx := func(user string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
	}
	if id, _, _, status, err := currentSkillBuilder(ctx("alice"), "Crew/demo", "current"); err != nil || status != 0 || id != "current" {
		t.Fatal(id, status, err)
	}
	if _, _, _, status, _ := currentSkillBuilder(ctx("alice"), "Crew/demo", "old"); status != 409 {
		t.Fatal("previous slot accepted", status)
	}
	if _, _, _, status, _ := currentSkillBuilder(ctx("bob"), "Crew/demo", "current"); status != 403 {
		t.Fatal("reader accessed Builder", status)
	}
}
