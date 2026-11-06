package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
)

// handleGetExecutionWebhookPayload returns the immutable request body that
// started one webhook run. It is intentionally separate from the execution-log
// response: the log view polls while a run is active, and webhook bodies can be
// large enough that including them in every poll would waste bandwidth.
func (api *StreamingAPI) handleGetExecutionWebhookPayload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	workspacePath := filepath.Clean(strings.TrimSpace(r.URL.Query().Get("workspace_path")))
	runFolder := filepath.Clean(strings.TrimSpace(r.URL.Query().Get("run_folder")))
	if workspacePath == "." || runFolder == "." {
		http.Error(w, "workspace_path and run_folder are required", http.StatusBadRequest)
		return
	}
	if strings.Contains(workspacePath, "..") || strings.Contains(runFolder, "..") {
		http.Error(w, "invalid workspace or run folder", http.StatusBadRequest)
		return
	}
	if !requireWorkflowVisible(w, r, workspacePath) {
		return
	}

	iterationFolder := strings.Split(filepath.ToSlash(runFolder), "/")[0]
	if iterationFolder == "" || strings.ContainsAny(iterationFolder, "\\\x00") {
		http.Error(w, "selected run was not started by a webhook", http.StatusNotFound)
		return
	}

	runIDContent, exists, err := readFileFromWorkspace(r.Context(), workspacePath+"/runs/"+iterationFolder+"/.webhook-run-id")
	if err != nil {
		http.Error(w, "could not resolve webhook run", http.StatusInternalServerError)
		return
	}
	runID := strings.TrimSpace(runIDContent)
	if !exists || runID == "" {
		http.Error(w, "webhook run identity is unavailable", http.StatusNotFound)
		return
	}

	content, exists, err := readFileFromWorkspace(r.Context(), webhookInputPath(workspacePath, runID))
	if err != nil {
		http.Error(w, "could not read webhook payload", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "webhook payload is no longer available", http.StatusNotFound)
		return
	}

	var delivery WorkflowWebhookDelivery
	if err := json.Unmarshal([]byte(content), &delivery); err != nil || !json.Valid(delivery.Payload) {
		http.Error(w, "stored webhook payload is invalid", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"run_id":      runID,
		"raw_payload": string(delivery.Payload),
	})
}
