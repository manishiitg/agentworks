package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// macOS shows a notification with the icon of the app that sends it, so a plain
// command line tool gets a generic icon. The CLI therefore keeps a tiny
// "AgentWorks" app in ~/Applications (an AppleScript applet with the AgentWorks
// icon, signed for this Mac only) and shows notifications through it. The
// person allows its notifications once, when macOS asks.

//go:embed assets/agentworks.icns
var notifierIcon []byte

// Bump when the app below changes, so an older copy is rebuilt.
const notifierVersion = "2"

const notifierScript = `on run
	-- Clicking a notification opens this app again with no message waiting: do nothing then.
	try
		set f to (POSIX path of (path to library folder from user domain)) & "Caches/agentworks/notify.txt"
		set t to read (POSIX file f) as «class utf8»
		if t is not "" then display notification t with title "AgentWorks"
	end try
end run

on reopen
	-- Nothing to show.
end reopen
`

var notifierMu sync.Mutex

func notifierApp() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", "AgentWorks.app"), nil
}

// notifierAddedNote is shown once, as the first notification after the app is created.
const notifierAddedNote = "AgentWorks added a small notifier app so its notifications show its icon. Remove it any time by deleting ~/Applications/AgentWorks.app, or with agentworks setup notifications --remove."

// ensureNotifierApp builds the app once (or after an update of it); created
// reports a fresh build, so the person can be told once.
func ensureNotifierApp(ctx context.Context) (app string, created bool, err error) {
	app, err = buildNotifierApp(ctx)
	return app, err == nil && notifierFresh, err
}

var notifierFresh bool

// notifierOff is the person's choice to not have the app (setup notifications --remove).
func notifierOff() string {
	cache, _ := os.UserCacheDir()
	return filepath.Join(cache, "agentworks", "notifier-off")
}

func buildNotifierApp(ctx context.Context) (string, error) {
	notifierFresh = false
	if _, err := os.Stat(notifierOff()); err == nil {
		return "", errors.New("notifier app turned off")
	}
	app, err := notifierApp()
	if err != nil {
		return "", err
	}
	stamp := filepath.Join(app, "Contents", "Resources", "agentworks-notifier-version")
	if b, e := os.ReadFile(stamp); e == nil && bytes.Equal(b, []byte(notifierVersion)) {
		return app, nil
	}
	if err = os.MkdirAll(filepath.Dir(app), 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(filepath.Dir(app), ".agentworks-notifier-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	built := filepath.Join(staging, "AgentWorks.app")
	run := func(name string, args ...string) error {
		out, e := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if e != nil {
			return errors.New(name + ": " + string(bytes.TrimSpace(out)))
		}
		return nil
	}
	plist := filepath.Join(built, "Contents", "Info.plist")
	resources := filepath.Join(built, "Contents", "Resources")
	steps := []func() error{
		func() error { return run("/usr/bin/osacompile", "-o", built, "-e", notifierScript) },
		// The applet's built-in asset catalog would override the icon file.
		func() error { _ = os.Remove(filepath.Join(resources, "Assets.car")); return nil },
		func() error { _ = run("/usr/bin/plutil", "-remove", "CFBundleIconName", plist); return nil },
		func() error { return os.WriteFile(filepath.Join(resources, "applet.icns"), notifierIcon, 0o644) },
		func() error {
			return run("/usr/bin/plutil", "-replace", "CFBundleIdentifier", "-string", "com.agentworks.notifier", plist)
		},
		func() error {
			return run("/usr/bin/plutil", "-replace", "CFBundleName", "-string", "AgentWorks", plist)
		},
		func() error {
			return os.WriteFile(filepath.Join(resources, "agentworks-notifier-version"), []byte(notifierVersion), 0o644)
		},
		func() error { return run("/usr/bin/codesign", "--force", "--deep", "--sign", "-", built) },
	}
	for _, step := range steps {
		if err = step(); err != nil {
			return "", err
		}
	}
	_ = os.RemoveAll(app)
	if err = os.Rename(built, app); err != nil {
		return "", err
	}
	_ = run("/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-f", app)
	notifierFresh = true
	return app, nil
}

// notifyWithIcon shows one notification through the AgentWorks app.
func notifyWithIcon(text string) error {
	notifierMu.Lock()
	defer notifierMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, created, err := ensureNotifierApp(ctx)
	if err != nil {
		return err
	}
	if created {
		_ = showNotification(app, notifierAddedNote)
	}
	return showNotification(app, text)
}

// showNotification hands one text to the app as data (never as script) and
// removes it again once shown, so the last message does not stay in the cache.
func showNotification(app, text string) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(cache, "agentworks")
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	note := filepath.Join(dir, "notify.txt")
	if err = os.WriteFile(note, []byte(text), 0o600); err != nil {
		return err
	}
	defer os.Remove(note)
	show, cancelShow := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShow()
	return exec.CommandContext(show, filepath.Join(app, "Contents", "MacOS", "applet")).Run()
}

// removeNotifierApp deletes the notifier app (agentworks setup notifications --remove).
func removeNotifierApp() (string, error) {
	app, err := notifierApp()
	if err != nil {
		return "", err
	}
	_ = os.MkdirAll(filepath.Dir(notifierOff()), 0o700)
	if err = os.WriteFile(notifierOff(), []byte("off\n"), 0o600); err != nil {
		return "", err
	}
	if err = os.RemoveAll(app); err != nil {
		return "", err
	}
	return "Removed " + app + " and will not add it again. AgentWorks notifications now use the plain macOS style. To get the icon back: agentworks setup notifications.", nil
}

// restoreNotifierApp lets the CLI add the app again (agentworks setup notifications).
func restoreNotifierApp() (string, error) {
	_ = os.Remove(notifierOff())
	return "AgentWorks will add its small notifier app the next time it notifies you, so notifications show its icon.", nil
}
