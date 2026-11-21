package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// setupStatusCommand is the machine-readable check behind the Mac app's Connect Claude Code / Codex action: which of
// the two apps are installed and which already have the full AgentWorks plugin (hooks). It changes nothing.
func setupStatusCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "status", Hidden: true, Args: cobra.NoArgs, Short: "Report which coding apps are installed and connected (JSON)", RunE: func(cmd *cobra.Command, _ []string) error {
		type app struct {
			Provider  string `json:"provider"`
			Name      string `json:"name"`
			Installed bool   `json:"installed"`
			Hooks     bool   `json:"hooks"`
		}
		apps := []app{}
		for _, a := range []struct{ provider, name string }{{"claude", "Claude Code"}, {"codex", "Codex"}} {
			apps = append(apps, app{Provider: a.provider, Name: a.name, Installed: codingAppInstalled(a.provider), Hooks: hooksPluginInstalled(a.provider)})
		}
		return json.NewEncoder(o.stdout).Encode(map[string]any{"apps": apps})
	}}
}
