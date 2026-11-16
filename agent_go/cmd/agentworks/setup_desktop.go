package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type desktopSetupApp struct {
	Provider string `json:"provider"`
	Outcome  string `json:"outcome"`
}

// The packaged Mac app uses the same setup implementation as Connect. Never
// trust provider hooks, log in, or select a native session on the person's behalf.
func setupDesktopCommand(o *options) *cobra.Command {
	var disable bool
	cmd := &cobra.Command{Use: "desktop", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		o.setupBatch = true
		if runtime.GOOS != "darwin" {
			return json.NewEncoder(o.stdout).Encode(map[string]any{"apps": []desktopSetupApp{}})
		}
		apps := setupDesktopApps(cmd.Context(), disable, codingAppInstalled, hooksPluginInstalled, func(ctx context.Context, provider string, hooks bool) error {
			child := *o
			child.stdout, child.stderr, child.stdin = io.Discard, io.Discard, strings.NewReader("")
			child.setupBatch = true
			if !hooks {
				return removeDesktopHooks(ctx, &child, provider)
			}
			args := []string{provider, "--hooks"}
			setup := setupCommand(&child)
			setup.SetArgs(args)
			return setup.ExecuteContext(ctx)
		})
		return json.NewEncoder(o.stdout).Encode(map[string]any{"apps": apps})
	}}
	cmd.Flags().BoolVar(&disable, "disable", false, "Remove native hooks, keeping the tools and provider trust choices")
	return cmd
}

func setupDesktopApps(ctx context.Context, disable bool, installed, hooked func(string) bool, setup func(context.Context, string, bool) error) []desktopSetupApp {
	apps := []desktopSetupApp{}
	for _, provider := range []string{"claude", "codex"} {
		outcome := "not_installed"
		if installed(provider) || (disable && hooked(provider)) {
			run, cancel := context.WithTimeout(ctx, 3*time.Minute)
			err := setup(run, provider, !disable)
			cancel()
			outcome = "installed"
			if disable {
				outcome = "removed"
			}
			if err != nil {
				outcome = "failed"
			}
		}
		apps = append(apps, desktopSetupApp{Provider: provider, Outcome: outcome})
	}
	return apps
}

// Turn off needs no network download and removes only our full hook package.
// Provider trust, other plugins, tools-only installs and current chats stay alone.
func removeDesktopHooks(ctx context.Context, o *options, provider string) error {
	if provider == "claude" {
		if !hooksPluginInstalled(provider) {
			return nil
		}
		return runPluginSteps(ctx, o, provider, [][]string{{"plugin", "uninstall", "agentworks@agentworks", "--scope", "user"}})
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err = codexHomeError(); err != nil {
		return err
	}
	return removeCodexPluginVariant(codexHomeDir(home), o, "agentworks-codex")
}
