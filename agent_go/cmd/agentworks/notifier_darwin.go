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
const notifierVersion = "1"

const notifierScript = `on run
	set f to (POSIX path of (path to library folder from user domain)) & "Caches/agentworks/notify.txt"
	set t to read (POSIX file f) as «class utf8»
	display notification t with title "AgentWorks"
end run`

var notifierMu sync.Mutex

func notifierApp() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", "AgentWorks.app"), nil
}

// ensureNotifierApp builds the app once (or after an update of it).
func ensureNotifierApp(ctx context.Context) (string, error) {
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
	return app, nil
}

// notifyWithIcon shows one notification through the AgentWorks app.
func notifyWithIcon(text string) error {
	notifierMu.Lock()
	defer notifierMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, err := ensureNotifierApp(ctx)
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(cache, "agentworks")
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "notify.txt"), []byte(text), 0o600); err != nil {
		return err
	}
	show, cancelShow := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShow()
	return exec.CommandContext(show, filepath.Join(app, "Contents", "MacOS", "applet")).Run()
}
