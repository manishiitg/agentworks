package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The "Chat summarized" row opens the summary the CLI wrote into its own session (#3236): Claude's text without
// its agent-only wrapper, Codex's kept messages without the injected instructions, matched to the row by time.
func TestCompactionSummariesFromSessionFiles(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "claude.jsonl")
	summary := "This session is being continued from a previous conversation that ran out of context.\n\n<system-reminder>\n<tools-used-before-summary>Bash, Edit</tools-used-before-summary>\n</system-reminder>\nSummary:\n1. Primary Request:\n   - Clean up the invoices.\n\nIf you need specific details from before compaction, read the transcript.\nContinue the conversation from where it left off."
	lines := []string{
		`{"type":"user","timestamp":"2026-01-02T09:00:00Z","message":{"role":"user","content":"hello"}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-01-02T10:00:00Z","compactMetadata":{"preTokens":315000,"postTokens":9000}}`,
		`{"type":"user","timestamp":"2026-01-02T10:00:01Z","isCompactSummary":true,"message":{"role":"user","content":` + jsonString(summary) + `}}`,
		`{"type":"user","timestamp":"2026-01-02T12:00:01Z","isCompactSummary":true,"message":{"role":"user","content":[{"type":"text","text":"Summary:\nSecond one."}]}}`,
	}
	if err := os.WriteFile(claude, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := compactionSummariesFromFile(claude, "claude-code")
	if err != nil || len(got) != 2 {
		t.Fatalf("claude summaries = %+v, %v", got, err)
	}
	if got[0].Text != "1. Primary Request:\n   - Clean up the invoices." || got[0].TokensBefore != 315000 || got[0].TokensAfter != 9000 {
		t.Fatalf("claude summary not cleaned or sized: %+v", got[0])
	}
	if got[1].TokensBefore != 0 || got[1].Text != "Second one." {
		t.Fatalf("second summary = %+v", got[1])
	}
	if pick, ok := pickCompactionSummary(got, time.Date(2026, 1, 2, 10, 0, 30, 0, time.UTC)); !ok || pick.Text != got[0].Text {
		t.Fatalf("row at 10:00 should open the first summary, got %+v %t", pick, ok)
	}
	if pick, ok := pickCompactionSummary(got, time.Time{}); !ok || pick.Text != "Second one." {
		t.Fatalf("no time should open the newest, got %+v", pick)
	}
	if _, ok := pickCompactionSummary(got, time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)); ok {
		t.Fatal("a row a day away matched a summary of another compaction")
	}

	codex := filepath.Join(dir, "rollout.jsonl")
	line := `{"timestamp":"2026-01-02T11:14:00Z","type":"compacted","payload":{"message":"","replacement_history":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work\n\nrules"}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"Clean up last month's invoices."}]},` +
		`{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are the agent."}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"[Current AgentWorks Instructions]\nlong rules\n\n[Current User Message]\nMatch by number."}]},` +
		`{"type":"compaction","encrypted_content":"sealed"}]}}`
	if err := os.WriteFile(codex, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := compactionSummariesFromFile(codex, "codex-cli")
	if err != nil || len(kept) != 1 || kept[0].Kind != "kept_messages" {
		t.Fatalf("codex = %+v, %v", kept, err)
	}
	if strings.Join(kept[0].Messages, "|") != "Clean up last month's invoices.|Match by number." {
		t.Fatalf("codex kept messages = %q", kept[0].Messages)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
