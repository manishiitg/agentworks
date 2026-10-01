package slots

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "slots.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidSlot(t *testing.T) {
	for _, ok := range []string{"slot01", "slot50", "slot123"} {
		if !ValidSlot(ok) {
			t.Fatalf("%s should be valid", ok)
		}
	}
	for _, bad := range []string{"", "root", "slot", "slot1", "slot01;id", "Slot01", "slot0001", "agents", "slot01 "} {
		if ValidSlot(bad) {
			t.Fatalf("%q must not be a valid slot", bad)
		}
	}
}

func TestLoadTableRejectsNamesThatAreNotSlots(t *testing.T) {
	if _, err := LoadTable(writeTable(t, `{"slots":{"root":"u1"}}`)); err == nil {
		t.Fatal("a table naming root must be refused")
	}
	table, err := LoadTable(writeTable(t, `{"slots":{"slot01":"u1","slot02":"u2"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if slot, ok := table.SlotFor("u2"); !ok || slot != "slot02" {
		t.Fatalf("u2 -> %q %v", slot, ok)
	}
	if _, ok := table.SlotFor("nobody"); ok {
		t.Fatal("a user without a slot must not get one")
	}
	if _, ok := table.SlotFor(""); ok {
		t.Fatal("an empty user id must not match")
	}
}

func TestForRespectsTheFeatureFlagAndNeverFallsBack(t *testing.T) {
	t.Setenv(EnvEnabled, "")
	if slot, enabled, err := For("u1"); enabled || err != nil || slot != "" {
		t.Fatalf("off: %q %v %v", slot, enabled, err)
	}

	t.Setenv(EnvEnabled, "on")
	t.Setenv(EnvTableFile, writeTable(t, `{"slots":{"slot03":"u1"}}`))
	if slot, enabled, err := For("u1"); !enabled || err != nil || slot != "slot03" {
		t.Fatalf("assigned: %q %v %v", slot, enabled, err)
	}
	if _, enabled, err := For("u2"); !enabled || !errors.Is(err, ErrNoSlot) {
		t.Fatalf("a user without a slot must be refused, got enabled=%v err=%v", enabled, err)
	}

	t.Setenv(EnvTableFile, filepath.Join(t.TempDir(), "missing.json"))
	if _, enabled, err := For("u1"); !enabled || err == nil || errors.Is(err, ErrNoSlot) {
		t.Fatalf("an unreadable table must be an error, not a fallback: enabled=%v err=%v", enabled, err)
	}
}
