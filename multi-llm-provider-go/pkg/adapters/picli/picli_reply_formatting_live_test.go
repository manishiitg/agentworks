package picli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestPiCLIRealReplyFormattingFidelityE2E is the live contract for
// CertReplyFormattingFidelity on pi.
//
// Pi is structurally exempt from the defect the other three had: it returns text
// from its injected marker stream and never parses the pane for the final answer.
// This test exists to keep that true. If anyone ever "simplifies" pi to scrape
// the pane like its siblings, or the marker hook stops being injected and the
// adapter silently falls back to pane text, the wrapping would return and this is
// what catches it.
//
// It is also the control case for the whole certification: pi passing while a
// pane-based provider fails localizes the bug to extraction rather than to the
// prompt or the model.
func TestPiCLIRealReplyFormattingFidelityE2E(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	t.Cleanup(func() { _ = CleanupPiCLIInteractiveSessions(context.Background()) })

	adapter := newRealPiCLIAdapter(t)
	workDir := piLiveWorkDir(t)
	ownerSessionID := "pi-real-reply-format-" + piRandomHex(4)
	token := "FMT_" + piRandomHex(5)

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

	stream := make(chan llmtypes.StreamChunk, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not use tools. Reply with markdown only. Preserve blank lines between paragraphs and keep each list item on its own line."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt),
	},
		WithInteractiveSessionID(ownerSessionID),
		WithWorkingDir(workDir),
		WithPersistentInteractiveSession(true),
		llmtypes.WithStreamingChan(stream),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)

	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
	if !ok {
		t.Fatal("missing native session")
	}
	transcript, ok, err := ReadNativeTranscriptFromWorkingDir(workDir, handle.NativeSessionID)
	if err != nil || !ok || len(transcript.Messages) == 0 {
		t.Fatalf("read native record: %v", err)
	}
	nativeFinal := extractTextFromPiMessage(transcript.Messages[len(transcript.Messages)-1])
	testcontracts.AssertFinalAnswerPreservesStructure(t, testcontracts.ReplyFormattingCase{
		Provider:       "pi-cli",
		NativeFinal:    nativeFinal,
		Extracted:      content,
		WantParagraphs: 2,
		WantBullets:    3,
		UserGoal:       "Return the two long paragraphs and the three-item list with their markdown structure intact.",
		ExpectedNote:   "Real Pi RPC text preserves paragraphs and list structure.",
	})
}
