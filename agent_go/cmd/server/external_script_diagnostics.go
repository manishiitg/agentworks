package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

const externalScriptLogTailBytes = 16 << 10

type externalScriptLog struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Tail      string `json:"tail"`
	Truncated bool   `json:"truncated"`
}

// Logs use the same project and file grants as normal reads. Open through the
// canonical anchored filesystem, then seek to the tail rather than buffering
// an entire program log (which may be much larger than a text file reply).
func externalScriptLogTail(ctx context.Context, workspace, run, name string, budget int) (externalScriptLog, error) {
	result := externalScriptLog{Path: name}
	clean, err := wf.CleanRelative(name)
	if err != nil || clean != name || !strings.HasPrefix(name, path.Join("runs", run)+"/") ||
		externalPathPrivate(false, name) || !externalFileGuard(ctx, false).Allows(name, false) {
		return result, &externalUpstreamError{403, "This log is outside the connection's folder grants."}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, relative, err := wf.OpenWorkflowDirectory(getWorkspaceDocsAbsPath(), workspace, name)
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err := externalNoSymlinks(root, relative); err != nil {
		return result, err
	}
	file, err := root.Open(relative)
	if err != nil {
		return result, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !stat.Mode().IsRegular() {
		return result, &externalUpstreamError{400, "Choose a program log file."}
	}
	result.Size = stat.Size()
	start := max(int64(0), result.Size-int64(budget))
	data, err := io.ReadAll(io.NewSectionReader(file, start, int64(budget)))
	if err != nil {
		return result, err
	}
	result.Tail = strings.ToValidUTF8(string(data), "")
	result.Truncated = start > 0
	return result, nil
}

func externalScriptReadJSON(ctx context.Context, workspace, name string, target any) (bool, error) {
	file, err := externalFileRequest(ctx, wf.Request{Root: workspace, Path: name, Operation: "read"})
	if err != nil || !file.Exists {
		return false, err
	}
	if file.Encoding != "utf-8" {
		return false, &externalUpstreamError{413, "This run record exceeds the text read limit; inspect its full file."}
	}
	decoder := json.NewDecoder(strings.NewReader(file.Content))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return false, &externalUpstreamError{502, "This saved run record could not be read."}
	}
	return true, nil
}

func externalScriptRunFiles(ctx context.Context, workspace, directory, glob string, depth int) (wf.Result, error) {
	result, err := externalFileRequest(ctx, wf.Request{Root: workspace, Operation: "list", Path: directory, Glob: glob, Depth: depth, Limit: 200})
	var upstream *externalUpstreamError
	if errors.Is(err, fs.ErrNotExist) || errors.As(err, &upstream) && upstream.status == 404 {
		return wf.Result{}, nil // A running or pre-launch failure may not have created this record yet.
	}
	return result, err
}

// Convert stored server paths into issued workflow-relative paths. A planted
// receipt cannot point the caller outside this run, even in the same Crew.
func externalScriptRunPath(workspace, run, name string) string {
	prefix := path.Join("runs", run) + "/"
	if strings.HasPrefix(name, workspace+"/") {
		name = strings.TrimPrefix(name, workspace+"/")
	} else if filepath.IsAbs(name) {
		root, _, err := crewroot.Resolve(getWorkspaceDocsAbsPath(), workspace+"/runs/"+run)
		if err != nil || !crewroot.Within(root, name) {
			return ""
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return ""
		}
		name = path.Join("runs", run, filepath.ToSlash(relative))
	}
	clean, err := wf.CleanRelative(name)
	if err != nil || clean != name || !strings.HasPrefix(name, prefix) || externalPathPrivate(false, name) {
		return ""
	}
	return name
}

func (api *StreamingAPI) externalScriptLogs(w http.ResponseWriter, r *http.Request, item DiscoveredWorkflow, folder string) {
	result := map[string]any{"workflow_id": item.Manifest.ID, "run_folder": folder}
	if err := workflow.ValidateWorkflowRunFolder(folder); err != nil {
		externalError(w, 400, "invalid_arguments", "Choose a saved run from this workflow.")
		return
	}
	if _, err := externalFileRequest(r.Context(), wf.Request{Root: item.WorkspacePath, Operation: "list", Path: path.Join("runs", folder), Depth: 1, Limit: 1}); err != nil {
		externalFailure(w, err)
		return
	}
	if err := externalScriptRunRecord(r.Context(), item.WorkspacePath, folder, result); err != nil {
		externalFailure(w, err)
		return
	}
	var inputValues map[string]any
	inputsPath := path.Join("runs", folder, "inputs.json")
	found, err := externalScriptReadJSON(r.Context(), item.WorkspacePath, inputsPath, &inputValues)
	if err != nil {
		externalFailure(w, err)
		return
	}
	result["inputs_path"] = inputsPath
	if found {
		raw, _ := json.Marshal(inputValues)
		if len(raw) <= externalScriptLogTailBytes {
			result["inputs"] = inputValues
		} else {
			result["inputs_note"] = "Saved inputs are larger than the preview. Read inputs_path for the full typed values."
		}
	}
	var runHeader struct {
		InputPreset string `json:"input_preset"`
	}
	_, err = externalScriptReadJSON(r.Context(), item.WorkspacePath, path.Join("runs", folder, "run_metadata.json"), &runHeader)
	if err != nil {
		externalFailure(w, err)
		return
	}
	result["input_preset"] = runHeader.InputPreset
	logs, err := externalScriptRunFiles(r.Context(), item.WorkspacePath, path.Join("runs", folder, "execution"), "**/script-output-*.log", 8)
	if err != nil {
		externalFailure(w, err)
		return
	}
	sort.Slice(logs.Entries, func(i, j int) bool { return logs.Entries[i].Path > logs.Entries[j].Path })
	tails := []externalScriptLog{}
	traceback := ""
	for _, entry := range logs.Entries {
		if entry.Type != "file" || len(tails) >= 4 {
			continue
		}
		budget := externalScriptLogTailBytes
		if len(tails) > 0 {
			budget = 4 << 10
		}
		log, err := externalScriptLogTail(r.Context(), item.WorkspacePath, folder, entry.Path, budget)
		if err != nil {
			externalFailure(w, err)
			return
		}
		tails = append(tails, log)
		if at := strings.LastIndex(log.Tail, "Traceback (most recent call last)"); at >= 0 {
			traceback = log.Tail[at:]
		}
	}
	result["script_logs"] = tails
	result["logs_truncated"] = logs.Truncated || len(logs.Entries) > len(tails)
	metadata, _ := result["metadata"].(RunMetadata)
	failed := []workflow.PythonStepEvidence{}
	for _, step := range metadata.PythonSteps {
		if step.Status == "failed" {
			failed = append(failed, step)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].StartedAt < failed[j].StartedAt })
	result["failure"] = map[string]any{"status": metadata.Status, "steps": failed, "traceback": traceback}
	agents, err := externalScriptAgentResults(r.Context(), item.WorkspacePath, folder)
	if err != nil {
		externalFailure(w, err)
		return
	}
	result["agents"] = agents
	result["note"] = "Python step labels share the script's stdout/stderr log; each recorded step has its own status and duration. Read the returned paths for full files. Edit the saved source, then use runs rerun to repeat real work with these saved input values."
	externalJSON(w, result)
}

