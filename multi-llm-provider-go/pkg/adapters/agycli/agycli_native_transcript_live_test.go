package agycli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestAgyCLIRealNativeTranscriptContract is the CertNativeTranscriptRecovery
// P0 proof: a real 2-turn conversation is re-read from the CLI's own
// conversations/<id>.db by native session id, and the user/assistant texts
// come back in order. This is the format-drift alarm for the protowire
// reader: if agy renumbers step types or payload fields, this fails loudly
// instead of silently backfilling wrong history.
func TestAgyCLIRealNativeTranscriptContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	canary1 := "AGY_TRANSCRIPT_" + agyRandomHex(t, 4)
	canary2 := "AGY_RESUME_READ_" + agyRandomHex(t, 4)

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	turn1, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not use tools."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this word: "+canary1),
	}, WithWorkingDir(workDir))
	if err != nil {
		t.Fatalf("turn 1 error = %v", err)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(turn1)
	if !ok || handle.NativeSessionID == "" {
		t.Fatalf("turn 1 missing conversation handle: %#v ok=%v", handle, ok)
	}
	if _, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this word: "+canary2),
	}, WithWorkingDir(workDir), WithResumeSessionID(handle.NativeSessionID)); err != nil {
		t.Fatalf("turn 2 (resume) error = %v", err)
	}

	transcript, ok, err := ReadNativeTranscript(handle.NativeSessionID)
	if err != nil {
		t.Fatalf("ReadNativeTranscript() error = %v", err)
	}
	if !ok {
		t.Fatalf("ReadNativeTranscript(%s) ok = false, want the just-created conversation", handle.NativeSessionID)
	}
	texts := make([]string, 0, len(transcript.Messages))
	for _, m := range transcript.Messages {
		var sb strings.Builder
		for _, p := range m.Parts {
			if part, ok := p.(llmtypes.TextContent); ok {
				sb.WriteString(part.Text)
			}
		}
		texts = append(texts, string(m.Role)+":"+sb.String())
	}
	t.Logf("transcript %s messages: %q", transcript.Path, texts)
	want := []struct {
		role llmtypes.ChatMessageType
		part string
	}{
		{llmtypes.ChatMessageTypeHuman, canary1},
		{llmtypes.ChatMessageTypeAI, canary1},
		{llmtypes.ChatMessageTypeHuman, canary2},
		{llmtypes.ChatMessageTypeAI, canary2},
	}
	if len(transcript.Messages) != len(want) {
		t.Fatalf("messages = %d, want %d: %q", len(transcript.Messages), len(want), texts)
	}
	for i, w := range want {
		if transcript.Messages[i].Role != w.role || !strings.Contains(texts[i], w.part) {
			t.Errorf("message %d = %q, want role %q containing %q", i, texts[i], w.role, w.part)
		}
	}
}
