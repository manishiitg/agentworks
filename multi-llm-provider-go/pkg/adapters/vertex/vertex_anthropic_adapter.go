package vertex

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"
)

// VertexAnthropicAdapter implements llmtypes.Model for Vertex AI Anthropic models
type VertexAnthropicAdapter struct {
	projectID  string
	locationID string
	modelID    string
	logger     interfaces.Logger
	httpClient *http.Client
}

// NewVertexAnthropicAdapter creates a new adapter for Vertex AI Anthropic models
func NewVertexAnthropicAdapter(projectID, locationID, modelID string, logger interfaces.Logger) *VertexAnthropicAdapter {
	return &VertexAnthropicAdapter{
		projectID:  projectID,
		locationID: locationID,
		modelID:    modelID,
		logger:     logger,
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for long-running requests
		},
	}
}

// GetModelID implements the llmtypes.Model interface
func (v *VertexAnthropicAdapter) GetModelID() string {
	return v.modelID
}

// GetModelMetadata implements the llmtypes.Model interface
// Vertex Anthropic models use the same metadata as direct Anthropic models
func (v *VertexAnthropicAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	// Guard against empty modelID - use default Anthropic model if not provided
	if modelID == "" {
		modelID = anthropic.ModelClaudeSonnet46
	}

	// Get metadata from Anthropic package
	metadata, err := anthropic.GetAnthropicModelMetadata(modelID)
	if err != nil {
		return nil, err
	}

	// Override provider to reflect Vertex as the provider
	metadata.Provider = "vertex"

	return metadata, nil
}

// GenerateContent implements the llmtypes.Model interface
func (v *VertexAnthropicAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Parse call options
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}

	// Get access token
	accessToken, err := GetAccessToken(ctx, v.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	// Handle JSON mode by adding instructions to messages (similar to direct Anthropic adapter)
	// This ensures structured output works correctly with Vertex Anthropic
	messagesToConvert := messages
	if opts.JSONMode {
		messagesToConvert = v.addJSONModeInstructions(messages)
	}

	// Convert messages to Anthropic format
	anthropicMessages, err := v.convertMessagesToAnthropic(messagesToConvert)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Generate unique request ID for tracking request/response correlation
	requestID := fmt.Sprintf("req_%d", time.Now().UnixNano())

	// Build request payload
	requestPayload := map[string]interface{}{
		"anthropic_version": "vertex-2023-10-16",
		"stream":            opts.StreamChan != nil, // Enable streaming if channel provided
		"max_tokens":        v.getMaxTokens(opts),
		"temperature":       v.getTemperature(opts),
		"messages":          anthropicMessages,
	}

	// Add tools if provided
	if len(opts.Tools) > 0 {
		tools := v.convertToolsToAnthropic(opts.Tools)
		requestPayload["tools"] = tools
	}

	// Log raw input details for all requests (not just errors)
	if v.logger != nil {
		v.logRawInput(requestID, v.modelID, requestPayload)
	}

	// Build endpoint URL
	endpoint := fmt.Sprintf(
		"https://aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:streamRawPredict",
		v.projectID,
		v.locationID,
		v.modelID,
	)

	if v.logger != nil {
		v.logger.Infof("🔍 [VERTEX ANTHROPIC] Request endpoint: %s", endpoint)
		v.logger.Infof("🔍 [VERTEX ANTHROPIC] Model: %s, Max tokens: %d, Temperature: %f",
			v.modelID, requestPayload["max_tokens"], requestPayload["temperature"])
		if tools, ok := requestPayload["tools"].([]map[string]interface{}); ok {
			v.logger.Infof("🔍 [VERTEX ANTHROPIC] Tools being sent: %d", len(tools))
			for i, tool := range tools {
				if name, ok := tool["name"].(string); ok {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Tool %d: %s", i+1, name)
				}
			}
		} else {
			v.logger.Infof("🔍 [VERTEX ANTHROPIC] No tools in request payload")
		}
	}

	// Vertex AI requires streaming for Anthropic models, but we accumulate all chunks
	return v.generateContent(ctx, endpoint, accessToken, requestPayload, opts, requestID)
}

