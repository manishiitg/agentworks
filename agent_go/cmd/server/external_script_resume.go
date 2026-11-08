package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// One process owns a run. This covers the interval between accepting resume
// and the normal runtime recording its new running status.
var pythonResumeClaims sync.Map

func canManagePythonResume(ctx context.Context, manifest *WorkflowManifest) bool {
	claims := GetUserFromContext(ctx)
	access := workflowAccessForManifest(claims, manifest)
	return claims != nil && userAccessForClaims(claims).CanEdit && (access == WorkflowAccessOwner || access == WorkflowAccessWrite)
}

// This is current UI eligibility, not durable run state. Admission still
// verifies captured bytes and closes races after the person presses Resume.
func setRunResumeCapability(ctx context.Context, manifest *WorkflowManifest, workspace, folder string, metadata *RunMetadata) {
	allowed := metadata != nil && manifest != nil && manifest.crewExecutable != nil && manifest.crewExecutable.Kind == "script" && canManagePythonResume(ctx, manifest) && metadata.Status == "failed" && len(metadata.SourceSnapshotHash) == 64 && len(metadata.InputsSnapshotHash) == 64 && externalFileGuard(ctx, false).Allows("runs/"+folder, false)
	if allowed {
		_, busy := pythonResumeClaims.Load(workflow.GetPromptDocsRoot() + "\x00" + workspace + "\x00" + folder)
		allowed = !busy
	}
	if metadata != nil {
		metadata.CanResume = &allowed
	}
}

func (api *StreamingAPI) externalScriptResume(w http.ResponseWriter, r *http.Request, item DiscoveredWorkflow, args map[string]any) {
	if !canManagePythonResume(r.Context(), item.Manifest) {
		externalError(w, 403, "forbidden", "Only this Crew's Builder can resume a failed run. Start a new run or ask its owner to resume it.")
		return
	}
	if !externalWorkflowIsScript(item) {
		externalError(w, 400, "invalid_arguments", "Choose a saved Python workflow.")
		return
	}
	for _, field := range []string{"inputs", "input_preset", "session_id", "source_hash", "resume_from_step"} {
		if _, supplied := args[field]; supplied {
			externalError(w, 400, "invalid_arguments", "Resume keeps this run's frozen source and inputs. Start a new run to change them.")
			return
		}
	}
	folder := externalArg(args, "run_folder")
	if !externalFileGuard(r.Context(), false).Allows("runs/"+folder, false) {
		externalError(w, 403, "forbidden", "This run is outside this connection's folder grants.")
		return
	}
	claim := workflow.GetPromptDocsRoot() + "\x00" + item.WorkspacePath + "\x00" + folder
	if _, running := pythonResumeClaims.LoadOrStore(claim, true); running {
		externalError(w, http.StatusConflict, "run_already_running", "This run is already running. Open its progress or wait for it to finish.")
		return
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { pythonResumeClaims.Delete(claim) }) }
	accepted := false
	defer func() {
		if !accepted {
			release()
		}
	}()
	for _, execution := range api.listRunningWorkflowExecutionsForWorkspace(item.WorkspacePath) {
		if execution.RunFolder == folder {
			externalError(w, http.StatusConflict, "run_already_running", "This run is already running. Open its progress or wait for it to finish.")
			return
		}
	}
	resume, err := workflow.ReadPythonRunResume(workflow.GetPromptDocsRoot(), item.WorkspacePath, folder, item.Manifest.ID)
	if err != nil {
		externalError(w, 400, "resume_unavailable", err.Error())
		return
	}
	resume.Release = release
	options := &workflow.ExecutionOptions{SelectedRunFolder: folder, RunFolderAllocated: true, InputPreset: resume.InputPreset, ResolvedInputs: resume.Values, ExecutionStrategy: "start_from_beginning_no_human", PythonResume: resume}
	sessionID, _, ok := api.externalRunSessionSetup(w, r, map[string]any{}, item)
	if !ok {
		return
	}
	query := QueryRequest{Query: "Resume: " + item.Manifest.Label, AgentMode: "workflow", PresetQueryID: item.Manifest.ID, SelectedFolder: item.WorkspacePath, TriggeredBy: "external", SessionTitle: "Resumed run", ExecutionOptions: &ExecutionOptions{SelectedRunFolder: folder, ExecutionStrategy: options.ExecutionStrategy}}
	r = r.WithContext(context.WithValue(context.WithoutCancel(r.Context()), directWebhookExecutionKey{}, options))
	response := &internalResponseCapture{header: http.Header{}}
	api.forwardRunTurn(response, r, query, sessionID, item)
	if response.status >= 400 {
		copyInternalResponse(w, response)
		return
	}
	accepted = true
	var receipt map[string]any
	if json.Unmarshal(response.body.Bytes(), &receipt) != nil || receipt == nil {
		copyInternalResponse(w, response)
		return
	}
	receipt["run_folder"] = folder
	receipt["resume_of"] = resume.PreviousExecutionID
	receipt["source_snapshot_hash"] = resume.SourceHash
	receipt["inputs_reused"] = true
	receipt["note"] = "Resuming the same captured source and inputs in this run folder. Successful explicitly keyed agent/script calls may reuse their recorded result; other Python code and app calls execute normally."
	for key, value := range response.header {
		w.Header()[key] = value
	}
	if response.status > 0 {
		w.WriteHeader(response.status)
	}
	_ = json.NewEncoder(w).Encode(receipt)
}
