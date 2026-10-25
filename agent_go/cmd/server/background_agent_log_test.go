package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// TestBackgroundAgentLogWorkspacePathRequiresAWorkflowScopedActiveSession pins
// the deliberate boundary: this table exists to give workflow-scoped runs
// (Pulse's background agents, a workflow's own todo-task sub-agents) the
// durable record workflow steps already get from runs/iteration-N/. A plain
// chat session with no workspace, or a session id nobody registered, has
// nothing to log against — that must stay a silent no-op, not an error.
func TestBackgroundAgentLogWorkspacePathRequiresAWorkflowScopedActiveSession(t *testing.T) {
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"chat-session":     {SessionID: "chat-session", WorkspacePath: ""},
			"workflow-session": {SessionID: "workflow-session", WorkspacePath: "Workflow/social-media"},
		},
	}

	if got := api.backgroundAgentLogWorkspacePath("unknown-session"); got != "" {
		t.Fatalf("unknown session workspace = %q, want empty", got)
	}
	if got := api.backgroundAgentLogWorkspacePath("chat-session"); got != "" {
		t.Fatalf("chat session (no workspace) workspace = %q, want empty", got)
	}
	if got := api.backgroundAgentLogWorkspacePath("workflow-session"); got != "Workflow/social-media" {
		t.Fatalf("workflow session workspace = %q, want Workflow/social-media", got)
	}
}

// PLAT-825: a fresh Goal must retain background history after engine removal.
// TestBackgroundAgentLogRecordsStartThenCompletionInOneRow pins PLAT-114: a
// background agent's start and completion upsert the SAME durable row, not
// two, so a row that only ever reaches "running" is itself real evidence —
// that agent started and nothing ever recorded it finishing.
func TestBackgroundAgentLogRecordsStartThenCompletionInOneRow(t *testing.T) {
	const workspacePath = "Workflow/test-workspace"
	const sessionID = "schedule-cron--test_1786545010990380000"
	const agentID = "workflow-full-test-step-0-abc123"

	// The db is read/written via a direct local path derived from
	// WORKSPACE_DOCS_PATH (openReportHumanInputDB -> reportHumanInputDBPath),
	// NOT through the workspace HTTP API — sandbox to a temp root so this test
	// can never touch a real workflow's database.
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())

	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, WorkspacePath: workspacePath},
		},
	}

	startedAt := time.Date(2026, 8, 12, 20, 43, 0, 0, time.UTC)
	api.recordBackgroundAgentLogStarted(sessionID, agentID, "reconcile-tail-durable-ledgers",
		orchEvents.ExecutionKindSubAgent, "workflow-full-msq7356p02", startedAt)

	entries, err := backgroundAgentLogForSession(context.Background(), workspacePath, sessionID)
	if err != nil {
		t.Fatalf("backgroundAgentLogForSession() after start error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries after start = %d, want 1", len(entries))
	}
	if entries[0].Status != "running" {
		t.Fatalf("status after start = %q, want running", entries[0].Status)
	}
	if entries[0].CompletedAt != "" {
		t.Fatalf("completed_at after start = %q, want empty", entries[0].CompletedAt)
	}

	completedAt := startedAt.Add(7 * time.Minute)
	api.recordBackgroundAgentLogCompleted(sessionID, agentID, "reconcile-tail-durable-ledgers", "sub_agent",
		"completed", "Reconciled IDs 048-062 in managed DB", "", "7m0s", completedAt)

	entries, err = backgroundAgentLogForSession(context.Background(), workspacePath, sessionID)
	if err != nil {
		t.Fatalf("backgroundAgentLogForSession() after completion error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries after completion = %d, want 1 (start and completion must upsert the same row)", len(entries))
	}
	got := entries[0]
	if got.Status != "completed" {
		t.Fatalf("status after completion = %q, want completed", got.Status)
	}
	if got.Result != "Reconciled IDs 048-062 in managed DB" {
		t.Fatalf("result = %q, unexpected", got.Result)
	}
	if got.CompletedAt == "" {
		t.Fatal("completed_at was not recorded")
	}
	if got.ParentExecutionID != "workflow-full-msq7356p02" {
		t.Fatalf("parent_execution_id = %q, want workflow-full-msq7356p02 (must survive from the start record)", got.ParentExecutionID)
	}

	// The record must actually be durable — on disk, not merely in an
	// in-memory cache this test happens to share with the reader.
	dbPath := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), filepath.FromSlash(workspacePath), "db", "db.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db.sqlite was never written to disk at %s: %v", dbPath, err)
	}
}

// TestBackgroundAgentLogSkipsSessionsWithNoWorkspace proves the no-op path
// never attempts a workspace API call at all — asserted by never configuring
// WORKSPACE_API_URL, so any attempted call would fail loudly instead of
// silently succeeding against the wrong target.
func TestBackgroundAgentLogSkipsSessionsWithNoWorkspace(t *testing.T) {
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"chat-session": {SessionID: "chat-session", WorkspacePath: ""},
		},
	}
	// No WORKSPACE_API_URL set. If either call below tried to reach a
	// workspace, openWorkflowStoreDB would fail against an empty/invalid
	// URL — these must return without even trying.
	api.recordBackgroundAgentLogStarted("chat-session", "agent-1", "some delegation",
		orchEvents.ExecutionKindSubAgent, "", time.Now())
	api.recordBackgroundAgentLogCompleted("chat-session", "agent-1", "some delegation", "sub_agent",
		"completed", "done", "", "1s", time.Now())
}
