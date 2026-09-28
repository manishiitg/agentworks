package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// MoveWorkspaceFileParams contains parameters for the move_workspace_file tool
type MoveWorkspaceFileParams struct {
	SourceFilepath      string `json:"source_filepath"`
	DestinationFilepath string `json:"destination_filepath"`
}

// MoveWorkspaceFile moves a file from one location to another
func (c *Client) MoveWorkspaceFile(ctx context.Context, params MoveWorkspaceFileParams) (MoveFileResult, error) {
	if params.SourceFilepath == "" {
		return MoveFileResult{}, fmt.Errorf("source_filepath is required")
	}
	if params.DestinationFilepath == "" {
		return MoveFileResult{}, fmt.Errorf("destination_filepath is required")
	}

	// Validate both paths against folder guard (write operations)
	if err := c.ValidatePathWithContext(ctx, params.SourceFilepath, true); err != nil {
		return MoveFileResult{}, fmt.Errorf("source path validation failed: %w", err)
	}
	if err := c.ValidatePathWithContext(ctx, params.DestinationFilepath, true); err != nil {
		return MoveFileResult{}, fmt.Errorf("destination path validation failed: %w", err)
	}

	// Build API URL for moving documents
	apiURL := c.BaseURL + "/api/documents/" + params.SourceFilepath + "/move"

	// Prepare request body
	requestBody := map[string]interface{}{
		"destination_path": params.DestinationFilepath,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return MoveFileResult{}, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return MoveFileResult{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return MoveFileResult{}, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	body, err := readResponseBody(resp)
	if err != nil {
		return MoveFileResult{}, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return MoveFileResult{}, fmt.Errorf("failed to parse API response: %w", err)
	}

	if !apiResp.Success {
		return MoveFileResult{}, fmt.Errorf("workspace API error: %s", apiResp.Error)
	}
	noteWorkspaceFileWrite(params.SourceFilepath, params.DestinationFilepath)

	return MoveFileResult{
		SourceFilepath:      params.SourceFilepath,
		DestinationFilepath: params.DestinationFilepath,
		Moved:               true,
	}, nil
}
