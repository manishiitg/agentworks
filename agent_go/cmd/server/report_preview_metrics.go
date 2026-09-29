package server

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Reuse the app's cost readers for the preview token's bound workflow.
// The report cannot choose another workspace or request the unbounded cost view.
func (api *StreamingAPI) handleReportPreviewMetrics(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims != nil && claims.Scope == reportPreviewScope && claims.ScopeWorkspace == "" {
		http.Error(w, "preview token is missing its workflow binding", http.StatusBadRequest)
		return
	}
	workspacePath, err := reportPreviewWorkspace(r, claims, r.URL.Query().Get("workspace"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if path.Clean(workspacePath) != workspacePath || strings.Contains(workspacePath, "\\") || !strings.HasPrefix(workspacePath, "Workflow/") {
		http.Error(w, "workspace must be a canonical Workflow path", http.StatusBadRequest)
		return
	}
	workspacePath, err = normalizeReportHumanInputWorkspacePath(workspacePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := reportPreviewReadable(r, claims, workspacePath); err != nil {
		http.Error(w, err.Error(), reportPreviewWorkspaceErrorStatus(err))
		return
	}
	cloned := r.Clone(r.Context())
	copiedURL := *r.URL
	cloned.URL = &copiedURL
	params := url.Values{"workspace_path": {workspacePath}}
	switch r.URL.Path {
	case reportPreviewAPIPrefix + "costs":
		params.Set("view", "summary")
		params.Set("days", r.URL.Query().Get("days"))
		params.Set("before", r.URL.Query().Get("before"))
		cloned.URL.RawQuery = params.Encode()
		api.handleGetCosts(w, cloned)
	default:
		http.NotFound(w, r)
	}
}
