package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

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
	before := 0
	if raw := r.URL.Query().Get("before"); raw != "" {
		before, err = strconv.Atoi(raw)
		if err != nil || before < 1 {
			http.Error(w, "before must be a positive recorded run number", http.StatusBadRequest)
			return
		}
	}
	out, status, message := relayRecordedRunsBefore(r.Context(), claims, workspace, manifest, guard, limit, r.URL.Query().Get("version"), before)
	if message != "" {
		http.Error(w, message, status)
		return
	}
	out["product"] = map[string]any{"id": manifest.ID, "name": manifest.Label, "purpose": manifest.Description}
	// Everyone with product access can ask: the Builder in their own Builder chat, everyone else in the Relay's Run chat
	// (read_relay, list/get runs, run_relay; 856-80).
	out["can_draft"] = relayRunCanReadDraft(claims, manifest) && (claims == nil || claims.Scope == "")
	out["can_ask"] = relayRunSeesAll(claims, manifest) && (claims == nil || claims.Scope == "")
	out = api.maskRelaySecrets(r.Context(), workspace, manifest, out)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// Everyone with product access sees its recorded work, regardless of who ran it.
// This does not grant product access to someone who can only call a function.
func relayRunSeesAll(claims *UserClaims, manifest *WorkflowManifest) bool {
	return workflowAccessForManifest(claims, manifest) != WorkflowAccessNone
}

func relayRunCanReadDraft(claims *UserClaims, manifest *WorkflowManifest) bool {
	if claims == nil {
		return !IsMultiUserMode()
	}
	access := workflowAccessForManifest(claims, manifest)
	return access == WorkflowAccessOwner || access == WorkflowAccessWrite
}

