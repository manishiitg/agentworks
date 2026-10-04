package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// frontendEnv writes the root-owned config the front-end reads, for a host with two slot users, and points the
// front-end at it.
func frontendEnv(t *testing.T) (cfg slots.ExecConfig, docs string) {
	t.Helper()
	root := t.TempDir()
	table := filepath.Join(root, "slots.json")
	if err := os.WriteFile(table, []byte(`{"slots":{"slot08":"user-a","slot09":"user-b"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = slots.ExecConfig{
		DocsRoot:      filepath.Join(root, "docs"),
		SlotTable:     table,
		SlotStateRoot: filepath.Join(root, "state"),
		SlotRunRoot:   filepath.Join(root, "run"),
	}
	body, _ := json.Marshal(cfg)
	conf := filepath.Join(root, "slotctl.json")
	if err := os.WriteFile(conf, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(slots.EnvConfig, conf)
	return cfg, cfg.DocsRoot
}

type decisionCase struct {
	name       string
	dir        string // relative to docs for "docs:" prefixed rows is resolved by the test
	script     string // "" none, "app" the app's own script, "slot08"/"slot09" a script in that slot's run folder
	wantExit   int
	wantPass   bool
	wantAsSlot bool
}

func cases(cfg slots.ExecConfig, docs string) []decisionCase {
	userA := filepath.Join(docs, "_users", "user-a", "Chats", "Code", "projects", "app-1")
	userB := filepath.Join(docs, "_users", "user-b", "Chats", "Code", "projects", "p")
	crew := filepath.Join(docs, "Crew", "crew-1")
	return []decisionCase{
		{"slot launch, script and folder agree", userA, "slot08", 0, false, true},
		{"slot launch for a shared folder", crew, "slot08", 0, false, true},
		{"declared app account, folder in a user's tree: app tmux", userA, "app", 0, true, false},
		{"Crew/goal turn as the app account", crew, "app", 0, true, false},
		{"script says A, folder is B's tree: refused", userB, "slot08", exitMismatch, false, false},
		{"script says B, folder is A's tree: refused", userA, "slot09", exitMismatch, false, false},
		{"script says A, folder is slot B's state folder: refused", filepath.Join(cfg.SlotStateRoot, "slot09", "cli"), "slot08", exitMismatch, false, false},
	}
}

func newSessionArgs(cfg slots.ExecConfig, name, dir, script string) []string {
	cmd := "/bin/sh /app/state/launch-1.sh"
	if strings.HasPrefix(script, "slot") {
		cmd = "/bin/sh " + filepath.Join(cfg.SlotRunRoot, script, "launch-1.sh")
	}
	return []string{"new-session", "-d", "-s", name, "-c", dir, cmd}
}

// TestRunDecisionNeverReachesTmuxOnAMismatch drives run() with the three exec hooks replaced: a mismatch must exit
// non-zero without reaching passthrough (the app account's tmux) or the slot, and the ordinary launches must
// reach the same place they always did.
func TestRunDecisionNeverReachesTmuxOnAMismatch(t *testing.T) {
	cfg, docs := frontendEnv(t)
	for _, tc := range cases(cfg, docs) {
		t.Run(tc.name, func(t *testing.T) {
			var passed, asSlotted bool
			oldP, oldA, oldR := passthroughFn, asSlotFn, runAsSlotFn
			t.Cleanup(func() { passthroughFn, asSlotFn, runAsSlotFn = oldP, oldA, oldR })
			passthroughFn = func([]string) int { passed = true; return 0 }
			asSlotFn = func(slots.ExecConfig, string, []string, []string, io.Writer, io.Writer) int {
				asSlotted = true
				return 0
			}
			runAsSlotFn = func(slots.ExecConfig, string, []string) int { return 0 }
			code := run(newSessionArgs(cfg, "claude-test", tc.dir, tc.script))
			if code != tc.wantExit || passed != tc.wantPass || asSlotted != tc.wantAsSlot {
				t.Fatalf("exit=%d passthrough=%v asSlot=%v; want exit=%d passthrough=%v asSlot=%v", code, passed, asSlotted, tc.wantExit, tc.wantPass, tc.wantAsSlot)
			}
		})
	}
}

// A refused Muse launch (the branch that logs and passes through) must also stop.
func TestRunDecisionMuseBranchRefusesAMismatch(t *testing.T) {
	cfg, docs := frontendEnv(t)
	oldP := passthroughFn
	t.Cleanup(func() { passthroughFn = oldP })
	passed := false
	passthroughFn = func([]string) int { passed = true; return 0 }
	userB := filepath.Join(docs, "_users", "user-b", "Chats", "Code", "projects", "p")
	if code := run(newSessionArgs(cfg, "muse-1", userB, "slot08")); code != exitMismatch || passed {
		t.Fatalf("muse mismatch: exit=%d passthrough=%v", code, passed)
	}
	if code := run(newSessionArgs(cfg, "muse-1", filepath.Join(docs, "Crew", "c"), "app")); code != 0 || !passed {
		t.Fatalf("muse app-account launch: exit=%d passthrough=%v", code, passed)
	}
}

// Hosts without slots set up pass everything through, mismatch or not.
func TestRunWithoutSlotsPassesThrough(t *testing.T) {
	t.Setenv(slots.EnvConfig, filepath.Join(t.TempDir(), "absent.json"))
	oldP := passthroughFn
	t.Cleanup(func() { passthroughFn = oldP })
	passed := false
	passthroughFn = func([]string) int { passed = true; return 0 }
	if code := run([]string{"new-session", "-d", "-s", "x", "-c", "/tmp", "/bin/sh /x.sh"}); code != 0 || !passed {
		t.Fatalf("exit=%d passthrough=%v", code, passed)
	}
}
