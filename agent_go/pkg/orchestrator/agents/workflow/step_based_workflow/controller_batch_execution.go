package step_based_workflow

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// runResolvedExecution executes one invocation with the inputs already recorded
// for this run. A saved input label never participates in execution identity.
func (hcpo *StepBasedWorkflowOrchestrator) runResolvedExecution(ctx context.Context, steps []PlanStepInterface, execCtx *ExecutionContext) error {
	folder := hcpo.selectedRunFolder
	if err := hcpo.markRunMetadataStarted(ctx, folder); err != nil {
		return err
	}
	manager := NewExecutionManager(hcpo)
	progress, _ := hcpo.loadStepProgress(ctx)
	setup, err := manager.PrepareExecution(ctx, hcpo.GetExecutionOptions(), progress, len(steps), folder)
	if err != nil {
		return err
	}
	setup.VariableValues = hcpo.variableValues
	if execCtx != nil {
		setup.Context.HumanInputs = cloneWorkflowStringMap(execCtx.HumanInputs)
	}
	if err := manager.ApplyCleanup(ctx, setup); err != nil {
		return err
	}
	hcpo.currentGroupName = ""
	hcpo.currentGroupIdx = 0
	hcpo.totalGroups = 0
	started := time.Now()
	err = hcpo.executePreparedRun(ctx, steps, 1, setup)
	status := "completed"
	if err != nil {
		status = "failed"
	}
	if errors.Is(err, ErrWorkflowWaitingForCapacity) {
		if metadataErr := hcpo.upsertRunMetadata(ctx, folder, func(meta map[string]interface{}) {
			meta["status"] = "waiting_for_capacity"
			delete(meta, "completed_at")
		}); metadataErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("persist capacity status: %v", metadataErr))
		}
	} else {
		hcpo.finalizeRunMetadata(ctx, folder, status, started, time.Now())
		hcpo.finishPythonAgentSessions(status)
	}
	if flushErr := hcpo.waitForTokenPersistence(); flushErr != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("Run finished, but cost persistence did not: %v", flushErr))
	}
	return err
}

func (hcpo *StepBasedWorkflowOrchestrator) waitForTokenPersistence() error {
	cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge)
	if !ok {
		return nil
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return cab.WaitForTokenPersistence(flushCtx)
}

// sanitizeNameForFolder sanitizes a group name for use in folder paths
// - Removes/replaces special characters that aren't safe for filesystem
// - Normalizes whitespace and converts to lowercase
// - Removes multiple consecutive dashes
// - Falls back to empty string if result is invalid
func (hcpo *StepBasedWorkflowOrchestrator) sanitizeNameForFolder(name string) string {
	return inputSetFolderName(name)
}

func inputSetFolderName(name string) string {
	if name == "" {
		return ""
	}

	sanitized := strings.TrimSpace(name)

	// Replace spaces with dashes
	sanitized = strings.ReplaceAll(sanitized, " ", "-")

	// Remove or replace special characters that aren't safe for folder names
	// Keep: letters, numbers, dashes, underscores
	// Remove: colons, slashes, backslashes, pipes, etc.
	specialCharPattern := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	sanitized = specialCharPattern.ReplaceAllString(sanitized, "-")

	// Normalize multiple consecutive dashes to single dash
	multiDashPattern := regexp.MustCompile(`-+`)
	sanitized = multiDashPattern.ReplaceAllString(sanitized, "-")

	// Remove leading/trailing dashes
	sanitized = strings.Trim(sanitized, "-")

	// Convert to lowercase for consistency
	sanitized = strings.ToLower(sanitized)

	if sanitized == "" {
		return ""
	}

	return sanitized
}

// sanitizeDisplayNameForFolder is an alias for backward compatibility
func (hcpo *StepBasedWorkflowOrchestrator) sanitizeDisplayNameForFolder(name string) string {
	return hcpo.sanitizeNameForFolder(name)
}
