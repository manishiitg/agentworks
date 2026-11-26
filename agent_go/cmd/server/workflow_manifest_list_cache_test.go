package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The sidebar inventory lists workflows every few seconds; a log showed 332 workspace reads and ~4 s per call because
// every listing re-read every folder. The list route must read only changed workflow.json files, and a write made through
// this server must show up at once.
func TestWorkflowManifestListReadsOnlyChangedFolders(t *testing.T) {
	var mu sync.Mutex
	reads := map[string]int{}
	stamp := manifestStamp{known: true, present: true, size: 10, modified: "t0"}
	previous := workflowManifests
	workflowManifests = &workflowManifestIndex{
		list: func(context.Context) ([]string, map[string]manifestStamp, error) {
			return []string{"Workflow/one", "Workflow/two"}, map[string]manifestStamp{"Workflow/one": stamp, "Workflow/two": stamp}, nil
		},
		read: func(_ context.Context, path string) (*WorkflowManifest, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			reads[path]++
			return NewWorkflowManifest(path), true, nil
		},
	}
	t.Cleanup(func() { workflowManifests = previous })
	api := &StreamingAPI{}
	list := func() {
		request := httptest.NewRequest(http.MethodGet, "/api/workflows/manifests", nil).WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
		response := httptest.NewRecorder()
		api.handleListWorkflowManifests(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("HTTP%d %s", response.Code, response.Body)
		}
	}
	list()
	list()
	if reads["Workflow/one"] != 1 || reads["Workflow/two"] != 1 {
		t.Fatalf("a second listing re-read manifests: %v", reads)
	}
	noteWorkspaceMutation("Workflow/one/workflow.json", false)
	list()
	if reads["Workflow/one"] != 2 || reads["Workflow/two"] != 1 {
		t.Fatalf("a write to one workflow should re-read only it: %v", reads)
	}
}
