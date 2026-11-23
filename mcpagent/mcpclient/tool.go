package mcpclient

import (
	"github.com/mark3labs/mcp-go/mcp"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PrintTools displays tools in a detailed, human-readable format (Debug level only)
func PrintTools(tools []mcp.Tool, logger loggerv2.Logger) {
	logger.Debug("Available tools", loggerv2.Int("count", len(tools)))
	for i, tool := range tools {
		logger.Debug("Tool",
			loggerv2.Int("index", i+1),
			loggerv2.String("name", tool.Name),
			loggerv2.String("description", tool.Description))
	}
}
