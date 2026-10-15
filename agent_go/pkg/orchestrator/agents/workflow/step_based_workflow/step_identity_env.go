package step_based_workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// withRunIdentityEnv adds the run's identity to the environment every step
// shell and script sees, so a step can tell which run it belongs to without
// guessing from folder names: WORKFLOW_RUN_ID and WORKFLOW_RUN_FOLDER (the run
// folder name), WORKFLOW_RUN_STARTED_AT and WORKFLOW_PLAN_REVISION (from the
// run's metadata, when it has been written).
func (hcpo *StepBasedWorkflowOrchestrator) withRunIdentityEnv(env map[string]string) map[string]string {
	runFolder := strings.TrimSpace(hcpo.selectedRunFolder)
	if runFolder == "" {
		return env
	}
	if env == nil {
		env = make(map[string]string)
	}
	runID := filepath.Base(filepath.ToSlash(runFolder))
	env["WORKFLOW_RUN_ID"] = runID
	env["WORKFLOW_RUN_FOLDER"] = runFolder
	if path := workflowRunMetadataPath(runFolder); path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if raw, err := hcpo.ReadWorkspaceFile(ctx, path); err == nil {
			var meta struct {
				PlanRevision string `json:"plan_revision"`
				StartedAt    string `json:"started_at"`
				RunStartedAt string `json:"run_started_at"`
			}
			if json.Unmarshal([]byte(raw), &meta) == nil {
				if meta.PlanRevision != "" {
					env["WORKFLOW_PLAN_REVISION"] = meta.PlanRevision
				}
				if started := firstNonEmptyString(meta.RunStartedAt, meta.StartedAt); started != "" {
					env["WORKFLOW_RUN_STARTED_AT"] = started
				}
			}
		}
	}
	return env
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// stepConfigWithDescribedHumanTools adds the human tools a step's own text
// asks for (email, Slack, a decision request) to a copy of its config.
// Nothing is added for steps that do not mention them, and the blocking
// human_feedback is never added: steps run unattended.
func stepConfigWithDescribedHumanTools(cfg *AgentConfigs, step PlanStepInterface) *AgentConfigs {
	if step == nil {
		return cfg
	}
	text := strings.ToLower(step.GetTitle() + "\n" + step.GetDescription())
	var add []string
	if strings.Contains(text, "email") || strings.Contains(text, "send_email") {
		add = append(add, "human_tools:send_email")
	}
	if strings.Contains(text, "slack") {
		add = append(add, "human_tools:send_slack_message")
	}
	if strings.Contains(text, "create_human_input_request") || strings.Contains(text, "decision request") || strings.Contains(text, "approval") {
		add = append(add, "human_tools:create_human_input_request")
	}
	if len(add) == 0 {
		return cfg
	}
	var copied AgentConfigs
	if cfg != nil {
		copied = *cfg
	}
	copied.EnabledCustomTools = append([]string(nil), copied.EnabledCustomTools...)
	for _, entry := range add {
		if !slices.Contains(copied.EnabledCustomTools, entry) {
			copied.EnabledCustomTools = append(copied.EnabledCustomTools, entry)
		}
	}
	return &copied
}
