package step_based_workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
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
