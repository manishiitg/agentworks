package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var setupApps = []struct{ provider, name string }{
	{"codex", "Codex"}, {"claude", "Claude"}, {"cursor", "Cursor"}, {"muse", "Muse"}, {"pi", "Pi"},
}

// Installation already requests setup. Each app uses the exact manual setup
// command/defaults; a missing or failing app never prevents the others.
func setupInstalledApps(ctx context.Context, o *options, installed func(string) bool, setup func(context.Context, *options, string) (string, error)) error {
	for _, app := range setupApps {
		if !installed(app.provider) {
			fmt.Fprintf(o.stdout, "%s not found; run agentworks setup %s after installing it.\n", app.name, app.provider)
			continue
		}
		note, err := setup(ctx, o, app.provider)
		if err != nil {
			fmt.Fprintf(o.stdout, "%s: setup needs attention: %s. Run agentworks setup %s to retry.\n", app.name, strings.Join(strings.Fields(err.Error()), " "), app.provider)
			continue
		}
		next := "restart " + app.name
		if app.provider == "muse" {
			next += "; review its AgentWorks hook approval"
		} else if app.provider == "codex" && runtime.GOOS != "windows" && hooksPluginInstalled("codex") {
			next += "; review AgentWorks in Settings > Hooks"
		}
		if note != "" {
			next += ". " + strings.TrimSpace(note)
		}
		fmt.Fprintf(o.stdout, "%s: set up; %s\n", app.name, next)
	}
	return nil
}

func setupOneInstalledApp(ctx context.Context, o *options, provider string) (string, error) {
	var details strings.Builder
	child := *o
	child.stdout, child.stderr = &details, &details
	child.stdin = strings.NewReader("") // provider trust stays a person's choice after restart
	child.setupBatch = true
	child.setupToolsNote = ""
	cmd := setupCommand(&child)
	cmd.SetArgs([]string{provider})
	err := cmd.ExecuteContext(ctx)
	return child.setupToolsNote, err
}

func codingAppInstalled(provider string) bool {
	if providerAvailable(provider) {
		return true
	}
	if provider == "cursor" {
		if _, err := exec.LookPath("cursor-agent"); err == nil {
			return true
		}
	}
	if runtime.GOOS == "windows" && provider == "codex" && len(windowsCodexPackageRoots()) > 0 {
		return true
	}
	var names []string
	switch provider {
	case "codex":
		names = []string{"Codex", "ChatGPT"}
	case "claude":
		names = []string{"Claude"}
	case "cursor":
		names = []string{"Cursor"}
	case "muse":
		names = []string{"Muse"}
	}
	home, _ := os.UserHomeDir()
	for _, name := range names {
		var dirs []string
		if runtime.GOOS == "darwin" {
			dirs = []string{filepath.Join("/Applications", name+".app"), filepath.Join(home, "Applications", name+".app")}
		} else if runtime.GOOS == "windows" {
			for _, root := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles")} {
				if root != "" {
					dirs = append(dirs, filepath.Join(root, name), filepath.Join(root, "Programs", name))
				}
			}
		}
		for _, dir := range dirs {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return true
			}
		}
	}
	return false
}

// A Windows setup changes the selected plugin, not provider hook approval. Old
// cached hooks stop loading after the required Codex restart.
func setupCodexPluginVariant(ctx context.Context, o *options, name string) error {
	if err := codexHomeError(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	other := "agentworks-codex"
	if name == other {
		other = "agentworks-codex-tools"
	}
	profile := codexHomeDir(home)
	if _, err = editCodexSettings(profile, o, func(doc map[string]any) error {
		plugins := codexSettingsTable(doc, "plugins")
		old, exists := plugins[other+"@agentworks"].(map[string]any)
		_, selected := plugins[name+"@agentworks"]
		if exists && !selected && old["enabled"] == false {
			plugins[name+"@agentworks"] = map[string]any{"enabled": false}
		}
		return nil
	}); err != nil {
		return err
	}
	if err = setupCodexPlugin(ctx, o, name); err != nil {
		return err
	}
	return removeCodexPluginVariant(profile, o, other)
}

func removeCodexPluginVariant(profile string, o *options, name string) error {
	if name != "agentworks-codex" && name != "agentworks-codex-tools" {
		return fmt.Errorf("unknown Codex plugin")
	}
	_, err := editCodexSettings(profile, o, func(doc map[string]any) error {
		if plugins, ok := doc["plugins"].(map[string]any); ok {
			delete(plugins, name+"@agentworks")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(profile, "plugins", "cache", "agentworks", name))
}

func updateCodexTools(ctx context.Context, o *options) bool {
	if !o.setupAll || codexHomeError() != nil {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	doc, err := readCodexSettings(codexHomeDir(home))
	if err != nil {
		return false
	}
	plugins := codexSettingsTable(doc, "plugins")
	_, full := plugins["agentworks-codex@agentworks"]
	_, tools := plugins["agentworks-codex-tools@agentworks"]
	if !full && !tools {
		return false
	}
	if err = setupCodexPluginVariant(ctx, o, "agentworks-codex-tools"); err != nil {
		fmt.Fprintf(o.stderr, "Could not switch Codex to the tools-only plugin: %v. Run agentworks setup codex to retry.\n", err)
	} else {
		fmt.Fprintln(o.stderr, "Codex uses the tools-only plugin. Restart Codex to stop its previous hooks and load the tools.")
	}
	return true
}
