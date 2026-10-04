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
