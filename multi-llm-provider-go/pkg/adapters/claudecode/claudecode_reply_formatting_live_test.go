package claudecode

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeCodeTmuxRealReplyFormattingFidelityE2E is the live contract for
// CertReplyFormattingFidelity on claude-code.
//
// Claude already preferred its JSONL transcript over the pane scrape, but the
// preference was gated on `len(transcript) > len(pane)`. That repairs truncation
// and is blind to RE-WRAPPING: wrapping substitutes a newline for a space, so
// both forms are the same length and the longer-is-better test never fires. The
// gate now goes through llmtypes.ReconcileFinalAnswer, which compares by words.
//
// This test is what proves that end to end. A fixture can show the comparison
// logic is right (TestClaudeFinalAnswerRepairsWrappingNotJustTruncation) but
// cannot prove a real pane wraps the way we assume, nor that claude's real JSONL
// holds the authored form.
func TestClaudeCodeTmuxRealReplyFormattingFidelityE2E(t *testing.T) {
	skipClaudeInteractiveLiveE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-reply-format-e2e-" + randomHex(4)
	token := "FMT_" + randomHex(5)

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

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Do not use tools. Reply with markdown only. Preserve blank lines between paragraphs and keep each list item on its own line."},
		}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithEffort("low"),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := strings.TrimSpace(firstChoiceText(resp))

	tmuxSession, ok := activeClaudeInteractiveOwner(ownerSessionID)
	if !ok || tmuxSession == "" {
		t.Fatalf("expected an active Claude Code tmux session for %s", ownerSessionID)
	}
	pane, err := captureTmuxPane(ctx, tmuxSession)
	if err != nil {
		t.Fatalf("capture Claude Code pane: %v", err)
	}

	testcontracts.AssertFinalAnswerPreservesStructure(t, testcontracts.ReplyFormattingCase{
		Provider:       "claude-code",
		TmuxScreen:     pane,
		Extracted:      content,
		WantParagraphs: 2,
		WantBullets:    3,
		UserGoal:       "Return the two long paragraphs and the three-item list with their markdown structure intact.",
		ExpectedNote: "Live claude-code turn. The pane capture is expected to be hard-wrapped; the extracted answer must NOT be, " +
			"because it should have come from the JSONL transcript via llmtypes.ReconcileFinalAnswer rather than from the screen.",
	})
}
