package cliruntime

import (
	"os"
	"path/filepath"
	"strings"
)

// MCPBridgeBinary uses the operator's override or the bridge shipped with this
// server. Empty keeps mcpagent's PATH and ~/go/bin fallback. No agent-controlled
// working directory participates in executable discovery.
func MCPBridgeBinary() string {
	if configured := strings.TrimSpace(os.Getenv("MCP_BRIDGE_BINARY")); configured != "" {
		return configured
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return bundledMCPBridge(executable)
}

func bundledMCPBridge(executable string) string {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	directory := filepath.Dir(executable)
	for _, candidate := range []string{filepath.Join(directory, "mcpbridge"), filepath.Join(directory, ".bin", "mcpbridge")} {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate
		}
	}
	return ""
}
