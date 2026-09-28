package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// DeleteWorkspaceFileParams contains parameters for the delete_workspace_file tool
type DeleteWorkspaceFileParams struct {
	Filepath string `json:"filepath"`
}

// DeleteWorkspaceFile deletes a file from the workspace
func (c *Client) DeleteWorkspaceFile(ctx context.Context, params DeleteWorkspaceFileParams) (DeleteFileResult, error) {
	if params.Filepath == "" {
		return DeleteFileResult{}, fmt.Errorf("filepath is required")
	}

	// Validate path against folder guard (write operation)
	if err := c.ValidatePathWithContext(ctx, params.Filepath, true); err != nil {
		return DeleteFileResult{}, err
	}

	// Build API URL with confirm parameter
	apiURL := c.BaseURL + "/api/documents/" + params.Filepath + "?confirm=true"

	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		return DeleteFileResult{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return DeleteFileResult{}, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	body, err := readResponseBody(resp)
	if err != nil {
		return DeleteFileResult{}, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return DeleteFileResult{}, fmt.Errorf("failed to parse API response: %w", err)
	}

	if !apiResp.Success {
		return DeleteFileResult{}, fmt.Errorf("workspace API error: %s", apiResp.Error)
	}
	noteWorkspaceFileWrite(params.Filepath)

	return DeleteFileResult{
		Filepath: params.Filepath,
		Deleted:  true,
	}, nil
}
