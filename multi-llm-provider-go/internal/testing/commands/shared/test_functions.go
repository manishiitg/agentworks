package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// validateRequiredToolArguments validates that all required arguments are present in a tool call
// Returns error if validation fails, nil if successful
// modelID is used to detect Bedrock models (which have a known limitation with required params)
func validateRequiredToolArguments(tool llmtypes.Tool, toolCall llmtypes.ToolCall, modelID string) error {
	if tool.Function == nil || toolCall.FunctionCall == nil {
		return fmt.Errorf("tool or tool call missing function definition")
	}

	// Extract required parameters from tool definition
	var requiredParams []string
	if tool.Function.Parameters != nil {
		requiredParams = tool.Function.Parameters.Required
	}

	// If no required parameters, validation passes
	if len(requiredParams) == 0 {
		return nil
	}

	// Parse tool call arguments
	argsStr := toolCall.FunctionCall.Arguments
	if argsStr == "" {
		argsStr = "{}"
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}

	// Check each required parameter
	var missingParams []string
	for _, param := range requiredParams {
		value, exists := args[param]
		if !exists || value == nil || value == "" {
			missingParams = append(missingParams, param)
		}
	}

	if len(missingParams) > 0 {
		// Check if this is a Bedrock model (known limitation - Bedrock models don't enforce required params)
		isBedrock := strings.HasPrefix(modelID, "us.") || strings.HasPrefix(modelID, "global.") || strings.Contains(modelID, "anthropic.claude")

		if isBedrock {
			// Check if we received NO arguments at all vs some arguments but missing required ones
			hasNoArgs := argsStr == "" || argsStr == "{}" || len(args) == 0

			if hasNoArgs {
				// CRITICAL: Bedrock model did not provide ANY arguments
				// This suggests the streaming/accumulation logic may not be working correctly
				// Check debug logs for [BEDROCK STREAM] to see what Input values were received during streaming
				return fmt.Errorf("CRITICAL: Bedrock model called tool with NO arguments (tool: %s, received args: %q). Check [BEDROCK STREAM] debug logs to see if Input was received during ContentBlockDeltaMemberToolUse events. This may indicate a streaming/accumulation issue rather than a model limitation", toolCall.FunctionCall.Name, argsStr)
			} else {
				// Bedrock provided some arguments but missing required ones
				return fmt.Errorf("CRITICAL: Bedrock model called tool with missing required arguments: %s (tool: %s, received args: %s). Model provided some arguments but not all required ones", strings.Join(missingParams, ", "), toolCall.FunctionCall.Name, argsStr)
			}
		}

		// For other providers, fail the validation
		return fmt.Errorf("missing required arguments: %s (tool: %s, received args: %s)", strings.Join(missingParams, ", "), toolCall.FunctionCall.Name, argsStr)
	}

	return nil
}

