package common

import (
	"context"
	"github.com/manishiitg/mcpagent/mcpclient"
	"sort"
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

// IncludeDefaultVaultTools keeps a project's private tool allowlist while
// making its authorized Vault connections available. The gateway, rather than
// workflow.json, is authoritative for Vault tool grants and argument rules.
func IncludeDefaultVaultTools(tools, scopedServers []string) []string {
	if len(tools) == 0 {
		return tools
	}
	out := append([]string(nil), tools...)
	seen := make(map[string]bool)
	for _, entry := range out {
		seen[entry] = true
	}
	defaults := []string{}
	for _, server := range scopedServers {
		if strings.HasPrefix(server, "vault_") && strings.Contains(server, "__scope_") && !seen[server+":*"] {
			defaults = append(defaults, server+":*")
			seen[server+":*"] = true
		}
	}
	sort.Strings(defaults)
	return append(out, defaults...)
}
