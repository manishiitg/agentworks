package models

// ExecuteShellRequest represents the request to execute a shell command
type ExecuteShellRequest struct {
	Command          string `json:"command" binding:"required"` // Shell command to execute (single string with all arguments)
	WorkingDirectory string `json:"working_directory"`          // Relative working directory within docs-dir (default: "." for root)
	Timeout          int    `json:"timeout,omitempty"`          // Timeout in seconds (default: 60, max: 300)
	UseShell         bool   `json:"use_shell,omitempty"`        // Execute through shell (enables pipes, redirects, &&, ||, etc.)

	// Folder guard configuration
	FolderGuard *FolderGuardConfig `json:"folder_guard,omitempty"`

	// Extra environment variables to inject. The handler applies a narrow allowlist
	// for runtime prefixes plus DB_PATH and PYTHONDONTWRITEBYTECODE.
	ExtraEnv map[string]string `json:"extra_env,omitempty"`

	// DBReadSnapshot is a trusted harness-only request. Before launching the
	// child process, the workspace service replaces DB_PATH with a consistent
	// standalone snapshot stored under STEP_OUTPUT_DIR. The public shell tool
	// schema does not expose this field.
	DBReadSnapshot bool `json:"db_read_snapshot,omitempty"`

	// ArtifactTransfer asks the trusted workspace server to move a browser artifact
	// from its managed staging directory into a path authorized by FolderGuard.
	// The command itself stays sandboxed; only this narrowly validated transfer is
	// performed outside the child process sandbox.
	ArtifactTransfer *BrowserArtifactTransfer `json:"artifact_transfer,omitempty"`

	// UploadTransfers are copied from currently authorized workspace/host read
	// paths into managed temporary staging before agent-browser runs.
	UploadTransfers []BrowserUploadTransfer `json:"upload_transfers,omitempty"`
}

// BrowserArtifactTransfer describes one backend-brokered browser output.
// SourcePath must be under the workspace server's managed temporary artifact
// directory. When Finalize is true, DestinationPath must be covered by
// FolderGuard.WritePaths.
type BrowserArtifactTransfer struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
	Kind            string `json:"kind"` // screenshot | video | download
	Finalize        bool   `json:"finalize"`
}

type BrowserUploadTransfer struct {
	SourcePath string `json:"source_path"`
	StagedPath string `json:"staged_path"`
}

type FolderGuardConfig struct {
	Enabled    bool     `json:"enabled"`
	ReadPaths  []string `json:"read_paths"`
	WritePaths []string `json:"write_paths"`
	// BlockedPaths denies READ AND WRITE (hard deny). Takes precedence over read/write paths.
	BlockedPaths []string `json:"blocked_paths"`
	// BlockedWritePaths denies WRITES only — reads pass through. Used for paths the agent
	// must inspect but cannot modify (e.g. a workflow's planning/ subtree).
	BlockedWritePaths []string `json:"blocked_write_paths,omitempty"`
	// StrictAllowlist switches the isolator to deny-by-default (see
	// security.Isolator.StrictAllowlist); DenyNetwork removes outbound network
	// in that mode. Both come from an agent profile's runtime.sandbox policy.
	StrictAllowlist bool   `json:"strict_allowlist,omitempty"`
	DenyNetwork     bool   `json:"deny_network,omitempty"`
	EnforcementMode string `json:"enforcement_mode"` // "strict" | "warn" | "audit"
	// BrowserSession is the managed browser this command's workflow, Crew or
	// Code project owns (set by the agent server from the session, never by
	// the model). The sandbox then grants only that browser's socket folder
	// and profile instead of every browser's.
	BrowserSession string `json:"browser_session,omitempty"`
}

// IsPathBlocked checks if a path is in the blocked paths list
func (f *FolderGuardConfig) IsPathBlocked(path string) bool {
	if f == nil || !f.Enabled || len(f.BlockedPaths) == 0 {
		return false
	}
	for _, blocked := range f.BlockedPaths {
		if len(path) >= len(blocked) && path[:len(blocked)] == blocked {
			return true
		}
		// Also check without trailing slash
		blockedNoSlash := blocked
		if len(blocked) > 0 && blocked[len(blocked)-1] == '/' {
			blockedNoSlash = blocked[:len(blocked)-1]
		}
		if path == blockedNoSlash {
			return true
		}
	}
	return false
}

// ExecuteShellResponse represents the response from shell command execution
type ExecuteShellResponse struct {
	Stdout          string `json:"stdout"`            // Standard output
	Stderr          string `json:"stderr"`            // Standard error
	ExitCode        int    `json:"exit_code"`         // Process exit code
	ExecutionTimeMs int    `json:"execution_time_ms"` // Execution time in milliseconds
	Command         string `json:"command"`           // Full command that was executed
}
