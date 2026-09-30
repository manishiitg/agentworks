package cliupdate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiTempNpmRootMatchesPi(t *testing.T) {
	// pi's getTemporaryDir("npm") with an empty suffix: sha256("npm-")[:8].
	// If pi ever changes this layout, this fails loudly instead of letting
	// the updater silently refresh a directory pi no longer reads.
	got := piTempNpmRoot(filepath.Join("agent"))
	want := filepath.Join("agent", "tmp", "extensions", "npm", "f35b2129")
	if got != want {
		t.Fatalf("temp npm root=%q want=%q", got, want)
	}
}

func TestNpmVersionNewer(t *testing.T) {
	cases := []struct {
		latest, installed string
		want              bool
	}{
		{"2.35.0", "2.10.0", true},
		{"2.10.0", "2.35.0", false},
		{"2.10.0", "2.10.0", false},
		{"2.10.1", "2.10.0", true},
		{"3.0.0", "2.99.9", true},
		{"2.10.0", "2.9.0", true},
		{"v2.35.0", "2.10.0", true},
		{"2.35.0+build.1", "2.35.0", false},
		{"2.35.0", "2.35.0-beta.1", true},
		{"2.35.0-beta.2", "2.35.0-beta.1", true},
		{"latest", "2.10.0", false},
		{"2.35.0", "installed", false},
		{"", "2.10.0", false},
		{"2.35.0", "", false},
	}
	for _, c := range cases {
		if got := npmVersionNewer(c.latest, c.installed); got != c.want {
			t.Fatalf("npmVersionNewer(%q, %q)=%v want=%v", c.latest, c.installed, got, c.want)
		}
	}
}

func TestParseNpmViewVersion(t *testing.T) {
	if got, err := parseNpmViewVersion(`"2.35.0"`); err != nil || got != "2.35.0" {
		t.Fatalf("string response: got=%q err=%v", got, err)
	}
	if got, err := parseNpmViewVersion(`["2.10.0", "2.35.0"]`); err != nil || got != "2.35.0" {
		t.Fatalf("array response: got=%q err=%v", got, err)
	}
	if _, err := parseNpmViewVersion(""); err == nil {
		t.Fatal("empty response accepted")
	}
	if _, err := parseNpmViewVersion("not json"); err == nil {
		t.Fatal("garbage response accepted")
	}
}

func writeCachedPlugin(t *testing.T, root, name, version string) {
	t.Helper()
	dir := filepath.Join(root, "node_modules", name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"`+version+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
}

// fakeNpm installs a scripted npm that serves `view` from latestByName and
// applies `install` by rewriting the cached package.json. It returns the path
// of the invocation log.
func fakeNpm(t *testing.T, latestByName map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := `#!/bin/sh
set -eu
echo "$*" >> "` + log + `"
if [ "$1" = "view" ]; then
  case "$2" in
`
	for name, latest := range latestByName {
		script += "    " + name + ") printf '\"" + latest + "\"\\n' ;;\n"
	}
	script += `    *) exit 3 ;;
  esac
  exit 0
fi
if [ "$1" = "install" ]; then
  spec="$2"; root="$4"
  name="${spec%@latest}"
  mkdir -p "$root/node_modules/$name"
  printf '{"version":"INSTALLED-BY-FAKE"}\n' > "$root/node_modules/$name/package.json"
  exit 0
fi
exit 4
`
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func pluginCacheEnv(t *testing.T) (agentDir, root string) {
	t.Helper()
	agentDir = t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	root = piTempNpmRoot(agentDir)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	return agentDir, root
}

func TestUpdatePiTempPluginsUpgradesOutdated(t *testing.T) {
	original := managedPiTempPlugins
	managedPiTempPlugins = []string{"test-managed-extension"}
	t.Cleanup(func() { managedPiTempPlugins = original })
	_, root := pluginCacheEnv(t)
	writeCachedPlugin(t, root, "test-managed-extension", "2.10.0")
	log := fakeNpm(t, map[string]string{"test-managed-extension": "2.35.0"})

	if err := updatePiTempPlugins(context.Background(), os.Environ()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "node_modules", "test-managed-extension", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "INSTALLED-BY-FAKE") {
		t.Fatalf("plugin was not reinstalled: %s", data)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "install test-managed-extension@latest --prefix " + root + " --legacy-peer-deps"
	if !strings.Contains(string(calls), want) {
		t.Fatalf("npm calls=%q want substring %q", calls, want)
	}
}

func TestUpdatePiTempPluginsSkipsCurrent(t *testing.T) {
	original := managedPiTempPlugins
	managedPiTempPlugins = []string{"test-managed-extension"}
	t.Cleanup(func() { managedPiTempPlugins = original })
	_, root := pluginCacheEnv(t)
	writeCachedPlugin(t, root, "test-managed-extension", "2.35.0")
	log := fakeNpm(t, map[string]string{"test-managed-extension": "2.35.0"})

	if err := updatePiTempPlugins(context.Background(), os.Environ()); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "install ") {
		t.Fatalf("current plugin was reinstalled: %q", calls)
	}
}

func TestUpdatePiTempPluginsSkipsMissingCache(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if err := updatePiTempPlugins(context.Background(), os.Environ()); err != nil {
		t.Fatal(err)
	}
}

func TestUpdatePiTempPluginsLeavesForeignPackagesAlone(t *testing.T) {
	_, root := pluginCacheEnv(t)
	writeCachedPlugin(t, root, "user-extension", "1.0.0")
	t.Setenv("PATH", t.TempDir())

	if err := updatePiTempPlugins(context.Background(), os.Environ()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "node_modules", "user-extension", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "1.0.0") {
		t.Fatalf("foreign package was modified: %s", data)
	}
}

func TestPiAgentDirOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", custom)
	got, err := piAgentDir()
	if err != nil || got != custom {
		t.Fatalf("override: got=%q err=%v", got, err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	got, err = piAgentDir()
	want := filepath.Join(home, ".pi", "agent")
	if err != nil || got != want {
		t.Fatalf("default: got=%q want=%q err=%v", got, want, err)
	}
}

func TestNativePiMCPDoesNotRefreshRetiredPlugin(t *testing.T) {
	_, root := pluginCacheEnv(t)
	writeCachedPlugin(t, root, "pi-mcp-adapter", "3.2.0")
	log := fakeNpm(t, map[string]string{"pi-mcp-adapter": "3.3.0"})
	if err := updatePiTempPlugins(context.Background(), os.Environ()); err != nil {
		t.Fatal(err)
	}
	if calls, _ := os.ReadFile(log); len(calls) != 0 {
		t.Fatalf("retired adapter refreshed: %s", calls)
	}
}
