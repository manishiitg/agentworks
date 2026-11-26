package musecli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdoptNativeMuseInputPinsNewestAcceptedSequence(t *testing.T) {
	owner := t.Name()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	log := `{"sequence":10,"payload_type":"runtime.user_intent.accepted","payload":{"refill_blocks":[{"kind":"text","text":"repeat"}]}}
{"sequence":11,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"assistant_message_committed","text":"old answer"}}}
{"sequence":20,"payload_type":"runtime.user_intent.accepted","payload":{"refill_blocks":[{"kind":"text","text":"repeat"}]}}
{"sequence":21,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"assistant_message_committed","text":"new answer"}}}
`
	if err := os.WriteFile(path, []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	entry := &musePersistentSession{logPath: path}
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = entry
	musePersistentPool.Unlock()
	t.Cleanup(func() { musePersistentPool.Lock(); delete(musePersistentPool.m, owner); musePersistentPool.Unlock() })
	if err := AdoptNativeInput(owner, "repeat"); err != nil {
		t.Fatal(err)
	}
	if entry.retainedBaselineSequence != 20 {
		t.Fatalf("baseline=%d", entry.retainedBaselineSequence)
	}
	rows := ReadRetainedTurnStructuredProgressMessages(owner, time.Now())
	if len(rows) != 1 || messageText(rows[0]) != "new answer" {
		t.Fatalf("native adoption replayed older response: %+v", rows)
	}
	if err := AdoptNativeInput(owner, "missing"); err == nil {
		t.Fatal("unaccepted text adopted")
	}
	if entry.retainedBaselineSequence != 20 {
		t.Fatal("failed adoption changed the last accepted boundary")
	}
}
