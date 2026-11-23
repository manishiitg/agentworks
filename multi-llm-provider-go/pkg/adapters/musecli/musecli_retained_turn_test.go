package musecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMuseRetainedProgressWhileBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	owner := t.Name()
	entry := &musePersistentSession{logPath: path, retainedBaselineSequence: 10}
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = entry
	musePersistentPool.Unlock()
	previousReady := museRetainedTurnReady
	museRetainedTurnReady = func(string, string) bool { return false }
	t.Cleanup(func() {
		museRetainedTurnReady = previousReady
		musePersistentPool.Lock()
		delete(musePersistentPool.m, owner)
		musePersistentPool.Unlock()
	})
	appendRecord := func(seq int, kind, text string) {
		t.Helper()
		err := json.NewEncoder(file).Encode(map[string]interface{}{
			"sequence": seq, "payload_type": "runtime.session",
			"payload": map[string]interface{}{"kind": "run", "event": map[string]string{"kind": kind, "text": text}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	appendRecord(5, "assistant_message_committed", "Previous answer.")
	for i, kind := range []string{"reasoning_summary_delta", "assistant_message_committed", "assistant_message_committed"} {
		// Identical text on distinct commits is still two legitimate updates.
		appendRecord(11+i, kind, "Checking the report.")
		got := ReadRetainedTurnProgressMessages(owner, start)
		if len(got) != 1 || messageText(got[0]) != "Checking the report." {
			t.Fatalf("missing update %d: %+v", i, got)
		}
		if got := ReadRetainedTurnProgressMessages(owner, start); len(got) != 0 {
			t.Fatalf("replayed update: %+v", got)
		}
		if got := ReadRetainedTurnMessages(owner, start); len(got) != 0 {
			t.Fatalf("progress completed busy turn: %+v", got)
		}
	}
	// A new submission takes a fresh baseline and skips the old turn.
	musePersistentPool.Lock()
	entry.retainedBaselineSequence = 13
	musePersistentPool.Unlock()
	if got := ReadRetainedTurnProgressMessages(owner, start.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("replayed old turn: %+v", got)
	}
	appendRecord(14, "assistant_message_committed", "New turn.")
	if got := ReadRetainedTurnProgressMessages(owner, start.Add(time.Minute)); len(got) != 1 || messageText(got[0]) != "New turn." {
		t.Fatalf("missing next turn: %+v", got)
	}
	_, err = file.WriteString(`{"sequence":15,"payload":{"kind":"run","event":{"kind":"assistant_message_committed","text":"Finished."}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := ReadRetainedTurnProgressMessages(owner, start.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("published uncommitted row: %+v", got)
	}
	if _, err := file.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	if got := ReadRetainedTurnProgressMessages(owner, start.Add(time.Minute)); len(got) != 1 || messageText(got[0]) != "Finished." {
		t.Fatalf("lost partial row: %+v", got)
	}
	museRetainedTurnReady = func(string, string) bool { return true }
	if got := ReadRetainedTurnMessages(owner, start.Add(time.Minute)); len(got) != 1 || messageText(got[0]) != "Finished." {
		t.Fatalf("lost final answer: %+v", got)
	}
}

func TestReadMuseRetainedTurnMessagesUsesPostSubmissionCommit(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "session.jsonl")
	content := `{"sequence":5,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"old","event":{"kind":"assistant_message_committed","text":"old answer"}}}` + "\n" +
		`{"sequence":11,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"new","event":{"kind":"assistant_message_committed","text":"new answer"}}}` + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	owner := "muse-retained-test"
	musePersistentPool.Lock()
	previousEntry := musePersistentPool.m[owner]
	musePersistentPool.m[owner] = &musePersistentSession{
		tmuxName:                 "mlp-muse-retained-test",
		logPath:                  logPath,
		retainedBaselineSequence: 10,
	}
	musePersistentPool.Unlock()
	previousReady := museRetainedTurnReady
	museRetainedTurnReady = func(tmuxName, gotLogPath string) bool {
		return tmuxName == "mlp-muse-retained-test" && gotLogPath == logPath
	}
	t.Cleanup(func() {
		museRetainedTurnReady = previousReady
		musePersistentPool.Lock()
		if previousEntry == nil {
			delete(musePersistentPool.m, owner)
		} else {
			musePersistentPool.m[owner] = previousEntry
		}
		musePersistentPool.Unlock()
	})

	messages := ReadRetainedTurnMessages(owner, time.Now())
	if len(messages) != 1 || messages[0].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("messages = %+v, want one AI message", messages)
	}
	if got := messageText(messages[0]); got != "new answer" {
		t.Fatalf("final text = %q, want new answer", got)
	}
}

func TestReadMuseRetainedTurnMessagesWaitsForReadyBoundary(t *testing.T) {
	owner := "muse-retained-not-ready"
	musePersistentPool.Lock()
	previousEntry := musePersistentPool.m[owner]
	musePersistentPool.m[owner] = &musePersistentSession{tmuxName: "mlp-muse-retained-not-ready", logPath: "unused"}
	musePersistentPool.Unlock()
	previousReady := museRetainedTurnReady
	museRetainedTurnReady = func(string, string) bool { return false }
	t.Cleanup(func() {
		museRetainedTurnReady = previousReady
		musePersistentPool.Lock()
		if previousEntry == nil {
			delete(musePersistentPool.m, owner)
		} else {
			musePersistentPool.m[owner] = previousEntry
		}
		musePersistentPool.Unlock()
	})

	if messages := ReadRetainedTurnMessages(owner, time.Now()); len(messages) != 0 {
		t.Fatalf("messages = %+v, want none before the ready boundary", messages)
	}
}
