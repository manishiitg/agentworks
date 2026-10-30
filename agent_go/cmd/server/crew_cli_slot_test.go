package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-872-84 (owner 2026-10-30): a Crew's coding CLI runs as its owner's slot, so the agent and the owner's terminal
// are one Linux user. Every turn in the Crew, the owner's and a Run person's, starts its runtime in the owner's slot
// state folder (group-shared, outside the documents) and declares the owner's slot. A Crew whose owner holds no
// slot, or a CLI whose server login would reach the slot as a file, keeps the app account and the app's private state.
func TestCrewTurnRunsAsItsOwnersSlot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(root, "docs"))
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	slotState := filepath.Join(root, "slots", "state")
	if err := os.MkdirAll(filepath.Join(slotState, "slot01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(slotState, "slot01"), 0o770|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "slotctl.json")
	if err := os.WriteFile(config, []byte(`{"allowed_exec":[],"allowed_cwd":[],"slot_run_root":"`+filepath.Join(root, "slots", "run")+`","slot_state_root":"`+slotState+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	table := filepath.Join(root, "slots.json")
	t.Setenv("AGENTWORKS_SLOTS", "on")
	t.Setenv("AGENTWORKS_SLOTS_FILE", table)
	t.Setenv("AGENTWORKS_SLOTCTL_CONFIG", config)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-token") // the server's Claude login, from the environment only

	crew := registerTestCrew(t, "demo-12345678", "alice", true)
	shared := codingAgentWorkspaceWorkingDir(crew)
	for _, tc := range []struct {
		name, slots, caller, wantUser, wantSlot string
	}{
		{"owner's turn", `{"slots":{"slot01":"alice"}}`, "alice", "alice", "slot01"},
		{"Run person's turn", `{"slots":{"slot01":"alice","slot02":"bob"}}`, "bob", "alice", "slot01"},
		{"owner without a slot", `{"slots":{"slot02":"bob"}}`, "alice", "", ""},
		{"a CLI whose server login is a file", `{"slots":{"slot01":"alice"}}`, "alice", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(table, []byte(tc.slots), 0o600); err != nil {
				t.Fatal(err)
			}
			provider := "claude-code"
			if strings.Contains(tc.name, "file") {
				provider = "codex-cli"
			}
			dir, err := crewCLIWorkingDir(crew, tc.caller, "work:project:demo", provider, tc.caller != "alice")
			if err != nil {
				t.Fatal(err)
			}
			inSlot := strings.HasPrefix(dir, filepath.Join(slotState, "slot01")+string(filepath.Separator))
			if inSlot != (tc.wantSlot != "") {
				t.Fatalf("runtime %s: in the owner's slot state = %v, want %v", dir, inSlot, tc.wantSlot != "")
			}
			if info, err := os.Stat(dir); err != nil || (inSlot && info.Mode().Perm() != 0o770) {
				t.Fatalf("slot runtime %s mode %v (%v), want 0770 for the platform and the slot", dir, info.Mode(), err)
			}
			got := decideTurnRunAs(context.Background(), turnRunAsInput{ProfileID: "work", WorkingFolder: crew, CLIWorkingDir: dir, SharedWorkingDir: shared, CallerID: tc.caller}, defaultRunAsDeps())
			if got.User != tc.wantUser || got.Slot != tc.wantSlot {
				t.Fatalf("runs as user=%q slot=%q, want user=%q slot=%q", got.User, got.Slot, tc.wantUser, tc.wantSlot)
			}
		})
	}
}
