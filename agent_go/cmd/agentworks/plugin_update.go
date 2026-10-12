package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// pluginUpdate is one coding CLI's AgentWorks plugin refresh after the CLI itself
// was updated. Auto commands are the provider's own documented update commands;
// they never approve trust or attach a chat. Manual is the one line the person
// runs when the app cannot be updated from the command line.
type pluginUpdate struct {
	Provider string
	Auto     [][]string
	Manual   string
}

// installedPluginUpdates decides what to refresh from what is installed under
// home and which provider commands exist.
func installedPluginUpdates(home string, onPath func(string) bool, read func(string) string) []pluginUpdate {
	var out []pluginUpdate
	if onPath("claude") && strings.Contains(read(filepath.Join(home, ".claude", "plugins", "installed_plugins.json")), "agentworks@agentworks") {
		out = append(out, pluginUpdate{Provider: "claude", Auto: [][]string{{"plugin", "marketplace", "update", "agentworks"}, {"plugin", "update", "agentworks@agentworks"}},
			Manual: "claude plugin marketplace update agentworks && claude plugin update agentworks@agentworks, then reload Claude Code"})
	}
	if onPath("codex") && (strings.Contains(read(filepath.Join(home, ".codex", "config.toml")), "agentworks-codex@agentworks") || strings.Contains(read(filepath.Join(home, ".codex", "plugins", "installed_plugins.json")), "agentworks-codex")) {
		out = append(out, pluginUpdate{Provider: "codex", Auto: [][]string{{"plugin", "marketplace", "upgrade", "agentworks"}, {"plugin", "add", "agentworks-codex@agentworks"}},
			Manual: "codex plugin marketplace upgrade agentworks && codex plugin add agentworks-codex@agentworks, then reload Codex"})
	}
	if onPath("muse") {
		out = append(out, pluginUpdate{Provider: "muse", Manual: "muse plugins marketplace update agentworks && muse plugins remove agentworks-muse && muse plugins install agentworks-muse@agentworks, then approve it and restart Muse (skip this if you do not use the AgentWorks plugin in Muse)"})
	}
	if onPath("pi") {
		out = append(out, pluginUpdate{Provider: "pi", Manual: "agentworks attach in a terminal and approve the Pi package, then reload Pi (skip this if you do not use AgentWorks in Pi)"})
	}
	return out
}

// updateInstalledPlugins runs the safe refresh commands and prints one plain
// line per provider that still needs a step from the person.
func updateInstalledPlugins(ctx context.Context, o *options) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	onPath := func(name string) bool { _, e := exec.LookPath(name); return e == nil }
	read := func(path string) string { b, _ := os.ReadFile(path); return string(b) }
	for _, update := range installedPluginUpdates(home, onPath, read) {
		done := len(update.Auto) > 0
		for _, args := range update.Auto {
			step, cancel := context.WithTimeout(ctx, 90*time.Second)
			result, e := exec.CommandContext(step, update.Provider, args...).CombinedOutput()
			cancel()
			if e != nil && !strings.Contains(strings.ToLower(string(result)), "already") {
				done = false
				break
			}
		}
		if done {
			fmt.Fprintf(o.stderr, "Updated the %s plugin. Reload %s to use it.\n", update.Provider, update.Provider)
			continue
		}
		fmt.Fprintf(o.stderr, "To update the %s plugin, run: %s\n", update.Provider, update.Manual)
	}
}
