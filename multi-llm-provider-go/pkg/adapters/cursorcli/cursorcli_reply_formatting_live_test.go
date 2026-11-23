package cursorcli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCursorCLIRealReplyFormattingFidelityE2E is the live contract for
// CertReplyFormattingFidelity on cursor.
//
// It asks the real CLI for a reply whose markdown structure is known up front —
// two paragraphs separated by a blank line, then a three-item list, with lines
// long enough that a terminal pane must wrap them — and asserts the answer the
// adapter hands back still has that structure.
//
// This is the only test that can prove the fix, because the defect lives in the
// gap between what the CLI wrote and what a terminal displayed. A fixture can
// show the reconciliation logic is sound (see
// TestCursorFinalAnswerPrefersUnwrappedTranscript) but cannot prove the real
// cursor-agent writes its store.db in the shape that logic assumes, nor that a
// real pane wraps the way we think. Only a live run closes that gap.
func TestCursorCLIRealReplyFormattingFidelityE2E(t *testing.T) {
	requireRealCursorCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCursorCLIInteractiveSessions(context.Background()) })

	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	ownerSessionID := "cursor-real-reply-format-" + cursorRandomHex(4)
	token := "FMT_" + cursorRandomHex(5)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// Long sentences on purpose: a short reply would fit the pane unwrapped and
	// the run would prove nothing. AssertFinalAnswerPreservesStructure fails
	// loudly if wrapping was not actually exercised.
	prompt := fmt.Sprintf(`This is a TEXT FORMATTING test. No research, no tools, and no knowledge of any identifier is needed — %s is just a label to copy through verbatim.

Reproduce the markdown below exactly as written, preserving every blank line and every "- " list marker, and output nothing else — no preamble, no explanation, no closing remark.

First paragraph for %s, written as one single long line of at least one hundred and forty characters so that it is certain to be wrapped by a narrow terminal window.

Second paragraph for %s, also one single long line of at least one hundred and forty characters, again long enough that the terminal will certainly wrap it onto several rows.

- first bullet for %s
- second bullet for %s
- third bullet for %s`, token, token, token, token, token, token)

	streamChan := make(chan llmtypes.StreamChunk, 4096)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Do not use tools. Reply with markdown only. Preserve blank lines between paragraphs and keep each list item on its own line."},
		}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithWorkingDir(t.TempDir()),
		WithPersistentInteractiveSession(true),
		WithDenyBuiltinTools(true),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)

	// The SDK keeps no transcript we read; the streamed reply is what the person watched arrive.
	streamed := drainCursorStream(streamChan)

	testcontracts.AssertFinalAnswerPreservesStructure(t, testcontracts.ReplyFormattingCase{
		Provider:       "cursor-cli",
		NativeFinal:    streamed.content,
		Extracted:      content,
		WantParagraphs: 2,
		WantBullets:    3,
		UserGoal:       "Return the two long paragraphs and the three-item list with their markdown structure intact.",
		ExpectedNote:   "Real Cursor SDK markdown is compared against the same turn’s streamed reply.",
	})
}
