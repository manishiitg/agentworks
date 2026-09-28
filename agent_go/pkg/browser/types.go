package browser

// FolderGuardConfig represents folder access restrictions
type FolderGuardConfig struct {
	Enabled           bool     `json:"enabled"`
	ReadPaths         []string `json:"read_paths"`
	WritePaths        []string `json:"write_paths"`
	BlockedPaths      []string `json:"blocked_paths"`
	BlockedWritePaths []string `json:"blocked_write_paths,omitempty"`
	// BrowserSession scopes the sandbox to this session's own managed browser.
	BrowserSession string `json:"browser_session,omitempty"`
}

// ShellExecuteRequest represents the request body for workspace-api /api/execute
type ShellExecuteRequest struct {
	Command          string             `json:"command"`
	WorkingDirectory string             `json:"working_directory"`
	Args             []string           `json:"args,omitempty"`
	Timeout          int                `json:"timeout,omitempty"`
	FolderGuard      *FolderGuardConfig `json:"folder_guard,omitempty"`
	ArtifactTransfer *ArtifactTransfer  `json:"artifact_transfer,omitempty"`
	UploadTransfers  []UploadTransfer   `json:"upload_transfers,omitempty"`
}

// ArtifactTransfer asks workspace-api to prepare a backend staging path and,
// when Finalize is true, publish it into the current session's authorized output.
type ArtifactTransfer struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
	Kind            string `json:"kind"`
	Finalize        bool   `json:"finalize"`
}

// UploadTransfer asks workspace-api to copy one currently authorized input
// file into backend-managed staging before a persistent browser daemon reads it.
type UploadTransfer struct {
	SourcePath string `json:"source_path"`
	StagedPath string `json:"staged_path"`
}

// ShellExecuteResponse represents the response data from workspace-api /api/execute
type ShellExecuteResponse struct {
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exit_code"`
	ExecutionTimeMs int    `json:"execution_time_ms"`
	Command         string `json:"command"`
}

// APIResponse represents the workspace-api response wrapper
type APIResponse struct {
	Success bool                 `json:"success"`
	Message string               `json:"message"`
	Data    ShellExecuteResponse `json:"data"`
	Error   string               `json:"error,omitempty"`
}
