package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func claudeProfileDir(home string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".claude")
}

type claudeVariantState struct {
	enabled        bool
	installed      bool
	otherInstalled bool
}

func readClaudeVariantState(dir, name string) (state claudeVariantState, err error) {
	state.enabled = true
	other := "agentworks"
	if name == other {
		other = "agentworks-tools"
	}
	var registry struct {
		Plugins map[string][]struct{ Scope string `json:"scope"` } `json:"plugins"`
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	for path, dest := range map[string]any{
		filepath.Join(dir, "plugins", "installed_plugins.json"): &registry,
		filepath.Join(dir, "settings.json"):                    &settings,
	} {
		raw, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return state, e
		}
		if e = json.Unmarshal(raw, dest); e != nil {
			return state, fmt.Errorf("read Claude plugin settings: %w", e)
		}
	}
	for id, entries := range registry.Plugins {
		for _, entry := range entries {
			if entry.Scope != "user" {
				continue
			}
			if id == name+"@agentworks" || id == other+"@agentworks" {
				state.installed = true
			}
			if id == other+"@agentworks" {
				state.otherInstalled = true
			}
		}
	}
	if enabled, selected := settings.EnabledPlugins[name+"@agentworks"]; selected {
		state.enabled = enabled
	} else if enabled, previous := settings.EnabledPlugins[other+"@agentworks"]; previous {
		state.enabled = enabled
	}
	return state, nil
}

// Use Claude's own user-scoped commands. Installing again enables a disabled
// plugin, so restore the person's choice before removing the previous variant.
// Project installations and provider trust remain with Claude/the person.
func setupClaudePluginVariant(ctx context.Context, o *options, name string) error {
	if name != "agentworks" && name != "agentworks-tools" {
		return fmt.Errorf("unknown Claude plugin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	state, err := readClaudeVariantState(claudeProfileDir(home), name)
	if err != nil {
		return err
	}
	steps := [][]string{{"plugin", "marketplace", "add", pluginsRepo, "--scope", "user"}, {"plugin", "install", name + "@agentworks", "--scope", "user"}}
	if !state.enabled {
		steps = append(steps, []string{"plugin", "disable", name + "@agentworks", "--scope", "user"})
	}
	if state.otherInstalled {
		other := "agentworks"
		if name == other {
			other = "agentworks-tools"
		}
		steps = append(steps, []string{"plugin", "uninstall", other + "@agentworks", "--scope", "user"})
	}
	return runPluginSteps(ctx, o, "claude", steps)
}

func updateClaudeTools(ctx context.Context, o *options) bool {
	if !o.setupAll {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	state, err := readClaudeVariantState(claudeProfileDir(home), "agentworks-tools")
	if err == nil && !state.installed {
		return false
	}
	if err == nil {
		err = setupClaudePluginVariant(ctx, o, "agentworks-tools")
	}
	if err != nil {
		fmt.Fprintf(o.stderr, "Could not switch Claude to the tools-only plugin: %v. Run agentworks setup claude to retry.\n", err)
	} else {
		fmt.Fprintln(o.stderr, "Claude uses the tools-only plugin. Restart Claude to stop its previous hooks and load the tools.")
		for _, project := range claudeProjectInstalls() {
			fmt.Fprintf(o.stderr, "A separate project copy still has hooks: cd %s && claude plugin uninstall agentworks@agentworks --scope project\n", shellWord(project))
		}
	}
	return true
}
