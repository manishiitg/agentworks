package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// HandleStreaming processes streaming chunks and returns collected tool calls and content
// Returns tool calls, content, and time to first token (TTFT) in milliseconds
// startTime should be the time when the request was initiated (before calling GenerateContent)
func HandleStreaming(streamChan <-chan llmtypes.StreamChunk, startTime time.Time) ([]llmtypes.ToolCall, string, int64) {
	var toolCalls []llmtypes.ToolCall
	var content strings.Builder
	var chunkCount int
	var firstTokenTime int64 = -1

	for chunk := range streamChan {
		chunkCount++

		// Track time to first token (first content chunk)
		if firstTokenTime == -1 && chunk.Type == llmtypes.StreamChunkTypeContent && chunk.Content != "" {
			firstTokenTime = time.Since(startTime).Milliseconds()
		}

		switch chunk.Type {
		case llmtypes.StreamChunkTypeContent:
			// Print content immediately
			if chunk.Content != "" {
				fmt.Print(chunk.Content)
				os.Stdout.Sync()
				content.WriteString(chunk.Content)
			}

		case llmtypes.StreamChunkTypeToolCall:
			// Collect tool calls
			if chunk.ToolCall != nil {
				// Validate and sanitize arguments before storing (critical for Bedrock)
				toolCall := *chunk.ToolCall
				if toolCall.FunctionCall != nil && toolCall.FunctionCall.Arguments != "" {
					// Validate JSON arguments
					var jsonObj map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &jsonObj); err == nil {
						// Valid JSON - re-marshal to ensure proper formatting
						if validatedJSON, err := json.Marshal(jsonObj); err == nil {
							toolCall.FunctionCall.Arguments = string(validatedJSON)
						} else {
							// Fallback to empty object if re-marshaling fails
							toolCall.FunctionCall.Arguments = "{}"
						}
					} else {
						// Invalid JSON - use empty object
						toolCall.FunctionCall.Arguments = "{}"
					}
				} else if toolCall.FunctionCall != nil {
					// Empty arguments - ensure it's "{}"
					toolCall.FunctionCall.Arguments = "{}"
				}

				toolCalls = append(toolCalls, toolCall)
				// Always show arguments (will be "{}" if empty)
				args := toolCall.FunctionCall.Arguments
				if args == "" {
					args = "{}"
				}
				fmt.Printf("\n[Tool call: %s, args: %s]\n", toolCall.FunctionCall.Name, args)
			}
		}
	}

	// Debug: If we received no chunks at all, something might be wrong
	if chunkCount == 0 {
		fmt.Println("\n[Warning: No streaming chunks received]")
	}

	// If no content chunks were received, TTFT is -1 (not applicable)
	return toolCalls, content.String(), firstTokenTime
}
