package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chunks"
)

// Publication uses the normal saved sign-in and bounded, resumable upload route.
func (c *Client) Publication(ctx context.Context, method, operation string, body any) (map[string]any, error) {
	path := "/api/external/v1/publications"
	if operation != "" {
		path += "/" + url.PathEscape(operation)
	}
	if method != http.MethodGet && method != http.MethodPost {
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

// PublicationPlan asks which chunks of the project package the server lacks.
func (c *Client) PublicationPlan(ctx context.Context, operation string, pkg chunks.File) ([]string, error) {
	raw, err := c.request(ctx, http.MethodPost, "/api/external/v1/publications/"+url.PathEscape(operation)+"/plan", map[string]any{"package": pkg}, true)
	if err != nil {
		return nil, err
	}
	var plan struct {
		Missing []string `json:"missing"`
	}
	err = json.Unmarshal(raw, &plan)
	return plan.Missing, err
}

// PublicationChunks sends chunk frames (chunks.WriteFrame) of the project package.
func (c *Client) PublicationChunks(ctx context.Context, operation string, frames []byte) error {
	_, err := c.send(ctx, http.MethodPut, "/api/external/v1/publications/"+url.PathEscape(operation)+"/chunks", "application/octet-stream", frames, true)
	return err
}