// relayRunCaller reads who started a run from its saved delivery. Empty when the
// record is missing, so a reader without full access never sees an unattributed run.
func relayRunCaller(ctx context.Context, scopeWorkspace, runID string) string {
	if !validRelayReportRunID(runID) {
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

const relayRunScanLimit = 500 // recorded folders examined per bounded request

// relayRecordedRuns lists bounded recorded run summaries, newest first. It is the
// one reader behind the dashboard data path and the relay tool's list action.
func relayRecordedRuns(ctx context.Context, claims *UserClaims, workspace string, manifest *WorkflowManifest, guard *wf.FolderGuard, limit int, version string) (map[string]any, int, string) {
	return relayRecordedRunsBefore(ctx, claims, workspace, manifest, guard, limit, version, 0)
}

func relayRecordedRunsBefore(ctx context.Context, claims *UserClaims, workspace string, manifest *WorkflowManifest, guard *wf.FolderGuard, limit int, version string, before int) (out map[string]any, status int, message string) {
	defer func() {
		if out != nil {
			out["fetched_at"] = time.Now().UTC().Format(time.RFC3339)
		}
	}()
	if claims != nil && claims.Scope == "" && claims.Provider != "bot_route" {
		account := userAccessForClaims(claims)
		if account.Disabled || IsMultiUserMode() && !account.Known {
			return nil, http.StatusForbidden, "Relay is unavailable for this account."
		}
	}
	seeAll := relayRunSeesAll(claims, manifest)
	scope := map[bool]string{true: "all", false: "own"}[seeAll]
	if version == "" {
		if relayRunCanReadDraft(claims, manifest) {
			version = "draft"
		} else {
			version = "active"
		}
	}
	rootWorkspace := workspace
	if version == "draft" && !relayRunCanReadDraft(claims, manifest) {
		return nil, http.StatusForbidden, "Draft tests are available in Builder mode."
	}
	if version == "active" {
		release, releaseWorkspace, err := activeRelayRelease(ctx, workspace)
		if err != nil {
			_, exists, readErr := readFileFromWorkspace(ctx, path.Join(relayReleaseRoot(workspace), "active.json"))
			if readErr != nil || exists {
				return nil, http.StatusServiceUnavailable, "Published Relay version is unavailable."
			}
			return map[string]any{"version": "", "published": false, "runs": []map[string]any{}, "scope": scope}, 0, ""
		}
		version, rootWorkspace = release.Version, releaseWorkspace
	}
	if version != "draft" && rootWorkspace == workspace {
		var err error
		_, rootWorkspace, err = readRelayRelease(ctx, workspace, version)
		if err != nil {
			return nil, http.StatusNotFound, "Relay version not found"
		}
	}
	root, err := webhookWorkspaceRoot(rootWorkspace)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing has run here yet: no recorded runs, not an error.
		return map[string]any{"version": version, "runs": []map[string]any{}, "scope": scope}, 0, ""
	}
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
	viewer := ""
	if claims != nil {
		viewer = claims.UserID
	}
	runs := []map[string]any{}
	scanned, truncated := 0, false
	responseBytes := 0
	lastExamined, lastReturned, nextBefore := 0, 0, 0
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		number := reportRelayRunNumber(entry.Name())
		if !entry.IsDir() || number == 0 || before > 0 && number >= before {
			continue
		}
		if len(runs) == limit {
			nextBefore = lastReturned
			break
		}
		if scanned >= relayRunScanLimit {
			truncated = true // older runs were not examined
			nextBefore = lastExamined
			break
		}
		scanned++
		lastExamined = number
		if guard != nil && !guard.Allows(path.Join("runs", entry.Name(), "relay_trace.json"), false) {
			continue
		}
		var trace struct {
			WorkflowID string `json:"workflow_id"`
			Status     string `json:"status"`
			Error      string `json:"error"`
			Attempt    int    `json:"attempt_number"`
			Calls      []struct {
				ID        string  `json:"id"`
				Name      string  `json:"name"`
				Status    string  `json:"status"`
				Error     string  `json:"error"`
				Output    any     `json:"output"`
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
		for index, call := range trace.Calls {
			if index >= 100 {
				truncated = true
				break
			}
			steps = append(steps, map[string]any{"id": relayReportText(call.ID), "name": relayReportText(call.Name), "status": relayReportText(call.Status), "reused": call.Reused, "error": relayReportText(call.Error), "output": relayReportValue(call.Output), "started_at": call.Started, "completed_at": call.Completed})
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
			if !readReportRelayJSON(root, resultPath, &result) {
				result = map[string]any{"unavailable": true, "reason": "not_recorded_or_exceeds_limit"}
			}
		}
		var duration any
		if started > 0 && completed >= started {
			duration = completed - started
		}
		caller, input := relayReportInput(root, guard, trace.WorkflowID)
		run := map[string]any{"run": entry.Name(), "run_id": relayReportText(trace.WorkflowID), "caller": caller, "input": relayReportValue(input), "version": version, "status": relayReportText(trace.Status), "error": relayReportText(trace.Error), "attempt": trace.Attempt, "started_at": started, "completed_at": completed, "duration_s": duration, "steps": steps, "result": relayReportValue(result)}
		encoded, _ := json.Marshal(run)
		if responseBytes+len(encoded) > 512<<10 {
			truncated = true
			nextBefore = lastReturned
			break
		}
		responseBytes += len(encoded)
		runs = append(runs, run)
		lastReturned = number
	}
	out = map[string]any{"version": version, "runs": runs, "scope": scope}
	if truncated {
		out["truncated"] = true
	}
	if nextBefore > 0 {
		out["next_before"] = nextBefore
	}
	return out, 0, ""
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
	return readReportRelayJSONLimit(root, name, target, 256<<10)
}

func readReportRelayJSONLimit(root *os.Root, name string, target any, limit int64) bool {
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
	if err != nil || !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Size() > limit {
		return false
	}
	return json.NewDecoder(io.LimitReader(f, limit)).Decode(target) == nil
}

// relaySecretValues are the values of the secrets a Relay's own runs use (the Builder's selection). Runs are visible to
// everyone with access to the Relay, but secret values never are.
func (api *StreamingAPI) relaySecretValues(ctx context.Context, workspace string, manifest *WorkflowManifest) []string {
	if manifest == nil {
		return nil
	}
	owner := workflowExecutionOwnerUserID(manifest)
	if owner == "" {
		return nil
	}
	selected := api.loadSelectedSecrets(ctx, owner, workspace, manifest.Capabilities.SelectedSecrets)
	var values []string
	for _, secret := range api.mergeGlobalSecretsFor(ctx, owner, selected, manifest.Capabilities.SelectedGlobalSecretNames) {
		if len(secret.Value) >= 4 {
			values = append(values, secret.Value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

// maskRelaySecrets returns value with every secret value of the Relay replaced by <REDACTED>, in any string inside it.
func (api *StreamingAPI) maskRelaySecrets(ctx context.Context, workspace string, manifest *WorkflowManifest, value any) map[string]any {
	secrets := api.relaySecretValues(ctx, workspace, manifest)
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var document any
	if json.Unmarshal(encoded, &document) != nil {
		return map[string]any{}
	}
	out, _ := maskSecretValues(document, secrets).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func maskSecretValues(value any, secrets []string) any {
	switch v := value.(type) {
	case string:
		for _, secret := range secrets {
			v = strings.ReplaceAll(v, secret, "<REDACTED>")
		}
		return v
	case []any:
		for i := range v {
			v[i] = maskSecretValues(v[i], secrets)
		}
	case map[string]any:
		for key, entry := range v {
			v[key] = maskSecretValues(entry, secrets)
		}
	}
	return value
}

const (
	relayRunTraceLimit = 1 << 20   // trace files carry every step output
	relayRunLogBytes   = 128 << 10 // newest part of a run's event log
	relayRunLogLines   = 200
)

// relayRunLogs returns the newest event lines of a run (level, message, step, time).
// The log is plain text written by the runtime; only these four fields are returned.
func relayRunLogs(root *os.Root, folder string, guard *wf.FolderGuard) (lines []map[string]any, truncated bool) {
	name := path.Join("runs", folder, "dbos_events.jsonl")
	if guard != nil && !guard.Allows(name, false) {
		return nil, false
	}
	for current := name; current != "."; current = path.Dir(current) {
		if info, err := root.Lstat(current); err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, false
		}
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	if info.Size() > relayRunLogBytes {
		truncated = true
		if _, err := f.Seek(info.Size()-relayRunLogBytes, io.SeekStart); err != nil {
			return nil, false
		}
	}
	data, _ := io.ReadAll(io.LimitReader(f, relayRunLogBytes))
	all := strings.Split(strings.TrimSpace(string(data)), "\n")
	if truncated && len(all) > 0 {
		all = all[1:] // first line may be cut
	}
	if len(all) > relayRunLogLines {
		all, truncated = all[len(all)-relayRunLogLines:], true
	}
	for _, line := range all {
		var event struct {
			Time    float64 `json:"timestamp"`
			Level   string  `json:"level"`
			Message string  `json:"message"`
			Step    *int    `json:"step_id"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		entry := map[string]any{"at": event.Time, "level": event.Level, "message": relayReportText(event.Message)}
		if event.Step != nil {
			entry["step"] = *event.Step
		}
		lines = append(lines, entry)
	}
	return lines, truncated
}

// relayRunFunction reads which function a run called from its saved delivery.
func relayRunFunction(root *os.Root, guard *wf.FolderGuard, runID string) string {
	if !validRelayReportRunID(runID) {
		return ""
	}
	file := path.Join("webhooks/deliveries", runID+".json")
	if guard != nil && !guard.Allows(file, false) {
		return ""
	}
	var delivery WorkflowWebhookDelivery
	var payload struct {
		Function string `json:"function"`
	}
	if !readReportRelayJSON(root, file, &delivery) || json.Unmarshal(delivery.Payload, &payload) != nil {
		return ""
	}
	return relayReportText(payload.Function)
}

// relayRecordedRun returns one recorded run in full: caller, function, input,
// each step's status, timing, error and output, the result, and the newest log
// lines. run is a run folder (iteration-N-hook) or a run ID. Prompts and tool
// transcripts stay private; callers mask secret values before returning it.
func relayRecordedRun(ctx context.Context, claims *UserClaims, workspace string, manifest *WorkflowManifest, guard *wf.FolderGuard, run, version string) (map[string]any, int, string) {
	if claims != nil && claims.Scope == "" && claims.Provider != "bot_route" {
		if account := userAccessForClaims(claims); account.Disabled || IsMultiUserMode() && !account.Known {
			return nil, http.StatusForbidden, "Relay is unavailable for this account."
		}
	}
	// Same version rules as the run list: callers run the active published version,
	// and draft tests are for Builder.
	canDraft := relayRunCanReadDraft(claims, manifest)
	if version == "" {
		version = "active"
		if canDraft {
			version = "draft"
		}
	}
	rootWorkspace := workspace
	switch {
	case version == "draft":
		if !canDraft {
			return nil, http.StatusForbidden, "Draft tests are available in Builder mode."
		}
	case version == "active":
		release, releaseWorkspace, err := activeRelayRelease(ctx, workspace)
		if err != nil {
			return nil, http.StatusNotFound, "Relay run not found"
		}
		version, rootWorkspace = release.Version, releaseWorkspace
	default:
		var err error
		if _, rootWorkspace, err = readRelayRelease(ctx, workspace, version); err != nil {
			return nil, http.StatusNotFound, "Relay version not found"
		}
	}
	root, err := webhookWorkspaceRoot(rootWorkspace)
	if err != nil {
		return nil, http.StatusNotFound, "Relay run not found"
	}
	defer root.Close()
	type traceFile struct {
		WorkflowID string `json:"workflow_id"`
		Status     string `json:"status"`
		Error      string `json:"error"`
		Attempt    int    `json:"attempt_number"`
		Calls      []struct {
			Name      string  `json:"name"`
			Status    string  `json:"status"`
			Error     string  `json:"error"`
			Reused    bool    `json:"checkpoint_reused"`
			Started   float64 `json:"started_at"`
			Completed float64 `json:"completed_at"`
			Output    any     `json:"output"`
		} `json:"calls"`
	}
	var trace traceFile
	folder := run
	readTrace := func(name string) bool {
		trace = traceFile{}
		tracePath := path.Join("runs", name, "relay_trace.json")
		return (guard == nil || guard.Allows(tracePath, false)) && readReportRelayJSONLimit(root, tracePath, &trace, relayRunTraceLimit)
	}
	if reportRelayRunNumber(run) == 0 {
		// A run ID: look through the newest runs for its trace.
		entries, _ := fs.ReadDir(root.FS(), "runs")
		sort.Slice(entries, func(i, j int) bool {
			return reportRelayRunNumber(entries[i].Name()) > reportRelayRunNumber(entries[j].Name())
		})
		folder = ""
		for i, entry := range entries {
			if i == relayRunScanLimit || ctx.Err() != nil {
				break
			}
			if entry.IsDir() && reportRelayRunNumber(entry.Name()) > 0 && readTrace(entry.Name()) && trace.WorkflowID == run {
				folder = entry.Name()
				break
			}
		}
	} else if !readTrace(folder) {
		folder = ""
	}
	if folder == "" {
		return nil, http.StatusNotFound, "Relay run not found"
	}
	// Someone with product access sees every run. A caller who can only call a function sees only the runs they started,
	// as in the list; a run with no recorded caller stays hidden from them.
	if !relayRunSeesAll(claims, manifest) {
		viewer := ""
		if claims != nil {
			viewer = claims.UserID
		}
		if recorded := relayRunCaller(ctx, rootWorkspace, trace.WorkflowID); recorded == "" || viewer == "" || recorded != viewer {
			return nil, http.StatusNotFound, "Relay run not found"
		}
	}
	caller, input := relayReportInput(root, guard, trace.WorkflowID)
	function := relayRunFunction(root, guard, trace.WorkflowID)
	steps := []map[string]any{}
	var started, completed float64
	for _, call := range trace.Calls {
		step := map[string]any{"name": relayReportText(call.Name), "status": relayReportText(call.Status), "reused": call.Reused}
		if call.Started > 0 {
			step["started_at"] = call.Started
			if started == 0 || call.Started < started {
				started = call.Started
			}
		}
		if call.Completed > 0 {
			step["completed_at"] = call.Completed
			if call.Started > 0 && call.Completed >= call.Started {
				step["duration_s"] = call.Completed - call.Started
			}
			if call.Completed > completed {
				completed = call.Completed
			}
		}
		if call.Error != "" {
			step["error"] = relayReportText(call.Error)
		}
		if call.Output != nil {
			step["output"] = relayReportValue(call.Output)
		}
		steps = append(steps, step)
	}
	var result any
	resultPath := path.Join("runs", folder, "relay_result.json")
	if guard == nil || guard.Allows(resultPath, false) {
		if !readReportRelayJSON(root, resultPath, &result) {
			result = map[string]any{"unavailable": true, "reason": "not_recorded_or_exceeds_limit"}
		}
	}
	out := map[string]any{"run": folder, "run_id": relayReportText(trace.WorkflowID), "version": version, "function": function, "caller": caller,
		"input": relayReportValue(input), "status": relayReportText(trace.Status), "attempt": trace.Attempt, "started_at": started,
		"steps": steps, "result": relayReportValue(result)}
	if trace.Error != "" {
		out["error"] = relayReportText(trace.Error)
	}
	if started > 0 && completed >= started {
		out["duration_s"] = completed - started
	}
	if logs, cut := relayRunLogs(root, folder, guard); len(logs) > 0 {
		out["logs"] = logs
		if cut {
			out["logs_truncated"] = true
		}
	}
	return out, 0, ""
}

// handleReportRelayRun serves one recorded run for the dashboard's drill-down,
// under the same access checks as the run list.
func (api *StreamingAPI) handleReportRelayRun(w http.ResponseWriter, r *http.Request) {
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
	run := r.URL.Query().Get("run")
	if run == "" || len(run) > 200 || strings.ContainsAny(run, "/\\") {
		http.Error(w, "run is required", http.StatusBadRequest)
		return
	}
	out, status, message := relayRecordedRun(r.Context(), claims, workspace, manifest, guard, run, r.URL.Query().Get("version"))
	if message != "" {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(api.maskRelaySecrets(r.Context(), workspace, manifest, out))
}
