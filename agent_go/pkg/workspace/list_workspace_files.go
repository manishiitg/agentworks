package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

type ListWorkspaceFilesParams struct {
	Folder   string `json:"folder"`
	MaxDepth *int   `json:"max_depth,omitempty"`
}

// ListWorkspaceFiles lists files using the REST API: GET /api/documents
func (c *Client) ListWorkspaceFiles(ctx context.Context, params ListWorkspaceFilesParams) (ListFilesResult, error) {
	params.Folder = c.resolveLocalCrewRelativePath(ctx, params.Folder)
	// Validate folder path against folder guard (read operation)
	if err := c.ValidatePathWithContext(ctx, params.Folder, false); err != nil {
		return ListFilesResult{}, err
	}

	query := url.Values{}
	query.Set("folder", params.Folder)
	if params.MaxDepth != nil {
		query.Set("max_depth", fmt.Sprintf("%d", *params.MaxDepth))
	}

	path := "/api/documents?" + query.Encode()
	respBody, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		return ListFilesResult{}, err
	}

	return ListFilesResult{
		Raw: json.RawMessage(respBody),
	}, nil
}