// generateContent handles responses (Vertex AI requires streaming for Anthropic models, but we accumulate all chunks)
func (v *VertexAnthropicAdapter) generateContent(ctx context.Context, endpoint, accessToken string, payload map[string]interface{}, opts *llmtypes.CallOptions, requestID string) (*llmtypes.ContentResponse, error) {
	// Ensure channel is closed when done (if streaming is enabled)
	defer func() {
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
	}()
	// Vertex requires streaming for Anthropic models
	payload["stream"] = true

	// Marshal request payload
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(payloadJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	// Execute request
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if v.logger != nil {
			v.logger.Infof("🔍 [VERTEX ANTHROPIC] Error response (status %d): %s", resp.StatusCode, string(body))
			v.logRawResponse(requestID, v.modelID, nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body)))
		}
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse streaming response
	var fullContent strings.Builder
	var toolCalls []llmtypes.ToolCall
	var currentToolUseBlock map[string]interface{} // Accumulate tool_use block data
	var partialJSONBuffer strings.Builder          // Accumulate partial_json fragments
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse SSE format: "data: {...}"
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var event map[string]interface{}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				if v.logger != nil {
					v.logger.Infof("Failed to parse SSE event: %v", err)
				}
				continue
			}

			// Handle Vertex AI streaming format
			// Events have a "type" field indicating the event type
			eventType, _ := event["type"].(string)

			// Handle content_block_start events (for tool_use blocks)
			if eventType == "content_block_start" {
				if contentBlock, ok := event["content_block"].(map[string]interface{}); ok {
					if blockType, ok := contentBlock["type"].(string); ok && blockType == "tool_use" {
						// Start accumulating tool_use block
						currentToolUseBlock = make(map[string]interface{})
						currentToolUseBlock["type"] = "tool_use"
						partialJSONBuffer.Reset() // Reset JSON buffer for new tool call
						if id, ok := contentBlock["id"].(string); ok {
							currentToolUseBlock["id"] = id
						}
						if name, ok := contentBlock["name"].(string); ok {
							currentToolUseBlock["name"] = name
						}
						// Initialize input map - it might be populated in content_block_start or content_block_delta
						currentToolUseBlock["input"] = make(map[string]interface{})
						if input, ok := contentBlock["input"].(map[string]interface{}); ok && len(input) > 0 {
							currentToolUseBlock["input"] = input
							if v.logger != nil {
								v.logger.Infof("🔍 [VERTEX ANTHROPIC] content_block_start has initial input: %v", input)
							}
						} else {
							if v.logger != nil {
								v.logger.Infof("🔍 [VERTEX ANTHROPIC] content_block_start has no input, will accumulate from deltas. Content block keys: %v", getMapKeys(contentBlock))
							}
						}
						if v.logger != nil {
							v.logger.Infof("🔍 [VERTEX ANTHROPIC] Started accumulating tool_use block: %v, initial input: %v",
								currentToolUseBlock["name"], currentToolUseBlock["input"])
						}
					}
				}
			}

			// Handle content_block_delta events
			if eventType == "content_block_delta" {
				if delta, ok := event["delta"].(map[string]interface{}); ok {
					// Text delta
					if text, ok := delta["text"].(string); ok && text != "" {
						fullContent.WriteString(text)
						// Stream content chunks immediately
						if opts.StreamChan != nil {
							select {
							case opts.StreamChan <- llmtypes.StreamChunk{
								Type:    llmtypes.StreamChunkTypeContent,
								Content: text,
							}:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
					}

					// Check if this is a tool_use delta (for tool call arguments)
					if currentToolUseBlock != nil {
						// Vertex AI sends tool arguments via partial_json in the delta (not in tool_use.partial_input)
						// partial_json is incremental JSON fragments that need to be accumulated
						if partialJSON, ok := delta["partial_json"].(string); ok && partialJSON != "" {
							if v.logger != nil {
								v.logger.Infof("🔍 [VERTEX ANTHROPIC] Found partial_json fragment: %s", partialJSON)
							}

							// Accumulate the JSON fragment
							partialJSONBuffer.WriteString(partialJSON)

							// Try to parse the accumulated JSON - it might be incomplete, so we try parsing incrementally
							accumulatedJSON := partialJSONBuffer.String()
							var parsedInput map[string]interface{}
							if err := json.Unmarshal([]byte(accumulatedJSON), &parsedInput); err != nil {
								// JSON is still incomplete, wait for more fragments
								if v.logger != nil {
									v.logger.Infof("🔍 [VERTEX ANTHROPIC] Accumulated JSON is incomplete, waiting for more fragments. Current: %s", accumulatedJSON)
								}
							} else {
								// Successfully parsed complete JSON - update the input
								currentToolUseBlock["input"] = parsedInput
								if v.logger != nil {
									v.logger.Infof("🔍 [VERTEX ANTHROPIC] Parsed complete JSON from accumulated fragments. Tool: %v, Input: %v",
										currentToolUseBlock["name"], parsedInput)
								}
							}
						}

						// Also check for tool_use.partial_input (legacy format)
						if toolUseDelta, ok := delta["tool_use"].(map[string]interface{}); ok {
							if v.logger != nil {
								v.logger.Infof("🔍 [VERTEX ANTHROPIC] content_block_delta for tool_use - raw delta: %v", toolUseDelta)
							}
							// Handle partial_input - can be a map or a string (JSON)
							if partialInputRaw, ok := toolUseDelta["partial_input"]; ok {
								if v.logger != nil {
									v.logger.Infof("🔍 [VERTEX ANTHROPIC] Found partial_input in delta: %v (type: %T)", partialInputRaw, partialInputRaw)
								}
								var partialInput map[string]interface{}

								// Try to parse as map first
								if partialInputMap, ok := partialInputRaw.(map[string]interface{}); ok {
									partialInput = partialInputMap
								} else if partialInputStr, ok := partialInputRaw.(string); ok {
									// Try to parse as JSON string
									if err := json.Unmarshal([]byte(partialInputStr), &partialInput); err != nil {
										if v.logger != nil {
											v.logger.Infof("⚠️ [VERTEX ANTHROPIC] Failed to parse partial_input as JSON: %v", err)
										}
										partialInput = make(map[string]interface{})
									}
								} else {
									if v.logger != nil {
										v.logger.Infof("⚠️ [VERTEX ANTHROPIC] partial_input is neither map nor string: %T", partialInputRaw)
									}
									partialInput = make(map[string]interface{})
								}

								// Merge partial input into existing input
								if existingInput, ok := currentToolUseBlock["input"].(map[string]interface{}); ok {
									// Merge maps - partialInput overwrites existing keys
									for k, v := range partialInput {
										existingInput[k] = v
									}
									if v.logger != nil {
										v.logger.Infof("🔍 [VERTEX ANTHROPIC] Merged partial_input into existing input. Tool: %v, Merged input: %v",
											currentToolUseBlock["name"], existingInput)
									}
								} else {
									// Initialize input map if it doesn't exist
									currentToolUseBlock["input"] = partialInput
									if v.logger != nil {
										v.logger.Infof("🔍 [VERTEX ANTHROPIC] Initialized input map with partial_input. Tool: %v, Input: %v",
											currentToolUseBlock["name"], partialInput)
									}
								}
							}
						}
					}
				}
			}

			// Handle content_block_stop events (complete tool_use blocks)
			if eventType == "content_block_stop" {
				if currentToolUseBlock != nil {
					// Try to parse accumulated partial_json if we have any
					if partialJSONBuffer.Len() > 0 {
						accumulatedJSON := partialJSONBuffer.String()
						var parsedInput map[string]interface{}
						if err := json.Unmarshal([]byte(accumulatedJSON), &parsedInput); err == nil {
							currentToolUseBlock["input"] = parsedInput
							if v.logger != nil {
								v.logger.Infof("🔍 [VERTEX ANTHROPIC] Parsed final JSON from accumulated fragments at stop: %v", parsedInput)
							}
						} else {
							if v.logger != nil {
								v.logger.Infof("⚠️ [VERTEX ANTHROPIC] Failed to parse accumulated partial_json at stop: %s, error: %v", accumulatedJSON, err)
							}
						}
						partialJSONBuffer.Reset()
					}

					// Check if content_block_stop has the complete input
					if contentBlock, ok := event["content_block"].(map[string]interface{}); ok {
						if blockType, ok := contentBlock["type"].(string); ok && blockType == "tool_use" {
							if v.logger != nil {
								v.logger.Infof("🔍 [VERTEX ANTHROPIC] content_block_stop for tool_use - content_block keys: %v", getMapKeys(contentBlock))
							}
							if input, ok := contentBlock["input"].(map[string]interface{}); ok && len(input) > 0 {
								// Use the complete input from stop event (overrides accumulated JSON)
								currentToolUseBlock["input"] = input
								if v.logger != nil {
									v.logger.Infof("🔍 [VERTEX ANTHROPIC] Using complete input from content_block_stop: %v", input)
								}
							} else {
								if v.logger != nil {
									v.logger.Infof("⚠️ [VERTEX ANTHROPIC] content_block_stop has no input or empty input. Accumulated input: %v", currentToolUseBlock["input"])
								}
							}
						}
					}

					// We have accumulated a tool_use block
					if v.logger != nil {
						v.logger.Infof("🔍 [VERTEX ANTHROPIC] Final tool_use block before parsing: %v", currentToolUseBlock)
					}
					toolCall := v.parseToolUse(currentToolUseBlock)
					if toolCall != nil {
						toolCalls = append(toolCalls, *toolCall)
						if v.logger != nil {
							v.logger.Infof("🔧 [VERTEX ANTHROPIC] Tool call detected: %s, args: %s", toolCall.FunctionCall.Name, toolCall.FunctionCall.Arguments)
						}
						// Stream tool call when complete
						if opts.StreamChan != nil {
							toolCallCopy := *toolCall
							select {
							case opts.StreamChan <- llmtypes.StreamChunk{
								Type:     llmtypes.StreamChunkTypeToolCall,
								ToolCall: &toolCallCopy,
							}:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
					} else {
						if v.logger != nil {
							v.logger.Infof("⚠️ [VERTEX ANTHROPIC] Failed to parse accumulated tool_use block: %v", currentToolUseBlock)
						}
					}
					currentToolUseBlock = nil // Reset
				} else if contentBlock, ok := event["content_block"].(map[string]interface{}); ok {
					// Fallback: try to get block from stop event
					if blockType, ok := contentBlock["type"].(string); ok && blockType == "tool_use" {
						toolCall := v.parseToolUse(contentBlock)
						if toolCall != nil {
							toolCalls = append(toolCalls, *toolCall)
							if v.logger != nil {
								v.logger.Infof("🔧 [VERTEX ANTHROPIC] Tool call detected from stop event: %s, args: %s", toolCall.FunctionCall.Name, toolCall.FunctionCall.Arguments)
							}
							// Stream tool call when complete
							if opts.StreamChan != nil {
								toolCallCopy := *toolCall
								select {
								case opts.StreamChan <- llmtypes.StreamChunk{
									Type:     llmtypes.StreamChunkTypeToolCall,
									ToolCall: &toolCallCopy,
								}:
								case <-ctx.Done():
									return nil, ctx.Err()
								}
							}
						}
					}
				}
			}

			// Legacy format support: Check for content block in event
			if content, ok := event["content"].([]interface{}); ok {
				for _, block := range content {
					if blockMap, ok := block.(map[string]interface{}); ok {
						if blockType, ok := blockMap["type"].(string); ok {
							if blockType == "text" {
								if text, ok := blockMap["text"].(string); ok && text != "" {
									fullContent.WriteString(text)
									// Stream content chunks immediately (legacy format)
									if opts.StreamChan != nil {
										select {
										case opts.StreamChan <- llmtypes.StreamChunk{
											Type:    llmtypes.StreamChunkTypeContent,
											Content: text,
										}:
										case <-ctx.Done():
											return nil, ctx.Err()
										}
									}
								}
							} else if blockType == "tool_use" {
								// Handle tool calls in legacy format
								toolCall := v.parseToolUse(blockMap)
								if toolCall != nil {
									toolCalls = append(toolCalls, *toolCall)
									if v.logger != nil {
										v.logger.Infof("🔧 [VERTEX ANTHROPIC] Tool call detected (legacy format): %s", toolCall.FunctionCall.Name)
									}
									// Stream tool call when complete (legacy format)
									if opts.StreamChan != nil {
										toolCallCopy := *toolCall
										select {
										case opts.StreamChan <- llmtypes.StreamChunk{
											Type:     llmtypes.StreamChunkTypeToolCall,
											ToolCall: &toolCallCopy,
										}:
										case <-ctx.Done():
											return nil, ctx.Err()
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if v.logger != nil {
			v.logRawResponse(requestID, v.modelID, nil, err)
		}
		return nil, fmt.Errorf("streaming error: %w", err)
	}

	choice := &llmtypes.ContentChoice{
		Content: fullContent.String(),
	}
	if len(toolCalls) > 0 {
		choice.ToolCalls = toolCalls
	}

	// Extract usage from GenerationInfo (if available)
	// Note: vertex_anthropic_adapter may not set GenerationInfo in streaming mode
	var usage *llmtypes.Usage
	if choice.GenerationInfo != nil {
		usage = llmtypes.ExtractUsageFromGenerationInfo(choice.GenerationInfo)
	}

	// Build response summary for logging
	responseSummary := map[string]interface{}{
		"content":    fullContent.String(),
		"tool_calls": make([]map[string]interface{}, 0, len(toolCalls)),
		"usage":      usage,
	}

	// Add tool calls details
	for _, tc := range toolCalls {
		tcDetail := map[string]interface{}{
			"id":   tc.ID,
			"name": tc.FunctionCall.Name,
			"args": tc.FunctionCall.Arguments,
		}
		responseSummary["tool_calls"] = append(responseSummary["tool_calls"].([]map[string]interface{}), tcDetail)
	}

	// Log raw response for successful requests
	if v.logger != nil {
		v.logRawResponse(requestID, v.modelID, responseSummary, nil)
	}

	// Return accumulated response
	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{choice},
		Usage:   usage,
	}, nil
}

// convertMessagesToAnthropic converts llmtypes messages to Anthropic format
func (v *VertexAnthropicAdapter) convertMessagesToAnthropic(messages []llmtypes.MessageContent) ([]map[string]interface{}, error) {
	anthropicMessages := make([]map[string]interface{}, 0, len(messages))
	// Track tool call IDs that have already been converted to tool_use blocks in inserted assistant messages
	// This prevents duplicate tool_use IDs when the original AI message is processed later
	convertedToolCallIDs := make(map[string]bool)
	// Track tool call IDs that were skipped in AI messages (will be in inserted assistant messages)
	// This ensures we insert assistant messages for skipped tool calls
	skippedToolCallIDs := make(map[string]bool)

	for i, msg := range messages {
		// Check if this message has tool results BEFORE processing it
		hasToolResults := false
		var toolResultIDs []string
		for _, part := range msg.Parts {
			if _, ok := part.(llmtypes.ToolCallResponse); ok {
				hasToolResults = true
				if toolResp, ok := part.(llmtypes.ToolCallResponse); ok {
					toolResultIDs = append(toolResultIDs, toolResp.ToolCallID)
				}
			}
		}

		// If this message has tool results, ensure we have the assistant message with tool_use blocks first
		if hasToolResults && (msg.Role == llmtypes.ChatMessageTypeTool || v.convertRole(msg.Role) == "user") {
			// Check if the immediately previous message (in original messages) is an AI message with matching tool calls
			// If so, it will be converted to tool_use blocks automatically, so we don't need to add another
			// UNLESS those tool calls were skipped (due to separate tool result messages)
			needsAssistantMessage := true
			if i > 0 {
				prevMsg := messages[i-1]
				if prevMsg.Role == llmtypes.ChatMessageTypeAI {
					// Collect all tool call IDs from the previous AI message
					prevToolCallIDs := make(map[string]bool)
					for _, part := range prevMsg.Parts {
						if toolCall, ok := part.(llmtypes.ToolCall); ok {
							prevToolCallIDs[toolCall.ID] = true
						}
					}

					// Check if any of the tool calls that match tool results were skipped
					// If they were skipped, we MUST insert an assistant message
					anyToolCallsSkipped := false
					for _, resultID := range toolResultIDs {
						if prevToolCallIDs[resultID] && skippedToolCallIDs[resultID] {
							// This tool call was in the previous AI message but was skipped
							// We need to insert an assistant message for it
							anyToolCallsSkipped = true
							if v.logger != nil {
								v.logger.Infof("🔧 [VERTEX ANTHROPIC] Tool call %s was skipped in previous AI message - must insert assistant message", resultID)
							}
							break
						}
					}

					// If tool calls were skipped, we MUST insert an assistant message
					if anyToolCallsSkipped {
						needsAssistantMessage = true
						if v.logger != nil {
							v.logger.Infof("🔧 [VERTEX ANTHROPIC] Tool calls were skipped - will insert assistant message")
						}
					} else {
						// Check if ALL tool calls in previous message have corresponding tool results in current message
						// AND all tool results in current message have corresponding tool calls in previous message
						allToolCallsHaveResults := true
						allToolResultsHaveCalls := true

						// Check: every tool call in previous message must have a tool result in current message
						for toolCallID := range prevToolCallIDs {
							found := false
							for _, resultID := range toolResultIDs {
								if resultID == toolCallID {
									found = true
									break
								}
							}
							if !found {
								allToolCallsHaveResults = false
								break
							}
						}

						// Check: every tool result in current message must have a tool call in previous message
						for _, resultID := range toolResultIDs {
							if !prevToolCallIDs[resultID] {
								allToolResultsHaveCalls = false
								break
							}
						}

						// If previous message has all matching tool calls AND all tool calls have results, it will be converted to tool_use blocks
						// So we don't need to add another assistant message
						if allToolCallsHaveResults && allToolResultsHaveCalls && len(prevToolCallIDs) > 0 {
							needsAssistantMessage = false
							if v.logger != nil {
								v.logger.Infof("🔧 [VERTEX ANTHROPIC] Previous AI message has %d tool calls, current message has %d tool results - all match, will be converted automatically", len(prevToolCallIDs), len(toolResultIDs))
							}
						} else {
							if v.logger != nil {
								v.logger.Infof("🔧 [VERTEX ANTHROPIC] Tool call/result mismatch - prev tool calls: %d, current tool results: %d, allToolCallsHaveResults: %v, allToolResultsHaveCalls: %v", len(prevToolCallIDs), len(toolResultIDs), allToolCallsHaveResults, allToolResultsHaveCalls)
							}
						}
					}
				}
			}

			// If we need to add assistant message, look for tool calls in previous messages
			if needsAssistantMessage && i > 0 {
				// Look backwards for tool calls that match these tool result IDs
				for j := i - 1; j >= 0; j-- {
					prevMsg := messages[j]
					if prevMsg.Role == llmtypes.ChatMessageTypeAI {
						// Check if this message has tool calls matching our tool result IDs
						toolUseBlocks := make([]map[string]interface{}, 0)
						matchedIDs := make(map[string]bool)

						for _, part := range prevMsg.Parts {
							if toolCall, ok := part.(llmtypes.ToolCall); ok {
								// Check if this tool call ID matches any tool result ID
								for _, resultID := range toolResultIDs {
									if toolCall.ID == resultID && !matchedIDs[resultID] {
										// Convert to tool_use block
										var inputData map[string]interface{}
										if err := json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &inputData); err != nil {
											inputData = make(map[string]interface{})
										}
										toolUseBlocks = append(toolUseBlocks, map[string]interface{}{
											"type":  "tool_use",
											"id":    toolCall.ID,
											"name":  toolCall.FunctionCall.Name,
											"input": inputData,
										})
										matchedIDs[resultID] = true
										break
									}
								}
							}
						}

						// If we found matching tool_use blocks for all tool results, insert assistant message
						if len(toolUseBlocks) == len(toolResultIDs) && len(toolUseBlocks) > 0 {
							assistantMsg := map[string]interface{}{
								"role":    "assistant",
								"content": toolUseBlocks,
							}
							anthropicMessages = append(anthropicMessages, assistantMsg)
							// Track the tool call IDs that were converted in this inserted assistant message
							// This prevents duplicates when we process the original AI message later
							for _, block := range toolUseBlocks {
								if id, ok := block["id"].(string); ok {
									convertedToolCallIDs[id] = true
									if v.logger != nil {
										v.logger.Infof("🔧 [VERTEX ANTHROPIC] Tracked converted tool_use ID: %s", id)
									}
								}
							}
							if v.logger != nil {
								v.logger.Infof("🔧 [VERTEX ANTHROPIC] Added assistant message with %d tool_use blocks (IDs: %v) before tool_result message", len(toolUseBlocks), toolResultIDs)
							}
							break
						}
					}
				}
			}
		}

		anthropicMsg := map[string]interface{}{
			"role": v.convertRole(msg.Role),
		}

		// Convert parts to Anthropic content format
		content := make([]map[string]interface{}, 0)

		// For AI messages with tool calls, look ahead to see which tool calls will need to be
		// in inserted assistant messages (when tool results don't match all tool calls)
		// This prevents duplicates when we later insert assistant messages
		toolCallsToSkip := make(map[string]bool)
		if msg.Role == llmtypes.ChatMessageTypeAI {
			// Collect all tool call IDs from this AI message
			aiToolCallIDs := make(map[string]bool)
			for _, part := range msg.Parts {
				if toolCall, ok := part.(llmtypes.ToolCall); ok {
					aiToolCallIDs[toolCall.ID] = true
				}
			}

			// Look ahead through future messages to see which tool calls will need to be
			// in inserted assistant messages (when tool results don't match all tool calls)
			if len(aiToolCallIDs) > 0 && i+1 < len(messages) {
				// Collect all tool result IDs from future messages until we find an AI or system message
				// or until we've found results for all tool calls
				var futureToolResultIDs []string
				for j := i + 1; j < len(messages); j++ {
					futureMsg := messages[j]
					// Stop if we hit an AI message (new turn) or system message
					if futureMsg.Role == llmtypes.ChatMessageTypeAI || futureMsg.Role == llmtypes.ChatMessageTypeSystem {
						break
					}
					// Collect tool result IDs from this message (tool results come as Tool or User messages)
					for _, part := range futureMsg.Parts {
						if toolResp, ok := part.(llmtypes.ToolCallResponse); ok {
							futureToolResultIDs = append(futureToolResultIDs, toolResp.ToolCallID)
						}
					}
					// If we've found results for all tool calls, we can stop looking ahead
					if len(futureToolResultIDs) >= len(aiToolCallIDs) {
						break
					}
				}

				// If we found tool results in future messages, check if they come in separate messages
				// (which means each will trigger an assistant message insertion)
				// If so, we need to skip those tool calls in the current AI message to prevent duplicates
				if len(futureToolResultIDs) > 0 {
					// Check if tool results come in separate messages (one result per message)
					// This indicates that assistant messages will be inserted for each one
					toolResultMessages := 0
					for j := i + 1; j < len(messages); j++ {
						futureMsg := messages[j]
						// Stop if we hit an AI message (new turn) or system message
						if futureMsg.Role == llmtypes.ChatMessageTypeAI || futureMsg.Role == llmtypes.ChatMessageTypeSystem {
							break
						}
						// Count messages that have tool results
						hasToolResult := false
						for _, part := range futureMsg.Parts {
							if _, ok := part.(llmtypes.ToolCallResponse); ok {
								hasToolResult = true
								break
							}
						}
						if hasToolResult {
							toolResultMessages++
						}
					}

					// If tool results come in separate messages (one per message), then assistant messages
					// will be inserted for each one, so we should skip ALL tool calls that have results
					// This handles the structured output case where each tool call gets its own result message
					if toolResultMessages > 0 && toolResultMessages == len(futureToolResultIDs) {
						// Tool results come in separate messages - skip all tool calls that have results
						// This prevents duplicates when conversation.go creates separate tool result messages
						for _, resultID := range futureToolResultIDs {
							if aiToolCallIDs[resultID] {
								toolCallsToSkip[resultID] = true
								if v.logger != nil {
									v.logger.Infof("🔧 [VERTEX ANTHROPIC] Will skip tool call %s in AI message - will be in inserted assistant message (separate tool result messages detected: %d messages for %d results)", resultID, toolResultMessages, len(futureToolResultIDs))
								}
							}
						}
					} else if len(futureToolResultIDs) > 0 {
						// Tool results might be in the same message or not all tool calls have results
						// Skip only the ones that have results to prevent duplicates
						for _, resultID := range futureToolResultIDs {
							if aiToolCallIDs[resultID] {
								toolCallsToSkip[resultID] = true
								if v.logger != nil {
									v.logger.Infof("🔧 [VERTEX ANTHROPIC] Will skip tool call %s in AI message - will be in inserted assistant message", resultID)
								}
							}
						}
					}
				}
			}
		}

		if v.logger != nil {
			v.logger.Infof("🔍 [VERTEX ANTHROPIC] Converting message with %d parts, role: %s", len(msg.Parts), msg.Role)
		}

		for i, part := range msg.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				if v.logger != nil {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Part %d: TextContent, text length: %d, text preview: %.50s", i+1, len(p.Text), p.Text)
				}
				content = append(content, map[string]interface{}{
					"type": "text",
					"text": p.Text,
				})
			case llmtypes.ImageContent:
				if v.logger != nil {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Part %d: ImageContent, data length: %d, mediaType: %s", i+1, len(p.Data), p.MediaType)
				}
				// Handle image content
				imageBlock := v.createImageBlock(p)
				if imageBlock != nil {
					content = append(content, imageBlock)
				} else {
					if v.logger != nil {
						v.logger.Infof("🔍 [VERTEX ANTHROPIC] Part %d: ImageContent created nil imageBlock", i+1)
					}
				}
			case llmtypes.ToolCallResponse:
				// Anthropic uses tool_result format
				hasToolResults = true
				toolResultIDs = append(toolResultIDs, p.ToolCallID)
				content = append(content, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": p.ToolCallID,
					"content":     p.Content,
				})
			case llmtypes.ToolCall:
				// Tool calls in assistant messages should be converted to tool_use blocks
				if msg.Role == llmtypes.ChatMessageTypeAI {
					// Check if this tool call ID was already converted in an inserted assistant message
					// This prevents duplicate tool_use IDs when we inserted an assistant message earlier
					if convertedToolCallIDs[p.ID] {
						if v.logger != nil {
							v.logger.Infof("🔧 [VERTEX ANTHROPIC] Skipping tool call %s (tool: %s) - already converted in inserted assistant message", p.ID, p.FunctionCall.Name)
						}
						continue
					}
					// Check if this tool call should be skipped because it will be in an inserted assistant message
					if toolCallsToSkip[p.ID] {
						// Track that this tool call was skipped
						skippedToolCallIDs[p.ID] = true
						if v.logger != nil {
							v.logger.Infof("🔧 [VERTEX ANTHROPIC] Skipping tool call %s (tool: %s) - will be in inserted assistant message", p.ID, p.FunctionCall.Name)
						}
						continue
					}
					// Convert ToolCall to Anthropic tool_use format
					var inputData map[string]interface{}
					if err := json.Unmarshal([]byte(p.FunctionCall.Arguments), &inputData); err != nil {
						// If arguments aren't valid JSON, create empty map
						inputData = make(map[string]interface{})
					}
					content = append(content, map[string]interface{}{
						"type":  "tool_use",
						"id":    p.ID,
						"name":  p.FunctionCall.Name,
						"input": inputData,
					})
					if v.logger != nil {
						v.logger.Infof("🔧 [VERTEX ANTHROPIC] Converted tool call %s (tool: %s) to tool_use block", p.ID, p.FunctionCall.Name)
					}
				} else {
					// Tool calls in non-assistant messages are unusual
					if v.logger != nil {
						v.logger.Infof("ToolCall found in non-assistant message - this is unusual")
					}
				}
			default:
				// Log any unhandled part types
				if v.logger != nil {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Part %d: Unhandled part type: %T, value: %+v", i+1, part, part)
				}
			}
		}

		if v.logger != nil {
			v.logger.Infof("🔍 [VERTEX ANTHROPIC] Final content array has %d blocks for message with role: %s", len(content), msg.Role)
			for i, block := range content {
				if blockType, ok := block["type"].(string); ok {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Content block %d: type=%s", i+1, blockType)
					if blockType == "text" {
						if text, ok := block["text"].(string); ok {
							v.logger.Infof("🔍 [VERTEX ANTHROPIC]   Text content preview: %.100s", text)
						}
					}
				} else if _, hasSource := block["source"]; hasSource {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Content block %d: image block (has source)", i+1)
				}
			}
		}

		anthropicMsg["content"] = content

		// For messages with both image and text, log the actual JSON structure
		if v.logger != nil && len(content) >= 2 {
			hasImage := false
			hasText := false
			for _, block := range content {
				if blockType, ok := block["type"].(string); ok {
					if blockType == "text" {
						hasText = true
					} else if blockType == "image" {
						hasImage = true
					}
				} else if _, hasSource := block["source"]; hasSource {
					hasImage = true
				}
			}
			if hasImage && hasText {
				// Log the JSON structure of this message (without base64 data for readability)
				// Create a copy for logging that truncates base64 data
				logMsg := make(map[string]interface{})
				for k, v := range anthropicMsg {
					if k == "content" {
						// Process content array to truncate base64 data
						if contentArr, ok := v.([]map[string]interface{}); ok {
							logContent := make([]map[string]interface{}, 0, len(contentArr))
							for _, block := range contentArr {
								logBlock := make(map[string]interface{})
								for bk, bv := range block {
									// Only truncate base64 data in image blocks (blocks with "source" key)
									// Text blocks (with "type": "text" and "text": "...") are kept fully visible
									if bk == "source" {
										// This is an image block - truncate the base64 data
										if sourceMap, ok := bv.(map[string]interface{}); ok {
											logSource := make(map[string]interface{})
											for sk, sv := range sourceMap {
												if sk == "data" {
													// Truncate base64 image data to 100 chars for logging
													if dataStr, ok := sv.(string); ok {
														if len(dataStr) > 100 {
															logSource[sk] = dataStr[:100] + "... [truncated, total: " + fmt.Sprintf("%d", len(dataStr)) + " chars]"
														} else {
															logSource[sk] = sv
														}
													} else {
														logSource[sk] = sv
													}
												} else {
													// Keep other source fields (type, media_type) fully visible
													logSource[sk] = sv
												}
											}
											logBlock[bk] = logSource
										} else {
											logBlock[bk] = bv
										}
									} else {
										// All other fields (type, text, etc.) are kept fully visible
										logBlock[bk] = bv
									}
								}
								logContent = append(logContent, logBlock)
							}
							logMsg[k] = logContent
						} else {
							logMsg[k] = v
						}
					} else {
						logMsg[k] = v
					}
				}
				msgJSON, err := json.MarshalIndent(logMsg, "", "  ")
				if err == nil {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Message with image+text JSON (base64 data truncated):\n%s", string(msgJSON))
				} else {
					v.logger.Infof("🔍 [VERTEX ANTHROPIC] Failed to marshal message JSON: %v", err)
				}
			}
		}

		anthropicMessages = append(anthropicMessages, anthropicMsg)
	}

	return anthropicMessages, nil
}

// convertRole converts llmtypes role to Anthropic role
func (v *VertexAnthropicAdapter) convertRole(role llmtypes.ChatMessageType) string {
	switch role {
	case llmtypes.ChatMessageTypeSystem:
		return "user" // Anthropic doesn't have system role, use user
	case llmtypes.ChatMessageTypeHuman:
		return "user"
	case llmtypes.ChatMessageTypeAI:
		return "assistant"
	case llmtypes.ChatMessageTypeTool:
		return "user" // Tool responses are sent as user messages
	default:
		return "user"
	}
}

// convertToolsToAnthropic converts llmtypes tools to Anthropic format
func (v *VertexAnthropicAdapter) convertToolsToAnthropic(tools []llmtypes.Tool) []map[string]interface{} {
	anthropicTools := make([]map[string]interface{}, 0, len(tools))

	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}

		anthropicTool := map[string]interface{}{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
		}

		// Convert parameters schema
		if tool.Function.Parameters != nil {
			params := make(map[string]interface{})
			if tool.Function.Parameters.Type != "" {
				params["type"] = tool.Function.Parameters.Type
			}
			if tool.Function.Parameters.Properties != nil {
				params["properties"] = tool.Function.Parameters.Properties
			}
			if tool.Function.Parameters.Required != nil {
				params["required"] = tool.Function.Parameters.Required
			}
			for key, value := range tool.Function.Parameters.Additional {
				if _, typed := params[key]; !typed {
					params[key] = value
				}
			}
			anthropicTool["input_schema"] = llmtypes.ModelToolSchema(params)
		}

		anthropicTools = append(anthropicTools, anthropicTool)
	}

	return anthropicTools
}

// parseToolUse parses a tool_use block from Anthropic response
func (v *VertexAnthropicAdapter) parseToolUse(block map[string]interface{}) *llmtypes.ToolCall {
	id, _ := block["id"].(string)
	name, _ := block["name"].(string)
	input, _ := block["input"].(map[string]interface{})

	// Convert input to JSON string
	inputJSON, err := json.Marshal(input)
	if err != nil {
		if v.logger != nil {
			v.logger.Infof("Failed to marshal tool input: %v", err)
		}
		inputJSON = []byte("{}")
	}

	return &llmtypes.ToolCall{
		ID:   id,
		Type: "function",
		FunctionCall: &llmtypes.FunctionCall{
			Name:      name,
			Arguments: string(inputJSON),
		},
	}
}

// getMaxTokens returns max tokens from options or default
func (v *VertexAnthropicAdapter) getMaxTokens(opts *llmtypes.CallOptions) int {
	if opts.MaxTokens > 0 {
		return opts.MaxTokens
	}
	return 512 // Default
}

// getTemperature returns temperature from options or default
func (v *VertexAnthropicAdapter) getTemperature(opts *llmtypes.CallOptions) float64 {
	if opts.Temperature > 0 {
		return opts.Temperature
	}
	return 1.0 // Default
}

// addJSONModeInstructions adds JSON mode instructions to messages (similar to direct Anthropic adapter)
// This ensures structured output works correctly with Vertex Anthropic models
func (v *VertexAnthropicAdapter) addJSONModeInstructions(messages []llmtypes.MessageContent) []llmtypes.MessageContent {
	jsonInstruction := "You must respond with valid JSON only, no other text. Return a JSON object."

	// Create a copy of messages to avoid modifying the original
	result := make([]llmtypes.MessageContent, len(messages))
	copy(result, messages)

	// Find system message and append JSON instruction
	for i := range result {
		if result[i].Role == llmtypes.ChatMessageTypeSystem {
			// Append JSON instruction to system message
			if len(result[i].Parts) > 0 {
				if textPart, ok := result[i].Parts[0].(llmtypes.TextContent); ok {
					result[i].Parts[0] = llmtypes.TextContent{
						Text: textPart.Text + "\n\n" + jsonInstruction,
					}
					return result
				}
			}
		}
	}

	// If no system message, prepend JSON instruction to first user message
	if len(result) > 0 && result[0].Role == llmtypes.ChatMessageTypeHuman {
		if len(result[0].Parts) > 0 {
			if textPart, ok := result[0].Parts[0].(llmtypes.TextContent); ok {
				result[0].Parts[0] = llmtypes.TextContent{
					Text: jsonInstruction + "\n\n" + textPart.Text,
				}
			}
		}
	}

	return result
}

// Call implements a convenience method for simple text generation
func (v *VertexAnthropicAdapter) Call(ctx context.Context, prompt string, options ...llmtypes.CallOption) (string, error) {
	messages := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: prompt},
			},
		},
	}

	resp, err := v.GenerateContent(ctx, messages, options...)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return resp.Choices[0].Content, nil
}

