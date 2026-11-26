package picli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// piSessionRuntimeDirs returns Pi's private agent and transcript directories
// for one native session. In AgentWorks, workingDir is already scoped by user,
// workflow and conversation; including the native Pi session keeps separate Pi
// processes isolated even if a caller intentionally reuses a workspace.
func piSessionRuntimeDirs(workingDir, nativeSessionID string) (agentDir, sessionDir string) {
	base := piRuntimeBaseDir(workingDir)
	sum := sha256.Sum256([]byte(strings.TrimSpace(nativeSessionID)))
	scope := "session-" + hex.EncodeToString(sum[:12])
	agentDir = filepath.Join(base, ".pi", "agentworks", scope, "agent")
	return agentDir, filepath.Join(agentDir, "sessions")
}

// piRuntimeBaseDir resolves the managed session workspace.
func piRuntimeBaseDir(workingDir string) string {
	base := strings.TrimSpace(workingDir)
	if base == "" {
		base = filepath.Join(os.TempDir(), "multi-llm-provider-go", "pi")
	}
	return base
}

// preparePiNativeMCPConfig writes the complete native MCP snapshot to a private
// agent directory. Managed launches use --approve (owner decision), so Pi trusts
// a project .pi/mcp.json; only a platform-written legacy one is removed below.
// Cleanup removes the credential-bearing snapshot.
func preparePiNativeMCPConfig(workingDir, nativeSessionID string, opts *llmtypes.CallOptions, custom *PiCustomProvider) (agentDir, sessionDir string, cleanup func(), err error) {
	agentDir, sessionDir = piSessionRuntimeDirs(workingDir, nativeSessionID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return "", "", nil, fmt.Errorf("failed to create Pi session runtime dir %s: %w", sessionDir, err)
	}
	removeStalePiProjectMCPConfig(workingDir)
	linkSharedPiExtensionCache(agentDir, opts != nil && opts.CLISecurity.LandlockEnforced())
	// The deployment's own Pi providers/models (PI_CLI_AGENT_TEMPLATE_DIR) and
	// the account's own endpoint (custom): a session agent dir otherwise knows
	// only Pi's built-in providers.
	if err := stagePiAgentTemplate(agentDir, custom); err != nil {
		return "", "", nil, err
	}
	// Remove the retired plugin config and its credentials.
	legacyPath := filepath.Join(agentDir, "mcp-adapter.json")
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return "", "", nil, fmt.Errorf("failed to remove legacy Pi MCP config %s: %w", legacyPath, err)
	}

	mcpConfig := strings.TrimSpace(piMCPConfigFromOptions(opts))
	if mcpConfig == "" {
		return agentDir, sessionDir, nil, nil
	}
	normalized, err := normalizePiMCPConfig(mcpConfig)
	if err != nil {
		return "", "", nil, err
	}
	cleanup = func() {
		_ = os.Remove(piNativeMCPConfigPath(agentDir))
	}
	mcpPath := piNativeMCPConfigPath(agentDir)
	if err := writePiPrivateFileAtomically(mcpPath, normalized); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("failed to write native Pi MCP config %s: %w", mcpPath, err)
	}
	return agentDir, sessionDir, cleanup, nil
}

// removeStalePiProjectMCPConfig deletes a legacy platform config at
// <workingDir>/.pi/mcp.json. The pre-session-scoping adapter wrote the bridge
// config there, and persistent runtime directories retain it across restarts.
// Older pi-mcp-adapter releases merged that project override after the session
// config, shadowing the fresh token. The current adapter ignores it and warns
// about it. Only files fingerprinting as platform-written are removed; foreign
// or unparseable files are left alone.
// Best-effort: the session config write below is the essential step.
func removeStalePiProjectMCPConfig(workingDir string) {
	path := filepath.Join(piRuntimeBaseDir(workingDir), ".pi", "mcp.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if !isPlatformPiProjectMCPConfig(raw) {
		return
	}
	_ = os.Remove(path)
}

// isPlatformPiProjectMCPConfig reports whether raw is a platform-written pi
// project config: {"mcpServers": {"api-bridge": {"env": {"MCP_API_TOKEN":
// ...}}}}. api-bridge + MCP_API_TOKEN is our invented pairing, so a match is
// never a user file. Anything else fails closed to "foreign, preserve".
func isPlatformPiProjectMCPConfig(raw []byte) bool {
	var decoded struct {
		McpServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	bridge, ok := decoded.McpServers["api-bridge"]
	if !ok {
		return false
	}
	token, ok := bridge.Env["MCP_API_TOKEN"]
	return ok && strings.TrimSpace(token) != ""
}

func writePiPrivateFileAtomically(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func piNativeMCPConfigPath(agentDir string) string {
	return filepath.Join(agentDir, "mcp.json")
}

func piNativeMCPArgs(agentDir, sessionDir string) []string {
	return []string{"--session-dir", sessionDir}
}

func piNativeMCPEnv(agentDir, sessionDir string) []string {
	return []string{
		"PI_CODING_AGENT_DIR=" + agentDir,
		"PI_CODING_AGENT_SESSION_DIR=" + sessionDir,
	}
}

// Keep npm extension installation shared while isolating every source of
// configuration, auth and transcripts. Extension discovery is disabled, so
// sharing this code cache does not load ambient extension declarations.
//
// A confined Pi does not share it: the cache is executable code that every user's Pi on the
// server would load, so a writable shared copy would let one user's Pi plant code in another's;
// and the confined Pi cannot write there anyway (npm's rename failed with EACCES, Confida
// 2026-09-30). It gets its own folder, and npm installs into that. A link left by an earlier,
// unconfined launch is replaced.
func linkSharedPiExtensionCache(agentDir string, confined bool) {
	if confined {
		target := filepath.Join(agentDir, "tmp", "extensions")
		if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(target)
		}
		_ = os.MkdirAll(target, 0o700)
		return
	}
	sharedAgentDir := piAmbientAgentDir()
	if sharedAgentDir == "" || filepath.Clean(sharedAgentDir) == filepath.Clean(agentDir) {
		return
	}
	source := filepath.Join(sharedAgentDir, "tmp", "extensions")
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		return
	}
	targetParent := filepath.Join(agentDir, "tmp")
	if err := os.MkdirAll(targetParent, 0o700); err != nil {
		return
	}
	target := filepath.Join(targetParent, "extensions")
	if _, err := os.Lstat(target); err == nil {
		return
	}
	_ = os.Symlink(source, target)
}

func piAmbientAgentDir() string {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return filepath.Clean(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}
