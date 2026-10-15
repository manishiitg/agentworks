package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A tools-only install (agentworks setup claude|codex --no-hooks): the skills and
// the AgentWorks message tools, no hooks, so nothing shows as "blocked" in the
// chat. The person says "connect this chat to AgentWorks"; the app tells
// AgentWorks which chat made each tool call.

const noHooksGivesUp = "Without hooks you give up: delivery at the end of a turn if the live connection drops, automatic reconnect after a restart (say \"connect this chat to AgentWorks\" once), and the typed /agentworks shortcuts. Joining another network (--also) is still your own request: ask the agent, which runs agentworks attach --also LOGIN in the chat."

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
		if raw, e := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json")); e == nil && strings.Contains(string(raw), "agentworks@agentworks") {
			fmt.Fprintln(o.stdout, "Note: the full AgentWorks plugin (with hooks) is also installed. Remove it so the hooks stop: claude plugin uninstall agentworks@agentworks --scope user")
		}
	case "codex":
		if e := codexHomeError(); e != nil {
			return "", "", e
		}
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks-plugins"}, {"plugin", "add", "agentworks-codex-tools@agentworks"}}
		installed, remove = "the AgentWorks tools plugin (agentworks-codex-tools@agentworks, no hooks) in your Codex profile (~/.codex); it serves the Codex app (the Codex command line keeps the version with hooks)", "codex plugin remove agentworks-codex-tools@agentworks"
		if raw, e := os.ReadFile(filepath.Join(codexHomeDir(home), "config.toml")); e == nil && strings.Contains(string(raw), "agentworks-codex@agentworks") {
			fmt.Fprintln(o.stdout, "Note: the full AgentWorks plugin (with hooks) is also installed. Remove it so the hooks stop: codex plugin remove agentworks-codex@agentworks")
		}
	default:
		return "", "", errors.New("--no-hooks is available for Claude Code and the Codex app")
	}
	for _, args := range steps {
		c := exec.CommandContext(ctx, providerExecutable(provider), args...)
		out, e := c.CombinedOutput()
		if e != nil && !strings.Contains(strings.ToLower(string(out)), "already") {
			return "", "", fmt.Errorf("%s plugin setup failed: %s", provider, strings.TrimSpace(string(out)))
		}
		fmt.Fprint(o.stderr, string(out))
	}
	return installed, remove, nil
}
