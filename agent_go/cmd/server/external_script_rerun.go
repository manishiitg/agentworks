package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"sort"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type externalScriptSourceChange struct {
	File   string `json:"file"`
	Before string `json:"before_revision,omitempty"`
	After  string `json:"after_revision,omitempty"`
}

// A rerun is a fresh, real invocation of the current saved code. Only the
// prior run's declared values/preset are reused; no runtime or side-effect cache
// is introduced. The existing start path revalidates today's input contract.
func (api *StreamingAPI) externalScriptRerun(w http.ResponseWriter, r *http.Request, item DiscoveredWorkflow, args map[string]any) {
	if !externalWorkflowIsScript(item) {
		externalError(w, 400, "invalid_arguments", "Choose a saved Python workflow. Start an older workflow with runs start instead.")
		return
	}
	folder := externalArg(args, "run_folder")
	record := map[string]any{}
	if err := externalScriptRunRecord(r.Context(), item.WorkspacePath, folder, record); err != nil {
		externalFailure(w, err)
		return
	}
	values := map[string]any{}
	found, err := externalScriptReadJSON(r.Context(), item.WorkspacePath, path.Join("runs", folder, "inputs.json"), &values)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if !found || values == nil {
		externalError(w, 404, "not_found", "This run has no saved inputs. Start a new run with the values you want instead.")
		return
	}
	var header struct {
		InputPreset string `json:"input_preset"`
	}
	if _, err := externalScriptReadJSON(r.Context(), item.WorkspacePath, path.Join("runs", folder, "run_metadata.json"), &header); err != nil {
		externalFailure(w, err)
		return
	}
	changes, note, err := externalScriptChanges(r.Context(), item, folder)
	if err != nil {
		externalFailure(w, err)
		return
	}
	start := map[string]any{"inputs": values, "input_preset": header.InputPreset}
	// Preserve the ordinary query receipt/status code and session lifecycle.
	// This only adds the relation and bounded changed-file summary to its JSON.
	response := &internalResponseCapture{header: http.Header{}}
	api.externalRunProxy(response, r, "run_full_workflow", start, item)
	if response.status >= 400 {
		copyInternalResponse(w, response)
		return
	}
	var receipt map[string]any
	if err := json.Unmarshal(response.body.Bytes(), &receipt); err != nil || receipt == nil {
		copyInternalResponse(w, response)
		return
	}
	receipt["rerun_of"] = folder
	receipt["source_changes"] = changes
	receipt["input_preset"] = header.InputPreset
	receipt["inputs_reused"] = true
	receipt["note"] = "Started a new real run using the saved input values and current source. Database writes, app calls and sends happen again. Source changes compare the previous execution snapshot with the saved code at this request."
	if note != "" {
		receipt["source_changes_note"] = note
	}
	for key, value := range response.header {
		w.Header()[key] = value
	}
	w.Header().Set("Content-Type", "application/json")
	if response.status > 0 {
		w.WriteHeader(response.status)
	}
	_ = json.NewEncoder(w).Encode(receipt)
}

func externalScriptChanges(ctx context.Context, item DiscoveredWorkflow, folder string) ([]externalScriptSourceChange, string, error) {
	previous := path.Join("runs", folder, "execution", item.Manifest.ID, "code")
	guard := externalFileGuard(ctx, false)
	if !crewCapabilitySubtreeAllowed(guard, previous, false) || !crewCapabilitySubtreeAllowed(guard, "code", false) {
		return nil, "Source changes are outside this connection's file grants. The run still uses the saved input values.", nil
	}
	revisions := func(root string) (map[string]string, bool, error) {
		files, err := externalScriptRunFiles(ctx, item.WorkspacePath, root, "", 8)
		if err != nil {
			return nil, false, err
		}
		if files.Truncated || files.DepthLimited {
			return nil, false, nil
		}
		values := map[string]string{}
		for _, file := range files.Entries {
			if file.Type != "file" {
				continue
			}
			relative := strings.TrimPrefix(file.Path, root+"/")
			if !workflow.CrewScriptBundleFile(relative) {
				continue
			}
			if len(values) >= 64 {
				return nil, false, nil
			}
			read, err := externalFileRequest(ctx, wf.Request{Root: item.WorkspacePath, Operation: "read", Path: file.Path})
			if err != nil {
				return nil, false, err
			}
			if read.Exists {
				values[relative] = read.Revision
			}
		}
		_, main := values["main.py"]
		return values, main, nil
	}
	before, completeBefore, err := revisions(previous)
	if err != nil {
		return nil, "", err
	}
	after, completeAfter, err := revisions("code")
	if err != nil {
		return nil, "", err
	}
	if !completeBefore || !completeAfter {
		return nil, "The full source comparison is unavailable or exceeds the preview. Read the previous snapshot and saved source for details.", nil
	}
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	changes := []externalScriptSourceChange{}
	for name := range names {
		if before[name] != after[name] {
			changes = append(changes, externalScriptSourceChange{File: name, Before: before[name], After: after[name]})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].File < changes[j].File })
	return changes, "", nil
}
