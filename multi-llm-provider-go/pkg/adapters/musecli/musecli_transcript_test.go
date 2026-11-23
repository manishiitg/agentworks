package musecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Synthetic fixture mirroring muse 1.1.1 shapes (usage magnitudes are
// invented; the envelope/field paths are real). Covers: intake prompt,
// assistant commit, same-run + foreign-run usage rows, an unrelated row,
// and a malformed line.
const museSampleSessionJSONL = `{"payload_type":"runtime.command_intake.received","payload":{"kind":"command_intake","record":{"kind":"received","command_id":"run-r1","command":{"prompt":"hello"}}}}
{"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-r1","event":{"kind":"assistant_message_committed","message_id":"m1","text":"hi there"}}}
{"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-r1","event":{"kind":"goal_usage_attribution","record":{"usage_family":"provider","quantity":{"unit":"tokens","reported":true,"input_tokens":11,"output_tokens":7,"cached_tokens":3,"reasoning_tokens":0},"owner":{"run_id":"run-r1"}}}}}
{"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-r2","event":{"kind":"goal_usage_attribution","record":{"usage_family":"provider","quantity":{"unit":"tokens","reported":true,"input_tokens":99,"output_tokens":99,"cached_tokens":0,"reasoning_tokens":0},"owner":{"run_id":"run-r2"}}}}}
{"payload_type":"session.end","payload":{"kind":"session_end"}}
not-json
`

func writeMuseSampleLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(museSampleSessionJSONL), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestReadMuseTranscriptUsageFiltersRun(t *testing.T) {
	usage, ok := readMuseTranscriptUsage(writeMuseSampleLog(t), "run-r1")
	if !ok {
		t.Fatal("want ok=true for matching run")
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 || usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v, want 11/7/18", usage)
	}
	if usage.CacheTokens == nil || *usage.CacheTokens != 3 {
		t.Fatalf("cache = %+v, want 3", usage.CacheTokens)
	}
	if usage.ReasoningTokens != nil {
		t.Fatalf("reasoning = %v, want nil for zero", *usage.ReasoningTokens)
	}
	if _, ok := readMuseTranscriptUsage(writeMuseSampleLog(t), "run-unknown"); ok {
		t.Fatal("want ok=false when no row matches")
	}
}

func TestReadMuseTranscriptMessagesInOrder(t *testing.T) {
	msgs, ok := readMuseTranscriptMessages(writeMuseSampleLog(t), "run-r1")
	if !ok {
		t.Fatal("want ok=true for readable log")
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want human+assistant", len(msgs))
	}
	if msgs[0].Role != llmtypes.ChatMessageTypeHuman || msgs[1].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("roles = %q/%q, want human/ai", msgs[0].Role, msgs[1].Role)
	}
	if text := messageText(msgs[1]); !strings.Contains(text, "hi there") {
		t.Fatalf("assistant text = %q", text)
	}
	if _, ok := readMuseTranscriptMessages(filepath.Join(t.TempDir(), "missing.jsonl"), "run-r1"); ok {
		t.Fatal("want ok=false for unreadable log")
	}
}

func TestReadMuseTranscriptMessagesAcceptsPersistentUserIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := `{"payload_type":"runtime.user_intent.accepted","payload":{"intent_id":"run-live","model_messages":[{"content":[{"kind":"text","text":"duplicate copy"}]}],"refill_blocks":[{"kind":"text","text":"follow up from chat"}]}}` + "\n" +
		`{"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-live","event":{"kind":"assistant_message_committed","text":"retained answer"}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	messages, ok := readMuseTranscriptMessages(path, "run-live")
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %+v, ok=%v; want one human and one assistant", messages, ok)
	}
	if got := messageText(messages[0]); got != "follow up from chat" {
		t.Fatalf("human text = %q", got)
	}
	if got := messageText(messages[1]); got != "retained answer" {
		t.Fatalf("assistant text = %q", got)
	}
}

func messageText(m llmtypes.MessageContent) string {
	var b strings.Builder
	for _, part := range m.Parts {
		switch c := part.(type) {
		case llmtypes.TextContent:
			b.WriteString(c.Text)
		case *llmtypes.TextContent:
			if c != nil {
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}
