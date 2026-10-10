package slotcheck

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layout is a fake slot host: an app folder with a release and its launcher, a docs tree with a workflow and one
// user's Crew and Code project, the slotctl allow-list, the slot table and a 0700 app-owned browser profile tree.
type layout struct {
	root, app, docs, runner, config, table, profiles, state string
	opts                                                    Options
	runs                                                    []Probe
}

const stranger = 424242 // a uid/gid nobody in the test tree has: "other" bits decide, as for a slot account

func newLayout(t *testing.T) *layout {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := &layout{root: root, app: filepath.Join(root, "srv", "app")}
	l.docs = filepath.Join(l.app, "data", "docs")
	l.runner = filepath.Join(l.app, "releases", "r1", "bin", "video-studio-landlock-runner")
	l.config = filepath.Join(root, "libexec", "slotctl.json")
	l.table = filepath.Join(root, "etc", "slots.json")
	l.profiles = filepath.Join(l.app, "state", "browser-profile-projects")
	l.state = filepath.Join(l.app, "slots", "state")
	for _, dir := range []string{
		filepath.Dir(l.runner), filepath.Join(l.docs, "Workflow", "wf1"),
		filepath.Join(l.docs, "_users", "u1", "Chats", "Work", "projects", "crew1"),
		filepath.Join(l.docs, "_users", "u1", "Chats", "Code", "projects", "code1"),
		filepath.Join(l.profiles, "project-3bdc30503fa28a4f--browser"),
		filepath.Join(l.state, "slot01"), filepath.Join(l.state, "slot50"), filepath.Dir(l.config), filepath.Dir(l.table),
	} {
		mustMkdir(t, dir)
	}
	// The fixed layout: every folder down to the launcher is traversable (releases 0711, as the deploy now sets it).
	for _, dir := range []string{root, filepath.Join(root, "srv"), l.app, filepath.Join(l.app, "releases"), filepath.Join(l.app, "releases", "r1"), filepath.Dir(l.runner)} {
		mustChmod(t, dir, 0o711)
	}
	mustChmod(t, filepath.Join(l.app, "state"), 0o700)
	mustChmod(t, l.profiles, 0o700)
	if err := os.WriteFile(l.runner, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	l.writeConfig(t, []string{l.docs, filepath.Join(l.app, "slots")})
	l.writeTable(t, map[string]string{"slot01": "u1"})
	t.Setenv("AGENTWORKS_SLOT_PREFIX", "slot")
	l.opts = Options{
		DocsRoot: l.docs, AppDir: l.app, Runner: l.runner, SlotctlConfig: l.config, SlotTable: l.table,
		TableOwnerUID: os.Getuid(), ServiceGIDs: []int{os.Getgid()}, TraversalRoot: root,
		Lookup: func(name string) (Account, bool) {
			switch name {
			case "slot01", "slot02", "slot50":
				return Account{Name: name, UID: stranger, GIDs: []int{stranger}, Home: filepath.Join(l.app, "slots", "home", name)}, true
			}
			return Account{}, false
		},
	}
	l.opts.Run = l.fakeChain(false)
	return l
}

func (l *layout) writeConfig(t *testing.T, allowedCwd []string) {
	t.Helper()
	cfg := map[string]any{
		"slot_prefix":     "slot",
		"allowed_exec":    []string{filepath.Join(l.app, "releases", "*", "bin", "video-studio-landlock-runner"), "/usr/bin/tmux", "/usr/bin/chmod"},
		"allowed_cwd":     allowedCwd,
		"slot_run_root":   filepath.Join(l.app, "slots", "run"),
		"slot_state_root": l.state,
		"docs_root":       l.docs,
		"slot_table":      l.table,
	}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(l.config, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (l *layout) writeTable(t *testing.T, assigned map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"slots": assigned})
	if err := os.WriteFile(l.table, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, l.table, 0o640)
}

// fakeChain stands in for sudo + slotctl + the launcher. It applies the two rules the real chain applies to a slot
// account: slotctl refuses a folder outside allowed_cwd, and the launcher (old: it fails the whole command on a grant
// it cannot stat; new: it skips that grant) must be reachable. oldGrantBuilder makes the probe ask for the project's
// browser profile, as the grant builder did before PLAT-478, with the launcher failing closed on it.
func (l *layout) fakeChain(oldGrantBuilder bool) func(context.Context, Probe) (string, string, error) {
	return func(_ context.Context, p Probe) (string, string, error) {
		l.runs = append(l.runs, p)
		if blocked, _ := firstBlocked(l.root, l.runner, Account{UID: stranger, GIDs: []int{stranger}}); blocked != "" {
			return "", "", errString("fork/exec " + l.runner + ": permission denied")
		}
		raw, _ := os.ReadFile(l.config)
		var cfg struct {
			AllowedCwd []string `json:"allowed_cwd"`
		}
		_ = json.Unmarshal(raw, &cfg)
		inside := false
		for _, root := range cfg.AllowedCwd {
			if p.Dir == root || strings.HasPrefix(p.Dir, root+"/") {
				inside = true
			}
		}
		if !inside {
			return "", "slotctl: refused: the working folder is outside the allowed folders\n", errString("exit status 126")
		}
		if oldGrantBuilder && strings.HasPrefix(p.BrowserSession, "project-") {
			return "", "SANDBOX_UNAVAILABLE: inspect Landlock path: stat " + filepath.Join(l.profiles, "project-3bdc30503fa28a4f--browser") + ": permission denied\n", errString("exit status 125")
		}
		return p.Dir + "\n", "", nil
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustChmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func failures(rows []Row) map[string]Row {
	out := map[string]Row{}
	for _, r := range rows {
		if r.Status == Fail {
			out[r.Check+"/"+r.Slot] = r
		}
	}
	return out
}

func TestTestSlotBelongsToProduct(t *testing.T) {
	l := newLayout(t)
	lookup := l.opts.Lookup
	l.opts.Lookup = func(name string) (Account, bool) {
		if name == "slot99" {
			return Account{Name: name, Home: filepath.Join(l.root, "other-product", "slots", "home", name)}, true
		}
		return lookup(name)
	}
	rows := Check(context.Background(), l.opts)
	if Failed(rows) || !strings.Contains(Format(rows), "test slot slot50") {
		t.Fatalf("must choose this product's spare slot: %s", Format(rows))
	}
	l.opts.TestSlot = "slot99"
	rows = Check(context.Background(), l.opts)
	if !Failed(rows) {
		t.Fatal("explicit foreign test slot was accepted")
	}
	for _, probe := range l.runs {
		if probe.Slot == "slot99" {
			t.Fatal("ran a probe as another product's slot")
		}
	}
}

func TestFixedLayoutPassesEveryCheck(t *testing.T) {
	l := newLayout(t)
	rows := Check(context.Background(), l.opts)
	out := Format(rows)
	t.Logf("fixed layout:\n%s", out)
	if Failed(rows) {
		t.Fatalf("the fixed layout must pass:\n%s", out)
	}
	// slot01 (assigned) in all four folders, and the test slot slot50 (highest unassigned account) in all four.
	want := map[string]bool{}
	for _, slot := range []string{"slot01", "slot50"} {
		for _, kind := range []string{KindDocs, KindWorkflow, KindCrew, KindCode} {
			want[slot+"/"+kind] = true
		}
	}
	for _, p := range l.runs {
		delete(want, p.Slot+"/"+p.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("probes not run: %v", want)
	}
	if strings.Contains(out, "u1") {
		t.Fatalf("the output names a user id (slot table content):\n%s", out)
	}
	for _, p := range l.runs {
		if (p.Kind == KindCrew || p.Kind == KindCode) && !strings.HasPrefix(p.BrowserSession, "project-") {
			t.Fatalf("a %s probe must carry a project browser session like a real chat: %+v", p.Kind, p)
		}
		if p.Kind == KindCode && p.UserHome == "" {
			t.Fatalf("a Code probe carries the slot's own home like the shell handler: %+v", p)
		}
	}
}

// Every failure seen on server A 2026-10-04 (PLAT-476, PLAT-478 layers 2 and 3, the root:root table) must be reported.
func TestEachKnownFailureIsDetected(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, l *layout)
		want   []string
	}{
		{"allowed_cwd lacks the docs root (PLAT-476)", func(t *testing.T, l *layout) {
			l.writeConfig(t, []string{filepath.Join(l.app, "data", "docs-elsewhere"), filepath.Join(l.app, "slots")})
		}, []string{"slotctl-allowed-cwd/-", "pwd-docs-root/slot01", "pwd-crew-project/slot01"}},
		{"releases is 0700 (layer 2)", func(t *testing.T, l *layout) {
			mustChmod(t, filepath.Join(l.app, "releases"), 0o700)
		}, []string{"runner-reachable/slot01", "runner-reachable/slot50", "pwd-docs-root/slot01"}},
		{"launcher fails on the browser profile grant (layer 3)", func(t *testing.T, l *layout) {
			l.opts.Run = l.fakeChain(true)
		}, []string{"pwd-crew-project/slot01", "pwd-code-project/slot01", "pwd-crew-project/slot50"}},
		{"slot table root:root (release incident)", func(t *testing.T, l *layout) {
			l.opts.ServiceGIDs = []int{stranger}
		}, []string{"slot-table-group/-"}},
		{"slot table world-readable", func(t *testing.T, l *layout) {
			mustChmod(t, l.table, 0o644)
		}, []string{"slot-table-mode/-"}},
		{"slot table owned by the service account", func(t *testing.T, l *layout) {
			l.opts.TableOwnerUID = 0
		}, []string{"slot-table-owner/-"}},
		{"slotctl.json missing", func(t *testing.T, l *layout) {
			_ = os.Remove(l.config)
		}, []string{"slotctl-config/-"}},
		{"allowed_exec does not list the launcher", func(t *testing.T, l *layout) {
			l.opts.Runner = filepath.Join(l.root, "elsewhere", "video-studio-landlock-runner")
			mustMkdir(t, filepath.Dir(l.opts.Runner))
			_ = os.WriteFile(l.opts.Runner, []byte("x"), 0o755)
		}, []string{"slotctl-allowed-exec/-"}},
		{"slot table unreadable", func(t *testing.T, l *layout) {
			_ = os.WriteFile(l.table, []byte("{not json"), 0o640)
		}, []string{"slot-table-read/-"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLayout(t)
			if tc.name == "slot table owned by the service account" && os.Getuid() == 0 {
				t.Skip("running as root: the table is root-owned")
			}
			tc.break_(t, l)
			rows := Check(context.Background(), l.opts)
			out := Format(rows)
			t.Logf("%s:\n%s", tc.name, out)
			if !Failed(rows) {
				t.Fatalf("not detected:\n%s", out)
			}
			got := failures(rows)
			for _, want := range tc.want {
				row, ok := got[want]
				if !ok {
					t.Fatalf("missing FAIL %s:\n%s", want, out)
				}
				if row.Fix == "" {
					t.Fatalf("FAIL %s has no fix", want)
				}
			}
			if !strings.Contains(out, "FAIL ") || !strings.Contains(out, "-- fix: ") {
				t.Fatalf("no FAIL <what> -- fix: <how> line:\n%s", out)
			}
		})
	}
}

func TestNoAssignedSlotUsesOnlyTheTestSlot(t *testing.T) {
	l := newLayout(t)
	l.writeTable(t, map[string]string{})
	rows := Check(context.Background(), l.opts)
	if Failed(rows) {
		t.Fatal(Format(rows))
	}
	for _, p := range l.runs {
		if p.Slot != "slot50" {
			t.Fatalf("ran as %s; only the unassigned test slot may run when nobody holds a slot", p.Slot)
		}
		if (p.Kind == KindCrew || p.Kind == KindCode) && p.Dir != filepath.Join(l.state, "slot50") {
			t.Fatalf("the test slot's project probes run in its own state folder, not in a user's tree: %+v", p)
		}
	}
}

// An assigned slot runs only in its own user's projects, never in another user's tree.
func TestAssignedSlotRunsOnlyInItsOwnUsersFolders(t *testing.T) {
	l := newLayout(t)
	mustMkdir(t, filepath.Join(l.docs, "_users", "u2", "Chats", "Work", "projects", "other"))
	l.writeTable(t, map[string]string{"slot01": "u1", "slot02": "u2"})
	rows := Check(context.Background(), l.opts)
	if Failed(rows) {
		t.Fatal(Format(rows))
	}
	for _, p := range l.runs {
		if strings.Contains(p.Dir, "/_users/") {
			owner := map[string]string{"slot01": "u1", "slot02": "u2"}[p.Slot]
			if !strings.Contains(p.Dir, "/_users/"+owner+"/") {
				t.Fatalf("%s ran in another user's folder %s", p.Slot, p.Dir)
			}
		}
	}
	skipped := 0
	for _, r := range rows {
		if r.Status == Skip && r.Slot == "slot02" && r.Check == "pwd-code-project" {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("slot02's user has no Code project: that probe is skipped, not failed:\n%s", Format(rows))
	}
}

func TestWorldListableReleasesIsAWarningOnly(t *testing.T) {
	l := newLayout(t)
	mustChmod(t, filepath.Join(l.app, "releases"), 0o755)
	rows := Check(context.Background(), l.opts)
	if Failed(rows) {
		t.Fatal(Format(rows))
	}
	if !strings.Contains(Format(rows), "WARN  releases-listable") {
		t.Fatalf("expected a warning:\n%s", Format(rows))
	}
}

func denyRows(rows []Row) map[string]Row {
	out := map[string]Row{}
	for _, r := range rows {
		if strings.HasPrefix(r.Check, "deny-") {
			out[r.Check] = r
		}
	}
	return out
}

func TestDenyChecksPassWhenEveryCommandIsRefused(t *testing.T) {
	l := newLayout(t)
	l.opts.RunCommand = func(_ context.Context, _ Probe, _ string) (string, string, error) {
		return "started\nrc=1\n", "", nil
	}
	got := denyRows(Check(context.Background(), l.opts))
	for _, name := range []string{"deny-write-outside", "deny-list-users", "deny-list-releases", "deny-list-state"} {
		if got[name].Status != Pass {
			t.Errorf("%s = %+v, want PASS", name, got[name])
		}
	}
}

func TestDenyChecksFailWhenACommandSucceeds(t *testing.T) {
	l := newLayout(t)
	l.opts.RunCommand = func(_ context.Context, _ Probe, command string) (string, string, error) {
		if strings.Contains(command, "_users") {
			return "started\nrc=0\n", "", nil
		}
		return "started\nrc=2\n", "", nil
	}
	got := denyRows(Check(context.Background(), l.opts))
	if got["deny-list-users"].Status != Fail || got["deny-list-releases"].Status != Pass {
		t.Fatalf("a leak in one place must fail only that check: %+v", got)
	}
}

func TestDenyChecksNeverCountALauncherThatDidNotStartAsARefusal(t *testing.T) {
	l := newLayout(t)
	l.opts.RunCommand = func(_ context.Context, _ Probe, _ string) (string, string, error) {
		return "", "SANDBOX_UNAVAILABLE: boom", errors.New("exit status 125")
	}
	for name, row := range denyRows(Check(context.Background(), l.opts)) {
		if row.Status != Fail || !strings.Contains(row.Detail, "did not run") {
			t.Errorf("%s = %+v, want FAIL (nothing proven)", name, row)
		}
	}
}

func fullRows(rows []Row) map[string]Row {
	out := map[string]Row{}
	for _, r := range rows {
		if strings.HasPrefix(r.Check, "wf-") || r.Check == "tmux-socket-unreachable" || r.Check == "slot-python-helpers" {
			out[r.Check] = r
		}
	}
	return out
}

func fullLayout(t *testing.T, leak string) *layout {
	l := newLayout(t)
	l.opts.Level = LevelFull
	if err := os.WriteFile(filepath.Join(l.docs, "Workflow", "wf1", "workflow.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(l.docs, "Workflow", "wf2"))
	if err := os.WriteFile(filepath.Join(l.docs, "Workflow", "wf2", "workflow.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.opts.RunCommand = func(_ context.Context, _ Probe, command string) (string, string, error) {
		if leak != "" && strings.Contains(command, leak) {
			return "started\nrc=0\n", "", nil
		}
		if strings.Contains(command, "agentworks_output.py") {
			return "started\nrc=0\n", "", nil
		}
		return "started\nrc=1\n", "", nil
	}
	l.opts.TmuxControl = func(_ context.Context, _ string, _ string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-sessions" {
			return "slotcheck-1: 1 windows\n", nil
		}
		return "", nil
	}
	return l
}

func TestFullLevelPassesWhenEverythingIsRefusedAndHelpersWork(t *testing.T) {
	l := fullLayout(t, "")
	var session string
	l.opts.TmuxControl = func(_ context.Context, _ string, _ string, args ...string) (string, error) {
		for i, a := range args {
			if a == "-s" && i+1 < len(args) {
				session = args[i+1]
			}
		}
		if len(args) > 0 && args[0] == "list-sessions" {
			return session + ": 1 windows\n", nil
		}
		return "", nil
	}
	got := fullRows(Check(context.Background(), l.opts))
	for _, name := range []string{"tmux-socket-unreachable", "slot-python-helpers", "wf-write-outside", "wf-read-other-workflow", "wf-list-users", "wf-list-state", "wf-read-env", "wf-app-tmux-socket"} {
		if got[name].Status != Pass {
			t.Errorf("%s = %+v, want PASS", name, got[name])
		}
	}
}

func TestFullLevelFailsWhenALeakIsReal(t *testing.T) {
	for check, marker := range map[string]string{"wf-read-env": ".env", "wf-app-tmux-socket": "/tmp/tmux-", "wf-list-users": "_users"} {
		l := fullLayout(t, marker)
		got := fullRows(Check(context.Background(), l.opts))
		if got[check].Status != Fail {
			t.Errorf("%s with a leak in %q = %+v, want FAIL", check, marker, got[check])
		}
	}
}

func TestFullLevelDoesNotCountADeadTmuxServerAsProof(t *testing.T) {
	l := fullLayout(t, "")
	l.opts.TmuxControl = func(_ context.Context, _ string, _ string, _ ...string) (string, error) {
		return "no server running", nil
	}
	got := fullRows(Check(context.Background(), l.opts))
	if got["tmux-socket-unreachable"].Status != Fail || !strings.Contains(got["tmux-socket-unreachable"].Detail, "nothing was proven") {
		t.Fatalf("a test server that is not alive proves nothing: %+v", got["tmux-socket-unreachable"])
	}
}

func TestBasicLevelRunsNoFullChecks(t *testing.T) {
	l := fullLayout(t, "")
	l.opts.Level = ""
	if got := fullRows(Check(context.Background(), l.opts)); len(got) != 0 {
		t.Fatalf("basic level must not run the extended checks: %v", got)
	}
}