// RunStreamingContentTest runs basic content streaming tests (no tool calls)
// Tests that content chunks stream correctly and match the final response
func RunStreamingContentTest(llm llmtypes.Model, modelID string) {
	// 120s matches the multi-turn streaming budget below; thinking-on
	// models (GLM-4.7, o-series) routinely spend 15-25s on internal
	// reasoning before any text streams, which can blow a 60s budget.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Test 1: Short content streaming
	log.Printf("\n📝 Test 1: Short content streaming")
	messages1 := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "Say hello in exactly 5 words."),
	}

	var streamedContent1 strings.Builder
	streamChan1 := make(chan llmtypes.StreamChunk, 100)

	done1 := make(chan bool)
	go func() {
		defer close(done1)
		for chunk := range streamChan1 {
			if chunk.Type == llmtypes.StreamChunkTypeContent {
				streamedContent1.WriteString(chunk.Content)
				log.Printf("   📝 Streamed: %s", chunk.Content)
			}
		}
	}()

	startTime1 := time.Now()
	resp1, err := llm.GenerateContent(ctx, messages1,
		llmtypes.WithModel(modelID),
		llmtypes.WithStreamingChan(streamChan1),
	)
	duration1 := time.Since(startTime1)
	<-done1

	if err != nil {
		log.Printf("❌ Test 1 failed: %v", err)
		return
	}

	if len(resp1.Choices) == 0 {
		log.Printf("❌ Test 1 failed - no choices returned")
		return
	}

	finalContent1 := resp1.Choices[0].Content
	streamedContent1Str := streamedContent1.String()

	// CRITICAL: Fail if no streaming chunks were received
	if len(streamedContent1Str) == 0 {
		log.Printf("❌ Test 1 failed - no streaming chunks received (streaming not working)")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Final content length: %d chars", len(finalContent1))
		log.Printf("      This indicates streaming is not working properly")
		return
	}

	// CRITICAL: Fail if streamed content doesn't match final content
	if streamedContent1Str != finalContent1 {
		log.Printf("❌ Test 1 failed - streamed content doesn't match final content")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Streamed length: %d chars", len(streamedContent1Str))
		log.Printf("      Final length: %d chars", len(finalContent1))
		log.Printf("      Streamed: %q", streamedContent1Str)
		log.Printf("      Final: %q", finalContent1)
		log.Printf("      This indicates a streaming implementation bug")
		return
	}

	log.Printf("✅ Test 1 passed in %s", duration1)
	log.Printf("   📊 Streaming stats:")
	log.Printf("      Streamed content: %d chars", len(streamedContent1Str))
	log.Printf("      Final content: %d chars", len(finalContent1))
	log.Printf("      Content match: ✅")

	logTokenUsage(resp1.Choices[0].GenerationInfo)

	// Test 2: Longer content streaming
	log.Printf("\n📝 Test 2: Longer content streaming")
	messages2 := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "Write a short 3-sentence story about a robot learning to paint."),
	}

	var streamedContent2 strings.Builder
	streamChan2 := make(chan llmtypes.StreamChunk, 100)

	done2 := make(chan bool)
	go func() {
		defer close(done2)
		chunkCount := 0
		for chunk := range streamChan2 {
			if chunk.Type == llmtypes.StreamChunkTypeContent {
				chunkCount++
				streamedContent2.WriteString(chunk.Content)
				if chunkCount <= 5 {
					log.Printf("   📝 Chunk %d: %s", chunkCount, chunk.Content)
				}
			}
		}
		log.Printf("   📊 Total chunks received: %d", chunkCount)
	}()

	startTime2 := time.Now()
	resp2, err := llm.GenerateContent(ctx, messages2,
		llmtypes.WithModel(modelID),
		llmtypes.WithStreamingChan(streamChan2),
	)
	duration2 := time.Since(startTime2)
	<-done2

	if err != nil {
		log.Printf("❌ Test 2 failed: %v", err)
		return
	}

	if len(resp2.Choices) == 0 {
		log.Printf("❌ Test 2 failed - no choices returned")
		return
	}

	finalContent2 := resp2.Choices[0].Content
	streamedContent2Str := streamedContent2.String()

	// CRITICAL: Fail if no streaming chunks were received
	if len(streamedContent2Str) == 0 {
		log.Printf("❌ Test 2 failed - no streaming chunks received (streaming not working)")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Final content length: %d chars", len(finalContent2))
		log.Printf("      This indicates streaming is not working properly")
		return
	}

	// CRITICAL: Fail if streamed content doesn't match final content
	if streamedContent2Str != finalContent2 {
		log.Printf("❌ Test 2 failed - streamed content doesn't match final content")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Streamed length: %d chars", len(streamedContent2Str))
		log.Printf("      Final length: %d chars", len(finalContent2))
		log.Printf("      Streamed: %q", streamedContent2Str)
		log.Printf("      Final: %q", finalContent2)
		log.Printf("      This indicates a streaming implementation bug")
		return
	}

	log.Printf("✅ Test 2 passed in %s", duration2)
	log.Printf("   📊 Streaming stats:")
	log.Printf("      Streamed content: %d chars", len(streamedContent2Str))
	log.Printf("      Final content: %d chars", len(finalContent2))
	log.Printf("      Content match: ✅")

	logTokenUsage(resp2.Choices[0].GenerationInfo)

	log.Printf("\n🎯 All content streaming tests completed successfully!")
}

