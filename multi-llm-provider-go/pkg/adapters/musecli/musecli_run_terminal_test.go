package musecli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func museTerminalFixture(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func museRunRow(seq int, runID, kind, terminal string) string {
	return fmt.Sprintf(`{"sequence":%d,"payload_type":"runtime.session","payload":{"kind":"run","run_id":%q,"event":{"kind":%q,"terminal":%q}}}`,
		seq, runID, kind, terminal)
}

func TestMuseRunTerminalIsCurrentRunOnly(t *testing.T) {
	path := museTerminalFixture(t,
		museRunRow(9, "current", "terminal", "completed"), // before this intake
		museRunRow(11, "older", "terminal", "completed"),
		`{"sequence":12,"payload_type":"runtime.session","payload":{"kind":"task","run_id":"current","event":{"kind":"completed"}}}`,
		museRunRow(13, "current", "model_completed", ""),
		museRunRow(14, "current", "assistant_message_committed", ""),
		`{"sequence":15,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"current","event":{"kind":"terminal"}}`, // incomplete append
	)
	if _, _, found, err := museRunTerminal(path, "current", 10); err != nil || found {
		t.Fatalf("intermediate rows must not complete turn: found=%v err=%v", found, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(museRunRow(16, "current", "terminal", "completed") + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	status, _, found, err := museRunTerminal(path, "current", 10)
	if err != nil || !found || status != "completed" {
		t.Fatalf("current run terminal = (%q, %v, %v), want completed", status, found, err)
	}
}

func TestMuseAcceptedIntentAndRunScopedAnswer(t *testing.T) {
	at := time.Now().UnixMicro()
	path := museTerminalFixture(t,
		fmt.Sprintf(`{"sequence":1,"recorded_at":%d,"payload_type":"runtime.user_intent.accepted","payload":{"intent_id":"old","refill_blocks":[{"kind":"text","text":"repeat prompt"}]}}`, at-1000000),
		museRunRow(2, "old", "assistant_message_committed", ""),
		fmt.Sprintf(`{"sequence":3,"recorded_at":%d,"payload_type":"runtime.user_intent.accepted","payload":{"intent_id":"current","refill_blocks":[{"kind":"text","text":"repeat prompt"}]}}`, at),
		`{"sequence":4,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"current","event":{"kind":"assistant_message_committed","text":"current answer"}}}`,
		`{"sequence":5,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"old","event":{"kind":"assistant_message_committed","text":"stale answer"}}}`,
	)
	runID, seq, ok := museAcceptedIntentSince(path, time.UnixMicro(at), "repeat prompt")
	if !ok || runID != "current" || seq != 3 {
		t.Fatalf("accepted intent = (%q, %d, %v), want current at 3", runID, seq, ok)
	}
	messages, ok := readMuseTranscriptMessages(path, runID)
	if !ok || museLastAssistantText(messages) != "current answer" {
		t.Fatalf("run-scoped final = %q, want current answer", museLastAssistantText(messages))
	}
}

func TestMuseRunTerminalFailureIsVisible(t *testing.T) {
	path := museTerminalFixture(t,
		`{"sequence":22,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"current","event":{"kind":"terminal","terminal":"failed","reason":"tool failed"}}}`,
	)
	status, reason, found, err := museRunTerminal(path, "current", 21)
	if err != nil || !found || status != "failed" || reason != "tool failed" {
		t.Fatalf("failed terminal = (%q, %q, %v, %v)", status, reason, found, err)
	}
}

func TestMuseRunTerminalIncrementalPartialRow(t *testing.T) {
	path := museTerminalFixture(t, museRunRow(1, "other", "terminal", "completed"))
	var offset int64
	if _, _, found, err := museRunTerminalSince(path, "current", 2, &offset); err != nil || found {
		t.Fatalf("initial scan: found=%v err=%v", found, err)
	}
	firstOffset := offset
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	row := museRunRow(3, "current", "terminal", "completed")
	if _, err := f.WriteString(row[:len(row)/2]); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := museRunTerminalSince(path, "current", 2, &offset); err != nil || found || offset != firstOffset {
		t.Fatalf("partial scan: found=%v offset=%d want=%d err=%v", found, offset, firstOffset, err)
	}
	if _, err := f.WriteString(row[len(row)/2:] + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	status, _, found, err := museRunTerminalSince(path, "current", 2, &offset)
	if err != nil || !found || status != "completed" {
		t.Fatalf("completed append: status=%q found=%v err=%v", status, found, err)
	}
}

// Muse records a long prompt that reached it as a paste as "[Pasted Content N chars]" in refill_blocks and the real text in
// model_messages; intake must still be confirmed from the real text (the turn ran, the old check called it unconfirmed).
func TestMuseAcceptedIntentWhenMuseCollapsesALongPrompt(t *testing.T) {
	at := time.Now().UnixMicro()
	prompt := "Webhook receipt watch. Post everything to Slack using the slack tool and list the unseen deliveries."
	path := museTerminalFixture(t,
		fmt.Sprintf(`{"sequence":25,"recorded_at":%d,"payload_type":"runtime.user_intent.accepted","payload":{"intent_id":"run-1","refill_blocks":[{"kind":"text","text":"[Pasted Content %d chars]"}],"model_messages":[{"content":[{"text":%q}]}]}}`, at, len(prompt), prompt),
	)
	runID, seq, ok := museAcceptedIntentSince(path, time.UnixMicro(at), promptSnippet(prompt))
	if !ok || runID != "run-1" || seq != 25 {
		t.Fatalf("accepted intent = (%q, %d, %v), want run-1 at 25", runID, seq, ok)
	}
	if _, _, ok := museAcceptedIntentSince(path, time.UnixMicro(at), "A different prompt entirely"); ok {
		t.Fatal("a placeholder record matched a prompt it does not hold")
	}
}
