package cursorcli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
)

// cursorLandlockReads are the files a confined Cursor launch needs outside
// its granted folders: files named in argv, the project configs written to
// <artifact dir>/.cursor (MCP servers, hooks, permissions) and the programs and
// files they name, and this process's MCP bridge token folder. The artifact dir
// is the private runtime for a Crew bound to a person's own folder, so read it
// from there, not from cwd (PLAT-872-57: the MCP command grant was missing).
func cursorLandlockReads(opts *llmtypes.CallOptions, args []string, workingDir string) []string {
	artifacts := llmtypes.CLIArtifactDirectory(opts, workingDir)
	configs := clisandbox.ConfigDirFiles(filepath.Join(artifacts, ".cursor"))
	read := clisandbox.ArgFilePaths(args)
	read = append(read, clisandbox.MCPCommandPaths(configs)...)
	read = append(read, clisandbox.JSONFilePaths(configs)...)
	return append(read, cursorBridgeTokenDirFor(artifacts))
}

func cursorLandlockArgs(opts *llmtypes.CallOptions, args []string, workingDir string) ([]string, func(), error) {
	if opts == nil {
		return args, func() {}, nil
	}
	if opts.CLISecurity.LandlockEnforced() {
		var err error
		writes := clisandbox.LandlockWritePaths(opts.CLISecurity, workingDir, nil)
		args, err = cursorNativeEntry(args, writes)
		if err != nil {
			return nil, func() {}, err
		}
	}
	return clisandbox.LandlockArgs(opts.CLISecurity, args, workingDir, cursorLandlockReads(opts, args, workingDir), nil)
}
