package mcpagent

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// Every MCP tool is offered to the model as "<alias>__<tool>" (PLAT-519; the
// naming rule itself is mcpclient.ToolNames): the alias is a short, stable name
// for the connection the tool comes from, and platform (direct/virtual) tools
// keep their own names. So an MCP tool can
// never take a platform tool's name, and two connections of the same server
// (one person with two Neon accounts) both stay callable.
//
// The alias comes from the connection's server name, never the whole internal
// key: a personal connection is stored as u<32 hex>__<name> (often with a
// _<hex> connection id on the name), which would eat most of the 64 characters
// a tool name may have. The visible name always translates back to the real
// server and tool through Agent.mcpToolRealNames; the bridge routes
// ($MCP_MCP/<server>/<tool>) and saved server:tool selections keep using the
// real names.

const mcpToolNameSeparator = mcpclient.ToolNameSeparator

var (
	personalServerPrefix = regexp.MustCompile(`^u[0-9a-f]{32}__`)
	connectionIDSuffix   = regexp.MustCompile(`_([0-9a-f]{8,})$`)
	validToolName        = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
)

func shortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// mcpAliasBase is a connection's alias before disambiguation, plus the
// connection id the server name carried (if any), used as the stable suffix
// when two connections share the alias.
func mcpAliasBase(server string) (alias, connectionID string) {
	name := personalServerPrefix.ReplaceAllString(server, "")
	if match := connectionIDSuffix.FindStringSubmatch(name); match != nil && len(name) > len(match[0]) {
		connectionID = match[1]
		name = name[:len(name)-len(match[0])]
	}
	alias = mcpclient.CleanToolNamePart(strings.ToLower(name))
	if len(alias) > mcpclient.MaxToolAliasLength {
		alias = strings.TrimRight(alias[:mcpclient.MaxToolAliasLength], "_-")
	}
	if alias == "" {
		alias = "mcp"
	}
	return alias, connectionID
}

// mcpToolAliases gives each connected server its alias. A server whose host
// declared one (MCPServerConfig.ToolAlias, for a server name that changes with
// its credentials) keeps it as given. Other servers that would share an alias
// get a 4-character suffix from their connection id (or from a hash of the
// server name); if even that collides, 8 characters of the hash.
func mcpToolAliases(servers []string, declared map[string]string) map[string]string {
	sorted := append([]string(nil), servers...)
	sort.Strings(sorted)
	bases := make(map[string]string, len(sorted))
	ids := make(map[string]string, len(sorted))
	count := make(map[string]int)
	for _, server := range sorted {
		if _, done := bases[server]; done {
			continue
		}
		base, id := mcpAliasBase(server)
		if alias := declared[server]; alias != "" {
			base, id = alias, ""
		}
		bases[server], ids[server] = base, id
		count[base]++
	}
	aliases := make(map[string]string, len(bases))
	used := make(map[string]string)
	for _, server := range sorted {
		if _, done := aliases[server]; done {
			continue
		}
		alias := bases[server]
		if count[alias] > 1 && declared[server] == "" {
			suffix := ids[server]
			if suffix == "" {
				suffix = shortHash(server)
			}
			alias = alias + "_" + suffix[:4]
			if other, taken := used[alias]; taken && other != server {
				alias = bases[server] + "_" + shortHash(server)[:8]
			}
		}
		used[alias] = server
		aliases[server] = alias
	}
	return aliases
}

// isValidToolName reports whether a name is accepted by every provider.
func isValidToolName(name string) bool {
	return validToolName.MatchString(name)
}

// realMCPToolName returns the name the MCP server registered for a
// model-facing tool name. A name outside the alias map falls back to the
// older "<server>__<tool>" form, then to itself.
func (a *Agent) realMCPToolName(exposedName, serverName string) string {
	if a != nil {
		if real, ok := a.mcpToolRealNames[exposedName]; ok {
			return real
		}
	}
	if serverName != "" {
		if real, ok := strings.CutPrefix(exposedName, serverName+mcpToolNameSeparator); ok {
			return real
		}
	}
	return exposedName
}

// realToolToServer is the real tool name -> server view for the shared
// code-execution registry, which is addressed by real names. Where two
// servers (or a server and a platform tool) share a real name, the platform
// tool wins and otherwise the first server in name order, so the view is stable.
func (a *Agent) realToolToServer() map[string]string {
	names := make([]string, 0, len(a.toolToServer))
	for name := range a.toolToServer {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make(map[string]string, len(names))
	for _, name := range names {
		server := a.toolToServer[name]
		real := a.realMCPToolName(name, "")
		if existing, ok := result[real]; ok && (existing == "custom" || server != "custom") {
			continue
		}
		result[real] = server
	}
	return result
}
