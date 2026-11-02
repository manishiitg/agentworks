package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// A tools-only install (agentworks setup claude|codex --no-hooks): the skills and
// the AgentWorks message tools, no hooks, so nothing shows as "blocked" in the
// chat. The person says "connect this chat to AgentWorks"; the app tells
// AgentWorks which chat made each tool call.

const noHooksGivesUp = "For the typed shortcuts and automatic reconnect, or if you use the Codex command line, run the same command with --hooks. Without hooks you give up: delivery at the end of a turn if the live connection drops, automatic reconnect after a restart (say \"connect this chat to AgentWorks\" once), and the typed /agentworks shortcuts. Joining another network (--also) is still your own request: ask the agent, which runs agentworks attach --also LOGIN in the chat."

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
		installed, remove = "the AgentWorks tools plugin (agentworks-tools@agentworks, no hooks) for your user in Claude Code (~/.claude)", "claude plugin uninstall agentworks-tools@agentworks --scope user"
	case "codex":
		if e := codexHomeError(); e != nil {
			return "", "", e
		}
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks-plugins"}, {"plugin", "add", "agentworks-codex-tools@agentworks"}}
		installed, remove = "the AgentWorks tools plugin (agentworks-codex-tools@agentworks, no hooks) in your Codex profile (~/.codex); it serves the Codex app (the Codex command line keeps the version with hooks)", "remove AgentWorks (tools only) in Codex Settings > Plugins, then restart Codex"
	default:
		return "", "", errors.New("--no-hooks is available for Claude Code and the Codex app")
	}
	if provider == "codex" {
		err = setupCodexPluginVariant(ctx, o, "agentworks-codex-tools")
	} else {
		err = setupClaudePluginVariant(ctx, o, "agentworks-tools")
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
