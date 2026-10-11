package step_based_workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func workflowSafeIDPart(s string, fallback string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return fallback
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		isSafe := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isSafe {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return fallback
	}
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
		if out == "" {
			return fallback
		}
	}
	return out
}

func parentAgentArtifactRoot(parentStepID, parentStepPath string) string {
	parentStepPath = strings.TrimSpace(parentStepPath)
	if strings.Contains(parentStepPath, string(filepath.Separator)) {
		return filepath.Clean(parentStepPath)
	}
	return workflowSafeIDPart(parentStepID, workflowSafeIDPart(parentStepPath, "agent"))
}

func subAgentCallID(executionID, taskID string, startedAtUnixNano int64) string {
	if id := workflowSafeIDPart(executionID, ""); id != "" {
		return id
	}
	taskPart := workflowSafeIDPart(taskID, "call")
	return fmt.Sprintf("%s-%d", taskPart, startedAtUnixNano)
}

func todoSubAgentArtifactFolderName(parentStepID, parentStepPath, routeID, callID string, scripted bool) string {
	root := parentAgentArtifactRoot(parentStepID, parentStepPath)
	routePart := workflowSafeIDPart(routeID, "route")
	callPart := workflowSafeIDPart(callID, "call")
	if scripted {
		return filepath.Join(root, "scripts", "routes", routePart, "calls", callPart)
	}
	return filepath.Join(root, "agents", routePart, "calls", callPart)
}

func genericAgentArtifactFolderName(parentStepID, parentStepPath, callID string) string {
	return filepath.Join(
		parentAgentArtifactRoot(parentStepID, parentStepPath),
		"agents", "generic", "calls", workflowSafeIDPart(callID, "call"),
	)
}

// agentSequenceRouteRoot returns the persistent agent-route directory for a
// nested call. session.json lives here while each invocation writes beneath
// calls/<call-id>/. Top-level sequences return no route root.
func agentSequenceRouteRoot(stepPath string) string {
	clean := filepath.Clean(strings.TrimSpace(stepPath))
	parts := strings.Split(clean, string(filepath.Separator))
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] == "calls" && i > 0 && parts[i-1] != "items" && parts[i-1] != "routes" {
			return filepath.Join(parts[:i]...)
		}
	}
	return ""
}

func nestedArtifactParentRoot(stepPath string) string {
	clean := filepath.Clean(strings.TrimSpace(stepPath))
	parts := strings.Split(clean, string(filepath.Separator))
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "agents" || parts[i] == "scripts" {
			if i == 0 {
				return ""
			}
			return filepath.Join(parts[:i]...)
		}
	}
	return ""
}

func nestedArtifactDelegationDepth(stepPath string) int {
	depth := 0
	for _, part := range strings.Split(filepath.Clean(strings.TrimSpace(stepPath)), string(filepath.Separator)) {
		if part == "agents" {
			depth++
		}
	}
	return depth
}

func (hcpo *StepBasedWorkflowOrchestrator) cleanupExecutionArtifactsForStepPath(ctx context.Context, stepPath string, stepID string) error {
	if hcpo.selectedRunFolder == "" {
		return fmt.Errorf("selectedRunFolder not set - cannot cleanup execution artifacts")
	}
	if strings.TrimSpace(stepPath) == "" {
		return fmt.Errorf("stepPath not set - cannot cleanup execution artifacts")
	}

	runWorkspacePath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	logsWorkspacePath := fmt.Sprintf("%s/logs", runWorkspacePath)

	folderNames := make([]string, 0, 2)
	seen := map[string]struct{}{}
	addFolderName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		folderNames = append(folderNames, name)
	}
	addFolderName(getArtifactFolderName(stepID, stepPath))
	addFolderName(stepPath)

	for _, folderName := range folderNames {
		executionFolderPath := fmt.Sprintf("%s/%s", executionWorkspacePath, folderName)
		hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning execution artifact folder: %s", executionFolderPath))
		if err := hcpo.CleanupDirectory(ctx, executionFolderPath, fmt.Sprintf("execution/%s", folderName)); err != nil {
			return fmt.Errorf("failed to cleanup execution artifact folder %s: %w", folderName, err)
		}

		logsFolderPath := fmt.Sprintf("%s/%s", logsWorkspacePath, folderName)
		if err := hcpo.archiveLogsFolder(ctx, logsFolderPath, folderName); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to archive logs for execution artifact folder %s: %v", folderName, err))
		}
	}

	return nil
}
