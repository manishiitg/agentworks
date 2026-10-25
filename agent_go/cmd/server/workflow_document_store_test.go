package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeWorkspaceDocumentStore is a minimal in-memory stand-in for the
// workspace API's GET/PUT /api/documents/<path> routes, just enough for
// readFileFromWorkspace/writeFileToWorkspace to round-trip against it.
type fakeWorkspaceDocumentStore struct {
	mu    sync.Mutex
	files map[string]string
}

func newFakeWorkspaceServer(t *testing.T) (*httptest.Server, *fakeWorkspaceDocumentStore) {
	t.Helper()
	store := &fakeWorkspaceDocumentStore{files: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		store.mu.Lock()
		defer store.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			content, ok := store.files[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"filepath": path, "content": content},
			})
		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			store.files[path] = body.Content
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, store
}

// PLAT-825-111: ordinary settings edits cannot redirect saved script source.
func TestWorkflowSettingsPreserveCodeSourceSelection(t *testing.T) {
	server, store := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const workspace = "Workflow/source-policy"
	manifest := NewWorkflowManifest("Source policy")
	raw, _ := json.Marshal(manifest)
	store.files[manifestPath(workspace)] = string(raw)
	manifest.CodeLayoutVersion = 0
	manifest.Label = "Updated label"
	if err := WriteWorkflowManifest(context.Background(), workspace, manifest); err != nil {
		t.Fatal(err)
	}
	var saved WorkflowManifest
	if err := json.Unmarshal([]byte(store.files[manifestPath(workspace)]), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.CodeLayoutVersion != 1 || saved.Label != manifest.Label {
		t.Fatal("ordinary settings changed source selection or lost the allowed edit")
	}
}
