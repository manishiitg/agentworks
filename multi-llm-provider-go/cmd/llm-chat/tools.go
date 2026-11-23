package main

import (
	"context"
	"fmt"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ToolExecutor interface for executing tools
type ToolExecutor interface {
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// Mock tool executors
type readFileExecutor struct{}

func (e *readFileExecutor) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path argument is required and must be a string")
	}
	return fmt.Sprintf("File contents: %s\nThis is a mock response.", path), nil
}

type writeFileExecutor struct{}

func (e *writeFileExecutor) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path argument is required and must be a string")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content argument is required and must be a string")
	}
	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path), nil
}

type getCurrentTimeExecutor struct{}

func (e *getCurrentTimeExecutor) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	timezone := "UTC"
	if tz, ok := args["timezone"].(string); ok && tz != "" {
		timezone = tz
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}

	now := time.Now().In(loc)
	return fmt.Sprintf("Current time in %s: %s", timezone, now.Format(time.RFC3339)), nil
}

type calculateExecutor struct{}

func (e *calculateExecutor) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	expression, ok := args["expression"].(string)
	if !ok {
		return "", fmt.Errorf("expression argument is required and must be a string")
	}
	// Mock calculation - just return a mock result
	return fmt.Sprintf("Calculation result: %s = 42 (mocked)", expression), nil
}

type searchFilesExecutor struct{}

func (e *searchFilesExecutor) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, ok := args["pattern"].(string)
	if !ok {
		return "", fmt.Errorf("pattern argument is required and must be a string")
	}
	directory := "."
	if dir, ok := args["directory"].(string); ok && dir != "" {
		directory = dir
	}
	return fmt.Sprintf("Found 3 files matching pattern '%s' in %s:\n- file1.txt\n- file2.txt\n- file3.txt", pattern, directory), nil
}

// InitializeTools initializes all fixed tools with mock executors
func InitializeTools() map[string]ToolExecutor {
	return map[string]ToolExecutor{
		"read_file":        &readFileExecutor{},
		"write_file":       &writeFileExecutor{},
		"get_current_time": &getCurrentTimeExecutor{},
		"calculate":        &calculateExecutor{},
		"search_files":     &searchFilesExecutor{},
	}
}

// GetDefaultTools converts the tool registry to llmtypes.Tool format
func GetDefaultTools() []llmtypes.Tool {
	return []llmtypes.Tool{
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "read_file",
				Description: "Read contents of a file",
				Parameters: llmtypes.NewParameters(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path to read",
						},
					},
					"required": []string{"path"},
				}),
			},
		},
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "write_file",
				Description: "Write content to a file",
				Parameters: llmtypes.NewParameters(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path to write",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "Content to write",
						},
					},
					"required": []string{"path", "content"},
				}),
			},
		},
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "get_current_time",
				Description: "Get the current time in a specific timezone",
				Parameters: llmtypes.NewParameters(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"timezone": map[string]interface{}{
							"type":        "string",
							"description": "Timezone (e.g., 'UTC', 'America/New_York')",
						},
					},
					"required": []string{"timezone"},
				}),
			},
		},
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "calculate",
				Description: "Perform a mathematical calculation",
				Parameters: llmtypes.NewParameters(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"expression": map[string]interface{}{
							"type":        "string",
							"description": "Mathematical expression to calculate",
						},
					},
					"required": []string{"expression"},
				}),
			},
		},
		{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        "search_files",
				Description: "Search for files by pattern in a directory",
				Parameters: llmtypes.NewParameters(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "File pattern to search for",
						},
						"directory": map[string]interface{}{
							"type":        "string",
							"description": "Directory to search in (default: current directory)",
						},
					},
					"required": []string{"pattern"},
				}),
			},
		},
	}
}
