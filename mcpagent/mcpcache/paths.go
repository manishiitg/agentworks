package mcpcache

import (
	"os"
	"path/filepath"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// GetGeneratedDirPath calculates the path to the generated/ directory
// This is a pure function - no side effects, just path calculation
// Returns the path where code-execution artifacts should be stored.
func GetGeneratedDirPath() string {
	// Use environment variable if set
	if dir := os.Getenv("MCP_GENERATED_DIR"); dir != "" {
		return dir
	}

	// Try to get absolute path (preferred for consistency)
	if absPath, err := filepath.Abs("generated"); err == nil {
		return absPath
	}

	// Fallback to relative path
	return filepath.Join(".", "generated")
}

// EnsureGeneratedDir creates the generated directory if it doesn't exist
// Returns an error if directory creation fails
func EnsureGeneratedDir(path string, logger loggerv2.Logger) error {
	if err := os.MkdirAll(path, 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for generated artifact directories
		if logger != nil {
			logger.Warn("Failed to create generated directory",
				loggerv2.Error(err),
				loggerv2.String("directory", path))
		}
		return err
	}
	return nil
}
