package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestParseStepConcernLines(t *testing.T) {
	text := "Collected 12 rows.\n`CONCERNS: source returned 0 rows for group b`\n- CONCERNS: stale cache from 2026-09-01\nCONCERNS: <what happened, the exact affected artifact or operation, and the evidence>\nconcerns: none\nCONCERNS:\nSTATUS: COMPLETED"
	got := parseStepConcernLines(text)
	want := []string{"source returned 0 rows for group b", "stale cache from 2026-09-01"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parseStepConcernLines = %q, want %q", got, want)
	}
}

func writeStepSummary(t *testing.T, path string, value interface{}, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func TestCollectStepConcernsReadsBothSummaryKindsSinceTheWindow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/example"
	runs := filepath.Join(root, "Workflow", "example", "runs")
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	fresh := since.Add(2 * time.Hour)
	later := since.Add(26 * time.Hour)

	// Scripted/regular step: script stdout is the execution_result.
	writeStepSummary(t, filepath.Join(runs, "iteration-5", "default", "logs", "collect", "execution", "execution-final-summary.json"),
		map[string]string{"step_id": "collect", "run_folder": "iteration-5", "execution_result": "done\nCONCERNS: prospect source returned 0 rows"}, fresh)
	// Same concern again in a later run: grouped, counted twice.
	writeStepSummary(t, filepath.Join(runs, "iteration-6", "default", "logs", "collect", "execution", "execution-final-summary.json"),
		map[string]string{"step_id": "collect", "run_folder": "iteration-6", "execution_result": "CONCERNS: prospect source returned 0 rows"}, later)
	// Message-sequence turn summaries.
	writeStepSummary(t, filepath.Join(runs, "iteration-6", "engage", "execution", "engage-comment", "session.json"),
		map[string]interface{}{"step_id": "engage-comment", "run_folder": "iteration-6", "entries": []map[string]string{
			{"summary": "Commented on 4 posts.\nCONCERNS: reply rate fell to 1% from 6% last week\nSTATUS: COMPLETED"},
			{"summary": "No concern here."},
		}}, later)
	// Before the window: ignored.
	writeStepSummary(t, filepath.Join(runs, "iteration-4", "default", "logs", "collect", "execution", "execution-final-summary.json"),
		map[string]string{"step_id": "collect", "run_folder": "iteration-4", "execution_result": "CONCERNS: old news"}, since.Add(-time.Hour))
	// Pulse's own run folder: not step evidence.
	writeStepSummary(t, filepath.Join(runs, "pulse", "p1", "logs", "x", "execution", "execution-final-summary.json"),
		map[string]string{"step_id": "x", "execution_result": "CONCERNS: reviewer text"}, later)

	view := collectStepConcerns(ws, since)
	if view.Total != 2 {
		t.Fatalf("total = %d, want 2: %+v", view.Total, view.Concerns)
	}
	byStep := map[string]StepConcern{}
	for _, c := range view.Concerns {
		byStep[c.StepID] = c
	}
	collect := byStep["collect"]
	if collect.Text != "prospect source returned 0 rows" || collect.SeenCount != 2 || len(collect.RunFolders) != 2 {
		t.Fatalf("collect concern = %+v", collect)
	}
	if engage := byStep["engage-comment"]; engage.Text != "reply rate fell to 1% from 6% last week" || engage.SeenCount != 1 {
		t.Fatalf("engage concern = %+v", engage)
	}
	for _, c := range view.Concerns {
		if c.Text == "old news" || c.Text == "reviewer text" {
			t.Fatalf("collected out-of-scope concern: %+v", c)
		}
	}
}

func TestStepConcernWindowStartsAtThePreviousPulse(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	ws := "Workflow/example"
	before := time.Now().UTC()
	if got := stepConcernWindowStart(ctx, ws); before.Sub(got) < 6*24*time.Hour {
		t.Fatalf("without a previous Pulse the window should be about a week, got %v", got)
	}
	first := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)
	second := time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)
	if err := markPulseStarted(ctx, ws, "p1", first); err != nil {
		t.Fatal(err)
	}
	if err := markPulseStarted(ctx, ws, "p2", second); err != nil {
		t.Fatal(err)
	}
	// While p2 runs, its evidence window starts where p1 started.
	if got := stepConcernWindowStart(ctx, ws); !got.Equal(first) {
		t.Fatalf("window start = %v, want the previous Pulse start %v", got, first)
	}
}

