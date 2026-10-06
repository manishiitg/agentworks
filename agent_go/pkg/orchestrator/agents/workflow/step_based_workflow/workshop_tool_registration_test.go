package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type workshopDefinitionDraft struct {
	tools  map[string]mcpagent.ToolDefinition
	skills []*llmtypes.Skill
}

func newWorkshopDefinitionDraft() *workshopDefinitionDraft {
	return &workshopDefinitionDraft{tools: make(map[string]mcpagent.ToolDefinition)}
}

func (d *workshopDefinitionDraft) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), displayGroup string) error {
	return d.RegisterCustomToolWithTimeout(name, description, parameters, execute, 0, displayGroup)
}

func (d *workshopDefinitionDraft) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, displayGroup string) error {
	d.tools[name] = mcpagent.ToolDefinition{Name: name, Description: description, InputSchema: parameters, Execute: execute, Timeout: timeout, DisplayGroup: displayGroup}
	return nil
}

func (d *workshopDefinitionDraft) AttachedSkills() []*llmtypes.Skill {
	return append([]*llmtypes.Skill(nil), d.skills...)
}

type workshopToolTestLogger struct{}

func (workshopToolTestLogger) Debug(string, ...loggerv2.Field)          {}
func (workshopToolTestLogger) Info(string, ...loggerv2.Field)           {}
func (workshopToolTestLogger) Warn(string, ...loggerv2.Field)           {}
func (workshopToolTestLogger) Error(string, error, ...loggerv2.Field)   {}
func (workshopToolTestLogger) Fatal(string, error, ...loggerv2.Field)   {}
func (l workshopToolTestLogger) With(...loggerv2.Field) loggerv2.Logger { return l }
func (workshopToolTestLogger) Close() error                             { return nil }

func TestRegisterWorkshopChatToolsIncludesArtifactReviewMarker(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base := &orchestrator.BaseOrchestrator{}
	base.SetWorkspacePath(workspacePath)
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: NewWorkshopStepRegistry(),
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})

	tool, ok := agent.tools["mark_changelog_artifact_reviewed"]
	if !ok {
		t.Fatal("actual workshop agent registry is missing mark_changelog_artifact_reviewed")
	}
	if tool.DisplayGroup != "workflow" {
		t.Fatalf("mark_changelog_artifact_reviewed category = %q, want workflow", tool.DisplayGroup)
	}
	if tool.Execute == nil {
		t.Fatal("mark_changelog_artifact_reviewed has no registered executor")
	}
	promptHealth, ok := agent.tools["get_plan_prompt_health"]
	if !ok {
		t.Fatal("actual workshop agent registry is missing get_plan_prompt_health")
	}
	if promptHealth.DisplayGroup != "workflow" || promptHealth.Execute == nil {
		t.Fatalf("get_plan_prompt_health registration = %#v, want executable workflow tool", promptHealth)
	}
}

func TestUpdateWorkflowConfigDoesNotExposeRetiredKnowledgebaseLock(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base := &orchestrator.BaseOrchestrator{}
	base.SetWorkspacePath(workspacePath)
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: NewWorkshopStepRegistry(),
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	tool, ok := agent.tools["update_workflow_config"]
	if !ok {
		t.Fatal("actual workshop agent registry is missing update_workflow_config")
	}
	properties, ok := tool.InputSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("update_workflow_config properties have unexpected shape: %#v", tool.InputSchema["properties"])
	}
	if _, exists := properties["lock_knowledgebase"]; exists {
		t.Fatal("retired workflow-wide lock_knowledgebase is still exposed to agents")
	}
}

