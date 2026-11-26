package musecli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBackgroundTaskReaderAfterForegroundTerminal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	nativeID := "test-session"
	path := filepath.Join(root, "muse", "sessions", "2026", "09", "23", nativeID, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	appendRows := func(rows ...string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, row := range rows {
			if _, err := f.WriteString(row); err != nil {
				t.Fatal(err)
			}
		}
	}
	row := func(seq int, scope, run, kind, task, extra string) string {
		return fmt.Sprintf(`{"sequence":%d,"payload_type":"runtime.session","payload":{"kind":%q,"run_id":%q,"event":{"kind":%q,"task_id":%q%s}}}`+"\n", seq, scope, run, kind, task, extra)
	}
	appendRows(
		row(1, "run", "old", "task_backgrounded", "old-task", ""),
		row(2, "task", "old", "completed", "old-task", ""),
	)
	baseline := LatestNativeSequence(nativeID)
	if baseline != 2 {
		t.Fatalf("baseline=%d", baseline)
	}
	appendRows(
		row(3, "run", "current", "task_backgrounded", "task-a", ""),
		row(4, "run", "current", "terminal", "", `,"terminal":"completed"`),
	)
	reader := NewBackgroundTaskReader(nativeID, baseline)
	first, err := reader.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Kind != "task_backgrounded" || first[0].Sequence != 3 {
		t.Fatalf("first=%+v", first)
	}
	// The partial trailing row must remain unread until newline commit.
	late := row(5, "task", "current", "status", "task-a", `,"message":"still working"`)
	appendRows(late[:len(late)-1])
	if events, err := reader.Poll(); err != nil || len(events) != 0 {
		t.Fatalf("partial=%+v err=%v", events, err)
	}
	appendRows("\n", row(6, "task", "current", "output", "task-a", `,"chunk":"done"`), row(7, "task", "current", "completed", "task-a", ""))
	second, err := reader.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 3 || second[0].Message != "still working" || second[1].Message != "done" || second[2].Kind != "completed" {
		t.Fatalf("late events=%+v", second)
	}
	if again, err := reader.Poll(); err != nil || len(again) != 0 {
		t.Fatalf("replayed=%+v err=%v", again, err)
	}
}

func TestBackgroundTaskReaderUsesOwnerIsolatedDataHome(t *testing.T) {
	root := t.TempDir()
	isolated := filepath.Join(root, "account-data")
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "ambient"))
	const owner = "background-owner-isolated"
	const nativeID = "native-isolated"
	path := filepath.Join(isolated, "muse", "sessions", "2026", "09", "23", nativeID, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	rows := `{"sequence":1,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"task_backgrounded","task_id":"task-1"}}}` + "\n"
	if err := os.WriteFile(path, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	reader := NewBackgroundTaskReaderForOwner(owner, nativeID, 0)
	if got, err := reader.Poll(); err != nil || len(got) != 0 {
		t.Fatalf("before pool=%+v err=%v", got, err)
	}
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = &musePersistentSession{nativeSessionID: nativeID, accountDataHome: isolated, logPath: path}
	musePersistentPool.Unlock()
	t.Cleanup(func() { musePersistentPool.Lock(); delete(musePersistentPool.m, owner); musePersistentPool.Unlock() })
	got, err := reader.Poll()
	if err != nil || len(got) != 1 || got[0].Kind != "task_backgrounded" {
		t.Fatalf("after pool=%+v err=%v", got, err)
	}
	if seq := LatestNativeSequenceForOwner(owner, nativeID); seq != 1 {
		t.Fatalf("isolated sequence=%d", seq)
	}
}

func TestBackgroundTaskReaderIncludesUnmarkedLateTask(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	const nativeID = "native-unmarked-late"
	path := filepath.Join(root, "muse", "sessions", "2026", "09", "23", nativeID, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	rows := `{"sequence":1,"payload_type":"runtime.session","payload":{"kind":"task","run_id":"run-1","event":{"kind":"started","task_id":"task-1"}}}` + "\n" +
		`{"sequence":2,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"terminal","terminal":"completed"}}}` + "\n" +
		`{"sequence":3,"payload_type":"runtime.session","payload":{"kind":"task","run_id":"run-1","event":{"kind":"status","task_id":"task-1","message":"wrapping up"}}}` + "\n" +
		`{"sequence":4,"payload_type":"runtime.session","payload":{"kind":"task","run_id":"run-1","event":{"kind":"completed","task_id":"task-1"}}}` + "\n"
	if err := os.WriteFile(path, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := NewBackgroundTaskReader(nativeID, 0).Poll()
	if err != nil || len(got) != 2 || got[0].Sequence != 3 || got[1].Kind != "completed" {
		t.Fatalf("late unmarked=%+v err=%v", got, err)
	}
}
