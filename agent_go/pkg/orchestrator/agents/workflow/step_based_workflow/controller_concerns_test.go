package step_based_workflow

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestExecutionEvidencePathsMatchPulseContract(t *testing.T) {
	logsRoot := getExecutionFolderPathForLogs("Workflow/demo/runs/iteration-0/default", "collect-price", "step-1")
	got := filepath.Join(logsRoot, finalExecutionSummaryFilename)
	want := "Workflow/demo/runs/iteration-0/default/logs/collect-price/execution/execution-final-summary.json"
	if got != want {
		t.Fatalf("final summary path = %q, want %q", got, want)
	}
}

func TestOrchestratorExecutionLogFilenamePreservesRetryIdentity(t *testing.T) {
	if got, want := orchestratorExecutionLogFilename(3, 0), "execution-attempt-3-iteration-0.json"; got != want {
		t.Fatalf("filename = %q, want %q", got, want)
	}
}

func TestFinalExecutionSummaryPreservesContributionConcerns(t *testing.T) {
	got := buildDirectModeCompletionSummary(
		"Produced the report.\nSTATUS: COMPLETED",
		"CONCERNS: knowledgebase write failed.\nSTATUS: COMPLETED",
		"CONCERNS: learnings write failed.\nSTATUS: COMPLETED",
	)
	for _, want := range []string{
		"Execution: Produced the report.",
		"KB review: CONCERNS: knowledgebase write failed.",
		"Learnings: CONCERNS: learnings write failed.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("final summary missing %q: %s", want, got)
		}
	}
}

func TestSummarizeExecutionResultPreservesOutcomeConcernsAndStatus(t *testing.T) {
	result := "Published 12 records to db/db.sqlite.\n" +
		"CONCERNS: knowledgebase/notes/acme.md was stale and read-only.\n" +
		"STATUS: COMPLETED"

	if got := summarizeExecutionResultForNotification(result); got != result {
		t.Fatalf("summary = %q, want complete final response %q", got, result)
	}
}

func TestSummarizeExecutionResultCapsFromTailWithoutLosingConcern(t *testing.T) {
	result := strings.Repeat("x", 5000) + "\nCONCERNS: final evidence\nSTATUS: COMPLETED"
	got := summarizeExecutionResultForNotification(result)
	if !strings.HasPrefix(got, "… (earlier response omitted)\n") {
		t.Fatalf("expected bounded summary prefix, got %q", got[:min(len(got), 80)])
	}
	for _, want := range []string{"CONCERNS: final evidence", "STATUS: COMPLETED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bounded summary lost %q: %s", want, got)
		}
	}
}

func TestLatestAssistantExecutionSummaryUsesFinalAssistantTurn(t *testing.T) {
	history := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: strings.Repeat("old narration ", 300)},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Finish the task."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Wrote the report.\nCONCERNS: source B timed out.\nSTATUS: COMPLETED"},
			},
		},
	}

	got := latestAssistantExecutionSummary(history)
	if strings.Contains(got, "old narration") {
		t.Fatalf("summary included earlier narration: %s", got)
	}
	for _, want := range []string{"Wrote the report.", "CONCERNS: source B timed out.", "STATUS: COMPLETED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %s", want, got)
		}
	}
}

// The step result is what the parent chat's completion notification and later
// steps' context see, so it is the last authored item's answer, not a count.
// Prevalidation gates and synthetic __...__ items are bookkeeping, and the
// count is only the fallback when no authored item produced text.
func TestAgentSummaryIsLastAuthoredAnswer(t *testing.T) {
	o := &StepBasedWorkflowOrchestrator{}
	session := &agentSequenceSession{
		StepID: "publish-sequence",
		Entries: []agentSequenceEntry{
			{ItemID: "draft", Status: "completed", Summary: "Drafted post.\nSTATUS: COMPLETED"},
			{ItemID: "publish", Status: "completed", Summary: "Published post.\nSTATUS: COMPLETED"},
			{ItemID: "__automatic_final_validation__", ItemType: "prevalidation", Status: "completed", Summary: "prevalidation passed"},
		},
	}
	if got := o.summarizeAgentSession(session); got != "Published post.\nSTATUS: COMPLETED" {
		t.Fatalf("agent result = %q, want the last authored item's answer", got)
	}
	empty := &agentSequenceSession{StepID: "s", Entries: []agentSequenceEntry{{ItemID: "a", Status: "completed"}}}
	if got := o.summarizeAgentSession(empty); got != "Agent s completed: 1 item(s) completed" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestValidationFailureConcernSurvivesSuccessfulRetry(t *testing.T) {
	failures := []validationFailureConcern{{Attempt: 1, Detail: "published receipt was missing"}}
	result := "Published the post.\nCONCERNS: analytics were delayed.\nSTATUS: COMPLETED"

	pending := withValidationFailureConcern(result, failures, 0, false)
	if !strings.Contains(pending, "CONCERNS: Validation history: attempt 1 failed - published receipt was missing; retry pending.") {
		t.Fatalf("pending result missing validation concern: %s", pending)
	}

	resolved := withValidationFailureConcern(pending, failures, 2, false)
	if strings.Count(resolved, validationConcernLinePrefix) != 1 {
		t.Fatalf("resolved result duplicated validation concern: %s", resolved)
	}
	for _, want := range []string{
		"CONCERNS: analytics were delayed.",
		"CONCERNS: Validation history: attempt 1 failed - published receipt was missing; corrected on attempt 2.",
		"STATUS: COMPLETED",
	} {
		if !strings.Contains(resolved, want) {
			t.Fatalf("resolved result missing %q: %s", want, resolved)
		}
	}
	if strings.Index(resolved, validationConcernLinePrefix) > strings.Index(resolved, "STATUS: COMPLETED") {
		t.Fatalf("validation concern must precede terminal status: %s", resolved)
	}
}

func TestValidationFailureConcernMarksExhaustedRetries(t *testing.T) {
	failures := []validationFailureConcern{
		{Attempt: 1, Detail: "receipt was missing"},
		{Attempt: 2, Detail: "receipt was still missing"},
		{Attempt: 3, Detail: "receipt was still missing"},
	}

	got := withValidationFailureConcern("STATUS: FAILED", failures, 0, true)
	for _, want := range []string{
		"attempt 1 failed - receipt was missing",
		"attempt 3 failed - receipt was still missing",
		"unresolved after 3 attempt(s).",
		"STATUS: FAILED",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("exhausted result missing %q: %s", want, got)
		}
	}
}

func TestNewValidationFailureConcernCompactsReasoning(t *testing.T) {
	got := newValidationFailureConcern(2, &ValidationResponse{
		ExecutionStatus: "FAILED",
		Reasoning:       "receipt\n\nwas   missing",
	})
	if got.Attempt != 2 || got.Detail != "receipt was missing" {
		t.Fatalf("concern = %+v, want compact attempt 2 detail", got)
	}
}