// createImageBlock creates an Anthropic image content block from ImageContent
// Note: Vertex AI Anthropic API uses Anthropic format for images
func (v *VertexAnthropicAdapter) createImageBlock(img llmtypes.ImageContent) map[string]interface{} {
	if img.SourceType == "base64" {
		// Anthropic format for base64 images
		return map[string]interface{}{
			"type": "image",
			"source": map[string]interface{}{
				"type":       "base64",
				"media_type": img.MediaType,
				"data":       img.Data,
			},
		}
	} else if img.SourceType == "url" {
		// Vertex AI Anthropic doesn't support URL images directly
		// We need to fetch and convert to base64
		if v.logger != nil {
			v.logger.Infof("Fetching image from URL and converting to base64: %s", img.Data)
		}

		// Fetch image from URL
		// Note: context is not available here, use background context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		imageBytes, mediaType, err := v.fetchImageFromURL(ctx, img.Data)
		if err != nil {
			if v.logger != nil {
				v.logger.Errorf("Failed to fetch image from URL: %v", err)
			}
			return nil
		}

		// Encode to base64
		base64Data := base64.StdEncoding.EncodeToString(imageBytes)

		if v.logger != nil {
			v.logger.Infof("Converted URL image to base64: %d bytes, MIME type: %s", len(imageBytes), mediaType)
		}

		// Return as base64 image using Anthropic format
		return map[string]interface{}{
			"type": "image",
			"source": map[string]interface{}{
				"type":       "base64",
				"media_type": mediaType,
				"data":       base64Data,
			},
		}
	}
	// Invalid source type
	if v.logger != nil {
		v.logger.Infof("Invalid image source type: %s", img.SourceType)
	}
	return nil
}

