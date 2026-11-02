package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// A tools-only install (agentworks setup claude|codex --no-hooks): the skills and
// the AgentWorks message tools, no hooks, so nothing shows as "blocked" in the
// chat. The person says "connect this chat to AgentWorks"; the app tells
// AgentWorks which chat made each tool call.

// installToolsPlugin installs the tools-only package for one app, for every project.
func installToolsPlugin(ctx context.Context, o *options, provider string) (installed, remove string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	var steps [][]string
	switch provider {
	case "claude":
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks-plugins", "--scope", "user"}, {"plugin", "install", "agentworks-tools@agentworks", "--scope", "user"}}
		installed, remove = "the AgentWorks tools", "claude plugin uninstall agentworks-tools@agentworks --scope user"
		if hooksPluginInstalled("claude") {
			newSetupStyle(o.stdout).Say("Note: the full AgentWorks plugin (with hooks) is also installed. Remove it so the hooks stop: claude plugin uninstall agentworks@agentworks --scope user")
		}
	case "codex":
		if e := codexHomeError(); e != nil {
			return "", "", e
		}
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks-plugins"}, {"plugin", "add", "agentworks-codex-tools@agentworks"}}
		installed, remove = "the AgentWorks tools", "remove AgentWorks (tools only) in Codex Settings > Plugins, then restart Codex"
		if raw, e := os.ReadFile(filepath.Join(codexHomeDir(home), "config.toml")); e == nil && strings.Contains(string(raw), "agentworks-codex@agentworks") && runtime.GOOS != "windows" {
			newSetupStyle(o.stdout).Say("Note: the full AgentWorks plugin (with hooks) is also installed. Remove that copy in Codex Settings > Plugins so its hooks stop.")
		}
	default:
		return "", "", errors.New("--no-hooks is available for Claude Code and the Codex app")
	}
	if provider == "codex" {
		err = setupCodexPluginVariant(ctx, o, "agentworks-codex-tools")
	} else {
		err = runPluginSteps(ctx, o, provider, steps)
	}
	return installed, remove, err
}

// hooksPluginInstalled reports whether the full AgentWorks plugin (with hooks) is already installed for the app.
func hooksPluginInstalled(provider string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	switch provider {
	case "claude":
		raw, e := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
		if e != nil || !strings.Contains(string(raw), "agentworks@agentworks") {
			return false
		}
		// A cached/registered full package can be disabled while tools-only is
		// enabled. Setup must not silently reactivate its command hooks.
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		raw, e = os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
		return e == nil && json.Unmarshal(raw, &settings) == nil && settings.EnabledPlugins["agentworks@agentworks"]
	case "codex":
		raw, e := os.ReadFile(filepath.Join(codexHomeDir(home), "config.toml"))
		return e == nil && strings.Contains(string(raw), "agentworks-codex@agentworks")
	}
	return false
}
