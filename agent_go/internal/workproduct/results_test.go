package workproduct

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746: ordinary deliverables are explicit, deduplicated files; a result
// cannot turn a local symlink or a private conversation into a file link.
func TestWorkResultsRecordAndConfinement(t *testing.T) {
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	docs := filepath.Join(base, "docs")
	project := filepath.Join(base, "project")
	state := filepath.Join(base, "state")
	for _, dir := range []string{filepath.Join(docs, "Crew", "example"), filepath.Join(project, ".agentworks", "outputs"), filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", Owner: "owner", ID: "example", LocalRoot: project}}})
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := crewroot.EnsurePrivateRoot(docs, "example", filepath.Join(docs, "Crew", "example")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "report.md")
	if err := os.WriteFile(file, []byte("finished report"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "private.txt")
	_ = os.WriteFile(outside, []byte("private"), 0600)
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(project, ".agentworks", "outputs", "results.json")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "PUT" || req.URL.Path != "/api/documents/Crew/example/outputs/results.json" {
			http.Error(w, "wrong file", 400)
			return
		}
		var payload struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(req.Body).Decode(&payload)
		if err := os.WriteFile(registry, []byte(payload.Content), 0600); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer upstream.Close()
	tool, err := workResultsFactory(upstream.URL)(agentprofiles.ToolRuntimeContext{UserID: "owner", WorkspacePath: "Crew/example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]interface{}) {
		t.Helper()
		if _, err := tool.Execute(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	call(map[string]interface{}{"action": "add", "path": "_files/report.md", "title": "Report"})
	call(map[string]interface{}{"action": "add", "path": "_files/report.md", "title": "Updated report"})
	got, err := ReadResults("Crew/example")
	if err != nil || len(got.Results) != 1 || got.Results[0].Title != "Updated report" || got.Results[0].Status != "ready" {
		t.Fatalf("results=%+v err=%v", got, err)
	}
	for _, path := range []string{"../private.txt", "/private.txt", "_files/escape", "builder/conversation/private.json", strings.Repeat("a", 1025)} {
		if _, err := tool.Execute(context.Background(), map[string]interface{}{"action": "add", "path": path, "title": "Bad"}); err == nil {
			t.Fatalf("admitted %q", path)
		}
	}
	stranger, _ := workResultsFactory(upstream.URL)(agentprofiles.ToolRuntimeContext{UserID: "other", WorkspacePath: "Crew/example"}, nil)
	if _, err := stranger.Execute(context.Background(), map[string]interface{}{"action": "list"}); err == nil {
		t.Fatal("another person read results")
	}
	_ = os.Remove(file)
	got, err = ReadResults("Crew/example")
	if err != nil || got.Results[0].Status != "missing" {
		t.Fatalf("missing result=%+v err=%v", got, err)
	}
	call(map[string]interface{}{"action": "remove", "path": "_files/report.md"})
	got, err = ReadResults("Crew/example")
	if err != nil || len(got.Results) != 0 {
		t.Fatalf("remove=%+v err=%v", got, err)
	}
}
