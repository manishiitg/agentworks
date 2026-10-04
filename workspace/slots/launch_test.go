package slots

import (
	"errors"
	"path/filepath"
	"testing"
)

// The tmux front-end runs a new session as the slot the platform named by putting the launch script in that slot's
// run folder (PLAT-442); the starting folder no longer decides, it can only veto a disagreement.
func TestSlotForLaunchFollowsTheScriptsRunFolder(t *testing.T) {
	cfg, docs := identityEnv(t)
	script := func(slot string) string {
		return "/bin/sh " + filepath.Join(cfg.SlotRunRoot, slot, "mlp-coding-agent-launch-1.sh")
	}
	runtime := filepath.Join(filepath.Dir(docs), "app", "state", "cli-runtimes", "v1", "abc")
	for _, tc := range []struct {
		name, dir, command, want string
		mismatch                 bool
	}{
		{"Code, A: script in A's folder, folder in A's tree", filepath.Join(docs, "_users", "user-a", "Chats", "Code", "projects", "app-1"), script("slot08"), "slot08", false},
		{"the owner declared the slot for a folder that names no user (a shared Crew/<id>)", filepath.Join(docs, "Crew", "crew-1"), script("slot08"), "slot08", false},
		{"the owner declared the slot for the app's runtime folder", runtime, script("slot08"), "slot08", false},
		{"Crew turn today: script on the app account's own folder, runtime dir", runtime, "/bin/sh /app/state/launch-1.sh", "", false},
		{"Goal: no slot script", filepath.Join(docs, "Workflow", "goal-1"), "/bin/sh /tmp/x.sh", "", false},
		{"folder in A's tree but the launch was not prepared for the slot (user not in the canary)", filepath.Join(docs, "_users", "user-a", "Chats", "Code", "projects", "app-1"), "/bin/sh /app/launch.sh", "", false},
		{"script says A, folder is B's tree: refused with a mismatch error, not run as either", filepath.Join(docs, "_users", "user-b", "Chats", "Code", "projects", "p"), script("slot08"), "", true},
		{"script says B, folder is A's tree", filepath.Join(docs, "_users", "user-a", "Chats", "Code", "projects", "p"), script("slot09"), "", true},
		{"script says A, folder is slot B's own state folder", filepath.Join(cfg.SlotStateRoot, "slot09", "cli"), script("slot08"), "", true},
		{"script says A, folder is slot B's run folder", filepath.Join(cfg.SlotRunRoot, "slot09", "f"), script("slot08"), "", true},
		{"a slot's run folder named only as a prefix of a longer name", filepath.Join(docs, "Crew", "c"), "/bin/sh " + cfg.SlotRunRoot + "/slot08x/launch.sh", "", false},
		{"the run root itself, no slot folder", filepath.Join(docs, "Crew", "c"), "/bin/sh " + cfg.SlotRunRoot + "/launch.sh", "", false},
		{"no command", filepath.Join(docs, "_users", "user-a", "Chats", "Code", "projects", "app-1"), "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cfg.SlotForLaunch(tc.dir, tc.command)
			if tc.mismatch != errors.Is(err, ErrSlotMismatch) || (err != nil) != tc.mismatch {
				t.Fatalf("SlotForLaunch(%q, %q) err = %v, want mismatch=%v", tc.dir, tc.command, err, tc.mismatch)
			}
			if got != tc.want {
				t.Fatalf("SlotForLaunch(%q, %q) = %q, want %q", tc.dir, tc.command, got, tc.want)
			}
		})
	}
}