func TestUpdateWorkflowConfigClearsNotificationSenderAndLegacyOverrides(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	controller, workspacePath := newPreValidationConcernTestOrchestrator(t)
	manifestPath := filepath.Join(workspacePath, "workflow.json")
	initial := `{"id":"notify-test","capabilities":{"notifications":{"gmail_connection_id":"old","run_summary_gmail_connection_ids":["old"],"pulse_summary_gmail_connection_ids":["other"],"run_summary_recipients":["ops@example.com"]}}}`
	if err := os.WriteFile(manifestPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	session := &WorkshopChatSession{
		controller:   controller,
		StepRegistry: NewWorkshopStepRegistry(),
		config:       &WorkshopConfig{WorkspacePath: controller.GetWorkspacePath()},
	}
	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	tool := agent.tools["update_workflow_config"]
	properties := tool.InputSchema["properties"].(map[string]interface{})
	if _, ok := properties["notification_gmail_connection_id"]; !ok {
		t.Fatal("Builder cannot set the Notify Gmail sender")
	}
	result, err := tool.Execute(context.Background(), map[string]interface{}{"notification_gmail_connection_id": ""})
	if err != nil || !strings.Contains(result, "Gmail notification sender: account default") {
		t.Fatalf("clear sender result = %q, %v", result, err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Capabilities struct {
			Notifications map[string]json.RawMessage `json:"notifications"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"gmail_connection_id", "run_summary_gmail_connection_ids", "pulse_summary_gmail_connection_ids"} {
		if _, ok := saved.Capabilities.Notifications[key]; ok {
			t.Errorf("legacy sender setting %q was retained", key)
		}
	}
	if _, ok := saved.Capabilities.Notifications["run_summary_recipients"]; !ok {
		t.Fatal("changing sender removed existing recipients")
	}
}

func TestScheduleToolsDoNotExposeGenericRunDurationLimit(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base := &orchestrator.BaseOrchestrator{}
	base.SetWorkspacePath(workspacePath)
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: NewWorkshopStepRegistry(),
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	for _, name := range []string{"create_schedule", "create_calendar_schedule", "update_schedule"} {
		tool, ok := agent.tools[name]
		if !ok {
			t.Fatalf("actual workshop agent registry is missing %s", name)
		}
		properties, ok := tool.InputSchema["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s properties have unexpected shape: %#v", name, tool.InputSchema["properties"])
		}
		if _, exists := properties["max_run_duration_minutes"]; exists {
			t.Fatalf("%s still exposes the retired generic run-duration limit", name)
		}
		if _, exists := properties["after_schedule_ids"]; !exists {
			t.Fatalf("%s lost schedule dependency fan-in while removing the run-duration limit", name)
		}
		if _, exists := properties["concurrency_mode"]; !exists {
			t.Fatalf("%s does not expose schedule concurrency mode", name)
		}
		if _, exists := properties["parallel_risk_acknowledged"]; !exists {
			t.Fatalf("%s does not expose parallel risk acknowledgement", name)
		}
	}
}

func TestUpdateWorkflowConfigShowsAndEnforcesDisabledServerCDP(t *testing.T) {
	t.Setenv(browser.EnvAgentBrowserCDPEnabled, "false")
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base := &orchestrator.BaseOrchestrator{}
	base.SetWorkspacePath(workspacePath)
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: NewWorkshopStepRegistry(),
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	tool := agent.tools["update_workflow_config"]
	properties := tool.InputSchema["properties"].(map[string]interface{})
	mode := properties["browser_mode"].(map[string]interface{})
	values := mode["enum"].([]interface{})
	for _, value := range values {
		if value == "cdp" {
			t.Fatalf("disabled deployment schema still exposes cdp: %v", values)
		}
	}
	if !strings.Contains(mode["description"].(string), "CDP is disabled") {
		t.Fatalf("disabled deployment description is unclear: %v", mode["description"])
	}
	result, err := tool.Execute(context.Background(), map[string]interface{}{"browser_mode": "cdp"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "CDP is disabled") {
		t.Fatalf("forced CDP was not rejected clearly: %q", result)
	}
}

func TestUpdateStepConfigDoesNotExposeRetiredLearningLock(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base := &orchestrator.BaseOrchestrator{}
	base.SetWorkspacePath(workspacePath)
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: NewWorkshopStepRegistry(),
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	tool, ok := agent.tools["update_step_config"]
	if !ok {
		t.Fatal("actual workshop agent registry is missing update_step_config")
	}
	properties, ok := tool.InputSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("update_step_config properties have unexpected shape: %#v", tool.InputSchema["properties"])
	}
	for _, retired := range []string{"lock_learnings", "lock_learnings_reason"} {
		if _, exists := properties[retired]; exists {
			t.Fatalf("retired field %q is still exposed to agents", retired)
		}
	}
}

func TestRunningStatusToolsShareRapidPollGuard(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath(workspacePath)
	registry := NewWorkshopStepRegistry()
	registry.Register(&WorkshopStepExecution{
		ID:     "workflow-1",
		StepID: "step-1",
		Status: WorkshopStepRunning,
	})
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: registry,
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	queryStep := agent.tools["query_step"].Execute
	listExecutions := agent.tools["list_executions"].Execute
	if queryStep == nil || listExecutions == nil {
		t.Fatal("running-status tools were not registered")
	}

	first, err := queryStep(context.Background(), map[string]interface{}{"step_id": "step-1"})
	if err != nil {
		t.Fatalf("first query_step: %v", err)
	}
	if !strings.Contains(first, statusPollNextAction) || strings.Contains(first, statusPollSuppressed) {
		t.Fatalf("first status response should direct the agent to end its turn without suppression:\n%s", first)
	}

	second, err := listExecutions(context.Background(), map[string]interface{}{"status_filter": "running"})
	if err != nil {
		t.Fatalf("second list_executions: %v", err)
	}
	if !strings.Contains(second, statusPollWarning) || strings.Contains(second, statusPollSuppressed) {
		t.Fatalf("second cross-tool status response should warn without suppression:\n%s", second)
	}

	third, err := queryStep(context.Background(), map[string]interface{}{"step_id": "step-1"})
	if err != nil {
		t.Fatalf("third query_step: %v", err)
	}
	if third != statusPollSuppressed {
		t.Fatalf("third unchanged cross-tool status response = %q, want compact suppression", third)
	}
}

func TestQueryStepTerminalResponsesContainOnlyExecutionOutcome(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base := &orchestrator.BaseOrchestrator{}
	base.SetWorkspacePath(workspacePath)
	registry := NewWorkshopStepRegistry()
	registry.Register(&WorkshopStepExecution{
		ID:     "exec-research-complete",
		StepID: "research",
		Status: WorkshopStepDone,
		Result: "Research output is ready.",
	})
	registry.Register(&WorkshopStepExecution{
		ID:     "exec-proposal-failed",
		StepID: "proposal",
		Status: WorkshopStepFailed,
		Err:    errors.New("proposal validation failed"),
	})
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: registry,
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	queryStep := agent.tools["query_step"].Execute
	if queryStep == nil {
		t.Fatal("query_step was not registered")
	}

	completed, err := queryStep(context.Background(), map[string]interface{}{"execution_id": "exec-research-complete"})
	if err != nil {
		t.Fatalf("query completed step: %v", err)
	}
	wantCompleted := "Step \"research\" completed.\nexecution_id: exec-research-complete\n\nResearch output is ready."
	if completed != wantCompleted {
		t.Fatalf("completed response = %q, want %q", completed, wantCompleted)
	}

	failed, err := queryStep(context.Background(), map[string]interface{}{"execution_id": "exec-proposal-failed"})
	if err != nil {
		t.Fatalf("query failed step: %v", err)
	}
	wantFailed := "Step \"proposal\" failed.\nexecution_id: exec-proposal-failed\nerror: proposal validation failed"
	if failed != wantFailed {
		t.Fatalf("failed response = %q, want %q", failed, wantFailed)
	}
}

func TestGenericAgentCanBeQueriedAndStoppedByExecutionID(t *testing.T) {
	agent := newWorkshopDefinitionDraft()
	workspacePath := t.TempDir()
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.SetWorkspacePath(workspacePath)
	registry := NewWorkshopStepRegistry()
	_, cancel := context.WithCancel(context.Background())
	registry.Register(&WorkshopStepExecution{
		ID:     "generic-agent-review-costs-123",
		StepID: "generic-agent:review-costs",
		Status: WorkshopStepRunning,
		cancel: cancel,
	})
	session := &WorkshopChatSession{
		controller:   &StepBasedWorkflowOrchestrator{BaseOrchestrator: base},
		StepRegistry: registry,
		config:       &WorkshopConfig{WorkspacePath: workspacePath},
	}

	RegisterWorkshopChatTools(agent, session, workshopToolTestLogger{})
	queryStep := agent.tools["query_step"].Execute
	stopStep := agent.tools["stop_step"].Execute
	if queryStep == nil || stopStep == nil {
		t.Fatal("generic execution control tools were not registered")
	}

	status, err := queryStep(context.Background(), map[string]interface{}{
		"execution_id": "generic-agent-review-costs-123",
	})
	if err != nil {
		t.Fatalf("query generic agent: %v", err)
	}
	if !strings.Contains(status, `Generic agent "review-costs"`) || !strings.Contains(status, "generic-agent-review-costs-123") {
		t.Fatalf("generic execution query omitted identity:\n%s", status)
	}

	stopped, err := stopStep(context.Background(), map[string]interface{}{
		"execution_id": "generic-agent-review-costs-123",
	})
	if err != nil {
		t.Fatalf("stop generic agent: %v", err)
	}
	if !strings.Contains(stopped, "has been canceled") {
		t.Fatalf("unexpected stop response: %s", stopped)
	}
	if snapshot, ok := registry.GetSnapshot("generic-agent-review-costs-123"); !ok || snapshot.Status != WorkshopStepCancelled {
		t.Fatalf("generic execution status = %+v, found=%v; want canceled", snapshot, ok)
	}
}

func TestRapidPollGuardAllowsChangedStateAndResetsAfterIdleWindow(t *testing.T) {
	registry := NewWorkshopStepRegistry()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	if got := registry.observeStatusPoll(now, "workflow-1:running"); got.Warn || got.Suppress {
		t.Fatalf("first decision = %+v, want allowed without warning", got)
	}
	if got := registry.observeStatusPoll(now.Add(time.Second), "workflow-1:running"); !got.Warn || got.Suppress {
		t.Fatalf("second decision = %+v, want warning only", got)
	}
	if got := registry.observeStatusPoll(now.Add(2*time.Second), "workflow-1:running"); !got.Suppress {
		t.Fatalf("third unchanged decision = %+v, want suppression", got)
	}
	if got := registry.observeStatusPoll(now.Add(3*time.Second), "workflow-1:done"); !got.Changed || got.Suppress {
		t.Fatalf("changed decision = %+v, want changed state returned", got)
	}
	if got := registry.observeStatusPoll(now.Add(statusPollWindow+4*time.Second), "workflow-2:running"); got.Count != 1 || got.Warn || got.Suppress {
		t.Fatalf("post-idle decision = %+v, want fresh budget", got)
	}
}

func TestWorkshopRegistersKnowledgeProjectSetupOnlyOnRootBuilder(t *testing.T) {
	for _, mode := range []string{"workshop", "run"} {
		t.Run(mode, func(t *testing.T) {
			draft := newWorkshopDefinitionDraft()
			base := &orchestrator.BaseOrchestrator{}
			base.SetWorkspacePath(t.TempDir())
			called := false
			config := &WorkshopConfig{WorkspacePath: base.GetWorkspacePath(), KnowledgebaseProjectTool: func(context.Context, string, map[string]interface{}) (string, error) {
				called = true
				return "selected", nil
			}}
			session := &WorkshopChatSession{controller: &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}, StepRegistry: NewWorkshopStepRegistry(), config: config, workshopModeOverride: mode}
			RegisterWorkshopChatTools(draft, session, workshopToolTestLogger{})
			def, found := draft.tools["brain_access"]
			if found != (mode == "workshop") {
				t.Fatalf("%s setup registration = %v", mode, found)
			}
			if found {
				schema, _ := json.Marshal(def.InputSchema)
				if !strings.Contains(string(schema), "set_project_access") || strings.Contains(string(schema), "bind_project") || strings.Contains(string(schema), `"const":"grant"`) {
					t.Fatal("Builder schema has incorrect access actions")
				}
				if _, err := def.Execute(t.Context(), map[string]interface{}{"action": "inspect_project"}); err != nil || !called {
					t.Fatal("Builder callback missing", err)
				}
			}
			if len(config.CustomTools) != 0 || len(config.CustomToolExecutors) != 0 {
				t.Fatal("setup leaked into step tool pool")
			}
		})
	}
}
