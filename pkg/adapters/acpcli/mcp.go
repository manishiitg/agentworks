package acpcli

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
)

type mcpEnv struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// mcpServer is an ACP stdio MCP server entry for session/new and session/load.
type mcpServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []mcpEnv `json:"env"`
}

// parseMCPServers turns the chat's {"mcpServers":{...}} document into ACP
// stdio entries: this is how the agent reaches the platform's tools. Servers
// without a command (http/url entries) are skipped; stdio is what the bridge
// uses and what every ACP agent must support.
func parseMCPServers(owner, configJSON string) ([]mcpServer, error) {
	servers := []mcpServer{}
	if strings.TrimSpace(configJSON) == "" {
		return servers, nil
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(configJSON), &doc); err != nil {
		return nil, fmt.Errorf("ACP MCP config is not JSON: %w", err)
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := doc.MCPServers[name]
		if strings.TrimSpace(entry.Command) == "" {
			log.Printf("[ACP_CLI] session=%s mcp_server=%s action=skipped reason=not_stdio", owner, name)
			continue
		}
		server := mcpServer{Name: name, Command: entry.Command, Args: append([]string{}, entry.Args...), Env: []mcpEnv{}}
		keys := make([]string, 0, len(entry.Env))
		for key := range entry.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			server.Env = append(server.Env, mcpEnv{Name: key, Value: entry.Env[key]})
		}
		servers = append(servers, server)
	}
	return servers, nil
}

// mcpReadPaths are the MCP programs and file arguments a confined agent must
// be able to read to start its MCP servers (the bridge often lives outside
// both the CLI install and the project).
func mcpReadPaths(servers []mcpServer) []string {
	var paths []string
	for _, server := range servers {
		command := server.Command
		if !filepath.IsAbs(command) {
			if resolved, err := exec.LookPath(command); err == nil {
				command = resolved
			}
		}
		if filepath.IsAbs(command) {
			paths = append(paths, command)
			if real, err := filepath.EvalSymlinks(command); err == nil {
				paths = append(paths, real)
			}
		}
		paths = append(paths, clisandbox.ArgFilePaths(server.Args)...)
	}
	return paths
}
