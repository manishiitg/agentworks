package server

import (
	"context"
	"fmt"
	"strings"
)

type workflowContractUpgradeListItem struct {
	Label         string `json:"label"`
	TargetVersion string `json:"target_version"`
	Details       string `json:"details"`
	AppliedAt     string `json:"applied_at,omitempty"`
}

func workflowContractUpgradeDetails(upgrade workflowVersionUpgrade) string {
	paragraphs := strings.Split(strings.TrimSpace(upgrade.query), "\n\n")
	details := make([]string, 0, 2)
	for _, paragraph := range paragraphs[1:] { // the first paragraph is the all-caps title
		paragraph = strings.Join(strings.Fields(paragraph), " ")
		if paragraph == "" {
			continue
		}
		details = append(details, paragraph)
		if len(strings.Join(details, " ")) >= 180 || len(details) == 2 {
			break
		}
	}
	summary := strings.Join(details, " ")
	const maxLength = 420
	runes := []rune(summary)
	if len(runes) <= maxLength {
		return summary
	}
	prefix := string(runes[:maxLength])
	if boundary := strings.LastIndexAny(prefix, ".!?"); boundary >= 120 {
		return strings.TrimSpace(prefix[:boundary+1])
	}
	return strings.TrimSpace(prefix) + "…"
}

// workflowContractUpgradeLists turns the migration ladder into a compact UI
// model. "Applied" is intentionally based on the manifest's monotonic
// contract marker: older workflows did not record per-rung timestamps, so the
// UI must not invent them.
func workflowContractUpgradeLists(manifest *WorkflowManifest) (pending, applied []workflowContractUpgradeListItem) {
	pending = make([]workflowContractUpgradeListItem, 0)
	applied = make([]workflowContractUpgradeListItem, 0)
	current := workflowContractVersionForUpgrade(manifest)
	currentRank, known := workflowContractVersionRank(current)
	if !known {
		return pending, applied
	}

	allActive := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractInitialVersion})
	pendingPlan := workflowVersionUpgradePlan(manifest)
	pendingLabels := make(map[string]struct{}, len(pendingPlan))
	for _, upgrade := range pendingPlan {
		pendingLabels[upgrade.label] = struct{}{}
	}
	appliedAtByVersion := make(map[string]string, len(manifest.ContractUpgradeHistory))
	for _, entry := range manifest.ContractUpgradeHistory {
		if version, appliedAt := strings.TrimSpace(entry.Version), strings.TrimSpace(entry.AppliedAt); version != "" && appliedAt != "" {
			appliedAtByVersion[version] = appliedAt
		}
	}

	for _, upgrade := range allActive {
		item := workflowContractUpgradeListItem{
			Label:         upgrade.label,
			TargetVersion: upgrade.to,
			Details:       workflowContractUpgradeDetails(upgrade),
			AppliedAt:     appliedAtByVersion[upgrade.to],
		}
		targetRank, targetKnown := workflowContractVersionRank(upgrade.to)
		if targetKnown && targetRank <= currentRank {
			applied = append(applied, item)
			continue
		}
		if _, isPending := pendingLabels[upgrade.label]; isPending {
			pending = append(pending, item)
		}
	}

	if manifestContractIsExecutionCompatible(manifest) && manifest.CodeLayoutVersion != 1 {
		pending = append(pending, workflowContractUpgradeListItem{
			Label:         "upgrade-nested-agent-code-layout",
			TargetVersion: WorkflowContractCurrentVersion,
			Details:       "Moves every scripted step from the legacy learnings folder into the canonical code/<step-id>/ source tree and verifies authored path assumptions before enabling the current code layout.",
		})
	}
	return pending, applied
}

// describeWorkflowContractUpgrades renders the workflow's pending contract
// migrations for its owner.
//
// The instruction text is included in full because the operator starts and
// supervises this work from Workshop chat. They need the real migration and
// verification contract, not a schedule-owned summary.
func describeWorkflowContractUpgrades(ctx context.Context, workspacePath string) (string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "No workspace path associated with this workflow session.", nil
	}

	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return fmt.Sprintf("Could not read workflow.json: %v", err), nil
	}
	if !found {
		return "No workflow manifest found.", nil
	}

	current := workflowContractVersionForUpgrade(manifest)
	var sb strings.Builder
	sb.WriteString("## Workflow contract version\n\n")
	sb.WriteString(fmt.Sprintf("- Current: `%s`\n- Platform current: `%s`\n\n", current, WorkflowContractCurrentVersion))

	if _, known := workflowContractVersionRank(current); !known && current != WorkflowContractCurrentVersion {
		sb.WriteString(fmt.Sprintf("**This version is not one this server knows.** Version ranks are a closed set so a workflow written by a newer server is never silently downgraded by an older one. There is no upgrade path from %q. Interactive workflow execution and direct webhooks will refuse to start until that is resolved; schedules continue using the saved contract without modifying it.\n", current))
		return sb.String(), nil
	}

	pending := workflowVersionUpgradePlan(manifest)
	if len(pending) == 0 {
		if manifestContractIsExecutionCompatible(manifest) && manifest.CodeLayoutVersion != 1 {
			sb.WriteString("## Manual code-layout migration required\n\n")
			sb.WriteString("The contract version is current, but `code_layout_version` is not `1`. Inspect every scripted bundle and authored path assumption, migrate scripts to the canonical `code/<step-id>/` tree, run focused checks without executing external side effects, then call `set_code_layout_version(code_layout_version=1)`. Re-read `workflow.json` and the migrated files before finishing. Do not stamp another contract version; this workflow already has the current version.\n")
			return sb.String(), nil
		}
		sb.WriteString("No pending migrations — this workflow is execution-compatible with the current contract. Retired no-op checkpoints do not block it.\n")
		return sb.String(), nil
	}

	sb.WriteString(fmt.Sprintf("## Pending migrations (%d)\n\n", len(pending)))
	sb.WriteString("Run them manually from this workflow's Workshop chat, one at a time in this order. A rung that does not complete stops the ones behind it; schedules never perform contract migrations.\n\n")
	for i, upgrade := range pending {
		sb.WriteString(fmt.Sprintf("### %d. %s → `%s`\n\n", i+1, upgrade.label, upgrade.to))
		sb.WriteString("```text\n")
		sb.WriteString(strings.TrimSpace(bindWorkflowUpgradeWorkspacePath(upgrade.query, workspacePath)))
		sb.WriteString("\n```\n\n")
	}

	return sb.String(), nil
}

// nextWorkflowContractUpgrade returns the single migration this workflow owes
// next. An empty target means it is already current, or on a version this
// server has no path from — neither of which an operator stamp should paper
// over.
func nextWorkflowContractUpgrade(ctx context.Context, workspacePath string) (string, string, error) {
	manifest, found, err := ReadWorkflowManifest(ctx, strings.TrimSpace(workspacePath))
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", fmt.Errorf("workflow manifest not found at %s", workspacePath)
	}
	pending := workflowVersionUpgradePlan(manifest)
	if len(pending) == 0 {
		return "", "", nil
	}
	return pending[0].to, pending[0].label, nil
}
