package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestKnowledgebaseSourceAPIConfigurationReadAndDetach(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	consumer := NewWorkflowManifest("consumer")
	consumer.ID = "consumer"
	source := NewWorkflowManifest("source")
	source.ID = "source"
	files := map[string]string{}
	for _, m := range []*WorkflowManifest{consumer, source} {
		dir := filepath.Join(root, "Workflow", m.ID, "knowledgebase", "notes")
		os.MkdirAll(dir, 0755)
		raw, _ := json.Marshal(m)
		files["Workflow/"+m.ID+"/workflow.json"] = string(raw)
		os.WriteFile(filepath.Join(root, "Workflow", m.ID, "workflow.json"), raw, 0644)
	}
	os.WriteFile(filepath.Join(root, "Workflow/source/knowledgebase/notes/fact.md"), []byte("verified source"), 0644)
	mock := &mockWorkspaceAPI{files: files}
	host := httptest.NewServer(mock)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	api := &StreamingAPI{}
	update := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.handleUpdateWorkflowManifest(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
		if rec.Code == 200 {
			mock.mu.Lock()
			data := mock.files["Workflow/consumer/workflow.json"]
			mock.mu.Unlock()
			os.WriteFile(filepath.Join(root, "Workflow/consumer/workflow.json"), []byte(data), 0644)
		}
		return rec
	}
	rec := update(`{"workspace_path":"Workflow/consumer","knowledgebase_sources":[{"workflow_id":"source","alias":"project_a","access":"read"}]}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	read := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.handleWorkflowKnowledgebaseSources(rec, httptest.NewRequest(http.MethodGet, "/?workspace_path=Workflow/consumer"+query, nil))
		return rec
	}
	rec = read("")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"available":true`) || strings.Contains(rec.Body.String(), root) {
		t.Fatal("source list invalid", rec.Body.String())
	}
	rec = read("&alias=project_a&path=notes/fact.md")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "verified source") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = read("&alias=project_a&path=../workflow.json"); rec.Code == 200 {
		t.Fatal("source sibling exposed")
	}
	if rec = read("&alias=unattached&path=notes/fact.md"); rec.Code != 404 {
		t.Fatal("unattached alias accepted")
	}
	rec = update(`{"workspace_path":"Workflow/consumer","knowledgebase_sources":[{"workflow_id":"source","alias":"project_a","access":"write"}]}`)
	if rec.Code != 400 {
		t.Fatal("write mode accepted", rec.Body.String())
	}
	rec = update(`{"workspace_path":"Workflow/consumer","knowledgebase_sources":[]}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec = read("&alias=project_a&path=notes/fact.md"); rec.Code != 404 {
		t.Fatal("detached source still readable")
	}
	persisted, _, err := ReadWorkflowManifest(context.Background(), "Workflow/consumer")
	if err != nil || len(persisted.KnowledgebaseSources) != 0 {
		t.Fatal("detach not persisted", err)
	}
}

func TestKnowledgebaseSourceManifestValidation(t *testing.T) {
	m := NewWorkflowManifest("test")
	m.KnowledgebaseSources = []workflowtypes.KnowledgebaseSource{{WorkflowID: "other", Alias: "project_a", Access: "read"}}
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
	m.KnowledgebaseSources = append(m.KnowledgebaseSources, m.KnowledgebaseSources[0])
	if err := ValidateManifest(m); err == nil {
		t.Fatal("duplicate sources accepted")
	}
}
