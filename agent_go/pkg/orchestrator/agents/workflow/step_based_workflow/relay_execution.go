package step_based_workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ResolveRelayVariableValues reads flat Relay configuration. A frozen legacy
// trigger may still bind a group: its values are configuration, never a batch.
// No saved artifact is rewritten when resolving a published version.
func ResolveRelayVariableValues(manifest *VariablesManifest, legacyGroupName string) (map[string]string, error) {
	values := make(map[string]string)
	if manifest == nil {
		return values, nil
	}
	for _, v := range manifest.Variables {
		values[v.Name] = v.Value
	}
	if len(manifest.Groups) == 0 {
		return values, nil
	}
	if legacyGroupName == "" {
		if len(manifest.Groups) != 1 {
			return nil, fmt.Errorf("Relay has multiple legacy variable groups; migrate the chosen configuration into variables[].value before running the draft")
		}
		legacyGroupName = manifest.Groups[0].Name
	}
	for _, group := range manifest.Groups {
		if group.Name == legacyGroupName {
			for name, value := range group.Values {
				values[name] = value
			}
			return values, nil
		}
	}
	return nil, fmt.Errorf("Relay legacy configuration %q was not found", legacyGroupName)
}

func (hcpo *StepBasedWorkflowOrchestrator) relayVariableValues() (map[string]string, error) {
	legacy := ""
	if opts := hcpo.GetExecutionOptions(); opts != nil {
		legacy = opts.RelayLegacyGroupName
	}
	values, err := ResolveRelayVariableValues(hcpo.variablesManifest, legacy)
	if err != nil {
		return nil, err
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil {
		for name, value := range opts.WebhookVariables {
			values[name] = value
		}
	}
	return values, nil
}

// One invocation, its own progress records. Relays skip the variable-group/batch
// wrapper and use the same cleanup, session lifecycle and step executor.
func (hcpo *StepBasedWorkflowOrchestrator) runRelayExecution(ctx context.Context, steps []PlanStepInterface) (string, error) {
	if len(steps) == 0 {
		return "", fmt.Errorf("Relay graph has no steps")
	}
	values, err := hcpo.relayVariableValues()
	if err != nil {
		return "", err
	}
	opts := hcpo.GetExecutionOptions()
	folder := ""
	if opts != nil {
		if len(opts.EnabledGroupNames) > 0 {
			return "", fmt.Errorf("Relays use flat configuration, not variable groups")
		}
		folder = opts.SelectedRunFolder
	}
	// API invocations and resume use their server-bound folder. A fresh
	// Builder test gets its own folder rather than overwriting another test.
	if folder == "" || folder == currentWorkflowRunFolder {
		folder = "relay-" + workflowExecutionIDToken()
		if opts != nil {
			opts.SelectedRunFolder = folder
		}
	}
	// Old capacity checkpoints may have a nested group folder. Preserve the
	// recorded path on resume, but never allow traversal outside runs/.
	if filepath.IsAbs(folder) || strings.Contains(folder, "\\") || filepath.ToSlash(filepath.Clean(folder)) != folder || folder == "." || folder == ".." || strings.HasPrefix(folder, "../") {
		return "", fmt.Errorf("invalid Relay run folder %q", folder)
	}
	hcpo.currentGroupName, hcpo.currentGroupIdx, hcpo.totalGroups = "", 0, 0
	hcpo.SetSelectedRunFolder(folder)
	hcpo.ApplyWorkflowLogContext(hcpo.GetWorkspacePath(), "")
	if err := hcpo.createRunFolderStructure(ctx, hcpo.GetWorkspacePath()+"/runs/"+folder); err != nil {
		return "", err
	}
	if err := hcpo.markRunMetadataStarted(ctx, folder); err != nil {
		return "", fmt.Errorf("bind Relay run to executable plan revision: %w", err)
	}
	progress, _ := hcpo.loadStepProgress(ctx)
	manager := hcpo.GetExecutionManager()
	setup, err := manager.PrepareExecution(ctx, opts, progress, len(steps), folder)
	if err != nil {
		return "", err
	}
	setup.VariableValues = values
	if err := manager.ApplyCleanup(ctx, setup); err != nil {
		return "", err
	}
	manager.ApplyExecutionContext(setup)
	SyncExactVariablesToWorkspaceEnv(hcpo.BaseOrchestrator, values)
	started := time.Now()
	err = hcpo.executePreparedRun(ctx, steps, 1, setup)
	status := "completed"
	if err != nil {
		status = "failed"
	}
	if errors.Is(err, ErrWorkflowWaitingForCapacity) {
		// Capacity suspension is nonterminal; no completion timestamp yet.
		if metadataErr := hcpo.upsertRunMetadata(ctx, folder, func(meta map[string]interface{}) {
			meta["status"] = "waiting_for_capacity"
			delete(meta, "completed_at")
		}); metadataErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("persist Relay capacity status: %v", metadataErr))
		}
	} else {
		hcpo.finalizeRunMetadata(ctx, folder, status, started, time.Now())
	}
	if err != nil {
		return "", err
	}
	return "Relay execution completed.", nil
}
