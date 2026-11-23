package agycli

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// agy runs on its own temporary home (a copy of the server's ~/.gemini
// login and config, see agyIsolatedHome), which wins over any other home;
// confined, that home is its only writable folder besides its working dir.
func agyLandlockArgs(opts *llmtypes.CallOptions, args []string, workingDir, agyHome string) ([]string, func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return args, func() {}, nil
	}
	read := append(clisandbox.ArgFilePaths(args), agyMCPLaunchReadPaths(agyHome)...)
	wrapped, cleanup, err := clisandbox.LandlockArgs(opts.CLISecurity, args, workingDir, read, []string{agyHome})
	if err != nil {
		return nil, func() {}, fmt.Errorf("confine agy: %w", err)
	}
	return wrapped, cleanup, nil
}

func agyLandlockCmd(opts *llmtypes.CallOptions, cmd *exec.Cmd, workingDir, agyHome string) (func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return func() {}, nil
	}
	cleanup, err := clisandbox.LandlockCmd(opts.CLISecurity, cmd, workingDir, agyMCPLaunchReadPaths(agyHome), []string{agyHome})
	if err != nil {
		return func() {}, fmt.Errorf("confine agy: %w", err)
	}
	return cleanup, nil
}

// Like the other native adapters, admit only the programs and file arguments
// in this launch's private MCP catalog. The bridge commonly lives outside both
// the CLI install and the granted project; without this it cannot start under
// Landlock/Seatbelt and AGY silently presents a native-only tool inventory.
func agyMCPLaunchReadPaths(agyHome string) []string {
	return clisandbox.MCPCommandPaths([]string{filepath.Join(agyHome, ".gemini", "config", "mcp_config.json")})
}
