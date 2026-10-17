package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Publication uses the normal saved sign-in and bounded, resumable upload route.
func (c *Client) Publication(ctx context.Context, method, operation string, body any) (map[string]any, error) {
	path := "/api/external/v1/publications"
	if operation != "" {
		path += "/" + url.PathEscape(operation)
	}
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodPut {
		return nil, fmt.Errorf("unsupported publication operation")
	}
	raw, err := c.request(ctx, method, path, body, true)
	if err != nil {
		return nil, err
	}
	var receipt map[string]any
	err = json.Unmarshal(raw, &receipt)
	return receipt, err
}
