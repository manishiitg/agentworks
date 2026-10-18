package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type ReadWorkspaceFileParams struct {
	Filepath string `json:"filepath"`
}

// documentAPIResponse represents the raw response from the workspace documents API (internal)
type documentAPIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Data    struct {
		Filepath string `json:"filepath"`
		Content  string `json:"content"`
		Type     string `json:"type,omitempty"`
		IsImage  bool   `json:"is_image,omitempty"`
		IsBinary bool   `json:"is_binary,omitempty"`
		Size     int64  `json:"size,omitempty"`
		MimeType string `json:"mime_type,omitempty"`
	} `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// ReadWorkspaceFile reads a file using the REST API: GET /api/documents/*filepath
func (c *Client) ReadWorkspaceFile(ctx context.Context, params ReadWorkspaceFileParams) (ReadFileResult, error) {
	// Validate path against folder guard (read operation)
	if err := c.ValidatePathWithContext(ctx, params.Filepath, false); err != nil {
		return ReadFileResult{}, err
	}

	if FileReadOverlay != nil {
		if raw, exists, handled := FileReadOverlay(ctx, params.Filepath); handled {
			if !exists {
				return ReadFileResult{}, fmt.Errorf("file not found: %w", os.ErrNotExist)
			}
			return ReadFileResult{Filepath: params.Filepath, Content: raw}, nil
		}
	}
	physical := params.Filepath
	if FileReadPath != nil {
		physical = FileReadPath(ctx, physical)
	}
	path := fmt.Sprintf("/api/documents/%s", physical)
	respBody, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		var status *httpStatusError
		if FileReadReceipt != nil && errors.As(err, &status) && status.statusCode == 404 {
			FileReadReceipt(ctx, params.Filepath, "")
		}
		return ReadFileResult{}, err
	}

	// Parse the API response to extract the document data
	var apiResp documentAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ReadFileResult{}, fmt.Errorf("failed to parse API response: %w", err)
	}

	// Check if the API returned an error (file not found, etc.)
	if apiResp.Error != "" {
		if FileReadReceipt != nil {
			FileReadReceipt(ctx, params.Filepath, "")
		}
		return ReadFileResult{}, fmt.Errorf("file not found: %s: %w", params.Filepath, os.ErrNotExist)
	}

	// Check if there's actual content
	if apiResp.Data.Content == "" && apiResp.Message == "File does not exist" {
		if FileReadReceipt != nil {
			FileReadReceipt(ctx, params.Filepath, "")
		}
		return ReadFileResult{}, fmt.Errorf("file not found: %s: %w", params.Filepath, os.ErrNotExist)
	}
	if apiResp.Data.IsBinary {
		return ReadFileResult{}, fmt.Errorf("cannot read binary file as text: %s", params.Filepath)
	}

	if FileReadReceipt != nil {
		FileReadReceipt(ctx, params.Filepath, apiResp.Data.Content)
	}
	return ReadFileResult{
		Filepath: apiResp.Data.Filepath,
		Content:  apiResp.Data.Content,
		Type:     apiResp.Data.Type,
		IsImage:  apiResp.Data.IsImage,
		IsBinary: apiResp.Data.IsBinary,
		Size:     apiResp.Data.Size,
		MimeType: apiResp.Data.MimeType,
	}, nil
}

// IsFileNotFound distinguishes an absent file from a refused or failed read.
func IsFileNotFound(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.statusCode == 404
	}
	return err != nil && (err.Error() == "file not found" || strings.HasPrefix(err.Error(), "file not found: "))
}
