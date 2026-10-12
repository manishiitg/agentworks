package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
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
	out, status, message := relayRecordedRuns(r.Context(), claims, workspace, manifest, guard, limit, r.URL.Query().Get("version"))
	if message != "" {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// relayRunSeesAll says whether the reader may see every caller's recorded run:
// Relay owners and editors do, everyone else only sees runs they made. A server
// without accounts has one reader, who owns everything.
func relayRunSeesAll(claims *UserClaims, manifest *WorkflowManifest) bool {
	if claims == nil {
		return !IsMultiUserMode()
	}
	access := workflowAccessForManifest(claims, manifest)
	return access == WorkflowAccessOwner || access == WorkflowAccessWrite
}

// relayRunCaller reads who started a run from its saved delivery. Empty when the
// record is missing, so a reader without full access never sees an unattributed run.
func relayRunCaller(ctx context.Context, scopeWorkspace, runID string) string {
	if runID == "" {
		return ""
	}
	content, exists, err := readFileFromWorkspace(ctx, webhookInputPath(scopeWorkspace, runID))
	if err != nil || !exists {
		return ""
	}
	var delivery WorkflowWebhookDelivery
	var payload struct {
		Caller string `json:"relay_caller"`
	}
	if json.Unmarshal([]byte(content), &delivery) != nil || json.Unmarshal(delivery.Payload, &payload) != nil {
		return ""
	}
	return payload.Caller
}

const relayRunScanLimit = 500 // recorded runs examined per request for a reader who sees only their own

// relayRecordedRuns lists bounded recorded run summaries, newest first. It is the
// one reader behind the dashboard data path and the relay tool's list action.
func relayRecordedRuns(ctx context.Context, claims *UserClaims, workspace string, manifest *WorkflowManifest, guard *wf.FolderGuard, limit int, version string) (map[string]any, int, string) {
	if version == "" {
		version = "draft"
	}
	rootWorkspace := workspace
	if version != "draft" {
		var err error
		_, rootWorkspace, err = readRelayRelease(ctx, workspace, version)
		if err != nil {
			return nil, http.StatusNotFound, "Relay version not found"
		}
	}
	root, err := webhookWorkspaceRoot(rootWorkspace)
	if err != nil {
		return nil, http.StatusNotFound, "Relay workspace unavailable"
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), "runs")
	if err != nil && !os.IsNotExist(err) {
		return nil, http.StatusInternalServerError, "Relay runs unavailable"
	}
	// Allocation numbers establish newest-first order without reading every
	// run's transcript, agent prompt, or private journal.
	sort.Slice(entries, func(i, j int) bool {
		return reportRelayRunNumber(entries[i].Name()) > reportRelayRunNumber(entries[j].Name())
	})
	seeAll := relayRunSeesAll(claims, manifest)
	viewer := ""
	if claims != nil {
		viewer = claims.UserID
	}
	runs := []map[string]any{}
	scanned := 0
	for _, entry := range entries {
		if len(runs) == limit || ctx.Err() != nil || !seeAll && scanned >= relayRunScanLimit {
			break
		}
		if !entry.IsDir() || reportRelayRunNumber(entry.Name()) == 0 {
			continue
		}
		scanned++
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
		if !seeAll {
			if caller := relayRunCaller(ctx, rootWorkspace, trace.WorkflowID); caller == "" || viewer == "" || caller != viewer {
				continue
			}
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
	return map[string]any{"version": version, "runs": runs, "scope": map[bool]string{true: "all", false: "own"}[seeAll]}, 0, ""
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
