package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// All controller entry points run the same preflight and interpreter command.
func scriptedCommand(main string, args []string) string {
	command := "python3 -B -c " + shellQuotePath(scriptedBundlePreflight) + " " + shellQuotePath(main) + " && python3 -B " + shellQuotePath(main)
	for _, arg := range args {
		command += " " + shellQuotePath(arg)
	}
	return command
}

// Layout is persisted in workflow.json, never inferred from directory existence.
// Unknown versions fail instead of executing from an unintended source directory.
func (hcpo *StepBasedWorkflowOrchestrator) loadCodeLayout(ctx context.Context) error {
	content, err := hcpo.ReadWorkspaceFile(ctx, "workflow.json")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(err.Error(), "404") {
			hcpo.codeLayoutVersion.Store(0)
			hcpo.platformStoresDisabled.Store(false)
			return nil
		}
		return fmt.Errorf("read code layout: %w", err)
	}
	var m struct {
		Version int    `json:"code_layout_version"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		return fmt.Errorf("parse code layout: %w", err)
	}
	if m.Version < 0 || m.Version > 1 {
		return fmt.Errorf("unsupported code_layout_version %d", m.Version)
	}
	if err := hcpo.configureExecutionProduct(m.Kind); err != nil {
		return err
	}
	hcpo.codeLayoutVersion.Store(int32(m.Version))
	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) usesCodeTree() bool {
	return hcpo.codeLayoutVersion.Load() == 1
}

// Review tools without a controller use the same persisted version selector.
func savedCodeDirectory(ctx context.Context, workspace, stepID string, readFile func(context.Context, string) (string, error)) string {
	content, err := readFile(ctx, normalizePathForWorkspaceAPI("workflow.json", workspace))
	var manifest struct {
		Version int `json:"code_layout_version"`
	}
	if err == nil && json.Unmarshal([]byte(content), &manifest) == nil && manifest.Version == 1 {
		return "code/" + stepID
	}
	return getScriptedDirRelPath(stepID)
}

func (hcpo *StepBasedWorkflowOrchestrator) scriptedSourceDir(stepID string) string {
	if hcpo.usesCodeTree() {
		return "code/" + stepID
	}
	return getScriptedDirRelPath(stepID)
}

// Returns workspace-qualified path, matching getExecutionFolderPath's contract.
func (hcpo *StepBasedWorkflowOrchestrator) scriptedWorkingDir(stepID, executionPath string) string {
	if hcpo.usesCodeTree() {
		return filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), hcpo.scriptedSourceDir(stepID)))
	}
	return executionPath + "/code"
}

func (hcpo *StepBasedWorkflowOrchestrator) codeRuntimeEnv(env map[string]string) map[string]string {
	if env == nil {
		env = map[string]string{}
	}
	if !hcpo.platformStoresEnabled() {
		delete(env, "DB_PATH")
		for key := range env {
			if strings.HasPrefix(key, "WORKFLOW_KB_") {
				delete(env, key)
			}
		}
		env["WORKFLOW_KB_ACCESS"] = KBAccessNone
		env[workflowDBAccessEnv] = DBAccessNone
	}
	delete(env, "WORKFLOW_TRIGGER_INPUT_FILE")
	delete(env, "WORKFLOW_TRIGGER_CONTEXT_FILE")
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.TriggerContextFile != "" {
		env["WORKFLOW_TRIGGER_CONTEXT_FILE"] = filepath.Join(GetPromptDocsRoot(), opts.TriggerContextFile)
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.WebhookInputFile != "" {
		env["WORKFLOW_TRIGGER_INPUT_FILE"] = filepath.Join(GetPromptDocsRoot(), opts.WebhookInputFile)
	}
	if hcpo.usesCodeTree() {
		root := filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), "code")
		env["WORKFLOW_CODE_ROOT"] = root
		deps := filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), ".sandbox-cache", "python-packages")
		env["WORKFLOW_CODE_DEPS"] = deps
		env["PYTHONPATH"] = root + string(filepath.ListSeparator) + deps
	}
	return env
}
