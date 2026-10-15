package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Reproduce a reused run-zero slot below eleven archives through both real
// HTTP handlers and the workspace transport, not just the sorting helper.
func TestRunPickerKeepsNewestReusedSlotBeforeLimit(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspace := "Workflow/example"
	files := map[string]string{workspace + "/workflow.json": `{"kind":"workflow"}`}
	for i := 1; i <= 11; i++ {
		at := time.Date(2026, 10, 10, 12, i, 0, 0, time.UTC)
		raw, _ := json.Marshal(RunMetadata{CreatedAt: at, StartedAt: at, CompletedAt: &at, Status: "completed"})
		files[fmt.Sprintf("%s/runs/iteration-%d/default/run_metadata.json", workspace, i)] = string(raw)
	}
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	started := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	completed := started.Add(time.Minute)
	raw, _ := json.Marshal(RunMetadata{CreatedAt: created, StartedAt: started, CompletedAt: &completed, Status: "completed"})
	for _, folder := range []string{"iteration-0/default", "iteration-0/named-run"} {
		files[workspace+"/runs/"+folder+"/run_metadata.json"] = string(raw)
	}
	originalFileCount := len(files)
	mock := &mockWorkspaceAPI{files: files}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	api := lifecycleTestAPI()
	list := httptest.NewRecorder()
	api.handleGetRunFolders(list, httptest.NewRequest(http.MethodGet, "/api/workflow/run-folders?workspace_path="+workspace, nil))
	var folders RunFoldersResponse
	if err := json.Unmarshal(list.Body.Bytes(), &folders); err != nil || list.Code != 200 {
		t.Fatalf("list: %d %s err=%v", list.Code, list.Body.String(), err)
	}
	if folders.DefaultRunFolder != "iteration-0/default" || folders.TotalCount != 13 || len(folders.Folders) != 10 {
		t.Fatalf("dropped the newest slot: %+v", folders)
	}
	state := httptest.NewRecorder()
	api.handleLoadWorkspaceState(state, httptest.NewRequest(http.MethodGet, "/api/workspace/state?workspace_path="+workspace, nil))
	var response WorkspaceStateResponse
	if err := json.Unmarshal(state.Body.Bytes(), &response); err != nil || state.Code != 200 || response.Data == nil {
		t.Fatalf("state: %d %s err=%v", state.Code, state.Body.String(), err)
	}
	if response.Data.DefaultRunFolder != folders.DefaultRunFolder || len(response.Data.RunFolders) != 10 {
		t.Fatalf("list and workspace disagree: %+v", response.Data)
	}
	for i, folder := range response.Data.RunFolders {
		if folder.Name != folders.Folders[i].Name || folder.Metadata == nil {
			t.Fatalf("different order or lost clock at %d: %+v", i, folder)
		}
	}
	if !response.Data.RunFolders[0].Metadata.StartedAt.Equal(started) {
		t.Fatal("lost reused slot's actual start time")
	}
	// Opening a reusable slot must never rewrite evidence just to choose a view.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.files) != originalFileCount {
		t.Fatal("listing wrote extra workspace records")
	}
}
