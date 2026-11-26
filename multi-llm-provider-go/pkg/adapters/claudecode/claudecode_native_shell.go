package claudecode

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

// claudeNativeShellTools are Claude's own tools that run a command: the shell,
// its background-shell helpers and the monitor that runs a command. In Full
// mode (PLAT-491) they are removed from --tools unless the escape hatch
// AGENTWORKS_CLI_NATIVE_SHELL is on; the bridge shell (execute_shell_command)
// is an MCP tool and stays. Task/TaskOutput/TaskStop only manage background
// work and are not command runners.
var claudeNativeShellTools = []string{"Bash", "PowerShell", "Monitor", "BashOutput", "KillShell", "KillBash"}

// claudeEffectiveTools returns the --tools value to pass: the caller's list
// without the native shell tools unless nativeshell.Enabled(). A list with no
// shell tool (mcp_only, hybrid) is returned unchanged.
func claudeEffectiveTools(tools string) string {
	if nativeshell.Enabled() || strings.TrimSpace(tools) == "" {
		return tools
	}
	denied := map[string]bool{}
	for _, name := range claudeNativeShellTools {
		denied[name] = true
	}
	parts := strings.Split(tools, ",")
	kept := parts[:0:0]
	for _, part := range parts {
		if denied[strings.TrimSpace(part)] {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ",")
}

// claudeNativeShellDisallowed is the --disallowedTools value for a "default"
// (all built-in tools) list, which cannot be filtered by name; "" otherwise.
func claudeNativeShellDisallowed(tools string) string {
	if nativeshell.Enabled() || strings.TrimSpace(tools) != "default" {
		return ""
	}
	return strings.Join(claudeNativeShellTools, ",")
}
