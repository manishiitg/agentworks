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

func TestForOptInRunsOnlySlotHoldersAsSlots(t *testing.T) {
	t.Setenv(EnvEnabled, "optin")
	t.Setenv(EnvTableFile, writeTable(t, `{"slots":{"slot03":"u1"}}`))
	if slot, enabled, err := For("u1"); !enabled || err != nil || slot != "slot03" {
		t.Fatalf("a slot holder runs as its slot: %q %v %v", slot, enabled, err)
	}
	if slot, enabled, err := For("u2"); enabled || err != nil || slot != "" {
		t.Fatalf("a user without a slot is unchanged in opt-in mode: %q %v %v", slot, enabled, err)
	}
	// a damaged table is still an error, never a silent fallback
	t.Setenv(EnvTableFile, writeTable(t, `{"slots": not json`))
	if _, _, err := For("u1"); err == nil {
		t.Fatal("a damaged table must be an error")
	}
}

func TestSlotPrefixMakesAProductsOwnAccountsTheOnlyValidOnes(t *testing.T) {
	t.Cleanup(func() { prefixMu.Lock(); configuredPref = ""; prefixMu.Unlock() })
	if !ValidSlot("slot07") || ValidSlot("cf07") {
		t.Fatal("the default prefix is slot")
	}
	t.Setenv(EnvPrefix, "cf")
	if !ValidSlot("cf07") || !ValidSlot("cf123") || ValidSlot("slot07") || ValidSlot("cf7") || ValidSlot("xcf07") {
		t.Fatal("prefix cf must accept only cf<2-3 digits>")
	}
	// the programs that run without the service's environment learn it from their config
	t.Setenv(EnvPrefix, "")
	cfg := filepath.Join(t.TempDir(), "slotctl.json")
	if err := os.WriteFile(cfg, []byte(`{"slot_prefix":"confida","slot_run_root":"/x/run"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadExecConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if !ValidSlot("confida03") || ValidSlot("slot03") {
		t.Fatalf("config prefix not applied: %q", Prefix())
	}
	if SlotOfDir("/x/state", "/x/state/confida03/work") != "confida03" || SlotOfDir("/x/state", "/x/state/slot03/work") != "" {
		t.Fatal("SlotOfDir must use the product's prefix")
	}
}

func TestForOptInWithoutAProvisionedTableChangesNothing(t *testing.T) {
	t.Setenv(EnvEnabled, "optin")
	t.Setenv(EnvTableFile, filepath.Join(t.TempDir(), "not-provisioned-yet.json"))
	if slot, enabled, err := For("u1"); enabled || err != nil || slot != "" {
		t.Fatalf("an unprovisioned opt-in host must be unchanged: %q %v %v", slot, enabled, err)
	}
	// "on" still treats a missing table as an error
	t.Setenv(EnvEnabled, "on")
	if _, enabled, err := For("u1"); !enabled || err == nil {
		t.Fatalf("on-mode must refuse without a table: enabled=%v err=%v", enabled, err)
	}
}
