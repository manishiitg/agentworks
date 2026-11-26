package agycli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgyDurableAckScopesRepeatedInputToNewUserRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agyWriteTestDB(t, filepath.Join(dir, "repeats.db"), []struct {
		stepType int
		payload  []byte
	}{
		{agyStepUser, agyTestPayload(agyStepUser, 19, 2, "same input")},
		{agyStepAssistant, agyTestPayload(agyStepAssistant, 20, 1, "old answer")},
		{agyStepUser, agyTestPayload(agyStepUser, 19, 2, "same input")},
		{agyStepUser, agyTestPayload(agyStepUser, 19, 2, "different input")},
	})
	if count, err := agyCountUserSteps("repeats", 0, "same input"); err != nil || count != 1 {
		t.Fatalf("matching new rows = %d, err %v; want 1", count, err)
	}
	if count, err := agyCountUserSteps("repeats", -1, "same input"); err != nil || count != 2 {
		t.Fatalf("matching all rows = %d, err %v; want 2", count, err)
	}
	session := &agyInteractiveSession{}
	agyStashDurableAck(session, "same input", "repeats", -1)
	agyStashDurableAck(session, "same input", "repeats", -1)
	first, ok := agyTakeDurableAck(session, "same input")
	if !ok || first.occurrence != 1 {
		t.Fatalf("first receipt = %+v, ok %t", first, ok)
	}
	second, ok := agyTakeDurableAck(session, "same input")
	if !ok || second.occurrence != 2 {
		t.Fatalf("second receipt = %+v, ok %t", second, ok)
	}
}
