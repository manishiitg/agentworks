package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func externalWorkflowIsScript(item DiscoveredWorkflow) bool {
	return item.Manifest != nil && item.Manifest.crewExecutable != nil && item.Manifest.crewExecutable.Kind == "script"
}

// Discovery has already selected the caller's readable workflows. The trusted
// binding, not an authored manifest kind, identifies a saved Python script.
func externalWorkflowReadView(ctx context.Context, item DiscoveredWorkflow, compact bool) (map[string]any, error) {
	view := externalCompactWorkflow(item)
	if !compact {
		raw, err := json.Marshal(externalWorkflowView(item))
		if err != nil {
			return nil, err
		}
		view = map[string]any{}
		if err := json.Unmarshal(raw, &view); err != nil {
			return nil, err
		}
	}
	if externalWorkflowIsScript(item) {
		definition, err := readCrewScript(ctx, item.WorkspacePath)
		if err != nil {
			return nil, err
		}
		view["kind"] = "script"
		view["inputs"] = definition.Inputs
		view["input_presets"] = definition.InputPresets
		view["secrets"] = definition.Secrets
	}
	return view, nil
}

// Run metadata is a public runtime projection, not the private helper transport.
// Read it through the same bounded file reader and connection grants as files.
// Checks/notices come from the canonical evidence reader used by the website.
func externalScriptRunRecord(ctx context.Context, item DiscoveredWorkflow, folder string, result map[string]any) error {
	workspace := item.WorkspacePath
	if err := workflow.ValidateWorkflowRunFolder(folder); err != nil {
		return &externalUpstreamError{400, "Choose a run from this workflow."}
	}
	if !externalFileGuard(ctx, false).Allows(path.Join("runs", folder), false) {
		return &externalUpstreamError{403, "Run evidence is outside this connection's folder grants."}
	}
	record, err := externalFileRequest(ctx, wf.Request{Root: workspace, Operation: "read", Path: path.Join("runs", folder, "run_metadata.json")})
	if err != nil {
		return err
	}
	if record.Exists {
		if record.Encoding != "utf-8" {
			return &externalUpstreamError{413, "Run record exceeds the text read limit."}
		}
		var metadata RunMetadata
		if err := json.Unmarshal([]byte(record.Content), &metadata); err != nil {
			return fmt.Errorf("read run record: %w", err)
		}
		setRunResumeCapability(ctx, item.Manifest, workspace, folder, &metadata)
		result["metadata"] = metadata
	}
	items, err := workflow.ReadPythonRunEvidence(workflow.GetPromptDocsRoot(), workspace, folder)
	if err != nil {
		return err
	}
	result["evidence"] = items
	return nil
}
