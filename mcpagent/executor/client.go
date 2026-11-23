package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// GetOrCreateMCPClient gets an existing MCP client or creates a new one using the cache system.
// Uses mcpcache to get cached or fresh connection with connection pooling and broken pipe recovery.
func GetOrCreateMCPClient(ctx context.Context, serverName, configPath string, logger loggerv2.Logger) (mcpclient.ClientInterface, error) {
	if logger == nil {
		logger = loggerv2.NewNoop()
	}

	result, err := mcpcache.GetCachedOrFreshConnection(
		ctx,
		nil, // No LLM needed for tool execution
		serverName,
		configPath,
		nil, // No tracers needed
		logger,
		false, // disableCache - use cache by default for executor
		nil,   // No runtime overrides needed for executor
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection for server %s: %w", serverName, err)
	}

	// Get the client from the Clients map
	client, exists := result.Clients[serverName]
	if !exists {
		return nil, fmt.Errorf("server %s not found in connection result", serverName)
	}

	return client, nil
}

// ConvertMCPResultToString converts MCP CallToolResult to string
func ConvertMCPResultToString(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}

	// Extract all text content directly
	var textParts []string
	for _, content := range result.Content {
		// Try both pointer and value type assertions
		if textContent, ok := content.(*mcp.TextContent); ok {
			textParts = append(textParts, textContent.Text)
		} else if textContent, ok := content.(mcp.TextContent); ok {
			textParts = append(textParts, textContent.Text)
		} else if embedded, ok := content.(*mcp.EmbeddedResource); ok {
			// Extract text from embedded resources
			switch r := embedded.Resource.(type) {
			case *mcp.TextResourceContents:
				textParts = append(textParts, r.Text)
			}
		}
	}

	joined := strings.Join(textParts, "\n")

	if result.IsError {
		if joined == "" {
			return "Tool execution error (no error details available)"
		}
		return fmt.Sprintf("Error: %s", joined)
	}

	if joined == "" && len(mcpResultImages(result)) > 0 {
		return "MCP tool returned image content."
	}
	if joined == "" {
		return "Tool execution completed (no output returned)"
	}

	return joined
}

// Preserve images alongside the legacy text result. HTTP bridge callers need
// the original MIME type and base64 bytes to return native MCP image blocks.
func mcpResultImages(result *mcp.CallToolResult) []mcp.ImageContent {
	if result == nil {
		return nil
	}
	var images []mcp.ImageContent
	for _, content := range result.Content {
		switch image := content.(type) {
		case mcp.ImageContent:
			images = append(images, image)
		case *mcp.ImageContent:
			if image != nil {
				images = append(images, *image)
			}
		}
	}
	return images
}
