package step_based_workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PythonRunResume is resolved by the authenticated server, never decoded from
// public query JSON. It binds one failed run to its captured source and inputs.
type PythonRunResume struct {
	Release             func() // The server releases one admitted attempt when its runtime ends.
	Script              CrewScriptConfiguration
	SourceHash          string
	InputsHash          string
	Values              map[string]string
	InputPreset         string
	PreviousExecutionID string
}

const pythonResumeNewRun = "This run's frozen source, hash or inputs are unavailable or changed. Start a new run."

// ReadPythonRunResume reads only the named workflow's stored run. The caller
// must first authorize owner/editor and run access; no path comes from a model.
func ReadPythonRunResume(docs, workspace, folder, scriptID string) (*PythonRunResume, error) {
	if err := ValidateWorkflowRunFolder(folder); err != nil {
		return nil, fmt.Errorf("Choose a failed run from this workflow.")
	}
	view := workflowfiles.WorkflowFS{Docs: docs, Root: workspace}
	base := path.Join("runs", folder)
	metadata, err := fs.ReadFile(view, path.Join(base, "run_metadata.json"))
	if err != nil {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	var header struct {
		Status      string `json:"status"`
		ExecutionID string `json:"execution_id"`
		SourceHash  string `json:"source_snapshot_hash"`
		InputsHash  string `json:"inputs_snapshot_hash"`
		InputPreset string `json:"input_preset"`
	}
	if json.Unmarshal(metadata, &header) != nil || len(header.SourceHash) != 64 || len(header.InputsHash) != 64 || header.ExecutionID == "" {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	if header.Status != "failed" {
		return nil, fmt.Errorf("Only a failed run can resume. Inspect this run or start a new one.")
	}
	if scriptID == "" || path.Base(scriptID) != scriptID || strings.ContainsAny(scriptID, "/\\.") {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	source := path.Join(base, "execution", scriptID, "code")
	hash, err := pythonBundleFingerprint(view, source)
	if err != nil || hash != header.SourceHash {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	files := map[string][]byte{}
	err = fs.WalkDir(view, source, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "__pycache__" {
				return fs.SkipDir
			}
			return nil
		}
		relative := strings.TrimPrefix(name, source+"/")
		if !CrewScriptBundleFile(relative) {
			return fmt.Errorf("invalid captured source")
		}
		data, err := fs.ReadFile(view, name)
		if err == nil {
			files[relative] = data
		}
		return err
	})
	if err != nil || len(files["main.py"]) == 0 || len(files["script.json"]) == 0 {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	var definition CrewScriptDefinition
	if json.Unmarshal(files["script.json"], &definition) != nil || definition.ID != scriptID {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	rawInputs, err := fs.ReadFile(view, path.Join(base, "inputs.json"))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(rawInputs)) != header.InputsHash {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	decoder := json.NewDecoder(bytes.NewReader(rawInputs))
	decoder.UseNumber()
	var typed map[string]interface{}
	if decoder.Decode(&typed) != nil || typed == nil {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	values := map[string]string{}
	for name, value := range typed {
		found := false
		for _, input := range definition.Inputs {
			if input.Name == name {
				values[name], err = WorkflowInputValue(input, value)
				found = true
				break
			}
		}
		if err != nil || !found {
			return nil, fmt.Errorf("%s", pythonResumeNewRun)
		}
	}
	if ValidateWorkflowLaunchInputs(definition.InputDefinitions(), values) != nil {
		return nil, fmt.Errorf("%s", pythonResumeNewRun)
	}
	return &PythonRunResume{Script: CrewScriptConfiguration{Definition: definition, Files: files}, SourceHash: header.SourceHash, InputsHash: header.InputsHash, Values: values, InputPreset: header.InputPreset, PreviousExecutionID: header.ExecutionID}, nil
}

func (hcpo *StepBasedWorkflowOrchestrator) resumingPython() bool {
	return hcpo.executionOptions != nil && hcpo.executionOptions.PythonResume != nil
}

func (hcpo *StepBasedWorkflowOrchestrator) recordPythonResumeCall(ctx context.Context, key, action string, arguments map[string]interface{}, reused bool, reason string) error {
	return hcpo.upsertRunMetadata(ctx, hcpo.selectedRunFolder, func(meta map[string]interface{}) {
		calls, _ := meta["python_resume_calls"].([]interface{})
		source, _ := arguments["source_call"].(string)
		meta["python_resume_calls"] = append(calls, map[string]interface{}{
			"key": boundedRunEvidenceText(key), "action": action, "reused": reused, "reason": reason,
			"source_call": boundedRunEvidenceText(source), "execution_id": meta["execution_id"],
			"at": time.Now().UTC().Format(time.RFC3339Nano),
		})
	})
}
