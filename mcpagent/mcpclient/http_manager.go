package mcpclient

import (
	"context"
	"fmt"
	"net/http"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

// HTTPManager provides simple HTTP connection management
type HTTPManager struct {
	url        string
	headers    map[string]string
	logger     loggerv2.Logger
	httpClient *http.Client // nil: the transport's default client
}

// WithHTTPClient sets the HTTP client every request uses.
func (h *HTTPManager) WithHTTPClient(client *http.Client) *HTTPManager {
	h.httpClient = client
	return h
}

// NewHTTPManager creates a new HTTP manager
func NewHTTPManager(url string, headers map[string]string, logger loggerv2.Logger) *HTTPManager {
	return &HTTPManager{
		url:     url,
		headers: headers,
		logger:  logger,
	}
}

// CreateClient creates a new HTTP client with direct connection
func (h *HTTPManager) CreateClient() (*client.Client, error) {
	// Create transport options
	var options []transport.StreamableHTTPCOption

	// Add headers if provided
	if len(h.headers) > 0 {
		options = append(options, transport.WithHTTPHeaders(h.headers))
	}
	options = append(options, transport.WithHTTPBasicClient(withStatusLogging(h.httpClient)))

	// Create StreamableHTTP transport
	httpTransport, err := transport.NewStreamableHTTP(h.url, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP transport: %w", err)
	}

	// Create client with transport
	return client.NewClient(httpTransport), nil
}

// Connect creates and starts an HTTP client
func (h *HTTPManager) Connect(ctx context.Context) (*client.Client, error) {
	client, err := h.CreateClient()
	if err != nil {
		return nil, err
	}

	// For HTTP connections, use background context for Start() to prevent connection cancellation
	// The provided context will be used for actual MCP calls (ListTools, etc.)
	// This prevents the HTTP connection from being canceled when the caller's context is done
	startCtx := context.Background()
	h.logger.Debug("Using background context for HTTP Start() to prevent connection cancellation")

	// Start the client with background context
	if err := client.Start(startCtx); err != nil {
		return nil, fmt.Errorf("failed to start HTTP client: %w", err)
	}

	return client, nil
}
