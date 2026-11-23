package agycli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type agyMCPServer struct {
	name    string
	command string
	args    []string
	env     map[string]string
}

// agyParseMCPServers decodes the mcpServers document. v1 supports stdio
// entries (command/args/env) only; http/url entries fail loudly —
// `agy mcp add` takes them positionally, but no caller needs them yet.
func agyParseMCPServers(configJSON string) ([]agyMCPServer, error) {
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Type    string            `json:"type"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(configJSON), &doc); err != nil {
		return nil, fmt.Errorf("agy MCP config not JSON: %w", err)
	}
	if len(doc.MCPServers) == 0 {
		return nil, fmt.Errorf("agy MCP config has no mcpServers")
	}
	var names []string
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]agyMCPServer, 0, len(names))
	for _, name := range names {
		entry := doc.MCPServers[name]
		if strings.TrimSpace(entry.URL) != "" || strings.EqualFold(strings.TrimSpace(entry.Type), "http") {
			return nil, fmt.Errorf("agy MCP server %q is http/url: v1 mounts stdio entries only", name)
		}
		if strings.TrimSpace(entry.Command) == "" {
			return nil, fmt.Errorf("agy MCP server %q has no command", name)
		}
		servers = append(servers, agyMCPServer{name: name, command: entry.Command, args: entry.Args, env: entry.Env})
	}
	return servers, nil
}

func agySanitizeServerName(base string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(base)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 24 {
		name = name[:24]
	}
	if name == "" {
		name = "server"
	}
	return name
}

func agyMountRandomHex() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
