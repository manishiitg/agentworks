package server

import (
	"context"
	"encoding/json"
	"fmt"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The observed seven-complete/one-failed case must be partial. Also cover
// more groups than the UI's recent-folder limit, without flattening unknown,
// interrupted or unverified recovery outcomes into a partial success.
func TestWorkshopMixedGroupOutcomeUsesTerminalRecords(t *testing.T) {
	since := time.Now().Add(-time.Minute)
	groups := []RunFolderInfo{}
	for i := 0; i < 16; i++ {
		status := "completed"
		if i == 15 {
			status = "failed"
		}
		groups = append(groups, RunFolderInfo{Name: fmt.Sprintf("iteration-42-sched/group-%d", i), Metadata: &RunMetadata{Status: status, StartedAt: since.Add(time.Second)}})
	}
	// Exercise the real workspace listing/metadata decoder. The normal UI
	// still returns ten recent folders, while reconciliation gets this entire
	// bound invocation and cannot borrow another run's results.
	const ws = "Workflow/mixed-groups"
	files := map[string]string{ws + "/workflow.json": `{"schema_version":1,"id":"mixed","kind":"workflow"}`}
	children := []virtualtools.WorkspaceFolderItem{}
	for _, group := range groups {
		children = append(children, virtualtools.WorkspaceFolderItem{Type: "folder", FilePath: ws + "/runs/" + group.Name})
		raw, _ := json.Marshal(group.Metadata)
		files[ws+"/runs/"+group.Name+"/run_metadata.json"] = string(raw)
	}
	listing := []virtualtools.WorkspaceFolderItem{{Type: "folder", FilePath: ws + "/runs", Children: []virtualtools.WorkspaceFolderItem{{Type: "folder", FilePath: ws + "/runs/iteration-42-sched", Children: children}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/documents" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": listing})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		content, ok := files[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"filepath": name, "content": content}})
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	api := &StreamingAPI{}
	listed, err := api.loadRunFoldersInternal(context.Background(), ws, "iteration-42-sched")
	if err != nil || len(listed) != 16 {
		t.Fatalf("bound invocation lost groups: %d, %v", len(listed), err)
	}
	partialListed := reconcileWorkshopPartialOutcome(nil, listed, since, 16)
	if partialListed == nil || partialListed.completed != 15 || partialListed.failed != 1 {
		t.Fatalf("saved records were not reconciled: %+v", partialListed)
	}
	ui, err := api.loadRunFoldersInternal(context.Background(), ws)
	if err != nil || len(ui) != 10 {
		t.Fatalf("UI recent-folder limit changed: %d, %v", len(ui), err)
	}
	partial := reconcileWorkshopPartialOutcome(nil, groups, since, 16)
	if partial == nil || partial.completed != 15 || partial.failed != 1 || !partialWorkshopRun(partial) {
		t.Fatalf("mixed outcome: %+v", partial)
	}
	groups[0].Metadata.Status = "canceled"
	if p := reconcileWorkshopPartialOutcome(nil, groups, since, 16); p != nil {
		t.Fatalf("cancellation hidden: %+v", p)
	}
	groups[0].Metadata.Status = "completed"
	groups[15].Metadata.Recovery = map[string]interface{}{"status": "step_recovered_run_unverified"}
	if p := reconcileWorkshopPartialOutcome(nil, groups, since, 16); p != nil {
		t.Fatalf("unverified whole run hidden: %+v", p)
	}
	groups[15].Metadata.Recovery = nil
	if p := reconcileWorkshopPartialOutcome(nil, groups[:10], since, 16); p != nil {
		t.Fatalf("incomplete group list accepted: %+v", p)
	}
}
