package common

import (
	"context"
	"github.com/manishiitg/mcpagent/mcpclient"
	"strings"
)

// Installed by the host before serving. Agent constructors use one boundary
// for chat, workflow steps, schedules and their subagents.
var ScopeAgentMCP func(context.Context, string, []string, mcpclient.RuntimeOverrides) ([]string, mcpclient.RuntimeOverrides, map[string]string, error)

// RemapMCPToolSelection preserves tool restrictions when connection names are
// isolated per user in the process-wide MCP pool. Builtin categories stay intact.
func RemapMCPToolSelection(tools []string, aliases map[string]string) []string {
	out := make([]string, 0, len(tools))
	for _, entry := range tools {
		server, tool, ok := strings.Cut(entry, ":")
		if target, found := aliases[strings.ToLower(server)]; ok && found {
			entry = target + ":" + tool
		}
		out = append(out, entry)
	}
	return out
}
