package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const workflowContinuationRecoveryScanLimit = 200

type WorkflowContinuationRecoverySummary struct {
	Scanned int
	Queued  int
	Skipped int
	Errors  []string
}

type workflowContinuationStateFile struct {
	Path  string
	State *WorkflowContinuationState
}

func (s *WorkshopChatSession) RecoverPendingContinuations(ctx context.Context) WorkflowContinuationRecoverySummary {
	if s == nil || s.controller == nil {
		return WorkflowContinuationRecoverySummary{}
	}
	return s.controller.recoverPendingWorkflowContinuations(ctx)
}

func (hcpo *StepBasedWorkflowOrchestrator) recoverPendingWorkflowContinuations(ctx context.Context) WorkflowContinuationRecoverySummary {
	if ctx == nil {
		ctx = context.Background()
	}
	summary := WorkflowContinuationRecoverySummary{}
	if hcpo == nil {
		return summary
	}

	stateFiles := hcpo.discoverWorkflowContinuationStateFiles(ctx)
	summary.Scanned = len(stateFiles)
	for _, stateFile := range stateFiles {
		if ctx.Err() != nil {
			summary.Errors = append(summary.Errors, ctx.Err().Error())
			return summary
		}
		queued, err := hcpo.queueWorkflowContinuationRecovery(ctx, stateFile.State)
		if err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", stateFile.Path, err))
			continue
		}
		if queued == 0 {
			summary.Skipped++
			continue
		}
		summary.Queued += queued
	}
	if summary.Queued > 0 || len(summary.Errors) > 0 {
		hcpo.GetLogger().Info(fmt.Sprintf("🔁 Workflow continuation recovery scanned=%d queued=%d skipped=%d errors=%d", summary.Scanned, summary.Queued, summary.Skipped, len(summary.Errors)))
	}
	return summary
}

func (hcpo *StepBasedWorkflowOrchestrator) discoverWorkflowContinuationStateFiles(ctx context.Context) []workflowContinuationStateFile {
	workspacePath := strings.TrimSpace(hcpo.GetWorkspacePath())
	if workspacePath == "" {
		return nil
	}
	seen := make(map[string]struct{})
	stateFiles := make([]workflowContinuationStateFile, 0)
	appendRunFolder := func(runFolder string) {
		if len(stateFiles) >= workflowContinuationRecoveryScanLimit {
			return
		}
		hcpo.appendWorkflowContinuationStateFiles(ctx, strings.TrimSpace(runFolder), seen, &stateFiles)
	}

	if strings.TrimSpace(hcpo.selectedRunFolder) != "" {
		appendRunFolder(hcpo.selectedRunFolder)
	}

	runsRoot := filepath.Join(workspacePath, "runs")
	runDirs, _ := hcpo.ListWorkspaceDirectories(ctx, runsRoot)
	sort.Strings(runDirs)
	sort.Sort(sort.Reverse(sort.StringSlice(runDirs)))
	for _, runDir := range runDirs {
		appendRunFolder(runDir)
		childRoot := filepath.Join(runsRoot, runDir)
		childDirs, _ := hcpo.ListWorkspaceDirectories(ctx, childRoot)
		sort.Strings(childDirs)
		sort.Sort(sort.Reverse(sort.StringSlice(childDirs)))
		for _, childDir := range childDirs {
			appendRunFolder(filepath.Join(runDir, childDir))
		}
	}
	return stateFiles
}

func (hcpo *StepBasedWorkflowOrchestrator) appendWorkflowContinuationStateFiles(ctx context.Context, runFolder string, seen map[string]struct{}, out *[]workflowContinuationStateFile) {
	if runFolder == "" || len(*out) >= workflowContinuationRecoveryScanLimit {
		return
	}
	logsPath := filepath.Join(hcpo.GetWorkspacePath(), "runs", runFolder, "logs")
	stepDirs, _ := hcpo.ListWorkspaceDirectories(ctx, logsPath)
	sort.Strings(stepDirs)
	sort.Sort(sort.Reverse(sort.StringSlice(stepDirs)))
	for _, stepDir := range stepDirs {
		if len(*out) >= workflowContinuationRecoveryScanLimit {
			return
		}
		statePath := filepath.Join(logsPath, stepDir, workflowContinuationStateFilename)
		if _, ok := seen[statePath]; ok {
			continue
		}
		seen[statePath] = struct{}{}
		content, err := hcpo.ReadWorkspaceFile(ctx, statePath)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		var state WorkflowContinuationState
		if err := json.Unmarshal([]byte(content), &state); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to parse workflow continuation state %s: %v", statePath, err))
			continue
		}
		if strings.TrimSpace(state.RunFolder) == "" {
			state.RunFolder = runFolder
		}
		if strings.TrimSpace(state.StepID) == "" {
			state.StepID = stepDir
		}
		*out = append(*out, workflowContinuationStateFile{Path: statePath, State: &state})
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) queueWorkflowContinuationRecovery(ctx context.Context, state *WorkflowContinuationState) (int, error) {
	if state == nil {
		return 0, nil
	}
	pendingPhases := state.PendingRecoveryPhases()
	if len(pendingPhases) == 0 {
		return 0, nil
	}
	queued := 0
	hcpo.withWorkflowContinuationRunFolder(state.RunFolder, func() {
		for _, phase := range pendingPhases {
			switch phase {
			case workflowContinuationPhaseLearningAgent, workflowContinuationPhaseKBUpdateAgent, workflowContinuationPhaseKBReview, workflowContinuationPhaseDirectLearning:
				hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, phase, workflowContinuationStatusSkipped, "automatic Goal reflection retired; explicit task contributions remain available", nil)
			}
		}
	})
	return queued, nil
}
