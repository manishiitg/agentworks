package codexcli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexCLIRealReplyFormattingFidelityE2E is the live contract for
// CertReplyFormattingFidelity on codex.
//
// Codex was already reading its final answer from the rollout rather than the
// pane before this certification existed (see the finalExtractionSource branch in
// codexcli_interactive_adapter.go), so this test is a REGRESSION guard rather
// than proof of a new fix: it fails if anyone reverts to pane-derived text, or if
// the rollout stops being found for the current turn and the fallback silently
// takes over.
//
// Only a live run can prove that, because the defect lives in the gap between
// what the CLI wrote to disk and what a terminal displayed.
func TestCodexCLIRealReplyFormattingFidelityE2E(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-reply-format-" + codexRandomHex(4)
	token := "FMT_" + codexRandomHex(5)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// Long single-line paragraphs on purpose: a short reply would fit the pane
	// unwrapped and prove nothing. AssertFinalAnswerPreservesStructure fails
	// loudly when wrapping was not actually exercised.
	prompt := fmt.Sprintf(`This is a TEXT FORMATTING test. No research, no tools, and no knowledge of any identifier is needed — %s is just a label to copy through verbatim.

Reproduce the markdown below exactly as written, preserving every blank line and every "- " list marker, and output nothing else — no preamble, no explanation, no closing remark.

First paragraph for %s, written as one single long line of at least one hundred and forty characters so that it is certain to be wrapped by a narrow terminal window.

Second paragraph for %s, also one single long line of at least one hundred and forty characters, again long enough that the terminal will certainly wrap it onto several rows.

- first bullet for %s
- second bullet for %s
- third bullet for %s`, token, token, token, token, token, token)

	streamChan := make(chan llmtypes.StreamChunk, 128)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Do not use tools. Reply with markdown only. Preserve blank lines between paragraphs and keep each list item on its own line."},
		}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)

	tmuxSession, ok := activeCodexInteractiveSession(ownerSessionID)
	if !ok || tmuxSession == "" {
		t.Fatalf("expected an active Codex tmux session for %s", ownerSessionID)
	}
	pane, err := captureCodexPane(ctx, tmuxSession)
	if err != nil {
		t.Fatalf("capture Codex pane: %v", err)
	}
	_ = drainCodexStream(streamChan)

	testcontracts.AssertFinalAnswerPreservesStructure(t, testcontracts.ReplyFormattingCase{
		Provider:       "codex-cli",
		TmuxScreen:     pane,
		Extracted:      content,
		WantParagraphs: 2,
		WantBullets:    3,
		UserGoal:       "Return the two long paragraphs and the three-item list with their markdown structure intact.",
		ExpectedNote: "Live codex turn. The pane capture is expected to be hard-wrapped; the extracted answer must NOT be, " +
			"because it should have come from the rollout JSONL (readCodexTranscriptFinalAssistantText) rather than from the screen.",
	})
}
