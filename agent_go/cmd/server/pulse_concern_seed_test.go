package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// recordTestReviewFinding files one reviewer finding through the live writer,
// the only production path that creates run_concerns rows.
func recordTestReviewFinding(t *testing.T, ctx context.Context, workspacePath, pulseRunID, module, concern string) {
	t.Helper()
	recordTestReviewFindingWithEvidence(t, ctx, workspacePath, pulseRunID, module, concern, []string{"runs/iteration-0/result.json"})
}

// seedLegacyStepObservation inserts a historical step-raised row. No live
// writer creates these any more, but databases retained from before
// 2026-08-29 still hold them and readers must keep treating them as
// observations, not issues. Call after a live write has created the schema.
func seedLegacyStepObservation(t *testing.T, workspacePath, runFolder, groupName, stepID, text string) {
	t.Helper()
	path := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(workspacePath, "/")), "db", "db.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open workflow db: %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO run_concerns
		(fingerprint, issue_id, step_id, phase, group_name, text, first_seen_run, first_seen_at, last_seen_run, last_seen_at, seen_count, status)
		VALUES (?, '', ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		"legacy-observation-"+stepID, stepID, step_based_workflow.ConcernPhaseExecution, groupName, text,
		runFolder, now, runFolder, now, step_based_workflow.ConcernStatusOpen); err != nil {
		t.Fatalf("seed legacy observation: %v", err)
	}
}
