package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/mcpagent/events"
)

// getWorkspaceAPIURL returns the workspace API base URL from environment or default
func getWorkspaceAPIURL() string {
	if url := os.Getenv("WORKSPACE_API_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8081"
}

// getDefaultFolderGuard returns the default FolderGuard config used by
// every workspace.Client constructed inside this package.
func getDefaultFolderGuard() *workspace.FolderGuardConfig {
	return &workspace.FolderGuardConfig{Enabled: true}
}

// WorkspaceFileContent is the response shape for read_workspace_file (used by orchestrator and server)
type WorkspaceFileContent struct {
	Content string `json:"content"`
}

// WorkspaceFile represents a file or folder from the workspace list API (used by orchestrator)
type WorkspaceFile struct {
	FilePath string          `json:"filepath"`
	Type     string          `json:"type"`
	Content  string          `json:"content,omitempty"`
	Children []WorkspaceFile `json:"children,omitempty"`
	Name     string          `json:"-"`
}

// WorkspaceAPIResponse is the generic workspace API response (Success, Message, Error, Data)
type WorkspaceAPIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Error   string      `json:"error"`
	Data    interface{} `json:"data"`
}

// WorkspaceFolderItem is a single item in a folder listing (can have Children for nested listing)
type WorkspaceFolderItem struct {
	Size         int64                 `json:"size,omitempty"`
	LastModified string                `json:"last_modified,omitempty"`
	FilePath     string                `json:"filepath"`
	Type         string                `json:"type"`
	Children     []WorkspaceFolderItem `json:"children,omitempty"`
}

// WorkspaceFolderListing is the folder listing response (array of folder items)
type WorkspaceFolderListing []WorkspaceFolderItem

// ParseWorkspaceFilesList parses the raw JSON string returned by ListWorkspaceFiles
// into a []WorkspaceFile slice, handling all known response formats:
//   - Direct array: [{filepath, type, children?}, ...]
//   - Files wrapper: {files: [{...}, ...]}
//   - API response wrapper: {success, data: [{...}, ...]}
//   - Singular object with children: {filepath, type, children: [{...}, ...]}
func ParseWorkspaceFilesList(jsonStr string) ([]WorkspaceFile, error) {
	// Format 1: direct array
	var filesList []WorkspaceFile
	if err := json.Unmarshal([]byte(jsonStr), &filesList); err == nil {
		return filesList, nil
	}

	// Format 2: {files: [...]}
	var altFormat struct {
		Files []WorkspaceFile `json:"files"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &altFormat); err == nil && len(altFormat.Files) > 0 {
		return altFormat.Files, nil
	}

	// Format 3: {success, data: [...]}
	var apiResp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &apiResp); err == nil && len(apiResp.Data) > 0 {
		// data could be an array or a singular object
		var dataList []WorkspaceFile
		if err2 := json.Unmarshal(apiResp.Data, &dataList); err2 == nil {
			return dataList, nil
		}
		// data is a singular object with children
		var singleObj WorkspaceFile
		if err2 := json.Unmarshal(apiResp.Data, &singleObj); err2 == nil && len(singleObj.Children) > 0 {
			return singleObj.Children, nil
		}
	}

	// Format 4: singular object with children (no wrapper)
	var singleObj WorkspaceFile
	if err := json.Unmarshal([]byte(jsonStr), &singleObj); err == nil && len(singleObj.Children) > 0 {
		return singleObj.Children, nil
	}

	return nil, fmt.Errorf("unable to parse workspace files list response")
}

// WorkspaceEventEmitter interface for emitting workspace file operation events
type WorkspaceEventEmitter interface {
	HandleEvent(ctx context.Context, event *events.AgentEvent) error
}

// Context keys for workspace event emission and folder guard paths
// These are now imported from the common package
const (
	// WorkspaceEventEmitterKey is the context key for the workspace event emitter
	WorkspaceEventEmitterKey common.ContextKey = "workspace_event_emitter"
	// TurnKey is the context key for the turn number
	TurnKey common.ContextKey = "turn"
	// ServerNameKey is the context key for the server name
	ServerNameKey common.ContextKey = "server_name"
	// FolderGuardReadPathsKey is the context key for folder guard read paths
	FolderGuardReadPathsKey = common.FolderGuardReadPathsKey
	// FolderGuardWritePathsKey is the context key for folder guard write paths
	FolderGuardWritePathsKey = common.FolderGuardWritePathsKey
	// FolderGuardBlockedPathsKey is the context key for blocked paths (deny list)
	FolderGuardBlockedPathsKey = common.FolderGuardBlockedPathsKey
	// FolderGuardAllowedWriteFolderKey is the context key for the only folder allowed for writes (chat mode)
	FolderGuardAllowedWriteFolderKey = common.FolderGuardAllowedWriteFolderKey
)
