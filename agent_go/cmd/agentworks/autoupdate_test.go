package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type autoUpdateFixture struct {
	o         *options
	root      string
	target    string
	downloads *atomic.Int32
	set       func(breaking bool, reason string)
}

// newAutoUpdateFixture serves a release (newer than the installed fake CLI) and isolates every folder the updater uses.
func newAutoUpdateFixture(t *testing.T) *autoUpdateFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows only notifies for now")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("AGENTWORKS_AUTO_UPDATE", "")
	const newSHA = "0123456789abcdef0123456789abcdef01234567"
	fakeBinary := "#!/bin/sh\necho '{\"version\": \"" + newSHA + "\"}'\n"
	sum := sha256.Sum256([]byte(fakeBinary))
	asset := "agentworks-" + runtime.GOOS + "-" + runtime.GOARCH
	var breaking atomic.Bool
	var reason atomic.Value
	reason.Store("")
	downloads := &atomic.Int32{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/downloads/cli/version.json", func(w http.ResponseWriter, r *http.Request) {
		epoch := ""
		if breaking.Load() {
			epoch = "epoch-2"
		}
		fmt.Fprintf(w, `{"version":%q,"cli_build":"build-new","compat_epoch":%q,"breaking_reason":%q}`, newSHA, epoch, reason.Load().(string))
	})
	mux.HandleFunc("/api/downloads/cli/"+asset, func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		_, _ = w.Write([]byte(fakeBinary))
	})
	mux.HandleFunc("/api/downloads/cli/"+asset+".sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
	})
	server := cliTestServer(mux)
	t.Cleanup(server.Close)
	target := filepath.Join(t.TempDir(), "agentworks")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldTarget, oldVersion, oldBuild := updateTargetOverride, cliVersion, cliBuild
	updateTargetOverride, cliVersion, cliBuild = target, "oldoldoldoldoldold", "build-old"
	oldStop, oldRestore, oldPlugins := autoUpdateStopHelper, autoUpdateRestoreHelper, autoUpdatePlugins
	autoUpdateStopHelper = func(context.Context) error { return nil }
	autoUpdateRestoreHelper = func(context.Context, *options, string) error { return nil }
	autoUpdatePlugins = func(context.Context, *options, bool) {}
	t.Cleanup(func() {
		updateTargetOverride, cliVersion, cliBuild = oldTarget, oldVersion, oldBuild
		autoUpdateStopHelper, autoUpdateRestoreHelper, autoUpdatePlugins = oldStop, oldRestore, oldPlugins
	})
	root := filepath.Join(home, "bridge")
	t.Setenv("HOME", home)
	o := &options{serverURL: server.URL, getenv: func(string) string { return "" }, stdout: io.Discard, stderr: io.Discard}
	return &autoUpdateFixture{o: o, root: root, target: target, downloads: downloads, set: func(b bool, r string) { breaking.Store(b); reason.Store(r) }}
}

func (f *autoUpdateFixture) installed(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(f.target)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func stateRoot(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(cache, "agentworks", "bridge")
}

// A normal release is installed by itself, once, and the next prompt says so once.
func TestAutoUpdateInstallsANormalReleaseAndSaysSoOnce(t *testing.T) {
	f := newAutoUpdateFixture(t)
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.installed(t), "0123456789abcdef") || f.downloads.Load() != 1 {
		t.Fatalf("the offered build was not installed (downloads %d): %q", f.downloads.Load(), f.installed(t))
	}
	if _, err := os.Stat(f.target + ".bak"); err == nil {
		t.Fatal("the previous program was left behind after a good update")
	}
	root := stateRoot(t)
	if line := autoUpdateHookLine(root, time.Now()); !strings.Contains(line, "updated itself") {
		t.Fatalf("no note after the update: %q", line)
	}
	if line := autoUpdateHookLine(root, time.Now()); line != "" {
		t.Fatalf("the note was repeated: %q", line)
	}
}

