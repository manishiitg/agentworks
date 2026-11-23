package agycli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"
)

// A native tool can end in ERROR without an executor-error row or final
// assistant. Drive the production waiter through SQLite and an isolated tmux
// pane; the pane alone never proves success or failure.
func TestAgyNativeTerminalFailureRealTmux(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is required")
	}
	for _, tc := range []struct {
		name            string
		status          int
		busy, recovered bool
	}{
		{"error", agyStepStatusError, false, false},
		{"interrupted", agyStepStatusInterrupted, false, false},
		{"busy_error_keeps_waiting", agyStepStatusError, true, false},
		{"later_assistant_recovers", agyStepStatusError, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "native-end.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, status INTEGER, step_payload BLOB)`); err != nil {
				t.Fatal(err)
			}
			insert := func(idx, kind, status int, payload []byte) {
				t.Helper()
				if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?)`, idx, kind, status, payload); err != nil {
					t.Fatal(err)
				}
			}
			// An older failed step must never end the current turn.
			insert(0, agyStepToolCall, agyStepStatusError, []byte{})
			insert(1, agyStepUser, 3, agyTestPayload(agyStepUser, 19, 2, "synthetic request"))
			insert(2, agyStepAssistant, 3, agyTestPayload(agyStepAssistant, 20, 1, "Working"))
			insert(3, agyStepToolCall, tc.status, []byte{})
			if tc.recovered {
				insert(4, agyStepAssistant, 3, agyTestPayload(agyStepAssistant, 20, 1, "Finished"))
			}
			socket := fmt.Sprintf("agy-native-end-%d-%s", os.Getpid(), tc.name)
			run := func(args ...string) {
				t.Helper()
				out, err := exec.Command(realTmux, append([]string{"-L", socket, "-f", "/dev/null"}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("tmux: %v: %s", err, out)
				}
			}
			status := "? for shortcuts"
			if tc.busy {
				status += " esc to cancel"
			}
			run("new-session", "-d", "-s", "qa", "-x", "200", "-y", "50", "/bin/sh", "-c", "printf '%s' '"+status+"'; sleep 30")
			defer exec.Command(realTmux, "-L", socket, "kill-server").Run()
			shim := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\nexec '%s' -L '%s' -f /dev/null \"$@\"\n", realTmux, socket)
			if err := os.WriteFile(filepath.Join(shim, "tmux"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", shim+":"+os.Getenv("PATH"))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			session := &agyInteractiveSession{tmuxSessionName: "qa", transcriptHome: home, conversationID: "native-end"}
			err = agyWaitTurnAnswer(ctx, session, 1)
			if tc.busy {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("busy error ended turn: %v", err)
				}
				return
			}
			if tc.recovered {
				if err != nil {
					t.Fatalf("recovered error ended turn: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), agyExecutorErrorPrefix) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("native failure not released: %v", err)
			}
		})
	}
}

// Captured from real AGY 1.4.4 drafts. A character marker hides the prompt
// snippet just as a line marker does; it must not cause a delivery timeout.
func TestAgyPastedMarkerSizes(t *testing.T) {
	for _, tc := range []struct {
		name, pane, unit string
		size             int
		ok               bool
	}{
		{"lines", "> [Pasted text #1 +17 lines]\n? for shortcuts", "lines", 17, true},
		{"characters", "> [Pasted text #1 1280 chars]\n? for shortcuts", "chars", 1280, true},
		{"wrapped", "> [Pasted text #1\n  +17 lines]", "lines", 17, true},
		{"latest", "[Pasted text #1 +17 lines]\n> [Pasted text #2 1280 chars]", "chars", 1280, true},
		{"unfinished latest", "[Pasted text #1 +17 lines]\n> [Pasted text #2 1280", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			size, unit, ok := agyPastedMarkerSize(tc.pane)
			if size != tc.size || unit != tc.unit || ok != tc.ok {
				t.Fatalf("marker = (%d, %q, %t), want (%d, %q, %t)", size, unit, ok, tc.size, tc.unit, tc.ok)
			}
		})
	}
}

func TestAgyMountFingerprint(t *testing.T) {
	if got := agyMountFingerprint(""); got != "unmounted" {
		t.Fatalf("empty = %q, want unmounted", got)
	}
	if got := agyMountFingerprint("  \n"); got != "unmounted" {
		t.Fatalf("blank = %q, want unmounted", got)
	}
	a := agyMountFingerprint(`{"mcpServers":{"x":{"command":"node"}}}`)
	b := agyMountFingerprint("  " + `{"mcpServers":{"x":{"command":"node"}}}` + "\n")
	c := agyMountFingerprint(`{"mcpServers":{"y":{"command":"node"}}}`)
	if a == "unmounted" || a != b {
		t.Fatalf("same config fingerprints differ: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different configs share fingerprint %q", a)
	}
}

func TestAgyTurnRecordBindsMatchingUserAndWaitsForFinalAssistant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "turn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, status INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	insert := func(idx, stepType int, payload []byte) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), `INSERT INTO steps VALUES (?, ?, 3, ?)`, idx, stepType, payload); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, agyStepUser, agyTestPayload(agyStepUser, 19, 2, "old prompt"))
	insert(1, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "old answer"))
	insert(2, agyStepUser, agyTestPayload(agyStepUser, 19, 2, "new prompt"))
	insert(3, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 3, "private thinking"))
	insert(4, agyStepToolCall, agyTestPayload(agyStepToolCall, 5, 2, "tool"))
	record, err := agyReadTurnRecord("turn", -1, "new prompt")
	if err != nil || record.userIdx != 2 || record.lastType != agyStepToolCall || record.answer != "" {
		t.Fatalf("pending tool record = %+v, err %v", record, err)
	}
	insert(5, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "new answer"))
	record, err = agyReadTurnRecord("turn", 2, "")
	if err != nil || record.lastType != agyStepAssistant || record.lastStatus != 3 || record.answer != "new answer" || record.finalAnswer != "new answer" {
		t.Fatalf("completed record = %+v, err %v", record, err)
	}
	insert(6, agyStepUser, agyTestPayload(agyStepUser, 19, 2, "tool prompt"))
	insert(7, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "working on it"))
	insert(8, agyStepToolCall, agyTestPayload(agyStepToolCall, 5, 2, "tool"))
	insert(9, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "final answer"))
	record, err = agyReadTurnRecord("turn", 6, "")
	if err != nil || record.answer != "working on it\n\nfinal answer" || record.finalAnswer != "final answer" {
		t.Fatalf("progress/final record = %+v, err %v", record, err)
	}
	if got := agyTurnReplySince("turn", 6); got != "final answer" {
		t.Fatalf("final reply = %q, want final answer without progress", got)
	}
}

func TestAgyTurnUsageDecodesGoldenPayload(t *testing.T) {
	raw, err := os.ReadFile("testdata/assistant_step_usage.bin")
	if err != nil {
		t.Fatalf("read golden payload: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Captured live 2026-09-20: field 5.9 holds input 8523, output 313,
	// thinking 309 — the same turn shape exec JSON reports. Pins the tag
	// mapping against real CLI bytes, not hand-built fixtures.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "golden.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO steps (idx, step_type, step_payload) VALUES (0, 14, ?)`, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO steps (idx, step_type, step_payload) VALUES (1, 15, ?)`, raw); err != nil {
		t.Fatal(err)
	}
	// A tool does not make the native model-generation count grow. A second
	// assistant step does, even though the adapter returns one bounded turn.
	if _, err := db.ExecContext(t.Context(), `INSERT INTO steps (idx, step_type, step_payload) VALUES (2, 132, ?), (3, 15, ?)`, raw, raw); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	both, calls := agyTurnUsageAndCountSince("golden", -1)
	if calls != 2 || both.InputTokens != 17046 || both.OutputTokens != 626 {
		t.Fatalf("bounded turn usage=%+v calls=%d, want two native generations", both, calls)
	}
	got, calls := agyTurnUsageAndCountSince("golden", 1)
	if calls != 1 {
		t.Fatalf("usage since previous model step: calls=%d, want 1", calls)
	}
	if got.InputTokens != 8523 || got.OutputTokens != 313 {
		t.Fatalf("usage = %+v, want input 8523 output 313", got)
	}
	if got.TotalTokens != 8836 {
		t.Fatalf("total = %d, want 8836", got.TotalTokens)
	}
	if got.ThoughtsTokens == nil || *got.ThoughtsTokens != 309 {
		t.Fatalf("thinking = %+v, want 309", got.ThoughtsTokens)
	}
	if since := agyTurnUsageSince("golden", 3); since.TotalTokens != 0 {
		t.Fatalf("usage since idx 3 = %+v, want zero (no new steps)", since)
	}
	if missing := agyTurnUsageSince("nope", -1); missing.TotalTokens != 0 {
		t.Fatalf("usage for missing conversation = %+v, want zero", missing)
	}
}

// A real CHECKPOINT row (captured 2026-06-10, conversation 0370df02 idx 4) is
// the intent-only title checkpoint AGY writes after every first answer: it must
// not read as a compaction. The same row with intent_only cleared is what a
// real compaction records, and becomes one End chunk timed from its metadata.
func TestAgyCheckpointStepCompaction(t *testing.T) {
	raw, err := os.ReadFile("testdata/checkpoint_step_intent_only.bin")
	if err != nil {
		t.Fatal(err)
	}
	var compacting []byte
	for rest := raw; len(rest) > 0; {
		num, typ, n := protowire.ConsumeTag(rest)
		m := protowire.ConsumeFieldValue(num, typ, rest[n:])
		if n < 0 || m < 0 {
			t.Fatal("bad fixture")
		}
		field := rest[:n+m]
		if num == 30 {
			inner, _ := protowire.ConsumeBytes(rest[n:])
			var kept []byte
			for r := inner; len(r) > 0; {
				in, it, k := protowire.ConsumeTag(r)
				l := protowire.ConsumeFieldValue(in, it, r[k:])
				if in != 9 {
					kept = append(kept, r[:k+l]...)
				}
				r = r[k+l:]
			}
			field = protowire.AppendBytes(protowire.AppendTag(nil, 30, protowire.BytesType), kept)
		}
		compacting = append(compacting, field...)
		rest = rest[n+m:]
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "c1.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, status INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	for idx, payload := range [][]byte{raw, compacting} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO steps VALUES (?, 23, 3, ?)`, idx, payload); err != nil {
			t.Fatal(err)
		}
	}
	record, err := agyReadTurnRecord("c1", -1, "", home)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.compactions) != 1 {
		t.Fatalf("want only the non-intent checkpoint, got %+v", record.compactions)
	}
	chunk := llmtypes.ContextCompactionChunk(record.compactions[0])
	got := chunk.ContextCompaction
	if chunk.Type != llmtypes.StreamChunkTypeContextCompaction || got.Provider != "agy-cli" || got.Phase != llmtypes.ContextCompactionPhaseEnd ||
		got.ID != "c1:1" || got.Outcome != llmtypes.ContextCompactionOutcomeSuccess ||
		!got.StartedAt.Equal(time.Unix(1781070351, 779980000)) || !got.EndedAt.Equal(time.Unix(1781070352, 571718000)) ||
		got.DurationMs != 791 || got.TokensBefore != 0 || got.TokensAfter != 0 {
		t.Fatalf("unexpected compaction chunk %+v", got)
	}
}

