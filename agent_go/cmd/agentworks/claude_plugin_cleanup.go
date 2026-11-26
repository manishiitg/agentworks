package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

const claudeFullPlugin = "agentworks@agentworks"
const claudeToolsPlugin = "agentworks-tools@agentworks"

type claudePluginCleanup struct {
	Removed string `json:"removed"`
	Kept    string `json:"kept"`
	Scope   string `json:"scope"`
	Outcome string `json:"outcome"`
}

type claudePluginScope struct {
	Scope       string `json:"scope"`
	ProjectPath string `json:"projectPath"`
}

// Setup has already installed the requested variant. Refresh every existing
// scope, then retire its other variant using Claude's own commands. Never edit
// approval/settings JSON, approve hooks, or touch another marketplace's plugins.
func finishClaudePluginSetup(ctx context.Context, o *options, home, wanted, scope string) error {
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return fmt.Errorf("cannot check Claude plugin copies: %w", err)
	}
	run := func(dir string, args ...string) error {
		cmd := exec.CommandContext(ctx, providerExecutable("claude"), args...)
		cmd.Dir, cmd.Stdin = dir, o.stdin
		out, err := cmd.CombinedOutput()
		if err != nil {
			return pluginSetupError("claude", err, out)
		}
		return nil
	}
	return reconcileClaudePlugins(raw, wanted, scope, func(entry claudePluginScope) (string, error) {
		path := filepath.Join(home, ".claude", "settings.json")
		if entry.Scope != "user" {
			path = filepath.Join(entry.ProjectPath, ".claude", "settings.json")
		}
		if entry.Scope == "local" {
			path = filepath.Join(entry.ProjectPath, ".claude", "settings.local.json")
		}
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", err
		}
		if settings.EnabledPlugins[claudeToolsPlugin] && !settings.EnabledPlugins[claudeFullPlugin] {
			return claudeToolsPlugin, nil
		}
		return "", nil
	}, run, func(r claudePluginCleanup) {
		o.pluginCleanup = append(o.pluginCleanup, r)
		fmt.Fprintf(o.stderr, "[NATIVE_CONNECT] event=plugin_cleanup removed=%s kept=%s scope=%s outcome=%s\n", r.Removed, r.Kept, r.Scope, r.Outcome)
	})
}

func reconcileClaudePlugins(raw []byte, wanted, targetScope string, preferred func(claudePluginScope) (string, error), run func(string, ...string) error, report func(claudePluginCleanup)) error {
	var installed struct {
		Plugins map[string][]claudePluginScope `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &installed); err != nil {
		return fmt.Errorf("cannot check Claude plugin copies: %w", err)
	}
	groups := map[claudePluginScope]map[string]bool{}
	for _, id := range []string{claudeFullPlugin, claudeToolsPlugin} {
		for _, entry := range installed.Plugins[id] {
			if entry.Scope == "user" {
				entry.ProjectPath = ""
			} else if (entry.Scope != "project" && entry.Scope != "local") || !filepath.IsAbs(entry.ProjectPath) {
				continue
			}
			if groups[entry] == nil {
				groups[entry] = map[string]bool{}
			}
			groups[entry][id] = true
		}
	}
	// A successful provider install must appear in the registry before removing
	// any previous variant. An incomplete install keeps the old tools usable.
	found := false
	for entry, ids := range groups {
		if entry.Scope == targetScope && ids[wanted] {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("Claude did not record its new AgentWorks plugin; kept the previous copy")
	}
	entries := make([]claudePluginScope, 0, len(groups))
	for entry := range groups {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Scope == entries[j].Scope {
			return entries[i].ProjectPath < entries[j].ProjectPath
		}
		return entries[i].Scope < entries[j].Scope
	})
	if err := run("", "plugin", "marketplace", "update", "agentworks"); err != nil {
		return err
	}
	for _, entry := range entries {
		ids := groups[entry]
		keep := claudeToolsPlugin
		if ids[claudeFullPlugin] {
			keep = claudeFullPlugin
		}
		if preferred != nil {
			choice, err := preferred(entry)
			if err != nil {
				return fmt.Errorf("cannot check Claude plugin choice: %w", err)
			}
			if ids[choice] {
				keep = choice
			}
		}
		if entry.Scope == targetScope && ids[wanted] {
			keep = wanted
		}
		receipt := claudePluginCleanup{Removed: "none", Kept: keep, Scope: entry.Scope, Outcome: "kept"}
		if err := run(entry.ProjectPath, "plugin", "update", keep, "--scope", entry.Scope); err != nil {
			receipt.Outcome = "failed"
			report(receipt)
			return err
		}
		other := claudeFullPlugin
		if keep == claudeFullPlugin {
			other = claudeToolsPlugin
		}
		if ids[other] {
			// Keep the plugin's data. No direct JSON/cache deletion or trust changes.
			if err := run(entry.ProjectPath, "plugin", "uninstall", other, "--scope", entry.Scope, "--keep-data"); err != nil {
				receipt.Outcome = "failed"
				report(receipt)
				return err
			}
			receipt.Removed, receipt.Outcome = other, "removed"
		}
		report(receipt)
	}
	return nil
}
