package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ToolResult represents the result of a tool execution
type ToolResult struct {
	ToolCallID string
	Name       string
	Result     string
	Error      error
}

// ExecuteToolCall executes a single tool call (mocked)
func ExecuteToolCall(ctx context.Context, toolCall llmtypes.ToolCall, toolRegistry map[string]ToolExecutor) (string, error) {
	if toolCall.FunctionCall == nil {
		return "", fmt.Errorf("tool call has no function call")
	}

	// Parse JSON arguments
	var args map[string]interface{}
	argsStr := toolCall.FunctionCall.Arguments
	if argsStr == "" || argsStr == "{}" {
		args = make(map[string]interface{})
	} else {
		if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
			return "", fmt.Errorf("failed to parse tool arguments: %w", err)
		}
	}

	// Look up executor
	executor, exists := toolRegistry[toolCall.FunctionCall.Name]
	if !exists {
		return "", fmt.Errorf("tool '%s' not found", toolCall.FunctionCall.Name)
	}

	// Log received arguments for debugging
	if len(args) == 0 {
		fmt.Printf("[DEBUG] Tool %s called with empty arguments (this may indicate missing required parameters)\n", toolCall.FunctionCall.Name)
	} else {
		fmt.Printf("[DEBUG] Tool %s called with arguments: %s\n", toolCall.FunctionCall.Name, argsStr)
	}

	// Execute with timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Execute mocked tool
	result, err := executor.Execute(ctx, args)
	if err != nil {
		return "", fmt.Errorf("tool execution failed: %w", err)
	}

	return result, nil
}

// ExecuteToolCallsParallel executes multiple tool calls in parallel (all mocked)
func ExecuteToolCallsParallel(ctx context.Context, toolCalls []llmtypes.ToolCall, toolRegistry map[string]ToolExecutor) []ToolResult {
	var wg sync.WaitGroup
	results := make([]ToolResult, len(toolCalls))

	for i, toolCall := range toolCalls {
		wg.Add(1)
		go func(index int, tc llmtypes.ToolCall) {
			defer wg.Done()

			result, err := ExecuteToolCall(ctx, tc, toolRegistry)
			results[index] = ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.FunctionCall.Name,
				Result:     result,
				Error:      err,
			}
		}(i, toolCall)
	}

	wg.Wait()
	return results
}
