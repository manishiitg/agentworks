//go:build linux

package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// PLAT-480 (F1): a slot command's policy hides the folder holding every slot's tmux socket; any other command's does not.
func TestSlotCommandPolicyHidesTheSlotRunRoot(t *testing.T) {
	root := t.TempDir()
	runRoot := filepath.Join(root, "run")
	if err := os.MkdirAll(filepath.Join(runRoot, "slot01"), 0o711); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "slotctl.json")
	body, _ := json.Marshal(map[string]any{"slot_prefix": "slot", "slot_run_root": runRoot})
	if err := os.WriteFile(cfg, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(slots.EnvConfig, cfg)

	policyFor := func(slot string) LandlockPolicy {
		iso := &Isolator{Slot: slot, BaseDir: project, WorkDir: project, ReadPaths: []string{project}, WritePaths: []string{project}}
		policy, err := iso.landlockPolicy()
		if err != nil {
			t.Fatalf("landlockPolicy(%q): %v", slot, err)
		}
		return policy
	}
	want := canonicalPath(runRoot)
	if got := policyFor("slot01").PrivateRoots; len(got) != 1 || got[0] != want {
		t.Errorf("slot command PrivateRoots = %v, want [%s]", got, want)
	}
	if got := policyFor("").PrivateRoots; len(got) != 0 {
		t.Errorf("a command that is not run as a slot must hide nothing, got %v", got)
	}
}

func TestSlotRunRootIsNotHiddenWhereSlotsAreNotConfigured(t *testing.T) {
	t.Setenv(slots.EnvConfig, filepath.Join(t.TempDir(), "missing.json"))
	if got := slotRunRootToHide("slot01"); got != "" {
		t.Errorf("no slotctl config means nothing to hide, got %q", got)
	}
}