// getMapKeys returns the keys of a map as a slice of strings (for logging)
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// fetchImageFromURL fetches an image from a URL and returns the bytes and MIME type
func (v *VertexAnthropicAdapter) fetchImageFromURL(ctx context.Context, url string) ([]byte, string, error) {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Fetch the image with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch image: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read image data
	imageBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Detect MIME type from Content-Type header or URL extension
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" || !strings.HasPrefix(mimeType, "image/") {
		// Try to detect from URL extension
		urlLower := strings.ToLower(url)
		if strings.HasSuffix(urlLower, ".jpg") || strings.HasSuffix(urlLower, ".jpeg") {
			mimeType = "image/jpeg"
		} else if strings.HasSuffix(urlLower, ".png") {
			mimeType = "image/png"
		} else if strings.HasSuffix(urlLower, ".gif") {
			mimeType = "image/gif"
		} else if strings.HasSuffix(urlLower, ".webp") {
			mimeType = "image/webp"
		} else {
			// Default to JPEG if we can't determine
			mimeType = "image/jpeg"
		}
	}

	// Clean up MIME type (remove charset if present)
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = mimeType[:idx]
	}

	return imageBytes, strings.TrimSpace(mimeType), nil
}

// logRawInput logs the complete raw request payload that will be sent to the Vertex Anthropic API
func (v *VertexAnthropicAdapter) logRawInput(requestID, modelID string, payload map[string]interface{}) {
	if v.logger == nil {
		return
	}

	rawInput := map[string]interface{}{
		"request_id": requestID,
		"model_id":   modelID,
		"payload":    payload,
	}

	rawInputJSON, _ := json.MarshalIndent(rawInput, "", "  ")
	v.logger.Debugf("🔍 [REQUEST_ID: %s] RAW VERTEX ANTHROPIC API INPUT (FULL JSON):\n%s", requestID, string(rawInputJSON))
}

