package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

func TestKnowledgebaseGitUsesSharedHandlersAndRootAuthority(t *testing.T) {
	api, _ := knowledgebaseServerTest(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if b, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	t.Setenv("AGENTWORKS_KNOWLEDGEBASE_BACKUP_REMOTE", remote)
	request := func(user, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		api.handleKnowledgebaseGit(w, r)
		return w
	}
	w := request("priya", "GET", "/api/knowledgebase/git?op=status", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"repos":[]`) {
		t.Fatalf("scoped status leaked: %d %s", w.Code, w.Body)
	}
	w = request("priya", "GET", "/api/knowledgebase/git?op=log", "")
	if w.Code != 404 {
		t.Fatalf("scoped history: %d %s", w.Code, w.Body)
	}
	w = request("admin", "GET", "/api/knowledgebase/git?op=status", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"writable":true`) {
		t.Fatalf("admin status: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"op":"stage","all":true,"request_id":"stage"}`, `{"op":"commit","message":"Back up live knowledge","request_id":"commit"}`, `{"op":"push","request_id":"push"}`} {
		w = request("admin", "POST", "/api/knowledgebase/git", body)
		if w.Code != 200 {
			t.Fatalf("shared action: %d %s", w.Code, w.Body)
		}
	}
	w = request("admin", "GET", "/api/knowledgebase/git?op=log", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Back up live knowledge") {
		t.Fatalf("history %d %s", w.Code, w.Body)
	}
	w = request("admin", "POST", "/api/knowledgebase/git", `{"op":"stage","all":true,"repo":"../../other","request_id":"bad-root"}`)
	if w.Code != 400 {
		t.Fatalf("accepted repo injection %d", w.Code)
	}
	w = request("priya", "POST", "/api/knowledgebase/git", `{"op":"pull","request_id":"reader"}`)
	if w.Code == 200 {
		t.Fatal("scoped reader changed repo")
	}
	s, err := knowledgebaseService()
	if err != nil {
		t.Fatal(err)
	}
	value, err := knowledgebaseDispatch(t.Context(), s, knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}, "admin", "brain_backup", map[string]any{"action": "git", "op": "status"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if !strings.Contains(string(raw), `"branch":"main"`) {
		t.Fatal(string(raw))
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	if _, err := knowledgebaseBackupExecutor(ctx, agentprofiles.ToolRuntimeContext{Product: "knowledgebase", UserID: "admin"}, map[string]any{"action": "git", "op": "log"}); err != nil {
		t.Fatal("shared chat Git executor", err)
	}
	if _, err := knowledgebaseBackupExecutor(ctx, agentprofiles.ToolRuntimeContext{Product: "knowledgebase", UserID: "admin"}, map[string]any{"action": "commit", "message": "not an allowed chat action", "request_id": "bad"}); err == nil {
		t.Fatal("chat accepted selected receipt actions")
	}
	// Six action tools: browse, read, update, backup (Git included), skills, access.
	if len(knowledgebase.ToolDefinitions()) != 6 {
		t.Fatal("Git added an extra MCP tool")
	}
	for _, def := range knowledgebase.ConnectionToolDefinitions(true) {
		if def.Name == "brain_backup" {
			raw, _ := json.Marshal(def.InputSchema)
			if strings.Contains(string(raw), `"const":"git"`) {
				t.Fatal("managed content connection exposed repo actions")
			}
		}
	}
}
