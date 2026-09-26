// Package upstream speaks to one upstream MCP server over Streamable HTTP.
package upstream

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Client is a bound upstream MCP session.
type Client struct {
	c   *client.Client
	url string
}

// Dial opens a Streamable HTTP session and runs MCP initialize.
func Dial(ctx context.Context, url string) (*Client, error) {
	c, err := client.NewStreamableHttpClient(url)
	if err != nil {
		return nil, fmt.Errorf("upstream client: %w", err)
	}
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("upstream start: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcp-gateway", Version: "0.1.0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		c.Close()
		return nil, fmt.Errorf("upstream initialize: %w", err)
	}
	return &Client{c: c, url: url}, nil
}

func (u *Client) Close() error { return u.c.Close() }

func (u *Client) URL() string { return u.url }

// Discover returns every tool the upstream exposes (all pages).
func (u *Client) Discover(ctx context.Context) ([]mcp.Tool, error) {
	res, err := u.c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("upstream tools/list: %w", err)
	}
	return res.Tools, nil
}

// Call invokes one upstream tool by its upstream name.
func (u *Client) Call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := u.c.CallTool(ctx, req)
	if err != nil {
		return nil, err
	}
	return res, nil
}
