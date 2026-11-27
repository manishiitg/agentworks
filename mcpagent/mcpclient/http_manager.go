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

// Connect creates and starts an HTTP client.
func (h *HTTPManager) Connect(ctx context.Context) (*client.Client, error) {
	c, _, err := h.connectWithCancel(ctx)
	return c, err
}

// connectWithCancel creates and starts an HTTP client, honoring ctx cancellation
// during Start() while detaching long-lived transport state once Start() succeeds.
func (h *HTTPManager) connectWithCancel(ctx context.Context) (*client.Client, context.CancelFunc, error) {
	c, err := h.CreateClient()
	if err != nil {
		return nil, nil, err
	}

	streamCtx, streamCancel := context.WithCancel(context.Background())
	stopAbort := context.AfterFunc(ctx, streamCancel)
	h.logger.Debug("Starting HTTP client with dial-abortable stream context")

	if err := c.Start(streamCtx); err != nil {
		stopAbort()
		streamCancel()
		_ = c.Close()
		return nil, nil, fmt.Errorf("failed to start HTTP client: %w", err)
	}
	if !stopAbort() {
		streamCancel()
		_ = c.Close()
		return nil, nil, fmt.Errorf("failed to start HTTP client: %w", ctx.Err())
	}

	return c, streamCancel, nil
}
