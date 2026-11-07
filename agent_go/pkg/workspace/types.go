package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// APIResponse represents the standard workspace API response wrapper (internal use)
type APIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// --- Typed result structs per API ---

// DeleteFileResult is the typed response from DeleteWorkspaceFile
type DeleteFileResult struct {
	Filepath string `json:"filepath"`
	Deleted  bool   `json:"deleted"`
}

// ReadFileResult is the typed response from ReadWorkspaceFile
type ReadFileResult struct {
	Filepath string `json:"filepath"`
	Content  string `json:"content"`
	Type     string `json:"type,omitempty"`
	IsImage  bool   `json:"is_image,omitempty"`
	IsBinary bool   `json:"is_binary,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// ListFilesResult is the typed response from ListWorkspaceFiles
type ListFilesResult struct {
	Raw json.RawMessage `json:"raw"` // Original API response (variable shape)
}

// UpdateFileResult is the typed response from UpdateWorkspaceFile
type UpdateFileResult struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// MoveFileResult is the typed response from MoveWorkspaceFile
type MoveFileResult struct {
	SourceFilepath      string `json:"source_filepath"`
	DestinationFilepath string `json:"destination_filepath"`
	Moved               bool   `json:"moved"`
}

// DiffPatchResult is the typed response from DiffPatchWorkspaceFile.
type DiffPatchResult struct {
	Data json.RawMessage `json:"data"`
}

// ShellCommandResult is the typed response from ExecuteShellCommand
type ShellCommandResult struct {
	LogFile         string  `json:"log_file,omitempty"`
	LogError        string  `json:"log_error,omitempty"`
	LogComplete     bool    `json:"log_complete"`
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	ExitCode        int     `json:"exit_code"`
	ExecutionTimeMs float64 `json:"execution_time_ms,omitempty"`
	Error           string  `json:"error,omitempty"`
	TimedOut        bool    `json:"-"`
}

// CommandFailed returns true if the shell command had a non-zero exit code or error
func (r ShellCommandResult) CommandFailed() bool {
	return r.ExitCode != 0 || r.Error != ""
}

// marshalResult marshals a typed result for an executor response.
func marshalResult(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(b), nil
}

// readResponseBody reads and validates HTTP response
func readResponseBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("workspace API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
