package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaudeDurableFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeUserRow(ts time.Time, content string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":"sess-1","message":{"role":"user","content":%s}}`, ts.Format(time.RFC3339Nano), quoteJSON(content))
}

func claudeEnqueueRow(ts time.Time, content string) string {
	return fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":%q,"sessionId":"sess-1","content":%s}`, ts.Format(time.RFC3339Nano), quoteJSON(content))
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func claudeDrainRow(op string, ts time.Time, content string) string {
	if content == "" {
		return fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":%q,"sessionId":"sess-1"}`, op, ts.Format(time.RFC3339Nano))
	}
	return fmt.Sprintf(`{"type":"queue-operation","operation":%q,"timestamp":%q,"sessionId":"sess-1","content":%s}`, op, ts.Format(time.RFC3339Nano), quoteJSON(content))
}

func TestClaudeInteractiveSessionRegistered(t *testing.T) {
	owner := "claude-registered-owner"
	if InteractiveSessionRegistered(owner) {
		t.Fatal("an unregistered owner must not be steer-ready")
	}
	claudeInteractiveOwnerRegistry.Set(owner, "mlp-claude-test")
	t.Cleanup(func() { claudeInteractiveOwnerRegistry.Delete(owner) })
	if !InteractiveSessionRegistered(owner) {
		t.Fatal("a registered owner must be steer-ready")
	}
}

// Two Escapes at an idle prompt open Claude Code's Rewind list; its
// highlighted "❯ (current)" entry must not be taken for a stale draft
// (local 2026-09-25 07:49: two live inputs failed trying to delete it).
func TestClaudeRewindSelectorIsNotADraft(t *testing.T) {
	rewind := strings.Join([]string{
		"  Rewind",
		"  Restore the code and/or conversation to the point before…",
		"    reply with just OK",
		"    No code changes",
		"  ❯ (current)",
		"  Enter to continue · Esc to cancel",
	}, "\n")
	if !isClaudeRewindSelector(rewind) {
		t.Fatal("the Rewind list must be recognised")
	}
	idle := "────\n❯ \n────\n  ⏸ manual mode on · ← for agents"
	if isClaudeRewindSelector(idle) {
		t.Fatal("an idle prompt is not the Rewind list")
	}
	if isClaudeRewindSelector("❯ explain what (current) means\nesc to cancel") {
		t.Fatal("typed text mentioning (current) is not the Rewind list")
	}
}
