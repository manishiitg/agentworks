//go:build linux

package slotcheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealSlotChain runs on a prepared Linux host only (container_e2e.sh builds one: real slot accounts, sudo rule,
// root-owned slotctl, a docs tree, a 0700 app-owned browser profile). It runs as the service account through the real
// chain: Isolator -> sudo -> slotctl -> Landlock launcher -> /bin/sh.
func TestRealSlotChain(t *testing.T) {
	if os.Getenv("AGENTWORKS_SLOT_CHAIN_E2E") != "1" {
		t.Skip("needs a prepared slot host (workspace/slotcheck/container_e2e.sh)")
	}
	docs, app := os.Getenv("E2E_DOCS"), os.Getenv("E2E_APP")
	profile := os.Getenv("E2E_PROFILE") // an existing project profile folder holding a Cookies file
	other := os.Getenv("E2E_OTHER_PROJECT")
	crew := os.Getenv("E2E_CREW")
	notGranted := os.Getenv("E2E_NOT_GRANTED") // world-readable, outside the probe's grants
	ctx := context.Background()

	base := Probe{Slot: "slot01", Kind: KindCrew, Dir: crew, DocsRoot: docs, ReadPaths: []string{crew}, WritePaths: []string{crew}, BrowserSession: "project-3bdc30503fa28a4f--browser"}

	t.Run("a Crew project command starts although the project's browser profile is 0700", func(t *testing.T) {
		out, errOut, err := runThroughShellTool(ctx, base, "pwd")
		if err != nil || strings.TrimSpace(out) != crew {
			t.Fatalf("pwd: out=%q err=%v stderr=%q", out, err, errOut)
		}
		if strings.Contains(errOut, "SANDBOX_UNAVAILABLE") {
			t.Fatalf("refused: %s", errOut)
		}
	})
	// PLAT-622: a person's command gets an allowlist of the shared service environment, not all of it.
	t.Run("the slot's shell gets only the allowlisted environment", func(t *testing.T) {
		t.Setenv("AUTH_ALLOWED_EMAILS", "e2e-canary")
		t.Setenv("GATEWAY_USERNAME", "e2e-canary")
		t.Setenv("NEW_SERVER_SETTING", "e2e-canary")
		t.Setenv("AGENTWORKS_SLOT_CLI_USERS", "e2e-canary")
		out, errOut, err := runThroughShellTool(ctx, base, "env")
		if err != nil || !strings.Contains(out, "PATH=") {
			t.Fatalf("env: err=%v stderr=%q", err, errOut)
		}
		for _, key := range []string{"AUTH_ALLOWED_EMAILS=", "GATEWAY_USERNAME=", "NEW_SERVER_SETTING=", "AGENTWORKS_SLOT_CLI_USERS="} {
			if strings.Contains(out, key) {
				t.Fatalf("%s reached the slot's shell", key)
			}
		}
	})
	t.Run("the slot reads its own project", func(t *testing.T) {
		out, errOut, err := runThroughShellTool(ctx, base, "cat own.txt")
		if err != nil || strings.TrimSpace(out) != "own" {
			t.Fatalf("own file: out=%q err=%v stderr=%q", out, err, errOut)
		}
	})
	negatives := map[string]string{
		"the browser profile (session cookies)": "cat " + filepath.Join(profile, "Cookies"),
		"the browser profile folder listing":    "ls " + profile,
		"another user's tree":                   "ls " + other,
		"another user's file":                   "cat " + filepath.Join(other, "secret.txt"),
		"the app's .env":                        "cat " + filepath.Join(app, ".env"),
		// World-readable by Unix permissions but not granted: only Landlock stops this, so the command really ran
		// confined (it never falls back to running unconfined).
		"a world-readable file outside the grants": "cat " + notGranted,
	}
	for name, command := range negatives {
		t.Run("cannot read "+name, func(t *testing.T) {
			out, errOut, err := runThroughShellTool(ctx, base, command)
			if err == nil {
				t.Fatalf("%q succeeded as the slot: %q", command, out)
			}
			if strings.Contains(out, "COOKIE") || strings.Contains(out, "SECRET") || strings.Contains(out, "PUBLIC") || strings.Contains(out, "ENVFILE") {
				t.Fatalf("%q leaked content: %q", command, out)
			}
			t.Logf("%s: refused (%v): %s", command, err, firstLine(errOut))
		})
	}
	// PLAT-480 (F1): the slot's tmux server belongs to the same account, and Landlock does not govern connect() on
	// pathname Unix sockets, so the only thing between a confined command and that server is that the socket is
	// not in the command's view. container_e2e.sh starts a real server on the socket first.
	if socket := os.Getenv("E2E_TMUX_SOCKET"); socket != "" {
		t.Run("cannot reach the slot's tmux server", func(t *testing.T) {
			list := "tmux -S " + socket + " list-sessions"
			out, errOut, err := runThroughShellTool(ctx, base, list)
			if err == nil || strings.Contains(out, "probe") {
				t.Fatalf("a confined command reached the slot's tmux server: out=%q err=%v", out, err)
			}
			t.Logf("refused: %s", firstLine(errOut))
			out, errOut, err = runThroughShellTool(ctx, base, "ls -A "+filepath.Dir(filepath.Dir(socket))+" | wc -l")
			if err != nil || strings.TrimSpace(out) != "0" {
				t.Fatalf("the slot run root is not empty in the command's view: out=%q err=%v stderr=%q", out, err, errOut)
			}
		})
	}
	// The slot run root looks empty to a command except for the folders it was granted inside it: the Code terminal's
	// own shell folder (its tmux server runs in the sandbox with its socket there) and the output helper.
	if keep := os.Getenv("E2E_RUN_KEEP"); keep != "" {
		granted := base
		granted.ReadPaths = append(append([]string{}, base.ReadPaths...), keep)
		granted.WritePaths = append(append([]string{}, base.WritePaths...), keep)
		t.Run("a granted folder inside the slot run root still works", func(t *testing.T) {
			out, errOut, err := runThroughShellTool(ctx, granted, "touch "+filepath.Join(keep, "made-by-command")+" && echo ok")
			if err != nil || strings.TrimSpace(out) != "ok" {
				t.Fatalf("write into the granted folder: out=%q err=%v stderr=%q", out, err, errOut)
			}
			// The other shells' folders and sockets in the same run folder are not there.
			other := filepath.Join(filepath.Dir(keep), "other", "secret")
			out, errOut, err = runThroughShellTool(ctx, granted, "cat "+other)
			if err == nil || strings.Contains(out, "OTHER") {
				t.Errorf("another shell's folder is visible: out=%q err=%v", out, err)
			}
			out, errOut, err = runThroughShellTool(ctx, granted, "ls -A "+filepath.Join(filepath.Dir(keep), "other")+" 2>&1; test -e "+filepath.Join(filepath.Dir(filepath.Dir(keep)), "tmux.sock"))
			if err == nil {
				t.Errorf("the slot's tmux socket is visible next to the granted folder: out=%q", out)
			}
		})
		t.Run("the output helper is still importable from a slot command", func(t *testing.T) {
			out, errOut, err := runThroughShellTool(ctx, base, `for d in $(echo "$PYTHONPATH" | tr ':' ' '); do if test -f "$d/agentworks_output.py"; then echo "$d"; exit 0; fi; done; exit 1`)
			if err != nil || !strings.Contains(out, "output-helper-") {
				t.Fatalf("output helper not reachable: out=%q err=%v stderr=%q", out, err, errOut)
			}
		})
	}
	t.Run("the whole self-test passes on the fixed layout", func(t *testing.T) {
		gids := []int{os.Getgid()}
		if groups, err := os.Getgroups(); err == nil {
			gids = append(gids, groups...)
		}
		exe := os.Getenv("E2E_RUNNER")
		rows := Check(ctx, Options{DocsRoot: docs, AppDir: app, Runner: exe, SlotctlConfig: os.Getenv("AGENTWORKS_SLOTCTL_CONFIG"),
			SlotTable: os.Getenv("AGENTWORKS_SLOTS_FILE"), TableOwnerUID: 0, ServiceGIDs: gids, Run: RunThroughShellTool, Lookup: LookupAccount})
		t.Logf("\n%s", Format(rows))
		if Failed(rows) {
			t.Fatal("self-test failed on the fixed layout")
		}
	})
}
