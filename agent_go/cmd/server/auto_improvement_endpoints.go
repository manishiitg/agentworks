package server

import (
	"encoding/json"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// =====================================================================
// HTTP endpoints for the auto-improvement framework.
//
// All read-only.
// =====================================================================

// FrameworkHealthResponse is the JSON shape of GET /api/workflow/framework-health.
// One stop shop for "is the framework wired correctly?": soul preconditions.
type FrameworkHealthResponse struct {
	Success           bool   `json:"success"`
	SoulExists        bool   `json:"soul_exists"`
	ObjectiveOK       bool   `json:"objective_ok"`
	SuccessCriteriaOK bool   `json:"success_criteria_ok"`
	Objective         string `json:"objective,omitempty"`
	SuccessCriteria   string `json:"success_criteria,omitempty"`
	Error             string `json:"error,omitempty"`
}

// handleGetFrameworkHealth surfaces the soul.md preconditions used by workflow
// setup and improvement flows.
func (api *StreamingAPI) handleGetFrameworkHealth(w http.ResponseWriter, r *http.Request) {
	if !setupCORS(w, r, http.MethodGet) {
		return
	}
	workspacePath, ok := requireWorkspacePath(w, r)
	if !ok {
		return
	}
	pre, err := ReadSoulPreconditions(r.Context(), workspacePath)
	if err != nil {
		writeAIJSON(w, FrameworkHealthResponse{Success: false, Error: err.Error()})
		return
	}
	resp := FrameworkHealthResponse{
		Success:           true,
		SoulExists:        pre.SoulExists,
		ObjectiveOK:       pre.ObjectiveOK,
		SuccessCriteriaOK: pre.SuccessCriteriaOK,
		Objective:         pre.Objective,
		SuccessCriteria:   pre.SuccessCriteria,
	}
	writeAIJSON(w, resp)
}

// BuilderDocResponse is the JSON shape of GET /api/workflow/builder-doc.
// It returns a stable workflow document (or empty if it does not exist yet).
type BuilderDocResponse struct {
	Success bool   `json:"success"`
	Doc     string `json:"doc"`     // "soul" or a compact dashboard card — echoed back
	Path    string `json:"path"`    // workspace-relative path that was read
	Exists  bool   `json:"exists"`  // false if the file does not exist yet
	Content string `json:"content"` // markdown body, "" when !exists
	Error   string `json:"error,omitempty"`
}

func (api *StreamingAPI) handleGetBuilderDoc(w http.ResponseWriter, r *http.Request) {
	if !setupCORS(w, r, http.MethodGet) {
		return
	}
	workspacePath, ok := requireWorkspacePath(w, r)
	if !ok {
		return
	}
	doc := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("doc")))
	requestedPath := strings.TrimSpace(r.URL.Query().Get("path"))
	var rel string
	switch doc {
	case "soul":
		rel = "soul/soul.md"
	case "card-health":
		rel = "builder/card.health.html"
	case "card-progress":
		rel = "builder/card.progress.html"
	case "card-cost":
		rel = "builder/card.cost.html"
	default:
		http.Error(w, "doc must be one of: soul, card-health, card-progress, card-cost", http.StatusBadRequest)
		return
	}
	if requestedPath != "" {
		http.Error(w, "path is not supported for builder documents", http.StatusBadRequest)
		return
	}
	full := path.Join(strings.Trim(workspacePath, "/"), rel)
	content, exists, err := readFileFromWorkspace(r.Context(), full)
	if err != nil {
		writeAIJSON(w, BuilderDocResponse{Success: false, Doc: doc, Path: rel, Error: err.Error()})
		return
	}
	if !exists {
		writeAIJSON(w, BuilderDocResponse{Success: true, Doc: doc, Path: rel, Exists: false, Content: ""})
		return
	}
	writeAIJSON(w, BuilderDocResponse{Success: true, Doc: doc, Path: rel, Exists: true, Content: content})
}

// --- Shared HTTP helpers ----------------------------------------------------

func setupCORS(w http.ResponseWriter, r *http.Request, method string) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", method+", OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Session-ID, X-User-ID")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return false
	}
	if r.Method != method {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func requireWorkspacePath(w http.ResponseWriter, r *http.Request) (string, bool) {
	workspacePath := r.URL.Query().Get("workspace_path")
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return "", false
	}
	cleaned := filepath.Clean(workspacePath)
	if strings.Contains(cleaned, "..") {
		http.Error(w, "Invalid workspace path", http.StatusBadRequest)
		return "", false
	}
	return cleaned, true
}

func writeAIJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