func externalScriptAgentResults(ctx context.Context, workspace, folder string) ([]map[string]any, error) {
	files, err := externalScriptRunFiles(ctx, workspace, path.Join("runs", folder, "helpers"), "**/*.json", 3)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, entry := range files.Entries {
		if entry.Type != "file" || len(rows) >= 50 {
			continue
		}
		var saved struct {
			Reply struct {
				OK           bool   `json:"ok"`
				Error        string `json:"error"`
				Answer       string `json:"answer_file"`
				Log          string `json:"log_file"`
				Kind         string `json:"log_kind"`
				Conversation string `json:"conversation_id"`
			} `json:"reply"`
		}
		_, err := externalScriptReadJSON(ctx, workspace, entry.Path, &saved)
		if err != nil {
			return nil, err
		}
		if saved.Reply.Answer == "" && saved.Reply.Kind != "provider_transcript" {
			continue
		}
		row := map[string]any{"ok": saved.Reply.OK, "conversation_id": saved.Reply.Conversation, "receipt_path": entry.Path, "log_kind": saved.Reply.Kind}
		if saved.Reply.Error != "" {
			text := []rune(saved.Reply.Error)
			row["error"] = string(text[:min(1000, len(text))])
		}
		for key, raw := range map[string]string{"answer_file": saved.Reply.Answer, "provider_log": saved.Reply.Log} {
			name := externalScriptRunPath(workspace, folder, raw)
			if name != "" && externalFileGuard(ctx, false).Allows(name, false) {
				row[key] = name
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
