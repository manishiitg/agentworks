package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An update refreshes only the plugins that are installed, and says plainly what
// to run for the ones the app cannot update from the command line.
func TestInstalledPluginUpdatesFollowWhatIsInstalled(t *testing.T) {
	home := "/home/example"
	files := map[string]string{
		filepath.Join(home, ".claude", "plugins", "installed_plugins.json"): `{"agentworks@agentworks":{}}`,
	}
	present := map[string]bool{"claude": true, "codex": true, "muse": true, "cursor-agent": true}
	got := installedPluginUpdates(home, func(n string) bool { return present[n] }, func(p string) string { return files[p] })
	names := map[string]pluginUpdate{}
	for _, u := range got {
		names[u.Provider] = u
	}
	if len(names["claude"].Auto) != 2 || names["claude"].Manual == "" {
		t.Fatalf("an installed Claude plugin must be refreshed with its documented commands: %+v", names["claude"])
	}
	if _, ok := names["codex"]; ok {
		t.Fatal("a Codex plugin that is not installed must not be touched")
	}
	if u, ok := names["muse"]; !ok || len(u.Auto) != 0 || !strings.Contains(u.Manual, "muse plugins install") {
		t.Fatalf("Muse is updated by the person, with the exact line printed: %+v", u)
	}
	if u, ok := names["cursor"]; !ok || len(u.Auto) != 0 || !strings.Contains(u.Manual, "setup_project.py") {
		t.Fatalf("Cursor project hooks are repaired by the person, never automatically: %+v", u)
	}
	if _, ok := names["pi"]; ok {
		t.Fatal("pi is not on PATH and must not appear")
	}
}

// Only the marketplace named agentworks is moved, and only when it still points at
// the full product repository.
func TestOldMarketplaceSourceIsDetectedPerProvider(t *testing.T) {
	home := "/home/example"
	files := map[string]string{
		filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"): `{"agentworks":{"source":{"source":"github","repo":"manishiitg/agentworks"}},"other":{"source":{"source":"github","repo":"manishiitg/agentworks-other"}}}`,
		filepath.Join(home, ".codex", "config.toml"):                         "[marketplaces.agentworks]\nsource_type = \"git\"\nsource = \"https://github.com/manishiitg/agentworks-plugins.git\"\n",
	}
	got := oldMarketplaceProviders(home, func(p string) string { return files[p] }, func() string {
		return "agentworks\tplugins=2\tsource=https://github.com/manishiitg/agentworks.git\n"
	})
	if !got["claude"] || !got["muse"] || got["codex"] {
		t.Fatalf("old source must be found for Claude and Muse, not for Codex on the plugins repository: %v", got)
	}
}

// A helper that is another build than the CLI is replaced once; a development
// build, the same build, and a second attempt within ten minutes never do.
func TestBridgeIsReplacedOnceWhenAnotherBuild(t *testing.T) {
	root := t.TempDir()
	old := cliVersion
	defer func() { cliVersion = old }()
	cliVersion = "dev"
	if bridgeIsOlder(root, "") {
		t.Fatal("a development build must not replace the helper")
	}
	cliVersion = "build-2"
	if bridgeIsOlder(root, "build-2") {
		t.Fatal("the same build keeps its helper")
	}
	if !bridgeIsOlder(root, "") {
		t.Fatal("a helper that reports no version is older than this build")
	}
	if bridgeIsOlder(root, "build-1") {
		t.Fatal("two builds must not replace each other repeatedly")
	}
}

// Codex is found on PATH or, on macOS, in the Codex desktop app's own command
// line; the profile is the one Codex itself uses (CODEX_HOME).
func TestCodexAppCommandLineAndHome(t *testing.T) {
	if got := codexExecutable(func(n string) bool { return n == "codex" }); got != "codex" {
		t.Fatalf("codex on PATH must win: %q", got)
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "codex")
	if err := os.WriteFile(app, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := codexAppBundleCLI
	defer func() { codexAppBundleCLI = old }()
	codexAppBundleCLI = app
	none := func(string) bool { return false }
	oldOS := codexGOOS
	defer func() { codexGOOS = oldOS }()
	codexGOOS = "darwin"
	if got := codexExecutable(none); got != app {
		t.Fatalf("the app's command line must be used when codex is not on PATH: %q", got)
	}
	codexGOOS = "linux"
	if got := codexExecutable(none); got != "" {
		t.Fatalf("no guessed fallback outside macOS: %q", got)
	}
	codexGOOS = "darwin"
	codexAppBundleCLI = filepath.Join(dir, "missing")
	if got := codexExecutable(none); got != "" {
		t.Fatalf("no codex anywhere must report not installed: %q", got)
	}
	t.Setenv("CODEX_HOME", "")
	if got := codexHomeDir("/home/example"); got != filepath.Join("/home/example", ".codex") {
		t.Fatalf("default profile: %q", got)
	}
	t.Setenv("CODEX_HOME", dir)
	if got := codexHomeDir("/home/example"); got != dir {
		t.Fatalf("CODEX_HOME must be honored: %q", got)
	}
	t.Setenv("CODEX_HOME", "relative/profile")
	if codexHomeDir("/home/example") != "" || codexHomeError() == nil {
		t.Fatal("a relative CODEX_HOME must be refused, never replaced by the default profile")
	}
	if !codexPluginDisabled("[plugins.\"agentworks-codex@agentworks\"]\nenabled = false\n") || codexPluginDisabled("[plugins.\"agentworks-codex@agentworks\"]\nenabled = true\n") || codexPluginDisabled("") {
		t.Fatal("a disabled Codex plugin must be detected, an enabled or unset one not")
	}
	// An app-only Codex user (nothing on PATH) still counts as having Codex.
	codexAppBundleCLI = app
	t.Setenv("PATH", "")
	if !providerAvailable("codex") {
		t.Fatal("the Codex app must count as an available Codex")
	}
}
