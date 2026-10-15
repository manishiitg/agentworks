package step_based_workflow

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"time"
)

// runIdentity is the identity of the run now executing. The id is the run's
// execution id: the in-memory one when the caller supplied it, otherwise the one
// in run_metadata.json, accepted only while that file says it belongs to this
// run folder and is still running (a reused iteration folder can hold the
// previous run's metadata until the new one is written).
type runIdentity struct{ ID, Folder, StartedAt, PlanRevision string }

func (hcpo *StepBasedWorkflowOrchestrator) currentRunIdentity() runIdentity {
	id := runIdentity{Folder: strings.TrimSpace(hcpo.selectedRunFolder)}
	if id.Folder == "" {
		return id
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil {
		id.ID = strings.TrimSpace(opts.ExecutionID)
	}
	path := workflowRunMetadataPath(id.Folder)
	if path == "" {
		return id
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := hcpo.ReadWorkspaceFile(ctx, path)
	if err != nil {
		return id
	}
	var meta struct {
		ExecutionID  string `json:"execution_id"`
		Status       string `json:"status"`
		RunFolder    string `json:"run_folder"`
		StartedAt    string `json:"started_at"`
		PlanRevision string `json:"plan_revision"`
	}
	if json.Unmarshal([]byte(raw), &meta) != nil || meta.RunFolder != id.Folder {
		return id
	}
	if id.ID != "" && meta.ExecutionID != id.ID {
		return id
	}
	if id.ID == "" && meta.Status != "running" {
		return id
	}
	id.ID, id.StartedAt, id.PlanRevision = meta.ExecutionID, meta.StartedAt, meta.PlanRevision
	return id
}

// withRunIdentityEnv adds the run's identity to the environment every step
// shell and script sees: WORKFLOW_RUN_ID (the execution id), WORKFLOW_RUN_FOLDER,
// WORKFLOW_RUN_STARTED_AT and WORKFLOW_PLAN_REVISION. Values that are not known
// yet are left out rather than guessed.
func (hcpo *StepBasedWorkflowOrchestrator) withRunIdentityEnv(env map[string]string) map[string]string {
	id := hcpo.currentRunIdentity()
	if id.Folder == "" {
		return env
	}
	if env == nil {
		env = make(map[string]string)
	}
	env["WORKFLOW_RUN_FOLDER"] = id.Folder
	for key, value := range map[string]string{"WORKFLOW_RUN_ID": id.ID, "WORKFLOW_RUN_STARTED_AT": id.StartedAt, "WORKFLOW_PLAN_REVISION": id.PlanRevision} {
		if value != "" {
			env[key] = value
		}
	}
	return env
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
	log.Printf("[TOOLS] step %s: human tools from description: %s", step.GetID(), strings.Join(add, ","))
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
