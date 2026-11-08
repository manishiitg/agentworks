package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// handleWorkflowRun starts a workflow run directly from the website, the same way MCP `runs start` and a schedule do
// (no chat, no model turn): workflows run without a chat (owner, 8 Nov). The body carries optional input_preset and
// inputs; the workspace_path query names the workflow. Owners and editors only: it is the Builder's Run button.
func (api *StreamingAPI) handleWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}
	manifest, found, err := ReadWorkflowManifest(r.Context(), workspacePath)
	if err != nil || !found || manifest == nil {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}
	args := map[string]any{}
	// A body that cannot be read must not become an empty run request: refuse before anything starts.
	raw, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if readErr != nil {
		http.Error(w, "the request body could not be read", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if json.Unmarshal(raw, &args) != nil {
			http.Error(w, "the body must be a JSON object with optional input_preset and inputs", http.StatusBadRequest)
			return
		}
	}
	target := DiscoveredWorkflow{WorkspacePath: workspacePath, Manifest: manifest, MyAccess: workflowAccessForManifest(GetUserFromContext(r.Context()), manifest)}
	if !externalWorkflowIsScript(target) {
		http.Error(w, "only a script workflow starts directly; this workflow is on the retired engine", http.StatusConflict)
		return
	}
	api.externalRunProxy(w, r, "run_full_workflow", args, target)
}
