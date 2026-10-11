package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
)

// Reports read bounded, recorded results through the caller's project grant.
// Private run directories and DBOS journals remain inaccessible to slot shells.
func (api *StreamingAPI) handleReportRelayRuns(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil && IsMultiUserMode() {
		http.Error(w, "authentication required", http.StatusForbidden)
		return
	}
	if claims != nil && claims.Scope == reportPreviewScope && claims.ScopeWorkspace == "" {
		http.Error(w, "preview token is missing its workflow binding", http.StatusBadRequest)
		return
	}
	workspace, err := reportPreviewWorkspace(r, claims, r.URL.Query().Get("workspace"))
	if err != nil || !isReportRunWorkflowRoot(workspace) {
		http.Error(w, "a canonical Relay workspace is required", http.StatusBadRequest)
		return
	}
	if err := reportPreviewReadable(r, claims, workspace); err != nil {
		http.Error(w, err.Error(), reportPreviewWorkspaceErrorStatus(err))
		return
	}
	guard := dashboardFileGuard(claims)
	if guard != nil && !guard.AllowsTraversal("runs") {
		http.Error(w, "outside connection read grants", http.StatusForbidden)
		return
	}
	manifest, found, err := ReadWorkflowManifest(r.Context(), workspace)
	if err != nil || !found || manifest.Kind != "relay" {
		http.Error(w, "Relay not found", http.StatusNotFound)
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			http.Error(w, "limit must be between 1 and 50", http.StatusBadRequest)
			return
		}
	}
	version := r.URL.Query().Get("version")
	if version == "" {
		version = "draft"
	}
	rootWorkspace := workspace
	if version != "draft" {
		_, rootWorkspace, err = readRelayRelease(r.Context(), workspace, version)
		if err != nil {
			http.Error(w, "Relay version not found", http.StatusNotFound)
			return
		}
	}
	root, err := webhookWorkspaceRoot(rootWorkspace)
	if err != nil {
		http.Error(w, "Relay workspace unavailable", http.StatusNotFound)
		return
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), "runs")
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "Relay runs unavailable", http.StatusInternalServerError)
		return
	}
	// Allocation numbers establish newest-first order without reading every
	// run's transcript, agent prompt, or private journal.
	sort.Slice(entries, func(i, j int) bool {
		return reportRelayRunNumber(entries[i].Name()) > reportRelayRunNumber(entries[j].Name())
	})
	runs := []map[string]any{}
	for _, entry := range entries {
		if len(runs) == limit || r.Context().Err() != nil {
			break
		}
		if !entry.IsDir() || reportRelayRunNumber(entry.Name()) == 0 {
			continue
		}
		if guard != nil && !guard.Allows(path.Join("runs", entry.Name(), "relay_trace.json"), false) {
			continue
		}
		var trace struct {
			WorkflowID string `json:"workflow_id"`
			Status     string `json:"status"`
			Attempt    int    `json:"attempt_number"`
			Calls      []struct {
				Name      string  `json:"name"`
				Status    string  `json:"status"`
				Reused    bool    `json:"checkpoint_reused"`
				Started   float64 `json:"started_at"`
				Completed float64 `json:"completed_at"`
			} `json:"calls"`
		}
		if !readReportRelayJSON(root, path.Join("runs", entry.Name(), "relay_trace.json"), &trace) {
			continue
		}
		steps := []map[string]any{}
		var started, completed float64
		for _, call := range trace.Calls {
			steps = append(steps, map[string]any{"name": call.Name, "status": call.Status, "reused": call.Reused})
			if call.Started > 0 && (started == 0 || call.Started < started) {
				started = call.Started
			}
			if call.Completed > completed {
				completed = call.Completed
			}
		}
		var result any
		resultPath := path.Join("runs", entry.Name(), "relay_result.json")
		if guard == nil || guard.Allows(resultPath, false) {
			readReportRelayJSON(root, resultPath, &result)
		}
		var duration any
		if started > 0 && completed >= started {
			duration = completed - started
		}
		runs = append(runs, map[string]any{"run": entry.Name(), "run_id": trace.WorkflowID, "version": version, "status": trace.Status, "attempt": trace.Attempt, "started_at": started, "duration_s": duration, "steps": steps, "result": result})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"version": version, "runs": runs})
}

func reportRelayRunNumber(name string) int {
	match := webhookFolderPattern.FindStringSubmatch(name)
	if len(match) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(match[1])
	return n
}

func readReportRelayJSON(root *os.Root, name string, target any) bool {
	// os.Root confines traversal. Reject links inside that root as well, so an
	// authored run cannot turn this summary reader into a private-file reader.
	for current := name; current != "."; current = path.Dir(current) {
		info, err := root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	before, err := root.Lstat(name)
	if err != nil {
		return false
	}
	f, err := root.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Size() > 256<<10 {
		return false
	}
	return json.NewDecoder(io.LimitReader(f, 256<<10)).Decode(target) == nil
}
