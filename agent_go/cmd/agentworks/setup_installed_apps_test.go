package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-856-499: one failed app must not stop setup of another installed app;
// missing apps get a later setup command rather than an attempted install.
func TestInstalledAppSetupContinuesAfterOneAppFails(t *testing.T) {
	var out strings.Builder
	var called []string
	err := setupInstalledApps(context.Background(), &options{stdout: &out}, func(provider string) bool {
		return provider == "codex" || provider == "cursor"
	}, func(_ context.Context, _ *options, provider string) error {
		called = append(called, provider)
		if provider == "codex" {
			return errors.New("provider setup failed")
		}
		return nil
	})
	if err != nil || strings.Join(called, ",") != "codex,cursor" {
		t.Fatalf("setup stopped at the first failure: %v %v", called, err)
	}
	for _, want := range []string{"Codex: setup needs attention", "Cursor: set up", "Claude not found; run agentworks setup claude"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing actionable result %q: %s", want, out.String())
		}
	}
}

// PLAT-856-500: removing the old variant must remove its enabled definition and
// cached hooks, while keeping the tools variant and other provider settings.
func TestCodexVariantRemovalLeavesNoOldHooksAndKeepsOtherSettings(t *testing.T) {
	profile := t.TempDir()
	config := `[plugins."agentworks-codex@agentworks"]
enabled = true
[plugins."agentworks-codex-tools@agentworks"]
enabled = false
[plugins."other@market"]
enabled = true
[hooks]
trusted = ["existing-trust"]
`
	if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(profile, "plugins", "cache", "agentworks", "agentworks-codex", "1.0.0", "hooks", "hooks.json")
	if err := writeDesktopPluginFile(cache, []byte(`{"hooks":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := removeCodexPluginVariant(profile, &options{stdout: io.Discard}, "agentworks-codex"); err != nil {
		t.Fatal(err)
	}
	doc, err := readCodexSettings(profile)
	if err != nil {
		t.Fatal(err)
	}
	plugins := doc["plugins"].(map[string]any)
	if _, exists := plugins["agentworks-codex@agentworks"]; exists {
		t.Fatal("full plugin still configured")
	}
	if _, err = os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("old hook file still installed")
	}
	if plugins["agentworks-codex-tools@agentworks"].(map[string]any)["enabled"] != false || plugins["other@market"].(map[string]any)["enabled"] != true {
		t.Fatal("removal changed a retained plugin's enabled state")
	}
	if _, ok := doc["hooks"]; !ok {
		t.Fatal("removal changed provider hook trust")
	}
}

// PLAT-856-499: the owner's tools-only Claude setup had an older full package
// in the registry but not enabled. Automatic setup must not turn its hooks on.
func TestSetupDoesNotSelectRegisteredButDisabledClaudeHooks(t *testing.T) {
	personHome := t.TempDir()
	t.Setenv("HOME", personHome)
	t.Setenv("USERPROFILE", personHome)
	root := filepath.Join(personHome, ".claude")
	if err := writeDesktopPluginFile(filepath.Join(root, "plugins", "installed_plugins.json"), []byte(`{"plugins":{"agentworks@agentworks":[{"scope":"user"}]}}`)); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "settings.json")
	for _, enabled := range []bool{false, true} {
		value := `{"enabledPlugins":{"agentworks-tools@agentworks":true}}`
		if enabled {
			value = `{"enabledPlugins":{"agentworks@agentworks":true}}`
		}
		if err := os.WriteFile(settings, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := hooksPluginInstalled("claude"); got != enabled {
			t.Fatalf("registered Claude hooks enabled=%v, setup selected=%v", enabled, got)
		}
	}
}
