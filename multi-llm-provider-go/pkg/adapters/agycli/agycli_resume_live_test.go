package agycli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live resume P0 proofs for agy: one conversation across three turns earns
// multi_turn (turn 2 recalls turn 1 via --conversation) and
// structured_multi_turn (turn 3 returns schema-conformant JSON on the same
// resumed conversation). Ordered assertions localize a failure to the step.

func TestAgyCLIRealResumeMultiTurnContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	secret := "AGY_RESUME_" + agyRandomHex(t, 4)

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	turn1, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "This is a conversation memory exercise. Answer from the chat only; do not use tools, commands, files, or code."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "My secret word is "+secret+". Please remember it for my next question and acknowledge in a short sentence."),
	}, WithWorkingDir(workDir))
	if err != nil {
		t.Fatalf("turn 1 error = %v", err)
	}
	handle1, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(turn1)
	if !ok || handle1.NativeSessionID == "" {
		t.Fatalf("turn 1 missing conversation handle: %#v ok=%v", handle1, ok)
	}

	turn2, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "From our conversation, what was my secret word? Please answer in a short sentence without using tools."),
	}, WithWorkingDir(workDir), WithResumeSessionID(handle1.NativeSessionID))
	if err != nil {
		t.Fatalf("turn 2 (resume) error = %v", err)
	}
	if content := strings.TrimSpace(turn2.Choices[0].Content); !strings.Contains(content, secret) {
		t.Fatalf("turn 2 content = %q, want recalled secret %s", content, secret)
	}
	handle2, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(turn2)
	if !ok || handle2.NativeSessionID != handle1.NativeSessionID {
		t.Fatalf("turn 2 handle = %#v, want same conversation %s", handle2, handle1.NativeSessionID)
	}

	turn3, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "From our conversation, return my secret word as JSON. Reply with ONLY the raw JSON object: no code fences, no prose, no tools, no other text."),
	},
		WithWorkingDir(workDir),
		WithResumeSessionID(handle1.NativeSessionID),
		llmtypes.WithJSONSchema(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"secret": map[string]interface{}{"type": "string"},
			},
			"required": []string{"secret"},
		}, "secret_reply", "", true),
	)
	if err != nil {
		t.Fatalf("turn 3 (structured resume) error = %v", err)
	}
	var decoded struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(turn3.Choices[0].Content)), &decoded); err != nil {
		t.Fatalf("turn 3 content = %q, want schema JSON: %v", turn3.Choices[0].Content, err)
	}
	if !strings.Contains(decoded.Secret, secret) {
		t.Fatalf("turn 3 secret = %q, want recalled secret %s", decoded.Secret, secret)
	}
	handle3, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(turn3)
	if !ok || handle3.NativeSessionID != handle1.NativeSessionID {
		t.Fatalf("turn 3 handle = %#v, want same conversation %s", handle3, handle1.NativeSessionID)
	}
}
