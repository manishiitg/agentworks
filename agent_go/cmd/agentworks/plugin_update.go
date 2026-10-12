package main

import (
	"context"
	"encoding/json"
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
	if onPath("cursor-agent") {
		out = append(out, pluginUpdate{Provider: "cursor", Manual: "nothing for projects that already use the stable hook command; they run the updated CLI. For an older project registration, review the plugin and run python3 /path/to/plugins/cursor/scripts/setup_project.py --project /path/to/project once (skip this if you do not use AgentWorks in Cursor)"})
	}
	if onPath("pi") {
		out = append(out, pluginUpdate{Provider: "pi", Manual: "agentworks attach in a terminal and approve the Pi package, then reload Pi (skip this if you do not use AgentWorks in Pi)"})
	}
	return out
}

const (
	pluginsRepo    = "manishiitg/agentworks-plugins"
	pluginsRepoURL = "https://github.com/manishiitg/agentworks-plugins.git"
	oldPluginsRepo = "manishiitg/agentworks"
)

// onOldPluginsSource reports whether a registered marketplace source is the full
// product repository rather than the small plugins repository.
func onOldPluginsSource(source string) bool {
	source = strings.ToLower(source)
	return strings.Contains(source, oldPluginsRepo) && !strings.Contains(source, "agentworks-plugins")
}

// oldMarketplaceProviders lists the providers whose "agentworks" marketplace still
// points at the full repository (a 400 MB clone) instead of the plugins repository.
// Only the marketplace named agentworks is looked at.
func oldMarketplaceProviders(home string, read func(string) string, museList func() string) map[string]bool {
	old := map[string]bool{}
	var known map[string]struct {
		Source struct {
			Repo string `json:"repo"`
			URL  string `json:"url"`
		} `json:"source"`
	}
	if json.Unmarshal([]byte(read(filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"))), &known) == nil {
		if m, ok := known["agentworks"]; ok && onOldPluginsSource(m.Source.Repo+" "+m.Source.URL) {
			old["claude"] = true
		}
	}
	inSection := false
	for _, line := range strings.Split(read(filepath.Join(home, ".codex", "config.toml")), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inSection = line == "[marketplaces.agentworks]"
			continue
		}
		if inSection && strings.HasPrefix(line, "source ") && onOldPluginsSource(line) {
			old["codex"] = true
		}
	}
	if museList != nil {
		for _, line := range strings.Split(museList(), "\n") {
			if strings.HasPrefix(line, "agentworks\t") && onOldPluginsSource(line) {
				old["muse"] = true
			}
		}
	}
	return old
}

// migrateMarketplace moves one provider's agentworks marketplace to the plugins
// repository, keeping the marketplace name so plugin ids do not change. It returns
// false when it could not, after trying to put the old source back.
func migrateMarketplace(ctx context.Context, provider string) bool {
	run := func(args ...string) bool {
		step, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		return exec.CommandContext(step, provider, args...).Run() == nil
	}
	var remove, add, restore, reinstall []string
	switch provider {
	case "claude":
		remove, add, restore = []string{"plugin", "marketplace", "remove", "agentworks"}, []string{"plugin", "marketplace", "add", pluginsRepo}, []string{"plugin", "marketplace", "add", oldPluginsRepo}
		reinstall = []string{"plugin", "install", "agentworks@agentworks"}
	case "codex":
		remove, add, restore = []string{"plugin", "marketplace", "remove", "agentworks"}, []string{"plugin", "marketplace", "add", pluginsRepo}, []string{"plugin", "marketplace", "add", oldPluginsRepo}
		reinstall = []string{"plugin", "add", "agentworks-codex@agentworks"}
	case "muse":
		remove, add, restore = []string{"plugins", "marketplace", "remove", "agentworks"}, []string{"plugins", "marketplace", "add", "agentworks", pluginsRepoURL}, []string{"plugins", "marketplace", "add", "agentworks", "https://github.com/" + oldPluginsRepo + ".git"}
	default:
		return false
	}
	if !run(remove...) {
		return false
	}
	if !run(add...) {
		run(restore...)
		if reinstall != nil {
			run(reinstall...)
		}
		return false
	}
	if reinstall != nil && !run(reinstall...) {
		return false
	}
	return true
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
	old := oldMarketplaceProviders(home, read, func() string {
		if !onPath("muse") {
			return ""
		}
		out, _ := exec.CommandContext(ctx, "muse", "plugins", "marketplace", "list").Output()
		return string(out)
	})
	for _, update := range installedPluginUpdates(home, onPath, read) {
		if old[update.Provider] {
			if migrateMarketplace(ctx, update.Provider) {
				fmt.Fprintf(o.stderr, "Switched the %s plugin source to the small plugins repository (%s).\n", update.Provider, pluginsRepo)
			} else {
				fmt.Fprintf(o.stderr, "Could not switch the %s plugin source to %s; it keeps using the full repository for now.\n", update.Provider, pluginsRepo)
			}
		}
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
