package common

import (
	"context"
	"sort"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// Installed by the host before serving. Agent constructors use one boundary
// for chat, workflow steps, schedules and their subagents.
var ScopeAgentMCP func(context.Context, string, []string, mcpclient.RuntimeOverrides) ([]string, mcpclient.RuntimeOverrides, map[string]string, error)

// AppendAgentMCPToolSources excludes host-owned builtin categories and keeps an
// empty scoped selection from becoming "all servers" in the SDK.
func AppendAgentMCPToolSources(sources []mcpagent.MCPToolSource, servers []string) []mcpagent.MCPToolSource {
	for _, server := range servers {
		if name := strings.TrimSpace(server); name != "" && (ScopeAgentMCP == nil || !IsBuiltinToolCategory(server)) {
			sources = append(sources, mcpagent.MCPToolSource{Name: name})
		}
	}
	if ScopeAgentMCP != nil && len(sources) == 0 {
		sources = []mcpagent.MCPToolSource{{Name: mcpclient.NoServers}}
	}
	return sources
}

// RemapMCPToolSelection preserves tool restrictions when connection names are
// isolated per user in the process-wide MCP pool. Builtin categories stay intact.
func RemapMCPToolSelection(tools []string, aliases map[string]string) []string {
	out := make([]string, 0, len(tools))
	for _, entry := range tools {
		if target, found := aliases[strings.ToLower(entry)]; found {
			out = append(out, target)
			continue
		}
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
	explicitServer := map[string]bool{}
	for _, entry := range out {
		seen[entry] = true
		server, _, ok := strings.Cut(entry, ":")
		if ok {
			explicitServer[server] = true
		}
	}
	defaults := []string{}
	for _, server := range scopedServers {
		// srv_* are the Mac's company-server Vault connections, governed the same way (cmd/server/server_vault_remote.go).
		if (strings.HasPrefix(server, "vault_") || strings.HasPrefix(server, "srv_")) && strings.Contains(server, "__scope_") && !seen[server+":*"] && !explicitServer[server] {
			defaults = append(defaults, server+":*")
			seen[server+":*"] = true
		}
	}
	sort.Strings(defaults)
	return append(out, defaults...)
}