// RunStreamingMultiTurnTest runs multi-turn conversation streaming tests
// Tests that streaming works correctly across multiple conversation turns
func RunStreamingMultiTurnTest(llm llmtypes.Model, modelID string) {
	// Use context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second) // Longer timeout for multi-turn
	defer cancel()

	log.Printf("\n📝 Test: Multi-turn conversation with streaming")

	// Turn 1: Initial question
	log.Printf("\n🔄 Turn 1: Initial question")
	messages1 := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "My name is Alice. What's 2+2?"),
	}

	var streamedContent1 strings.Builder
	streamChan1 := make(chan llmtypes.StreamChunk, 100)

	done1 := make(chan bool)
	go func() {
		defer close(done1)
		for chunk := range streamChan1 {
			if chunk.Type == llmtypes.StreamChunkTypeContent {
				streamedContent1.WriteString(chunk.Content)
				log.Printf("   📝 Turn 1 streamed: %s", chunk.Content)
			}
		}
	}()

	startTime1 := time.Now()
	resp1, err := llm.GenerateContent(ctx, messages1,
		llmtypes.WithModel(modelID),
		llmtypes.WithStreamingChan(streamChan1),
	)
	duration1 := time.Since(startTime1)
	<-done1

	if err != nil {
		log.Printf("❌ Turn 1 failed: %v", err)
		return
	}

	if len(resp1.Choices) == 0 {
		log.Printf("❌ Turn 1 failed - no choices returned")
		return
	}

	finalContent1 := resp1.Choices[0].Content
	streamedContent1Str := streamedContent1.String()

	// CRITICAL: Fail if no streaming chunks were received
	if len(streamedContent1Str) == 0 {
		log.Printf("❌ Turn 1 failed - no streaming chunks received (streaming not working)")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Final content length: %d chars", len(finalContent1))
		log.Printf("      This indicates streaming is not working properly")
		return
	}

	// CRITICAL: Fail if streamed content doesn't match final content
	if streamedContent1Str != finalContent1 {
		log.Printf("❌ Turn 1 failed - streamed content doesn't match final content")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Streamed length: %d chars", len(streamedContent1Str))
		log.Printf("      Final length: %d chars", len(finalContent1))
		log.Printf("      Streamed: %q", streamedContent1Str)
		log.Printf("      Final: %q", finalContent1)
		log.Printf("      This indicates a streaming implementation bug")
		return
	}

	log.Printf("✅ Turn 1 passed in %s", duration1)
	log.Printf("   📊 Streaming stats:")
	log.Printf("      Streamed content: %d chars", len(streamedContent1Str))
	log.Printf("      Final content: %d chars", len(finalContent1))
	log.Printf("      Content match: ✅")
	logTokenUsage(resp1.Choices[0].GenerationInfo)

	// Turn 2: Follow-up question (with conversation history)
	log.Printf("\n🔄 Turn 2: Follow-up question (with conversation history)")
	messages2 := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "My name is Alice. What's 2+2?"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeAI, finalContent1),
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "What's my name?"),
	}

	var streamedContent2 strings.Builder
	streamChan2 := make(chan llmtypes.StreamChunk, 100)

	done2 := make(chan bool)
	go func() {
		defer close(done2)
		for chunk := range streamChan2 {
			if chunk.Type == llmtypes.StreamChunkTypeContent {
				streamedContent2.WriteString(chunk.Content)
				log.Printf("   📝 Turn 2 streamed: %s", chunk.Content)
			}
		}
	}()

	startTime2 := time.Now()
	resp2, err := llm.GenerateContent(ctx, messages2,
		llmtypes.WithModel(modelID),
		llmtypes.WithStreamingChan(streamChan2),
	)
	duration2 := time.Since(startTime2)
	<-done2

	if err != nil {
		log.Printf("❌ Turn 2 failed: %v", err)
		return
	}

	if len(resp2.Choices) == 0 {
		log.Printf("❌ Turn 2 failed - no choices returned")
		return
	}

	finalContent2 := resp2.Choices[0].Content
	streamedContent2Str := streamedContent2.String()

	// CRITICAL: Fail if no streaming chunks were received
	if len(streamedContent2Str) == 0 {
		log.Printf("❌ Turn 2 failed - no streaming chunks received (streaming not working)")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Final content length: %d chars", len(finalContent2))
		log.Printf("      This indicates streaming is not working properly")
		return
	}

	// CRITICAL: Fail if streamed content doesn't match final content
	if streamedContent2Str != finalContent2 {
		log.Printf("❌ Turn 2 failed - streamed content doesn't match final content")
		log.Printf("      Model: %s", modelID)
		log.Printf("      Streamed length: %d chars", len(streamedContent2Str))
		log.Printf("      Final length: %d chars", len(finalContent2))
		log.Printf("      Streamed: %q", streamedContent2Str)
		log.Printf("      Final: %q", finalContent2)
		log.Printf("      This indicates a streaming implementation bug")
		return
	}

	// CRITICAL: Validate context retention - response should contain "Alice"
	if len(finalContent2) == 0 {
		log.Printf("❌ Turn 2 failed - empty response")
		return
	}

	finalContent2Lower := strings.ToLower(finalContent2)
	if !strings.Contains(finalContent2Lower, "alice") {
		log.Printf("❌ Turn 2 failed - context retention validation failed")
		log.Printf("   Expected response to contain 'Alice' (from Turn 1)")
		log.Printf("   Response: %s", finalContent2)
		log.Printf("   This indicates the model is not maintaining conversation context")
		return
	}

	log.Printf("✅ Turn 2 passed in %s", duration2)
	log.Printf("   📊 Streaming stats:")
	log.Printf("      Streamed content: %d chars", len(streamedContent2Str))
	log.Printf("      Final content: %d chars", len(finalContent2))
	log.Printf("      Content match: ✅")
	log.Printf("   Context retention: ✅ (response contains 'Alice')")
	log.Printf("   💬 Response: %s", finalContent2)

	logTokenUsage(resp2.Choices[0].GenerationInfo)

	// Turn 3: Another follow-up with tool calls
	log.Printf("\n🔄 Turn 3: Follow-up with tool call (with full conversation history)")

	readFileTool := llmtypes.Tool{
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
	}

	messages3 := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "My name is Alice. What's 2+2?"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeAI, finalContent1),
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "What's my name?"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeAI, finalContent2),
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "Can you read the go.mod file?"),
	}

	var streamedContent3 strings.Builder
	var streamedToolCalls3 []llmtypes.ToolCall
	streamChan3 := make(chan llmtypes.StreamChunk, 100)

	done3 := make(chan bool)
	go func() {
		defer close(done3)
		for chunk := range streamChan3 {
			switch chunk.Type {
			case llmtypes.StreamChunkTypeContent:
				streamedContent3.WriteString(chunk.Content)
				log.Printf("   📝 Turn 3 streamed content: %s", chunk.Content)
			case llmtypes.StreamChunkTypeToolCall:
				if chunk.ToolCall != nil {
					streamedToolCalls3 = append(streamedToolCalls3, *chunk.ToolCall)
					log.Printf("   📦 Turn 3 streamed tool call: %s (ID: %s)", chunk.ToolCall.FunctionCall.Name, chunk.ToolCall.ID)
				}
			}
		}
	}()

	startTime3 := time.Now()
	resp3, err := llm.GenerateContent(ctx, messages3,
		llmtypes.WithModel(modelID),
		llmtypes.WithTools([]llmtypes.Tool{readFileTool}),
		llmtypes.WithToolChoiceString("auto"),
		llmtypes.WithStreamingChan(streamChan3),
	)
	duration3 := time.Since(startTime3)
	<-done3

	if err != nil {
		log.Printf("❌ Turn 3 failed: %v", err)
		return
	}

	if len(resp3.Choices) == 0 {
		log.Printf("❌ Turn 3 failed - no choices returned")
		return
	}

	finalContent3 := resp3.Choices[0].Content
	finalToolCalls3 := resp3.Choices[0].ToolCalls
	streamedContent3Str := streamedContent3.String()

	// CRITICAL: Validate content streaming (if there's content, it must be streamed)
	if len(finalContent3) > 0 {
		if len(streamedContent3Str) == 0 {
			log.Printf("❌ Turn 3 failed - content in response but no content chunks were streamed (streaming not working)")
			log.Printf("      Model: %s", modelID)
			log.Printf("      Final content length: %d chars", len(finalContent3))
			log.Printf("      This indicates streaming is not working properly")
			return
		}
		if streamedContent3Str != finalContent3 {
			log.Printf("❌ Turn 3 failed - streamed content doesn't match final content")
			log.Printf("      Model: %s", modelID)
			log.Printf("      Streamed length: %d chars", len(streamedContent3Str))
			log.Printf("      Final length: %d chars", len(finalContent3))
			log.Printf("      Streamed: %q", streamedContent3Str)
			log.Printf("      Final: %q", finalContent3)
			log.Printf("      This indicates a streaming implementation bug")
			return
		}
	}

	// CRITICAL: Validate tool call streaming (if there are tool calls, they must be streamed)
	if len(finalToolCalls3) > 0 {
		if len(streamedToolCalls3) == 0 {
			log.Printf("❌ Turn 3 failed - tool calls in response but none were streamed (streaming not working)")
			log.Printf("      Model: %s", modelID)
			log.Printf("      Final tool calls: %d", len(finalToolCalls3))
			log.Printf("      This indicates streaming is not working properly")
			return
		}
		if len(streamedToolCalls3) != len(finalToolCalls3) {
			log.Printf("❌ Turn 3 failed - streamed tool call count (%d) doesn't match final count (%d)", len(streamedToolCalls3), len(finalToolCalls3))
			log.Printf("      Model: %s", modelID)
			log.Printf("      This indicates a streaming implementation bug")
			return
		}
		// Verify all tool call IDs match
		streamedIDs := make(map[string]bool)
		for _, tc := range streamedToolCalls3 {
			streamedIDs[tc.ID] = true
		}
		for _, finalTC := range finalToolCalls3 {
			if !streamedIDs[finalTC.ID] {
				log.Printf("❌ Turn 3 failed - tool call ID %s in final response but not in streamed tool calls", finalTC.ID)
				return
			}
		}
	}

	// CRITICAL: Validate tool call arguments if tool calls were returned
	if len(finalToolCalls3) > 0 {
		log.Printf("\n🔍 Validating tool call arguments")
		readFileTool := llmtypes.Tool{
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
		}

		for i, tc := range finalToolCalls3 {
			if tc.FunctionCall == nil {
				log.Printf("❌ Turn 3 failed - tool call %d has no FunctionCall", i+1)
				return
			}
			if err := validateRequiredToolArguments(readFileTool, tc, modelID); err != nil {
				log.Printf("❌ Turn 3 failed - tool call %d missing required arguments: %v", i+1, err)
				log.Printf("      Model: %s", modelID)
				return
			}
			log.Printf("   ✅ Tool call %d: %s - valid arguments", i+1, tc.FunctionCall.Name)
		}
	}

	log.Printf("✅ Turn 3 passed in %s", duration3)
	log.Printf("   📊 Streaming stats:")
	log.Printf("      Streamed content: %d chars", len(streamedContent3Str))
	log.Printf("      Final content: %d chars", len(finalContent3))
	log.Printf("      Streamed tool calls: %d ✅", len(streamedToolCalls3))
	log.Printf("      Final tool calls: %d ✅", len(finalToolCalls3))

	if len(finalToolCalls3) > 0 {
		log.Printf("   Tool: %s", finalToolCalls3[0].FunctionCall.Name)
		log.Printf("   Args: %s", finalToolCalls3[0].FunctionCall.Arguments)
		log.Printf("   ✅ Tool calls were streamed correctly")
		log.Printf("   ✅ Tool call arguments validated")
	} else {
		log.Printf("   ⚠️ No tool calls returned (model may not support tool calling)")
	}

	logTokenUsage(resp3.Choices[0].GenerationInfo)

	log.Printf("\n🎯 All multi-turn conversation streaming tests completed successfully!")
	log.Printf("   📊 Summary:")
	log.Printf("      Turn 1: Content streaming ✅")
	log.Printf("      Turn 2: Content streaming with history ✅")
	log.Printf("      Turn 3: Content + tool call streaming with full history ✅")
}

func logTokenUsage(info *llmtypes.GenerationInfo) {
	if info == nil {
		return
	}

	log.Printf("📊 Token Usage:")
	if info.InputTokens != nil {
		log.Printf("   Input tokens: %v", *info.InputTokens)
	}
	if info.OutputTokens != nil {
		log.Printf("   Output tokens: %v", *info.OutputTokens)
	}
	if info.TotalTokens != nil {
		log.Printf("   Total tokens: %v", *info.TotalTokens)
	}
	if info.Additional != nil {
		if cacheRead, ok := info.Additional["cache_read_input_tokens"]; ok {
			log.Printf("   Cache read tokens: %v", cacheRead)
		}
		if cacheCreate, ok := info.Additional["cache_creation_input_tokens"]; ok {
			log.Printf("   Cache creation tokens: %v", cacheCreate)
		}
	}
}