func TestCollectStepOutputsLinesUpEachStepsLatestRuns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	runs := filepath.Join(root, "Workflow", "example", "runs")
	base := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	for i, text := range []string{
		"Found 10 builders and followed all 10.\nSTATUS: COMPLETED",
		"Rebuilt the output from the previous run; no new actions were performed.\nCONCERNS: no fresh run today",
		"Rebuilt the output from the previous run again.",
	} {
		run := fmt.Sprintf("iteration-%d", i+1)
		writeStepSummary(t, filepath.Join(runs, run, "default", "execution", "connect", "session.json"),
			map[string]interface{}{"step_id": "connect", "run_folder": run, "entries": []map[string]string{
				{"item_type": "user_message", "summary": text},
				{"item_type": "prevalidation", "summary": "prevalidation passed"},
			}}, base.Add(time.Duration(i)*time.Hour))
		// The generic sequence summary carries no account of the work.
		writeStepSummary(t, filepath.Join(runs, run, "default", "logs", "connect", "execution", "execution-final-summary.json"),
			map[string]string{"step_id": "connect", "run_folder": run, "execution_result": "Agent connect completed: 2 item(s) completed"}, base.Add(time.Duration(i)*time.Hour))
	}
	writeStepSummary(t, filepath.Join(runs, "iteration-3", "default", "logs", "collect", "execution", "execution-final-summary.json"),
		map[string]string{"step_id": "collect", "run_folder": "iteration-3", "execution_result": "wrote 4 rows"}, base)
	writeStepSummary(t, filepath.Join(runs, "pulse", "p1", "logs", "x", "execution", "execution-final-summary.json"),
		map[string]string{"step_id": "x", "execution_result": "reviewer text"}, base)

	view := collectStepOutputs("Workflow/example", "")
	if len(view.Steps) != 2 {
		t.Fatalf("steps = %+v, want connect and collect only", view.Steps)
	}
	connect := view.Steps[0]
	if connect.StepID != "connect" || len(connect.Runs) != 3 {
		t.Fatalf("connect = %+v", connect)
	}
	if connect.Runs[0].RunFolder != "iteration-3" || connect.Runs[0].Result != "Rebuilt the output from the previous run again." {
		t.Fatalf("newest run = %+v", connect.Runs[0])
	}
	if got := connect.Runs[1].Result; got != "Rebuilt the output from the previous run; no new actions were performed." {
		t.Fatalf("CONCERNS line should be dropped from the result, got %q", got)
	}
	if got := connect.Runs[2].Result; got != "Found 10 builders and followed all 10." {
		t.Fatalf("STATUS line should be dropped from the result, got %q", got)
	}
	if only := collectStepOutputs("Workflow/example", "collect"); len(only.Steps) != 1 || only.Steps[0].Runs[0].Result != "wrote 4 rows" {
		t.Fatalf("step filter = %+v", only)
	}
}

func TestPulseBacklogShowsHowOftenAnIssueWasDeferred(t *testing.T) {
	queued := step_based_workflow.PulseFindingEvent{EventType: step_based_workflow.ConcernStatusQueuedForEngineering}
	fresh := step_based_workflow.PulseFindingLifecycle{Fingerprint: "a", Kind: step_based_workflow.PulseFindingKindIssue, Status: "open"}
	stuck := step_based_workflow.PulseFindingLifecycle{Fingerprint: "b", Kind: step_based_workflow.PulseFindingKindIssue, Status: "queued_for_engineering",
		Events: []step_based_workflow.PulseFindingEvent{queued, {EventType: "observed_again"}, queued, queued}}
	cards := pulseLifecycleAgentProjection([]step_based_workflow.PulseFindingLifecycle{fresh, stuck}, "issue_id")
	if _, ok := cards[0]["times_deferred"]; ok {
		t.Fatalf("a never-deferred issue should carry no deferral count: %+v", cards[0])
	}
	if cards[1]["times_deferred"] != 3 || !strings.Contains(cards[1]["deferral_note"].(string), "Do not queue it again") {
		t.Fatalf("deferred issue card = %+v", cards[1])
	}
}
