package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// Only a CLI physically shipped inside the app is owned by the app updater.
func desktopBundle(executable string) string {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return ""
	}
	const suffix = "/Contents/Resources/bin/agentworks"
	if !strings.HasSuffix(filepath.ToSlash(resolved), suffix) {
		return ""
	}
	bundle := strings.TrimSuffix(resolved, filepath.FromSlash(suffix))
	if filepath.Ext(bundle) != ".app" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(bundle, "Contents", "Info.plist")); err != nil {
		return ""
	}
	return bundle
}

func bundledDesktop() string {
	executable, _ := os.Executable()
	return desktopBundle(executable)
}

func openDesktopCommand() *cobra.Command {
	return &cobra.Command{Use: "open", Short: "Open the AgentWorks Mac app", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS != "darwin" {
				return fmt.Errorf("the desktop app currently supports macOS; use your AgentWorks website on this computer")
			}
			bundle := bundledDesktop()
			if bundle == "" {
				home, _ := os.UserHomeDir()
				for _, candidate := range []string{"/Applications/AgentWorks.app", filepath.Join(home, "Applications", "AgentWorks.app")} {
					if info, err := os.Stat(candidate); err == nil && info.IsDir() {
						bundle = candidate
						break
					}
				}
			}
			if bundle == "" {
				return fmt.Errorf("install the AgentWorks Mac app first; for source development use ./run_agentworks in your checkout")
			}
			return exec.CommandContext(cmd.Context(), "/usr/bin/open", bundle).Run()
		},
	}
}