// AGY ends a turn it cannot finish by writing an error step (type 17) and
// nothing after it. Live 2026-10-14: an upstream 500 left the app waiting ten
// minutes for an answer that was never coming.
func TestAgyTurnEndedByExecutorErrorFailsFastAndOnlyAfterTheFloor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "exec-error.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER, status INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	insert := func(idx, stepType int, payload []byte) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), `INSERT INTO steps VALUES (?, ?, 3, ?)`, idx, stepType, payload); err != nil {
			t.Fatal(err)
		}
	}
	errorPayload := []byte("\x0a\x10trajectory-id-xyz\x12\x80Agent execution terminated due to error. agent executor error: generating and executing: Error 500, Message: Internal error encountered., Status: INTERNAL, Details: [] (1) attached stack trace -- stack trace: agentexecutor.go:353")
	// An older turn that ended in an error, then a new turn that is still working.
	insert(0, agyStepUser, agyTestPayload(agyStepUser, 19, 2, "first prompt"))
	insert(1, agyStepExecutorError, errorPayload)
	insert(2, agyStepUser, agyTestPayload(agyStepUser, 19, 2, "second prompt"))
	insert(3, agyStepToolCall, agyTestPayload(agyStepToolCall, 5, 2, "tool"))
	record, err := agyReadTurnRecord("exec-error", 2, "")
	if err != nil || agyTurnEndedByExecutorError(record) {
		t.Fatalf("an error step before the turn's floor failed the new turn: %+v err %v", record, err)
	}
	// The new turn then ends in an error step: it fails at once with AGY's own text.
	insert(4, agyStepExecutorError, errorPayload)
	record, err = agyReadTurnRecord("exec-error", 2, "")
	if err != nil || !agyTurnEndedByExecutorError(record) {
		t.Fatalf("an error step after the floor did not end the turn: %+v err %v", record, err)
	}
	failure := agyExecutorTurnError(record.executorError)
	var typed *llmerrors.Error
	if !errors.As(failure, &typed) || typed.Kind != llmerrors.KindServerError || typed.Provider != "agy-cli" || !strings.HasPrefix(typed.Err.Error(), agyExecutorErrorPrefix) ||
		!strings.Contains(failure.Error(), "Error 500, Message: Internal error encountered.") || strings.Contains(failure.Error(), "stack trace") {
		t.Fatalf("failure = %v kind %v", failure, llmerrors.KindOf(failure))
	}
	// A notice step after the error (type 101) does not hide it.
	insert(5, 101, []byte("notice"))
	record, err = agyReadTurnRecord("exec-error", 2, "")
	if err != nil || !agyTurnEndedByExecutorError(record) {
		t.Fatalf("a notice after the error step hid it: %+v err %v", record, err)
	}
	// An assistant step after it means the turn went on: not failed.
	insert(6, agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "recovered"))
	record, err = agyReadTurnRecord("exec-error", 2, "")
	if err != nil || agyTurnEndedByExecutorError(record) {
		t.Fatalf("a turn that went on after an error step was failed: %+v err %v", record, err)
	}
}
