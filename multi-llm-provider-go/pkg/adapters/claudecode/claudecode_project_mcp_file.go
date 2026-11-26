package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// writeClaudeCodeProjectMCPFile is the counterpart to
// writeClaudeCodeProjectInstructionFile: when MetadataKeyWriteProjectInstructionFile
// is set AND MetadataKeyMCPConfig carries a JSON document, also drop the
// MCP server list at <workingDir>/.mcp.json (Claude Code's project-scoped
// MCP convention). removeFiles deletes it on cleanup.
//
// Returns the absolute path (so the caller can append it to tempFiles
// for the existing removeFiles cleanup flow) plus a nil error. An empty
// workingDir is treated as a no-op (returns ""). Errors are reserved for
// the actual write — empty inputs short-circuit.
//
// Risk caveat: .mcp.json is a single-file convention; an operator's
// pre-existing .mcp.json is overwritten and removed. The projection stays
// behind MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS.
func writeClaudeCodeProjectMCPFile(workingDir, mcpJSON string, accountHome ...string) (string, error) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return "", nil
	}
	if strings.TrimSpace(mcpJSON) == "" {
		return "", nil
	}

	path := filepath.Join(workingDir, ".mcp.json")

	if err := os.WriteFile(path, []byte(mcpJSON), 0o600); err != nil {
		return "", fmt.Errorf("write .mcp.json: %w", err)
	}

	// Pre-approve the MCP server names in ~/.claude.json projects entry
	// so Claude Code's "New MCP server found in .mcp.json — approve?"
	// startup prompt does not appear. Without this pre-approval, the
	// tmux adapter cannot dismiss the prompt and the session times out.
	// Errors are silently ignored — the worst case is the operator
	// gets prompted and the session times out, which is no worse than
	// not doing this at all.
	preApproveClaudeMCPServersForWorkingDir(workingDir, mcpJSON, accountHome...)

	return path, nil
}

// preApproveClaudeMCPServersForWorkingDir parses the operator-supplied
// MCP JSON to extract server names, then ensures each is listed in
// ~/.claude.json's projects.<workingDir>.enabledMcpjsonServers array.
// This is the same pre-approval mechanism Claude Code uses internally
// when the operator clicks "Use this MCP server" — we just bypass the
// interactive prompt by recording the consent up front.
//
// The function follows the same path-resolution conventions as
// preTrustClaudeWorkingDir (records under raw AND symlink-resolved
// paths so macOS /var → /private/var aliases match).
func preApproveClaudeMCPServersForWorkingDir(workingDir, mcpJSON string, accountHome ...string) {
	serverNames := extractClaudeMCPServerNames(mcpJSON)
	if len(serverNames) == 0 {
		return
	}
	// Parse the raw mcpServers map so we can ALSO install it under
	// projects.<dir>.mcpServers (which Claude Code uses to decide
	// what's "new" vs "known"). Failure to parse is non-fatal: we
	// still write enabledMcpjsonServers, which is what we depend on
	// most.
	var docForServers struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	_ = json.Unmarshal([]byte(mcpJSON), &docForServers)
	parsedMCPServers := map[string]any{}
	for name, raw := range docForServers.MCPServers {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			parsedMCPServers[name] = v
		}
	}

	paths := pathidentity.Candidates(workingDir)

	configPath, err := claudeUserConfigPath(accountHome...)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return
	}

	preTrustClaudeMu.Lock()
	defer preTrustClaudeMu.Unlock()

	raw, readErr := os.ReadFile(configPath)
	var config map[string]any
	if readErr == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &config)
	}
	if config == nil {
		config = map[string]any{}
	}

	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	for _, p := range paths {
		entry, _ := projects[p].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		existing, _ := entry["enabledMcpjsonServers"].([]any)
		seen := make(map[string]bool, len(existing))
		for _, v := range existing {
			if s, ok := v.(string); ok {
				seen[s] = true
			}
		}
		merged := make([]any, 0, len(existing)+len(serverNames))
		merged = append(merged, existing...)
		for _, name := range serverNames {
			if !seen[name] {
				merged = append(merged, name)
				seen[name] = true
			}
		}
		entry["enabledMcpjsonServers"] = merged
		// Also populate the per-project mcpServers map with the actual
		// server configs. Claude Code uses the diff between
		// projects.<dir>.mcpServers and the workspace .mcp.json to
		// decide whether the server is "new" and trigger the discovery
		// prompt. By installing the same config under both, the prompt
		// is suppressed because there's no diff.
		entry["mcpServers"] = parsedMCPServers
		projects[p] = entry
	}
	config["projects"] = projects

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(configPath, out, 0o600)
}

// extractClaudeMCPServerNames returns the top-level keys of the
// "mcpServers" object in the supplied JSON, or an empty slice if the
// JSON is malformed or lacks the expected shape. Server names are
// returned in stable iteration order is not guaranteed; callers should
// not depend on ordering.
func extractClaudeMCPServerNames(mcpJSON string) []string {
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(mcpJSON), &doc); err != nil {
		return nil
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	return names
}
