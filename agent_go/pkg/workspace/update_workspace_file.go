package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type UpdateWorkspaceFileParams struct {
	Filepath string `json:"filepath"`
	Content  string `json:"content"`
}

// UpdateWorkspaceFile creates or updates a file using the REST API: PUT /api/documents/{filepath}
func (c *Client) UpdateWorkspaceFile(ctx context.Context, params UpdateWorkspaceFileParams) (UpdateFileResult, error) {
	if params.Filepath == "" {
		return UpdateFileResult{}, fmt.Errorf("filepath is required")
	}

	// Validate path against folder guard (write operation)
	if err := c.ValidatePathWithContext(ctx, params.Filepath, true); err != nil {
		return UpdateFileResult{}, err
	}

	// URL-encode the filepath segments
	pathSegments := strings.Split(params.Filepath, "/")
	encodedSegments := make([]string, len(pathSegments))
	for i, segment := range pathSegments {
		encodedSegments[i] = url.PathEscape(segment)
	}
	encodedPath := strings.Join(encodedSegments, "/")

	// Build request body
	requestBody := map[string]interface{}{
		"content": params.Content,
	}

	// Use PUT to /api/documents/{filepath} for update/create
	path := fmt.Sprintf("/api/documents/%s", encodedPath)
	respBody, err := c.request(ctx, "PUT", path, requestBody)
	if err != nil {
		return UpdateFileResult{}, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		noteWorkspaceFileWrite(params.Filepath)
		return UpdateFileResult{Success: true, Message: string(respBody)}, nil
	}
	if apiResp.Success {
		noteWorkspaceFileWrite(params.Filepath)
	}

	return UpdateFileResult{
		Success: apiResp.Success,
		Message: apiResp.Message,
	}, nil
}