// A release marked breaking is never installed: the person is told, once a day, with the reason.
func TestAutoUpdateLeavesABreakingReleaseToThePerson(t *testing.T) {
	f := newAutoUpdateFixture(t)
	f.set(true, "the message format changed")
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.installed(t), "0123456789abcdef") || f.downloads.Load() != 0 {
		t.Fatal("a breaking release was installed automatically")
	}
	root := stateRoot(t)
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if line := autoUpdateHookLine(root, now); !strings.Contains(line, "update needs you: the message format changed") {
		t.Fatalf("the reason was not said: %q", line)
	}
	if line := autoUpdateHookLine(root, now.Add(2*time.Hour)); line != "" {
		t.Fatalf("said twice in one day: %q", line)
	}
	if line := autoUpdateHookLine(root, now.Add(25*time.Hour)); !strings.Contains(line, "needs you") {
		t.Fatalf("not said again the next day: %q", line)
	}
}

// The person's off switch stops installs; the chat is only told an update exists.
func TestAutoUpdateOffInstallsNothingAndOnlyTells(t *testing.T) {
	f := newAutoUpdateFixture(t)
	if err := autoUpdateSetting(f.o, "off"); err != nil {
		t.Fatal(err)
	}
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	if f.downloads.Load() != 0 || strings.Contains(f.installed(t), "0123456789abcdef") {
		t.Fatal("an update was installed while automatic updates were off")
	}
	if line := autoUpdateHookLine(stateRoot(t), time.Now()); !strings.Contains(line, "automatic updates are off") {
		t.Fatalf("no notice while off: %q", line)
	}
	if err := autoUpdateSetting(f.o, "on"); err != nil || !autoUpdateOn() {
		t.Fatalf("could not turn updates back on: %v", err)
	}
}

// A new helper that does not come up rolls the program back, says so, and does not retry the same build.
func TestAutoUpdateRollsBackWhenTheNewHelperDoesNotStart(t *testing.T) {
	f := newAutoUpdateFixture(t)
	restores := 0
	autoUpdateRestoreHelper = func(context.Context, *options, string) error {
		restores++
		if restores == 1 {
			return errors.New("did not answer")
		}
		return nil // the old program's helper after the rollback
	}
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.installed(t), "echo old") {
		t.Fatalf("the previous program was not restored: %q", f.installed(t))
	}
	if line := autoUpdateHookLine(stateRoot(t), time.Now()); !strings.Contains(line, "rolled back") {
		t.Fatalf("no notice after a rollback: %q", line)
	}
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	if f.downloads.Load() != 1 {
		t.Fatalf("the same build was tried again (%d downloads)", f.downloads.Load())
	}
}

// An offer that has not changed since it was last acted on is never installed again: a person who updated by hand
// (or a server that went back) is not overruled by the schedule. The plugin step's own text never reaches a chat.
func TestAutoUpdateDoesNotRedoAnOfferItAlreadyActedOn(t *testing.T) {
	f := newAutoUpdateFixture(t)
	autoUpdatePlugins = func(_ context.Context, o *options, _ bool) {
		fmt.Fprintln(o.stderr, "Muse plugin updated. Run: muse plugins approve something-secret")
	}
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	note := autoUpdateHookLine(stateRoot(t), time.Now())
	if !strings.Contains(note, "approve it and start a new Muse chat") || strings.Contains(note, "something-secret") {
		t.Fatalf("the note must be fixed wording, not the command: %q", note)
	}
	// The person goes back to another build by hand; the server still offers the same one.
	if err := os.WriteFile(f.target, []byte("#!/bin/sh\necho manual\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cliVersion, cliBuild = "manualmanualmanual", "build-manual"
	before := f.downloads.Load()
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	if f.downloads.Load() != before || !strings.Contains(f.installed(t), "manual") {
		t.Fatal("the schedule overruled a manual choice for an offer it had already acted on")
	}
	if raw, _ := os.ReadFile(filepath.Join(stateRoot(t), "bridge.log")); !strings.Contains(string(raw), "[UPDATE] skip: the offer has not changed") {
		t.Fatalf("no [UPDATE] line for the skip: %q", raw)
	}
}

// Marking a release breaking bumps the epoch once; a client already on that epoch keeps updating by itself.
func TestAutoUpdateInstallsWhenTheClientIsOnTheOfferedEpoch(t *testing.T) {
	f := newAutoUpdateFixture(t)
	f.set(true, "the message format changed")
	old := cliCompatEpoch
	cliCompatEpoch = "epoch-2"
	t.Cleanup(func() { cliCompatEpoch = old })
	if err := runAutoUpdate(f.o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.installed(t), "0123456789abcdef") {
		t.Fatal("a client on the offered epoch was not updated")
	}
}
