package llmtypes

import (
	"testing"
	"time"
)

func TestSyntheticTerminalEmitsRoleAwareRows(t *testing.T) {
	ch := make(chan StreamChunk, 16)
	term := NewSyntheticTerminalWithTransport(ch, "test-cli", "auto", "structured_cli")

	term.Header("test-cli --output-format stream-json model=auto msgs=1")
	term.UserText("[AUTO-NOTIFICATION] Background agent started.\nAck briefly; do not call tools.")
	term.AssistantText("Acknowledged.")
	term.AssistantText(" I will wait for completion.")
	term.ToolStart("read_file", `{"path":"foo.txt"}`)
	term.ToolEnd("read_file", "ok", 25*time.Millisecond)
	term.Done(1200, "10 in · 3 out")

	latest := drainLatestTerminalChunk(t, ch)
	rows, ok := latest.Metadata["rows"].([]TerminalRow)
	if !ok {
		t.Fatalf("rows metadata type = %T, want []TerminalRow", latest.Metadata["rows"])
	}
	if len(rows) != 5 {
		t.Fatalf("rows len = %d, want 5: %#v", len(rows), rows)
	}
	if rows[0].Kind != "banner" {
		t.Fatalf("row[0].Kind = %q, want banner", rows[0].Kind)
	}
	if rows[1].Kind != "user" || rows[1].Text != "[AUTO-NOTIFICATION] Background agent started.\nAck briefly; do not call tools." {
		t.Fatalf("user row = %#v", rows[1])
	}
	if rows[2].Kind != "asst" || rows[2].Text != "Acknowledged. I will wait for completion." {
		t.Fatalf("assistant row = %#v", rows[2])
	}
	if rows[3].Kind != "tool" || rows[3].Name != "read_file" || rows[3].ResultPrefix != "✓" || rows[3].Result == "" {
		t.Fatalf("tool row = %#v", rows[3])
	}
	if rows[4].Kind != "done" {
		t.Fatalf("row[4].Kind = %q, want done", rows[4].Kind)
	}
	if latest.Content == "" || latest.Metadata["transport"] != "structured_cli" {
		t.Fatalf("latest chunk missing content/transport: %#v", latest)
	}
}

func TestFormatConversationRowsKeepsUserAndAssistantRoles(t *testing.T) {
	rows := formatConversationRows([]MessageContent{
		{
			Role: ChatMessageTypeHuman,
			Parts: []ContentPart{
				TextContent{Text: "first line\nsecond line"},
			},
		},
		{
			Role: ChatMessageTypeAI,
			Parts: []ContentPart{
				TextContent{Text: "assistant reply"},
			},
		},
	})

	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2: %#v", len(rows), rows)
	}
	if rows[0].Kind != "user" || rows[0].Text != "first line\nsecond line" {
		t.Fatalf("user row = %#v", rows[0])
	}
	if rows[1].Kind != "asst" || rows[1].Text != "assistant reply" {
		t.Fatalf("assistant row = %#v", rows[1])
	}
}

func drainLatestTerminalChunk(t *testing.T, ch <-chan StreamChunk) StreamChunk {
	t.Helper()
	var latest StreamChunk
	for {
		select {
		case latest = <-ch:
		default:
			if latest.Type == "" {
				t.Fatal("no terminal chunks emitted")
			}
			return latest
		}
	}
}
