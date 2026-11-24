package sessiontransfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCutBundle(t *testing.T, format string, lines []string) *Bundle {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	return &Bundle{Format: format, Root: root, Files: map[string]File{"session.jsonl": info}}
}

func readCut(t *testing.T, b *Bundle) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(b.Root, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func TestCutBeforeClaudeDropsPromptAndBookkeeping(t *testing.T) {
	b := writeCutBundle(t, "claude-jsonl", []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"add a retry"}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"user","uuid":"t1","message":{"role":"user","content":[{"type":"tool_result","content":"add a retry"}]}}`,
		`{"type":"file-history-snapshot","messageId":"u2"}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"text","text":"[notice]\nadd a retry"}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"text","text":"again"}]}}`,
	})
	kept, err := CutBefore(b, "add a retry", 1)
	if err != nil {
		t.Fatal(err)
	}
	lines := readCut(t, b)
	if kept != 1 || len(lines) != 3 || !strings.Contains(lines[2], `"t1"`) {
		t.Fatalf("kept=%d lines=%v", kept, lines)
	}
	if b.Files["session.jsonl"].Size != int64(len(strings.Join(lines, "\n"))+1) {
		t.Fatalf("bundle size not refreshed: %+v", b.Files["session.jsonl"])
	}
}

func TestCutBeforeClaudeOlderMatchAndMissing(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"same"}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":"x"}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"same"}}`,
	}
	b := writeCutBundle(t, "claude-jsonl", lines)
	kept, err := CutBefore(b, "same", 2)
	if got := readCut(t, b); err != nil || kept != 0 || len(got) != 1 || got[0] != "" {
		t.Fatalf("kept=%d err=%v lines=%v", kept, err, got)
	}
	b = writeCutBundle(t, "claude-jsonl", lines)
	if _, err := CutBefore(b, "same", 3); err != ErrCutNotFound {
		t.Fatalf("want ErrCutNotFound, got %v", err)
	}
	if _, err := CutBefore(b, "other", 1); err != ErrCutNotFound {
		t.Fatalf("want ErrCutNotFound, got %v", err)
	}
}

func TestCutBeforeCodexCutsAtTurnStart(t *testing.T) {
	b := writeCutBundle(t, "codex-rollout", []string{
		`{"type":"session_meta","payload":{"id":"s"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first ask"}]}}`,
		`{"type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"t2"}}`,
		`{"type":"turn_context","payload":{}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second ask"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"second ask"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete"}}`,
	})
	kept, err := CutBefore(b, "second ask", 1)
	if err != nil {
		t.Fatal(err)
	}
	lines := readCut(t, b)
	if kept != 1 || len(lines) != 5 || !strings.Contains(lines[4], "task_complete") {
		t.Fatalf("kept=%d lines=%v", kept, lines)
	}
}

func TestCutBeforeRefusesOtherFormats(t *testing.T) {
	b := writeCutBundle(t, "pi-jsonl", []string{`{"type":"session"}`})
	if _, err := CutBefore(b, "x", 1); err == nil {
		t.Fatal("expected refusal")
	}
}
