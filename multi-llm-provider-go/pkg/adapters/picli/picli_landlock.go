package picli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// piLandlockReads: Pi starts from a generated launch script in its own temp
// folder (with its MCP config beside it), plus files named in argv and the
// programs those configs name.
func piLandlockReads(args []string, env ...string) []string {
	read := clisandbox.ArgFilePaths(args)
	// Pi's native MCP config (mcp.json, in its private agent folder) names the bridge program it
	// spawns; without a read/exec grant on that program the bridge failed with "spawn
	// .../mcpbridge EACCES" and Pi ran without its platform tools (Confida 2026-09-30).
	if agentDir := piAgentDirFromEnv(env); agentDir != "" {
		config := []string{piNativeMCPConfigPath(agentDir)}
		if auth, err := filepath.EvalSymlinks(filepath.Join(agentDir, "auth.json")); err == nil {
			read = append(read, auth)
		}
		read = append(read, clisandbox.MCPCommandPaths(config)...)
		read = append(read, clisandbox.JSONFilePaths(config)...)
	}
	for _, file := range read {
		if filepath.Base(file) == "launch-pi.sh" {
			dir := filepath.Dir(file)
			read = append(read, dir)
			configs := clisandbox.ConfigDirFiles(dir)
			read = append(read, clisandbox.MCPCommandPaths(configs)...)
			read = append(read, clisandbox.JSONFilePaths(configs)...)
		}
	}
	return read
}

// piAgentDirFromEnv is the PI_CODING_AGENT_DIR of a launch's environment ("KEY=value" entries).
func piAgentDirFromEnv(env []string) string {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PI_CODING_AGENT_DIR="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// piLandlockWrites is the launch folder itself: Pi's marker extension there appends to
// markers.jsonl, so read access alone made Pi exit at start with "Failed to load extension ...
// EACCES ... markers.jsonl" once the server's temp folder moved out of the shared /tmp
// (Confida 2026-09-30). The folder is created for this one launch and removed after it.
func piLandlockWrites(args []string) []string {
	var write []string
	for _, file := range clisandbox.ArgFilePaths(args) {
		if filepath.Base(file) == "launch-pi.sh" {
			write = append(write, filepath.Dir(file))
		}
	}
	return write
}

func piLandlockArgs(opts *llmtypes.CallOptions, args []string, workingDir string, env ...string) ([]string, func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return args, func() {}, nil
	}
	wrapped, cleanup, err := clisandbox.LandlockArgs(opts.CLISecurity, args, workingDir, piLandlockReads(args, env...), piLandlockWrites(args))
	if err != nil {
		return nil, func() {}, fmt.Errorf("confine Pi: %w", err)
	}
	return wrapped, cleanup, nil
}

func piLandlockCmd(opts *llmtypes.CallOptions, cmd *exec.Cmd, workingDir string, runtimeDirs ...string) (func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return func() {}, nil
	}
	cleanup, err := clisandbox.LandlockCmd(opts.CLISecurity, cmd, workingDir, piLandlockReads(cmd.Args, cmd.Env...), runtimeDirs)
	if err != nil {
		return func() {}, fmt.Errorf("confine Pi: %w", err)
	}
	return cleanup, nil
}