// logRawResponse logs the complete raw response from the Vertex Anthropic API
func (v *VertexAnthropicAdapter) logRawResponse(requestID, modelID string, responseSummary map[string]interface{}, err error) {
	if v.logger == nil {
		return
	}

	if err != nil {
		errorInfo := map[string]interface{}{
			"request_id": requestID,
			"model_id":   modelID,
			"error":      err.Error(),
			"error_type": fmt.Sprintf("%T", err),
		}
		errorJSON, _ := json.MarshalIndent(errorInfo, "", "  ")
		v.logger.Errorf("❌ [REQUEST_ID: %s] RAW VERTEX ANTHROPIC API ERROR (FULL JSON):\n%s", requestID, string(errorJSON))
		return
	}

	if responseSummary == nil {
		return
	}

	// Build complete response structure
	responseInfo := map[string]interface{}{
		"request_id": requestID,
		"model_id":   modelID,
	}

	// Copy all fields from responseSummary
	for k, v := range responseSummary {
		responseInfo[k] = v
	}

	responseJSON, _ := json.MarshalIndent(responseInfo, "", "  ")
	v.logger.Debugf("🔍 [REQUEST_ID: %s] COMPLETE RAW VERTEX ANTHROPIC API RESPONSE (FULL JSON):\n%s", requestID, string(responseJSON))
}
