package mcpclient

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ClientInterface defines the common interface for Client
type ClientInterface interface {
	// Connect establishes a connection
	Connect(ctx context.Context) error

	// ConnectWithRetry establishes connection with retry logic
	ConnectWithRetry(ctx context.Context) error

	// ConnectWithTimeout establishes connection with a timeout
	ConnectWithTimeout(timeout time.Duration) error

	// Close closes the connection
	Close() error

	// GetServerInfo returns server information
	GetServerInfo() *mcp.Implementation

	// ListTools lists all available tools
	ListTools(ctx context.Context) ([]mcp.Tool, error)

	// CallTool calls a tool with arguments
	CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcp.CallToolResult, error)

	// Ping checks if the connection is still alive
	Ping(ctx context.Context) error

	// SetContextCancel stores the context cancel function for later cleanup (used for SSE connections)
	SetContextCancel(cancel context.CancelFunc)

	// GetContextCancel retrieves the stored context cancel function
	GetContextCancel() context.CancelFunc

	// SetContext stores the context for later use (used for SSE connections)
	SetContext(ctx context.Context)

	// GetContext retrieves the stored context
	GetContext() context.Context
}
