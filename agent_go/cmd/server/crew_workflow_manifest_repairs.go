package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Refresh locations, never identities or access. Only the target's main owner
// may repair it, even when the caller can otherwise use a shared Crew.
func refreshedWorkflowAttachments(userID string, attachments []workflowtypes.CrewAttachment) ([]workflowtypes.CrewAttachment, error) {
	result := append([]workflowtypes.CrewAttachment(nil), attachments...)
	if len(result) == 0 {
		return result, nil
	}
	projects, err := defaultProjectOwners().All()
	if err != nil {
		return nil, err
	}
	for i, attachment := range result {
		if err := workflowtypes.ValidateCrewAttachmentBinding(attachment); err != nil {
			return nil, fmt.Errorf("Crew connection %q: %w", attachment.Alias, err)
		}
		var matches []projectOwnerRecord
		for _, project := range projects {
			if project.ProjectID == attachment.CrewProjectID && normalizeInternalProfileID(attachment.CrewProfileID) == project.Product {
				matches = append(matches, project)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("Crew connection %q: its saved target project is missing or ambiguous; restore that project or choose its intended connection in chat. Nothing was changed", attachment.Alias)
		}
		target := matches[0]
		if target.OwnerID != sanitizeUserIDForPath(userID) {
			return nil, fmt.Errorf("Crew connection %q: you do not own its target project; ask its owner to repair the connection. Nothing was changed", attachment.Alias)
		}
		if err := validateProjectStorageRecord(target); err != nil {
			return nil, fmt.Errorf("Crew connection %q: its registered location needs repair: %w", attachment.Alias, err)
		}
		result[i].CrewWorkspacePath = projectStorageRoot(target)
	}
	return result, nil
}

func workflowBrowserModeRepair(mode string) (string, bool) {
	// PLAT-788's one-off migrate-browser-modes.py policy. This does not
	// select a browser account or turn an explicit off state back on.
	if mode == "auto" || mode == "headless" && !browser.ManagedEnabled() || mode == "cdp" && browser.ManagedEnabled() {
		return browser.DefaultMode(), true
	}
	return mode, false
}

func repairWorkflowManifest(ctx context.Context, root string, current *WorkflowManifest, attachments, browserMode bool, reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("describe the repair in reason")
	}
	pending := workflowVersionUpgradePlan(current)
	if current.Version != workflowContractPortableVersion && (len(pending) == 0 || pending[0].to != workflowContractPortableVersion) {
		return "", fmt.Errorf("complete the earlier updates first; this repair belongs to the portable workflow update")
	}
	var field string
	var before, after any
	if attachments {
		field, before = "crew_attachments", current.CrewAttachments
		updated, err := refreshedWorkflowAttachments(GetUserIDFromContext(ctx), current.CrewAttachments)
		if err != nil {
			return "", err
		}
		current.CrewAttachments, after = updated, updated
	} else if browserMode {
		mode, changed := workflowBrowserModeRepair(current.Capabilities.BrowserMode)
		if !changed {
			return "Browser setting already uses a current mode; nothing changed.", nil
		}
		field, before, after = "capabilities.browser_mode", current.Capabilities.BrowserMode, mode
		current.Capabilities.BrowserMode = mode
	}
	if err := WriteWorkflowManifest(ctx, root, current); err != nil {
		return "", fmt.Errorf("repair was not saved: %w", err)
	}
	if err := stepworkflow.RecordWorkflowManifestRepair(ctx, root, field, before, after, reason, readWorkflowUpgradeFile, writeFileToWorkspace); err != nil {
		return "", fmt.Errorf("repair saved, but its change log failed: %w", err)
	}
	return "Repair saved without changing the version or starting a run. Check status, then stamp only after every check passes.", nil
}
