package server

import (
	"testing"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// TestCapacityWaitFromAnEarlierRunIsIgnored is the iteration-0 trap.
//
// Run folders are reused: iteration-0 is the live slot and is only rotated to a
// permanent name on the next run. So a capacity-wait record written by
// yesterday's run is still sitting in the folder today's run uses. Trusting it
// would suspend a run that never hit a wall, at whatever step the earlier run
// happened to stop on — and because an outstanding wait suppresses the
// schedule, that would silently stop the workflow.
func TestCapacityWaitFromAnEarlierRunIsIgnored(t *testing.T) {
	runStart := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	stale := &stepworkflow.WorkflowCapacityWait{RecordedAt: runStart.Add(-24 * time.Hour)}
	if capacityWaitBelongsToRun(stale, runStart) {
		t.Error("a record written a day before this run was accepted as this run's wait")
	}

	fresh := &stepworkflow.WorkflowCapacityWait{RecordedAt: runStart.Add(90 * time.Second)}
	if !capacityWaitBelongsToRun(fresh, runStart) {
		t.Error("a record written during this run was rejected")
	}

	// The boundary belongs to the run: a wall hit in the same instant the run
	// was recorded as started is this run's wall.
	atStart := &stepworkflow.WorkflowCapacityWait{RecordedAt: runStart}
	if !capacityWaitBelongsToRun(atStart, runStart) {
		t.Error("a record written exactly at run start was rejected")
	}

	if capacityWaitBelongsToRun(nil, runStart) {
		t.Error("a missing record was treated as a wait")
	}
}

// TestCapacityWaitNotificationThresholdFollowsTheSchedule.
//
// A pause shorter than the gap to the next run is self-healing and interrupting
// somebody for it is noise. A pause that outlasts the next run means scheduled
// runs are being skipped, which is a degraded schedule and worth saying out
// loud rather than leaving as a quiet gap in the history.
func TestCapacityWaitNotificationThresholdFollowsTheSchedule(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	nextRun := now.Add(6 * time.Hour)

	shortWait := &stepworkflow.WorkflowCapacityWait{RetryAt: now.Add(2 * time.Hour)}
	if capacityWaitOutlastsNextRun(shortWait, nextRun) {
		t.Error("a pause that ends before the next run was treated as degrading the schedule")
	}

	longWait := &stepworkflow.WorkflowCapacityWait{RetryAt: now.Add(30 * time.Hour)}
	if !capacityWaitOutlastsNextRun(longWait, nextRun) {
		t.Error("a pause spanning the next scheduled run was not flagged")
	}

	// An unknown reset has no end, so it always outlasts the next run. This is
	// the seven-day window's case, where the alternative is a workflow silently
	// paused for days.
	unknown := &stepworkflow.WorkflowCapacityWait{}
	if !capacityWaitOutlastsNextRun(unknown, nextRun) {
		t.Error("a wait with no stated reset was treated as short")
	}
}

// TestQuotaGateOnlyAppliesToSchedulesThatActuallyUseClaudeCode.
//
// The gate keys on the Claude Code credential, and a workflow can share the
// deployment-wide credential without ever calling Claude. Skipping a Codex or
// Pi workflow's runs because a Claude window is exhausted would stop a schedule
// for a limit it never touches — a worse failure than the one the gate prevents.
func TestQuotaGateOnlyAppliesToSchedulesThatActuallyUseClaudeCode(t *testing.T) {
	claude := &ScheduleContext{Capabilities: WorkflowCapabilities{LLMConfig: &workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeExplicit,
		BuilderLLM:    &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5"},
	}}}
	if !scheduleUsesClaudeCode(claude) {
		t.Error("a claude-code schedule was not recognized by the gate")
	}

	codex := &ScheduleContext{Capabilities: WorkflowCapabilities{LLMConfig: &workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeExplicit,
		BuilderLLM:    &workflowtypes.AgentLLMConfig{Provider: "codex", ModelID: "gpt-5"},
	}}}
	if scheduleUsesClaudeCode(codex) {
		t.Error("a codex schedule would be blocked by a Claude window it never touches")
	}

	// No configuration at all must fall through to a normal fire rather than
	// being treated as Claude by default.
	if scheduleUsesClaudeCode(&ScheduleContext{}) {
		t.Error("a schedule with no LLM config was treated as claude-code")
	}
	if scheduleUsesClaudeCode(nil) {
		t.Error("a nil schedule context was treated as claude-code")
	}
}
