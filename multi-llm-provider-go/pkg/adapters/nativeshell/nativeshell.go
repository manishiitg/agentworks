// Package nativeshell holds the one switch that decides whether a coding CLI
// keeps its own built-in shell in Full mode.
//
// Owner decision 2026-10-05 (PLAT-491) turned the CLI's built-in shell off by
// default because it runs as the app account with the platform's environment.
// Owner decision 2026-10-15 (PLAT-861-202): it is ON by default everywhere;
// AGENTWORKS_CLI_NATIVE_SHELL=off turns it off for a deployment that needs to.
package nativeshell

import (
	"os"
	"strings"
)

// EnvVar switches the CLI's built-in shell in Full mode: off/0/false/no turns
// it off; anything else (including unset) leaves it on.
const EnvVar = "AGENTWORKS_CLI_NATIVE_SHELL"

// Enabled reports whether the CLI keeps its built-in shell. The default is true.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvVar))) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}
