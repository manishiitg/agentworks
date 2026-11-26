package server

import (
	"encoding/json"
	"net/http"

	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
)

// A consumer-scoped endpoint; aliases resolve only through that consumer's
// current authorized attachments. It never accepts a caller-supplied host path.
func (api *StreamingAPI) handleWorkflowKnowledgebaseSources(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspace := r.URL.Query().Get("workspace_path")
	if workspace == "" {
		http.Error(w, "workspace_path required", http.StatusBadRequest)
		return
	}
	if !requireWorkflowVisible(w, r, workspace) {
		return
	}
	sources, err := workflowkb.Resolve(step.GetPromptDocsRoot(), workspace, nil)
	if err != nil {
		// Restored or hand-edited invalid attachments remain visible so the owner
		// can remove them. They never become file-read capabilities.
		if r.URL.Query().Get("alias") == "" {
			if manifest, readErr := workflowkb.ReadManifest(step.GetPromptDocsRoot(), workspace); readErr == nil {
				unavailable := []workflowkb.ResolvedSource{}
				for _, ref := range manifest.Sources {
					unavailable = append(unavailable, workflowkb.ResolvedSource{KnowledgebaseSource: ref, Reason: err.Error()})
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "sources": unavailable})
				return
			}
		}
		http.Error(w, "Unable to resolve knowledge sources: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	alias := r.URL.Query().Get("alias")
	if alias == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "sources": sources})
		return
	}
	for _, source := range sources {
		if source.Alias != alias {
			continue
		}
		if !source.Available {
			http.Error(w, source.Reason, http.StatusConflict)
			return
		}
		content, err := workflowkb.ReadFile(source, r.URL.Query().Get("path"))
		if err != nil {
			http.Error(w, "Unable to read attached KB file: "+err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "content": string(content)})
		return
	}
	http.Error(w, "Knowledge source is not attached", http.StatusNotFound)
}
