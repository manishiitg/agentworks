package slotfs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func setup(t *testing.T) (state, run string) {
	t.Helper()
	root := t.TempDir()
	state, run = filepath.Join(root, "state"), filepath.Join(root, "run")
	for _, d := range []string{filepath.Join(state, "slot05", "cli"), filepath.Join(run, "slot05")} {
		if err := os.MkdirAll(d, 0o770); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(root, "slotctl.json")
	body := `{"slot_run_root":"` + run + `","slot_state_root":"` + state + `"}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfig, cfg)
	return state, run
}

func TestSlotOfOnlyMatchesSlotFoldersUnderTheRoots(t *testing.T) {
	state, run := setup(t)
	if slot, ok := SlotOf(filepath.Join(state, "slot05", "cli", "x")); !ok || slot != "slot05" {
		t.Fatalf("state path: %q %v", slot, ok)
	}
	if slot, ok := SlotOf(filepath.Join(run, "slot05", "f")); !ok || slot != "slot05" {
		t.Fatalf("run path: %q %v", slot, ok)
	}
	for _, p := range []string{state, filepath.Join(state, "notaslot", "x"), filepath.Join(state, "slot5", "x"), "/tmp/slot05", "", filepath.Join(state, "..", "slot05")} {
		if _, ok := SlotOf(p); ok {
			t.Fatalf("%q must not be a slot launch", p)
		}
	}
}

func TestNothingChangesWhenUnconfigured(t *testing.T) {
	state, _ := setup(t)
	hint := filepath.Join(state, "slot05", "cli")
	t.Setenv(EnvConfig, filepath.Join(t.TempDir(), "missing.json"))
	if IsSlotLaunch(hint) || TempDir(hint) != os.TempDir() || Mode(hint, 0o600) != 0o600 {
		t.Fatal("with no allow-list a launch must be exactly as before")
	}
}

func TestLaunchFilesForASlotLandInItsRunFolderWithGroupAccess(t *testing.T) {
	state, run := setup(t)
	hint := filepath.Join(state, "slot05", "cli")
	f, err := CreateTemp(hint, "launch-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if filepath.Dir(f.Name()) != filepath.Join(run, "slot05") {
		t.Fatalf("file %s is not in the slot's run folder", f.Name())
	}
	if info, _ := f.Stat(); info.Mode().Perm() != 0o660 {
		t.Fatalf("mode %o, want 660", info.Mode().Perm())
	}
	dir, err := MkdirTemp(hint, "cfg-*")
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o770 {
		t.Fatalf("dir mode %o, want 770", info.Mode().Perm())
	}
	if Mode(hint, 0o600) != 0o660 || Mode(hint, 0o700) != 0o770 || Mode(hint, 0o644) != 0o644 {
		t.Fatalf("Mode: %o %o %o", Mode(hint, 0o600), Mode(hint, 0o700), Mode(hint, 0o644))
	}
}

func TestWrapCmdRunsTheCommandAsTheSlotWithTheRequestInAFile(t *testing.T) {
	state, run := setup(t)
	hint := filepath.Join(state, "slot05", "cli")
	cmd := exec.CommandContext(context.Background(), "/srv/agents/releases/x/bin/runner", "--config", "/c.json", "--", "/usr/bin/env", "cursor-agent")
	cmd.Dir = hint
	cmd.Env = []string{"A=1"}
	cleanup, err := WrapCmd(cmd, hint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != sudoPath || len(cmd.Args) != 8 || cmd.Args[3] != "slot05" || cmd.Args[5] != "exec" || cmd.Args[6] != "--request-file" {
		t.Fatalf("argv %v", cmd.Args)
	}
	file := cmd.Args[7]
	if filepath.Dir(file) != filepath.Join(run, "slot05") {
		t.Fatalf("request file %s is not in the slot's run folder", file)
	}
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), `"A=1"`) || !strings.Contains(string(data), "cursor-agent") {
		t.Fatalf("request %q err %v", data, err)
	}
	for _, e := range cmd.Env {
		if !strings.HasPrefix(e, "PATH=") {
			t.Fatalf("sudo must not receive the platform environment: %v", cmd.Env)
		}
	}
	cleanup()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the request file")
	}
	if _, err := WrapCmd(exec.CommandContext(context.Background(), "/bin/true"), "/tmp/elsewhere", nil); !errors.Is(err, ErrNotSlot) {
		t.Fatalf("a launch outside the slots must not be wrapped: %v", err)
	}
}

func TestAUsersOwnTreeIsTheirSlotLaunch(t *testing.T) {
	root := t.TempDir()
	table := filepath.Join(root, "slots.json")
	if err := os.WriteFile(table, []byte(`{"slots":{"slot08":"user-a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "slotctl.json")
	body := `{"slot_run_root":"` + root + `/run","slot_state_root":"` + root + `/state","docs_root":"` + root + `/docs","slot_table":"` + table + `"}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfig, cfg)
	if slot, ok := SlotOf(root + "/docs/_users/user-a/Chats/Code/projects/p"); !ok || slot != "slot08" {
		t.Fatalf("own tree: %q %v", slot, ok)
	}
	if _, ok := SlotOf(root + "/docs/_users/user-b/Chats/Code/projects/p"); ok {
		t.Fatal("a user without a slot has no slot launch")
	}
	if _, ok := SlotOf(root + "/docs/Workflow/x"); ok {
		t.Fatal("shared folders are not anyone's tree")
	}
}

// A product with its own slot accounts on a shared host (prefix "cf") recognises only its own, and a
// default-prefix account is not one of its slots.
func TestSlotOfHonoursTheProductsSlotPrefix(t *testing.T) {
	root := t.TempDir()
	state, run := filepath.Join(root, "state"), filepath.Join(root, "run")
	cfg := filepath.Join(root, "slotctl.json")
	body := `{"slot_prefix":"cf","slot_run_root":"` + run + `","slot_state_root":"` + state + `"}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfig, cfg)
	if slot, ok := SlotOf(filepath.Join(state, "cf07", "cli", "x")); !ok || slot != "cf07" {
		t.Fatalf("own slot: %q %v", slot, ok)
	}
	if _, ok := SlotOf(filepath.Join(state, "slot07", "cli", "x")); ok {
		t.Fatal("another product's slot name must not be recognised")
	}
	t.Setenv(EnvSlotctl, "/usr/local/libexec/agentworks/example/slotctl")
	if slotctl() != "/usr/local/libexec/agentworks/example/slotctl" {
		t.Fatal("the slot program override is ignored")
	}
}

func TestWithSlotDockerPointsTheCommandAtItsOwnSocket(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "slotctl.json")
	t.Setenv(EnvConfig, cfg)
	prior := lookupSlotUID
	lookupSlotUID = func(string) (string, error) { return "1042", nil }
	t.Cleanup(func() { lookupSlotUID = prior })
	env := []string{"PATH=/usr/bin", "DOCKER_HOST=unix:///run/user/990/docker.sock", "HOME=/tmp"}

	if err := os.WriteFile(cfg, []byte(`{"slot_run_root":"`+root+`/run","slot_state_root":"`+root+`/state","slot_docker":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WithSlotDocker(env, "slot05")
	var hosts []string
	for _, e := range got {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			hosts = append(hosts, e)
		}
	}
	if len(hosts) != 1 || hosts[0] != "DOCKER_HOST=unix:///run/user/1042/docker.sock" {
		t.Fatalf("DOCKER_HOST = %v: the platform's socket must be replaced by the slot's own", hosts)
	}
	if len(got) != len(env) {
		t.Fatalf("other entries must be kept: %v", got)
	}

	// without slot_docker the environment is untouched
	if err := os.WriteFile(cfg, []byte(`{"slot_run_root":"`+root+`/run","slot_state_root":"`+root+`/state"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WithSlotDocker(env, "slot05"); strings.Join(got, "|") != strings.Join(env, "|") {
		t.Fatalf("a host without slot Docker must not change the environment: %v", got)
	}
}

// A missing write grant is never created: made as an empty file, a Crew's runtime folders (db/, assets/,
// reports/ …) became 0-byte files the website's Files could not open (5 Nov).
func TestMissingWriteGrantIsNotCreated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "db")
	if err := grantLocked("slot05", missing, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing grant %s was created (stat err %v)", missing, err)
	}
}

// A 0600 file or 0700 dir created by the slot account itself (such as an MCP
// readiness marker written by mcpbridge running as the slot) has no group bits
// for groupCovers to match, and setfacl from the platform account fails with
// Operation not permitted. grantLocked and traverse must skip paths already
// owned by the slot.
func TestGrantLockedSkipsPathsOwnedBySlot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix ownership semantics only")
	}

	// Install a trap setfacl binary on PATH that always fails like an unowned file.
	fakeBin := t.TempDir()
	fakeSetfacl := filepath.Join(fakeBin, "setfacl")
	if err := os.WriteFile(fakeSetfacl, []byte("#!/bin/sh\necho 'setfacl: Operation not permitted' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".mcpbridge-ready-test.marker")
	if err := os.WriteFile(marker, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	privateDir := filepath.Join(root, "slot-private")
	if err := os.Mkdir(privateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nestedMarker := filepath.Join(privateDir, ".mcpbridge-ready-nested.marker")
	if err := os.WriteFile(nestedMarker, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prior := lookupSlotUID
	t.Cleanup(func() { lookupSlotUID = prior })

	// Owned by the slot itself: neither grantLocked nor traverse may invoke setfacl.
	lookupSlotUID = func(string) (string, error) { return strconv.Itoa(os.Getuid()), nil }
	for _, tc := range []struct {
		name  string
		path  string
		write bool
	}{
		{name: "read_0600_marker", path: marker, write: false},
		{name: "write_0600_marker", path: marker, write: true},
		{name: "nested_in_0700_slot_dir", path: nestedMarker, write: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := grantLocked("slot01", tc.path, tc.write); err != nil {
				t.Fatalf("grantLocked(%q, write=%v) failed on slot-owned path: %v", tc.path, tc.write, err)
			}
		})
	}

	// Negative control: when owned by another UID, grantLocked must still invoke setfacl.
	lookupSlotUID = func(string) (string, error) { return "999999", nil }
	err := grantLocked("slot01", marker, false)
	if err == nil || !strings.Contains(err.Error(), "Operation not permitted") {
		t.Fatalf("expected trap setfacl error for non-slot-owned 0600 file, got: %v", err)
	}
}
