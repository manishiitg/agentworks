package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/internal/recorder"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// BedrockAdapter is an adapter that implements llmtypes.Model interface
// using the AWS Bedrock Converse API
type BedrockAdapter struct {
	client  *bedrockruntime.Client
	modelID string
	logger  interfaces.Logger
}

// NewBedrockAdapter creates a new adapter instance
func NewBedrockAdapter(client *bedrockruntime.Client, modelID string, logger interfaces.Logger) *BedrockAdapter {
	return &BedrockAdapter{
		client:  client,
		modelID: modelID,
		logger:  logger,
	}
}

// GetModelID implements the llmtypes.Model interface
func (b *BedrockAdapter) GetModelID() string {
	return b.modelID
}

// GetModelMetadata implements the llmtypes.Model interface
// attachCostEstimate fills GenerationInfo.Additional["cost_usd_estimated"]
// from tokens × registry rates. Bedrock's Converse API has no USD field.
func (b *BedrockAdapter) attachCostEstimate(resp *llmtypes.ContentResponse, modelID string) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return
	}
	meta, err := b.GetModelMetadata(modelID)
	if err != nil || meta == nil {
		return
	}
	gi := resp.Choices[0].GenerationInfo
	cost := llmtypes.ComputeUSDCostFromMetadata(meta, gi)
	if cost <= 0 {
		return
	}
	if gi.Additional == nil {
		gi.Additional = map[string]interface{}{}
	}
	gi.Additional["cost_usd_estimated"] = cost
	gi.Additional["cost_model_id"] = modelID
}

func (b *BedrockAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	return GetBedrockModelMetadata(modelID)
}

// GenerateContent implements the llmtypes.Model interface
func (b *BedrockAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Parse call options
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}

	// Determine model ID (from option or default)
	modelID := b.modelID
	if opts.Model != "" {
		modelID = opts.Model
	}

	return llmtypes.WithObservability(ctx, llmtypes.ObservabilityConfig{
		Provider:     "bedrock",
		Model:        modelID,
		Opts:         opts,
		MessageCount: len(messages),
		Messages:     messages,
		HeaderLine:   fmt.Sprintf("bedrock.converse model=%s msgs=%d tools=%d", modelID, len(messages), len(opts.Tools)),
		// Not yet reachable from AgentWorks workflow steps, but the CLI
		// adapters already declare "tmux" / "structured_cli" here so their
		// transport-class chip renders correctly; this is the same
		// declaration for a direct API call, so the label is correct the
		// day this adapter IS wired in.
		RequestMetaExtra: map[string]interface{}{"transport": "api"},
	}, func(sink *llmtypes.StreamSink) (*llmtypes.ContentResponse, error) {
		_ = sink
		resp, err := b.generateContentInner(ctx, opts, modelID, messages)
		if err == nil {
			b.attachCostEstimate(resp, modelID)
		}
		return resp, err
	})
}

func (b *BedrockAdapter) generateContentInner(ctx context.Context, opts *llmtypes.CallOptions, modelID string, messages []llmtypes.MessageContent) (*llmtypes.ContentResponse, error) {
	// Convert messages to Converse API format
	converseMessages := convertMessagesToConverse(messages)

	// Extract system message if present
	var systemMessage []types.SystemContentBlock
	for _, msg := range messages {
		if string(msg.Role) == string(llmtypes.ChatMessageTypeSystem) {
			for _, part := range msg.Parts {
				if textPart, ok := part.(llmtypes.TextContent); ok {
					systemMessage = append(systemMessage, &types.SystemContentBlockMemberText{
						Value: textPart.Text,
					})
				}
			}
		}
	}

	// Build inference configuration
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	// Clamp to int32 max to prevent integer overflow
	if maxTokens > math.MaxInt32 {
		maxTokens = math.MaxInt32
	}
	inferenceConfig := &types.InferenceConfiguration{
		MaxTokens: aws.Int32(int32(maxTokens)),
	}
	if opts.Temperature > 0 {
		temp := float32(opts.Temperature)
		inferenceConfig.Temperature = &temp
	}

	// Handle JSON mode via AdditionalModelRequestFields
	// TODO: Verify correct format - current attempts with response_format are failing with validation errors
	// Attempted formats that failed:
	// - {"response_format": {"type": "json_object"}}
	// - {"response_format": "json"}
	// Error: "The format of the additionalModelRequestFields field is invalid"
	//
	// Research suggests structured output might need to use toolConfig with JSON schema instead
	// For now, using prompt-based approach as fallback until correct format is confirmed from AWS docs
	// TODO: Implement proper AdditionalModelRequestFields format once confirmed
	// var additionalFields document.Interface
	if opts.JSONMode {
		// Add JSON instruction to first user message as fallback
		// This ensures pure JSON output without markdown code blocks
		if len(converseMessages) > 0 && len(converseMessages[0].Content) > 0 {
			jsonInstruction := &types.ContentBlockMemberText{
				Value: "You must respond with valid JSON only. Return pure JSON with no markdown code blocks, no ```json markers, no explanations, and no additional text. The response must be valid JSON that can be parsed directly.",
			}
			converseMessages[0].Content = append([]types.ContentBlock{jsonInstruction}, converseMessages[0].Content...)
		}
	}

	// Convert tools if provided
	var tools []types.Tool
	var toolConfig *types.ToolConfiguration
	if len(opts.Tools) > 0 {
		tools = b.convertToolsToConverse(opts.Tools)
		toolConfig = &types.ToolConfiguration{
			Tools: tools,
		}

		// Handle tool choice
		if opts.ToolChoice != nil {
			toolChoice := convertToolChoiceToConverse(opts.ToolChoice)
			if toolChoice != nil {
				toolConfig.ToolChoice = toolChoice
			}
		}
	}

	// Build Converse API input
	converseInput := &bedrockruntime.ConverseInput{
		ModelId:         aws.String(modelID),
		Messages:        converseMessages,
		InferenceConfig: inferenceConfig,
	}

	if len(systemMessage) > 0 {
		converseInput.System = systemMessage
	}

	// TODO: Set AdditionalModelRequestFields once correct format is confirmed
	// if additionalFields != nil {
	// 	converseInput.AdditionalModelRequestFields = additionalFields
	// }

	if toolConfig != nil {
		converseInput.ToolConfig = toolConfig
	}

	// Log input details if logger is available (for debugging errors)
	if b.logger != nil {
		b.logInputDetailsConverse(modelID, messages, converseInput, opts)
	}

	// Always use streaming internally - for non-streaming requests, StreamChan is nil
	// and we accumulate internally without sending chunks to the channel
	return b.generateContentStreaming(ctx, modelID, converseInput, opts, messages)
}

// generateContentStreaming handles streaming responses from Bedrock ConverseStream API
func (b *BedrockAdapter) generateContentStreaming(ctx context.Context, modelID string, converseInput *bedrockruntime.ConverseInput, opts *llmtypes.CallOptions, messages []llmtypes.MessageContent) (*llmtypes.ContentResponse, error) {
	// Check for recorder in context (only if recording/replay might be enabled)
	rec, _ := recorder.FromContext(ctx)
	var recordedEvents []map[string]interface{}

	// Handle replay mode (only build requestInfo if needed)
	if rec != nil && rec.IsReplayEnabled() {
		if b.logger != nil {
			b.logger.Infof("▶️  [RECORDER] Replaying recorded Bedrock events")
		}
		requestInfo := buildRequestInfo(messages, modelID, opts)
		events, err := rec.LoadBedrockEvents(requestInfo)
		if err != nil {
			return nil, fmt.Errorf("failed to load recorded events: %w", err)
		}
		recordedEvents = events

		// Process recorded events as if they came from a live stream
		return b.processRecordedEvents(ctx, recordedEvents, opts, modelID)
	}

	// Convert ConverseInput to ConverseStreamInput
	streamInput := &bedrockruntime.ConverseStreamInput{
		ModelId:         converseInput.ModelId,
		Messages:        converseInput.Messages,
		System:          converseInput.System,
		InferenceConfig: converseInput.InferenceConfig,
		ToolConfig:      converseInput.ToolConfig,
	}

	// Create streaming request
	streamOutput, err := b.client.ConverseStream(ctx, streamInput)
	if err != nil {
		if b.logger != nil {
			b.logErrorDetailsConverse(modelID, nil, converseInput, opts, err, nil)
		}
		return nil, fmt.Errorf("bedrock converse stream: %w", err)
	}

	stream := streamOutput.GetStream()
	defer stream.Close()

	// Ensure channel is closed when done
	defer func() {
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
	}()

	// Accumulate response data
	var accumulatedContent strings.Builder
	var accumulatedToolCalls []llmtypes.ToolCall
	var stopReason string
	var usage *types.TokenUsage

	// Track tool calls by ID (Bedrock streams tool calls incrementally)
	toolCallMap := make(map[string]*llmtypes.ToolCall)
	completedToolCallIDs := make(map[string]bool)

	// Track content block index to tool use ID mapping
	contentBlockIndexToToolUseID := make(map[int32]string)

	// Collect events for recording
	var recordedEventChunks []interface{}

	// Process streaming events from channel
	for event := range stream.Events() {
		// Record event if recording is enabled
		if rec != nil && rec.IsRecordingEnabled() {
			eventJSON, err := json.Marshal(event)
			if err == nil {
				var eventMap map[string]interface{}
				if json.Unmarshal(eventJSON, &eventMap) == nil {
					recordedEventChunks = append(recordedEventChunks, eventMap)
				}
			}
		}
		// Handle different event types
		switch eventVariant := event.(type) {
		case *types.ConverseStreamOutputMemberContentBlockDelta:
			// Content block delta
			deltaEvent := eventVariant.Value
			if deltaEvent.Delta != nil {
				switch deltaVariant := deltaEvent.Delta.(type) {
				case *types.ContentBlockDeltaMemberText:
					// Text content delta
					if deltaVariant.Value != "" {
						accumulatedContent.WriteString(deltaVariant.Value)

						// Stream content chunks immediately
						if opts.StreamChan != nil {
							select {
							case opts.StreamChan <- llmtypes.StreamChunk{
								Type:    llmtypes.StreamChunkTypeContent,
								Content: deltaVariant.Value,
							}:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
					}
				case *types.ContentBlockDeltaMemberToolUse:
					// Tool use delta - accumulate incrementally
					toolUseDelta := deltaVariant.Value
					// Get tool use ID from index mapping
					contentBlockIndex := aws.ToInt32(deltaEvent.ContentBlockIndex)
					toolUseID, exists := contentBlockIndexToToolUseID[contentBlockIndex]
					if !exists {
						// Skip if we don't have the tool use ID yet (shouldn't happen)
						if b.logger != nil {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockDeltaMemberToolUse: No toolUseID found for contentBlockIndex %d", contentBlockIndex)
						}
						continue
					}

					// Ensure tool call exists
					if toolCallMap[toolUseID] == nil {
						toolCallMap[toolUseID] = &llmtypes.ToolCall{
							ID:   toolUseID,
							Type: "function",
							FunctionCall: &llmtypes.FunctionCall{
								Name:      "",
								Arguments: "{}",
							},
						}
						if b.logger != nil {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockDeltaMemberToolUse: Created new tool call entry for ID %s (no existing entry)", toolUseID)
						}
					}

					// Log Input details for debugging
					if b.logger != nil {
						if toolUseDelta.Input == nil {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockDeltaMemberToolUse: toolUseID=%s, Input is NIL", toolUseID)
						} else if *toolUseDelta.Input == "" {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockDeltaMemberToolUse: toolUseID=%s, Input is EMPTY STRING", toolUseID)
						} else {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockDeltaMemberToolUse: toolUseID=%s, Input length=%d, Input=%q", toolUseID, len(*toolUseDelta.Input), *toolUseDelta.Input)
						}
					}

					// Accumulate input (arguments) - Input is a string in delta
					// CRITICAL: Bedrock sends INCREMENTAL FRAGMENTS in each delta, not complete JSON
					// We need to accumulate (concatenate) the fragments, not overwrite
					if toolUseDelta.Input != nil && *toolUseDelta.Input != "" {
						// Accumulate by appending to existing arguments (fragments are incremental)
						currentArgs := toolCallMap[toolUseID].FunctionCall.Arguments
						if currentArgs == "{}" {
							// First fragment - start fresh
							toolCallMap[toolUseID].FunctionCall.Arguments = *toolUseDelta.Input
						} else {
							// Subsequent fragments - append to accumulate
							toolCallMap[toolUseID].FunctionCall.Arguments = currentArgs + *toolUseDelta.Input
						}
						if b.logger != nil {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockDeltaMemberToolUse: toolUseID=%s, Accumulated arguments: %q (added fragment: %q)", toolUseID, toolCallMap[toolUseID].FunctionCall.Arguments, *toolUseDelta.Input)
						}
					} else {
						if b.logger != nil {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockDeltaMemberToolUse: toolUseID=%s, NOT updating arguments (Input is nil or empty), current args=%q", toolUseID, toolCallMap[toolUseID].FunctionCall.Arguments)
						}
					}
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStart:
			// Content block started
			startEvent := eventVariant.Value
			if startEvent.Start != nil {
				switch startVariant := startEvent.Start.(type) {
				case *types.ContentBlockStartMemberToolUse:
					// Tool use block started
					toolUseStart := startVariant.Value
					toolUseID := aws.ToString(toolUseStart.ToolUseId)
					toolName := aws.ToString(toolUseStart.Name)
					contentBlockIndex := aws.ToInt32(startEvent.ContentBlockIndex)

					// Map index to tool use ID
					contentBlockIndexToToolUseID[contentBlockIndex] = toolUseID

					// Initialize tool call
					toolCallMap[toolUseID] = &llmtypes.ToolCall{
						ID:   toolUseID,
						Type: "function",
						FunctionCall: &llmtypes.FunctionCall{
							Name:      toolName,
							Arguments: "{}",
						},
					}

					// Log tool use start
					if b.logger != nil {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStartMemberToolUse: toolUseID=%s, toolName=%s, contentBlockIndex=%d, initialized with empty arguments", toolUseID, toolName, contentBlockIndex)
					}
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			// Content block stopped (tool use block complete)
			stopEvent := eventVariant.Value
			contentBlockIndex := aws.ToInt32(stopEvent.ContentBlockIndex)
			toolUseID, exists := contentBlockIndexToToolUseID[contentBlockIndex]
			if !exists {
				continue
			}

			// Mark as complete and validate arguments before streaming
			if !completedToolCallIDs[toolUseID] && toolCallMap[toolUseID] != nil {
				completedToolCallIDs[toolUseID] = true

				// Validate and sanitize arguments now that the tool call is complete
				toolCall := toolCallMap[toolUseID]
				toolName := ""
				if toolCall.FunctionCall != nil {
					toolName = toolCall.FunctionCall.Name
				}

				// Log arguments before sanitization
				if b.logger != nil {
					if toolCall.FunctionCall == nil {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, FunctionCall is NIL", toolUseID, toolName)
					} else if toolCall.FunctionCall.Arguments == "" {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, Arguments is EMPTY STRING", toolUseID, toolName)
					} else if toolCall.FunctionCall.Arguments == "{}" {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, Arguments is EMPTY OBJECT %q", toolUseID, toolName, toolCall.FunctionCall.Arguments)
					} else {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, Arguments BEFORE sanitization: %q (length=%d)", toolUseID, toolName, toolCall.FunctionCall.Arguments, len(toolCall.FunctionCall.Arguments))
					}
				}

				if toolCall.FunctionCall != nil && toolCall.FunctionCall.Arguments != "" {
					originalArgs := toolCall.FunctionCall.Arguments
					sanitizedArgs := validateAndSanitizeJSON(originalArgs)
					toolCall.FunctionCall.Arguments = sanitizedArgs

					// Log sanitization results
					if b.logger != nil {
						if sanitizedArgs == "{}" && originalArgs != "{}" {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, Invalid JSON detected: original=%q, sanitized=%q", toolUseID, toolName, originalArgs, sanitizedArgs)
						} else if sanitizedArgs != originalArgs {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, Arguments reformatted: original=%q, sanitized=%q", toolUseID, toolName, originalArgs, sanitizedArgs)
						} else {
							b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, Arguments AFTER sanitization: %q (unchanged)", toolUseID, toolName, sanitizedArgs)
						}
					}
				} else {
					if b.logger != nil {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, No arguments to sanitize (FunctionCall is nil or Arguments is empty)", toolUseID, toolName)
					}
				}

				// Stream complete tool call
				if opts.StreamChan != nil {
					// Create a copy to avoid pointer issues
					toolCallCopy := *toolCall
					finalArgs := "{}"
					if toolCallCopy.FunctionCall != nil {
						finalArgs = toolCallCopy.FunctionCall.Arguments
					}
					if b.logger != nil {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, toolName=%s, Streaming tool call with final arguments: %q", toolUseID, toolName, finalArgs)
					}
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
				if b.logger != nil {
					if completedToolCallIDs[toolUseID] {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, Already completed, skipping", toolUseID)
					} else if toolCallMap[toolUseID] == nil {
						b.logger.Debugf("[BEDROCK STREAM] ContentBlockStop: toolUseID=%s, Tool call not found in map, skipping", toolUseID)
					}
				}
			}

		case *types.ConverseStreamOutputMemberMessageStop:
			// Message stopped - extract stop reason
			messageStop := eventVariant.Value
			if messageStop.StopReason != "" {
				stopReason = string(messageStop.StopReason)
			}

		case *types.ConverseStreamOutputMemberMetadata:
			// Metadata event - extract usage
			metadata := eventVariant.Value
			if metadata.Usage != nil {
				usage = metadata.Usage
			}
		}
	}

	// Check for stream errors
	if err := stream.Err(); err != nil {
		if b.logger != nil {
			b.logErrorDetailsConverse(modelID, nil, converseInput, opts, err, nil)
		}
		return nil, fmt.Errorf("bedrock streaming error: %w", err)
	}

	// Convert accumulated tool calls to slice
	for _, toolCall := range toolCallMap {
		accumulatedToolCalls = append(accumulatedToolCalls, *toolCall)
		// If tool call wasn't streamed yet, stream it now
		if !completedToolCallIDs[toolCall.ID] && opts.StreamChan != nil {
			// Create a copy to avoid pointer issues
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

	// Build final response
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{},
	}

	choice := &llmtypes.ContentChoice{
		Content:    accumulatedContent.String(),
		StopReason: stopReason,
		ToolCalls:  accumulatedToolCalls,
	}

	// Extract token usage
	if usage != nil {
		genInfo := &llmtypes.GenerationInfo{}
		inputTokens := int(aws.ToInt32(usage.InputTokens))
		outputTokens := int(aws.ToInt32(usage.OutputTokens))
		totalTokens := int(aws.ToInt32(usage.TotalTokens))

		genInfo.InputTokens = &inputTokens
		genInfo.OutputTokens = &outputTokens
		genInfo.TotalTokens = &totalTokens
		genInfo.PromptTokens = &inputTokens
		genInfo.CompletionTokens = &outputTokens
		choice.GenerationInfo = genInfo
	}

	resp.Choices = append(resp.Choices, choice)

	// Extract usage from GenerationInfo
	if len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		resp.Usage = llmtypes.ExtractUsageFromGenerationInfo(resp.Choices[0].GenerationInfo)
	}

	// Record events if recording is enabled (only build requestInfo if needed)
	if rec != nil && rec.IsRecordingEnabled() && len(recordedEventChunks) > 0 {
		requestInfo := buildRequestInfo(messages, modelID, opts)
		filePath, err := rec.RecordBedrockEvents(recordedEventChunks, requestInfo)
		if err != nil {
			if b.logger != nil {
				b.logger.Errorf("Failed to save recorded events: %v", err)
			}
		} else if b.logger != nil {
			b.logger.Infof("📹 [RECORDER] Saved %d events to %s", len(recordedEventChunks), filePath)
		}
	}

	return resp, nil
}

// Call implements a convenience method for simple text generation
func (b *BedrockAdapter) Call(ctx context.Context, prompt string, options ...llmtypes.CallOption) (string, error) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, prompt),
	}

	resp, err := b.GenerateContent(ctx, messages, options...)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	// Extract text content from first choice
	// Content is a string in llmtypes
	return resp.Choices[0].Content, nil
}

// GenerateEmbeddings implements the llmtypes.EmbeddingModel interface
// Input can be a single string or a slice of strings
// Supports Amazon Titan Text Embeddings models (v1 and v2)
func (b *BedrockAdapter) GenerateEmbeddings(ctx context.Context, input interface{}, options ...llmtypes.EmbeddingOption) (*llmtypes.EmbeddingResponse, error) {
	// Parse embedding options
	opts := &llmtypes.EmbeddingOptions{
		Model: "amazon.titan-embed-text-v1", // Default model
	}
	for _, opt := range options {
		opt(opts)
	}

	// Use provided model or default
	modelID := opts.Model
	if modelID == "" {
		modelID = "amazon.titan-embed-text-v1"
	}

	// Convert input to slice of strings
	var inputTexts []string
	switch v := input.(type) {
	case string:
		// Validate single string input
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("input cannot be empty")
		}
		inputTexts = []string{v}
	case []string:
		// Array of strings input
		if len(v) == 0 {
			return nil, fmt.Errorf("input cannot be empty")
		}
		// Validate that no string in the array is empty
		for i, text := range v {
			if strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("input at index %d cannot be empty", i)
			}
		}
		inputTexts = v
	default:
		return nil, fmt.Errorf("input must be a string or []string, got %T", input)
	}

	// Determine default dimensions based on model
	defaultDimensions := 1536 // Titan v1 default
	if strings.Contains(modelID, "titan-embed-text-v2") {
		defaultDimensions = 1024 // Titan v2 default
	}

	// Use provided dimensions or default
	dimensions := defaultDimensions
	if opts.Dimensions != nil {
		dimensions = *opts.Dimensions
	}

	// Log input details if logger is available
	if b.logger != nil {
		b.logger.Debugf("Bedrock GenerateEmbeddings INPUT - model: %s, input_count: %d, dimensions: %d",
			modelID, len(inputTexts), dimensions)
	}

	// Process each input text (Bedrock Titan supports single embedding per request)
	// For batch, we'll make multiple requests
	embeddings := make([]llmtypes.Embedding, 0, len(inputTexts))
	var totalPromptTokens int

	for i, text := range inputTexts {
		// Build request body for Titan embedding
		requestBody := map[string]interface{}{
			"inputText": text,
		}

		// Add dimensions for Titan v2 (v1 doesn't support custom dimensions)
		if strings.Contains(modelID, "titan-embed-text-v2") && dimensions != 1024 {
			requestBody["dimensions"] = dimensions
		}

		// Marshal request body to JSON
		bodyJSON, err := json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}

		// Create InvokeModel input
		invokeInput := &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(modelID),
			Body:        bodyJSON,
			ContentType: aws.String("application/json"),
			Accept:      aws.String("application/json"),
		}

		// Call InvokeModel
		result, err := b.client.InvokeModel(ctx, invokeInput)
		if err != nil {
			if b.logger != nil {
				b.logger.Errorf("Bedrock GenerateEmbeddings ERROR - model: %s, input_index: %d, error: %v", modelID, i, err)
			}
			return nil, fmt.Errorf("bedrock invoke model: %w", err)
		}

		// Parse response (result.Body is already []byte)
		var embeddingResponse struct {
			Embedding       []float64 `json:"embedding"`
			InputTokenCount int       `json:"inputTokenCount,omitempty"`
		}
		if err := json.Unmarshal(result.Body, &embeddingResponse); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		// Convert float64 to float32
		embedding32 := make([]float32, len(embeddingResponse.Embedding))
		for j, v := range embeddingResponse.Embedding {
			embedding32[j] = float32(v)
		}

		embeddings = append(embeddings, llmtypes.Embedding{
			Index:     i,
			Embedding: embedding32,
			Object:    "embedding",
		})

		// Accumulate token usage
		if embeddingResponse.InputTokenCount > 0 {
			totalPromptTokens += embeddingResponse.InputTokenCount
		}
	}

	// Build response
	response := &llmtypes.EmbeddingResponse{
		Embeddings: embeddings,
		Model:      modelID,
		Object:     "list",
	}

	// Add usage information if available
	if totalPromptTokens > 0 {
		response.Usage = &llmtypes.EmbeddingUsage{
			PromptTokens: totalPromptTokens,
			TotalTokens:  totalPromptTokens,
		}
	}

	return response, nil
}

// convertMessagesToConverse converts llmtypes messages to Converse API format
// processRecordedEvents processes recorded events as if they came from a live stream
func (b *BedrockAdapter) processRecordedEvents(ctx context.Context, recordedEvents []map[string]interface{}, opts *llmtypes.CallOptions, modelID string) (*llmtypes.ContentResponse, error) {
	// Accumulate response data (same as live streaming)
	var accumulatedContent strings.Builder
	var accumulatedToolCalls []llmtypes.ToolCall
	var stopReason string
	var usage *types.TokenUsage

	// Track tool calls by ID (Bedrock streams tool calls incrementally)
	toolCallMap := make(map[string]*llmtypes.ToolCall)
	completedToolCallIDs := make(map[string]bool)

	// Track content block index to tool use ID mapping
	contentBlockIndexToToolUseID := make(map[int32]string)

	// Process each recorded event directly from map structure
	for _, eventMap := range recordedEvents {
		// Events are stored as {"Value": {...}} where Value contains the event data
		valueMap, ok := eventMap["Value"].(map[string]interface{})
		if !ok {
			continue
		}

		// Process events directly from the map structure (similar to Vertex approach)
		// Check for ContentBlockDelta (has ContentBlockIndex and Delta)
		if contentBlockIndex, hasIndex := valueMap["ContentBlockIndex"]; hasIndex {
			if deltaMap, hasDelta := valueMap["Delta"].(map[string]interface{}); hasDelta {
				// This is a ContentBlockDelta event
				// Check if it's text content
				if textValue, hasText := deltaMap["Value"].(string); hasText && textValue != "" {
					accumulatedContent.WriteString(textValue)
					if opts.StreamChan != nil {
						select {
						case opts.StreamChan <- llmtypes.StreamChunk{
							Type:    llmtypes.StreamChunkTypeContent,
							Content: textValue,
						}:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				} else if deltaValue, hasDeltaValue := deltaMap["Value"].(map[string]interface{}); hasDeltaValue {
					// Tool use delta - Input is nested under Value
					if toolUseInput, hasToolUse := deltaValue["Input"].(string); hasToolUse {
						index := int32(contentBlockIndex.(float64))
						toolUseID, exists := contentBlockIndexToToolUseID[index]
						if !exists {
							continue
						}

						if toolCallMap[toolUseID] == nil {
							toolCallMap[toolUseID] = &llmtypes.ToolCall{
								ID:   toolUseID,
								Type: "function",
								FunctionCall: &llmtypes.FunctionCall{
									Name:      "",
									Arguments: "{}",
								},
							}
						}

						if toolUseInput != "" {
							currentArgs := toolCallMap[toolUseID].FunctionCall.Arguments
							if currentArgs == "{}" {
								toolCallMap[toolUseID].FunctionCall.Arguments = toolUseInput
							} else {
								toolCallMap[toolUseID].FunctionCall.Arguments = currentArgs + toolUseInput
							}
						}
					}
				}
			} else if startMap, hasStart := valueMap["Start"].(map[string]interface{}); hasStart {
				// This is a ContentBlockStart event
				// Start can be ToolUse, which has a nested Value structure
				if startValue, hasValue := startMap["Value"].(map[string]interface{}); hasValue {
					// ToolUse start event
					if toolUseID, hasID := startValue["ToolUseId"].(string); hasID {
						toolName := ""
						if name, ok := startValue["Name"].(string); ok {
							toolName = name
						}
						index := int32(contentBlockIndex.(float64))

						contentBlockIndexToToolUseID[index] = toolUseID
						toolCallMap[toolUseID] = &llmtypes.ToolCall{
							ID:   toolUseID,
							Type: "function",
							FunctionCall: &llmtypes.FunctionCall{
								Name:      toolName,
								Arguments: "{}",
							},
						}
					}
				}
			} else {
				// This is a ContentBlockStop event
				index := int32(contentBlockIndex.(float64))
				toolUseID, exists := contentBlockIndexToToolUseID[index]
				if !exists {
					continue
				}

				if !completedToolCallIDs[toolUseID] && toolCallMap[toolUseID] != nil {
					completedToolCallIDs[toolUseID] = true
					toolCall := toolCallMap[toolUseID]

					if toolCall.FunctionCall != nil && toolCall.FunctionCall.Arguments != "" {
						originalArgs := toolCall.FunctionCall.Arguments
						sanitizedArgs := validateAndSanitizeJSON(originalArgs)
						toolCall.FunctionCall.Arguments = sanitizedArgs
					}

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
		} else if _, hasStopReason := valueMap["StopReason"].(string); hasStopReason {
			// This is a MessageStop event - stopReason value is not needed here
		} else if usageMap, hasUsage := valueMap["Usage"].(map[string]interface{}); hasUsage {
			// This is a Metadata event - reconstruct TokenUsage
			usageJSON, _ := json.Marshal(usageMap)
			var tokenUsage types.TokenUsage
			if json.Unmarshal(usageJSON, &tokenUsage) == nil {
				usage = &tokenUsage
			}
		}
		continue // Skip the switch statement below since we processed directly
	}

	// Convert accumulated tool calls to slice
	for _, toolCall := range toolCallMap {
		accumulatedToolCalls = append(accumulatedToolCalls, *toolCall)
		if !completedToolCallIDs[toolCall.ID] && opts.StreamChan != nil {
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

	// Build final response
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{},
	}

	choice := &llmtypes.ContentChoice{
		Content:    accumulatedContent.String(),
		StopReason: stopReason,
		ToolCalls:  accumulatedToolCalls,
	}

	// Extract token usage
	if usage != nil {
		genInfo := &llmtypes.GenerationInfo{}
		inputTokens := int(aws.ToInt32(usage.InputTokens))
		outputTokens := int(aws.ToInt32(usage.OutputTokens))
		totalTokens := int(aws.ToInt32(usage.TotalTokens))

		genInfo.InputTokens = &inputTokens
		genInfo.OutputTokens = &outputTokens
		genInfo.TotalTokens = &totalTokens
		genInfo.PromptTokens = &inputTokens
		genInfo.CompletionTokens = &outputTokens
		choice.GenerationInfo = genInfo
	}

	resp.Choices = append(resp.Choices, choice)

	// Extract usage from GenerationInfo
	if len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		resp.Usage = llmtypes.ExtractUsageFromGenerationInfo(resp.Choices[0].GenerationInfo)
	}

	return resp, nil
}

// buildRequestInfo creates a RequestInfo from messages and options for recording/matching
func buildRequestInfo(messages []llmtypes.MessageContent, modelID string, opts *llmtypes.CallOptions) recorder.RequestInfo {
	// Convert messages to RequestInfo format
	messageInfos := make([]recorder.MessageInfo, 0, len(messages))
	for _, msg := range messages {
		// Convert parts to interface{} for JSON serialization
		parts := make([]interface{}, 0, len(msg.Parts))
		for _, part := range msg.Parts {
			// Marshal and unmarshal to get clean JSON representation
			partJSON, _ := json.Marshal(part)
			var partInterface interface{}
			if err := json.Unmarshal(partJSON, &partInterface); err != nil {
				// If unmarshal fails, use the original part
				partInterface = part
			}
			parts = append(parts, partInterface)
		}
		messageInfos = append(messageInfos, recorder.MessageInfo{
			Role:  string(msg.Role),
			Parts: parts,
		})
	}

	// Build options info
	optionsInfo := recorder.OptionsInfo{
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		JSONMode:    opts.JSONMode,
		ToolsCount:  len(opts.Tools),
	}

	return recorder.RequestInfo{
		Messages: messageInfos,
		ModelID:  modelID,
		Options:  optionsInfo,
	}
}

func convertMessagesToConverse(langMessages []llmtypes.MessageContent) []types.Message {
	converseMessages := make([]types.Message, 0, len(langMessages))

	for _, msg := range langMessages {
		var contentBlocks []types.ContentBlock
		var systemPrompt string

		// Extract content parts
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				if string(msg.Role) == string(llmtypes.ChatMessageTypeSystem) {
					// Collect system messages separately
					if systemPrompt != "" {
						systemPrompt += "\n"
					}
					systemPrompt += p.Text
				} else {
					// Add text content block
					contentBlocks = append(contentBlocks, &types.ContentBlockMemberText{
						Value: p.Text,
					})
				}
			case llmtypes.ImageContent:
				// Handle image content (simplified - would need base64 conversion)
				// For now, skip images in Converse API migration
				// TODO: Implement image support
			case llmtypes.ToolCallResponse:
				// Tool response - convert to ToolResult content block
				contentBlocks = append(contentBlocks, &types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String(p.ToolCallID),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{
								Value: p.Content,
							},
						},
					},
				})
			case llmtypes.ToolCall:
				// Tool call in assistant message - convert to ToolUse content block
				var inputDoc document.Interface
				if p.FunctionCall.Arguments != "" {
					// Validate and sanitize JSON arguments before creating document
					sanitizedArgs := validateAndSanitizeJSON(p.FunctionCall.Arguments)
					// Parse JSON into map, then create document from map (more reliable than []byte)
					var jsonObj map[string]interface{}
					if err := json.Unmarshal([]byte(sanitizedArgs), &jsonObj); err == nil {
						inputDoc = document.NewLazyDocument(jsonObj)
					} else {
						// Fallback to empty object if parsing fails
						inputDoc = document.NewLazyDocument(map[string]interface{}{})
					}
				} else {
					// Use empty map directly (more reliable than []byte("{}"))
					inputDoc = document.NewLazyDocument(map[string]interface{}{})
				}
				contentBlocks = append(contentBlocks, &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String(p.ID),
						Name:      aws.String(p.FunctionCall.Name),
						Input:     inputDoc,
					},
				})
			}
		}

		// Skip system messages (will be handled separately)
		if string(msg.Role) == string(llmtypes.ChatMessageTypeSystem) {
			continue
		}

		// Create message based on role
		if len(contentBlocks) > 0 {
			var role types.ConversationRole
			switch string(msg.Role) {
			case string(llmtypes.ChatMessageTypeHuman):
				role = types.ConversationRoleUser
			case string(llmtypes.ChatMessageTypeAI):
				role = types.ConversationRoleAssistant
			default:
				role = types.ConversationRoleUser
			}

			converseMessages = append(converseMessages, types.Message{
				Role:    role,
				Content: contentBlocks,
			})
		}
	}

	return converseMessages
}

// convertToolsToConverse converts llmtypes tools to Converse API format
func (b *BedrockAdapter) convertToolsToConverse(llmTools []llmtypes.Tool) []types.Tool {
	converseTools := make([]types.Tool, 0, len(llmTools))

	for _, tool := range llmTools {
		if tool.Function == nil {
			continue
		}

		// Extract function parameters as JSON schema
		var inputSchema map[string]interface{}
		if tool.Function.Parameters != nil {
			inputSchema = make(map[string]interface{})
			if tool.Function.Parameters.Type != "" {
				inputSchema["type"] = tool.Function.Parameters.Type
			}
			if tool.Function.Parameters.Properties != nil {
				inputSchema["properties"] = tool.Function.Parameters.Properties
			}
			if tool.Function.Parameters.Required != nil {
				inputSchema["required"] = tool.Function.Parameters.Required
			}
			if tool.Function.Parameters.AdditionalProperties != nil {
				inputSchema["additionalProperties"] = tool.Function.Parameters.AdditionalProperties
			}
			if tool.Function.Parameters.PatternProperties != nil {
				inputSchema["patternProperties"] = tool.Function.Parameters.PatternProperties
			}
			if tool.Function.Parameters.Additional != nil {
				for k, v := range tool.Function.Parameters.Additional {
					inputSchema[k] = v
				}
			}
		}

		inputSchema = llmtypes.ModelToolSchema(inputSchema)

		// Ensure input_schema has required fields
		if inputSchema == nil {
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": make(map[string]interface{}),
			}
		}

		// Ensure type is set (required by Converse API)
		if _, hasType := inputSchema["type"]; !hasType {
			inputSchema["type"] = "object"
		}

		// Convert schema to document for ToolInputSchema
		// document.NewLazyDocument can accept Go types directly (map, struct, etc.)
		// This is the recommended approach per AWS SDK documentation
		schemaDoc := document.NewLazyDocument(inputSchema)

		// Debug: Log the schema being sent
		if b.logger != nil {
			schemaBytes, _ := json.Marshal(inputSchema)
			b.logger.Debugf("Tool schema for %s: %s", tool.Function.Name, string(schemaBytes))
		}

		// Create Converse tool definition
		converseTool := &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(tool.Function.Name),
				Description: aws.String(tool.Function.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: schemaDoc,
				},
			},
		}

		converseTools = append(converseTools, converseTool)
	}

	return converseTools
}

// convertToolChoiceToConverse converts llmtypes tool choice to Converse API format
func convertToolChoiceToConverse(toolChoice interface{}) types.ToolChoice {
	if toolChoice == nil {
		return &types.ToolChoiceMemberAuto{}
	}

	// Handle string-based tool choice
	if choiceStr, ok := toolChoice.(string); ok {
		switch choiceStr {
		case "auto", "":
			return &types.ToolChoiceMemberAuto{}
		case "required", "any":
			return &types.ToolChoiceMemberAny{}
		case "none":
			// Converse API doesn't support "none" - return auto instead
			return &types.ToolChoiceMemberAuto{}
		default:
			// Specific tool name
			return &types.ToolChoiceMemberTool{
				Value: types.SpecificToolChoice{
					Name: aws.String(choiceStr),
				},
			}
		}
	}

	// Handle ToolChoice struct if it's that type
	if tc, ok := toolChoice.(*llmtypes.ToolChoice); ok && tc != nil {
		if tc.Type == "required" {
			return &types.ToolChoiceMemberAny{}
		} else if tc.Type == "none" {
			// Converse API doesn't support "none" - return auto instead
			return &types.ToolChoiceMemberAuto{}
		} else if tc.Type == "function" && tc.Function != nil && tc.Function.Name != "" {
			return &types.ToolChoiceMemberTool{
				Value: types.SpecificToolChoice{
					Name: aws.String(tc.Function.Name),
				},
			}
		}
	}

	// Default to auto
	return &types.ToolChoiceMemberAuto{}
}

// validateAndSanitizeJSON validates and sanitizes JSON arguments
// Returns valid JSON string, or "{}" if input is invalid
func validateAndSanitizeJSON(input string) string {
	if input == "" {
		return "{}"
	}

	// Try to parse as JSON to validate
	var jsonObj map[string]interface{}
	if err := json.Unmarshal([]byte(input), &jsonObj); err != nil {
		// Invalid JSON - return empty object
		return "{}"
	}

	// Re-marshal to ensure it's properly formatted
	validatedJSON, err := json.Marshal(jsonObj)
	if err != nil {
		return "{}"
	}

	return string(validatedJSON)
}

// logInputDetailsConverse logs input details for Converse API
func (b *BedrockAdapter) logInputDetailsConverse(modelID string, messages []llmtypes.MessageContent, input *bedrockruntime.ConverseInput, opts *llmtypes.CallOptions) {
	if b.logger == nil {
		return
	}

	// Build input summary
	inputSummary := map[string]interface{}{
		"model_id":      modelID,
		"message_count": len(input.Messages),
		"json_mode":     opts.JSONMode,
		"tools_count":   0,
	}

	// Add inference config details
	if input.InferenceConfig != nil {
		if input.InferenceConfig.Temperature != nil {
			inputSummary["temperature"] = *input.InferenceConfig.Temperature
		}
		if input.InferenceConfig.MaxTokens != nil {
			inputSummary["max_tokens"] = *input.InferenceConfig.MaxTokens
		}
	}

	// Add tool config details
	if input.ToolConfig != nil && input.ToolConfig.Tools != nil {
		inputSummary["tools_count"] = len(input.ToolConfig.Tools)
	}

	// Add system message info
	if input.System != nil {
		inputSummary["system_blocks"] = len(input.System)
	}

	// Add message summaries (first 200 chars of each)
	messageSummaries := make([]string, 0, len(messages))
	for i, msg := range messages {
		role := string(msg.Role)
		var contentPreview string
		if len(msg.Parts) > 0 {
			if textPart, ok := msg.Parts[0].(llmtypes.TextContent); ok {
				content := textPart.Text
				if len(content) > 200 {
					contentPreview = content[:200] + "..."
				} else {
					contentPreview = content
				}
			} else {
				contentPreview = fmt.Sprintf("[%T]", msg.Parts[0])
			}
		}
		messageSummaries = append(messageSummaries, fmt.Sprintf("%s: %s", role, contentPreview))
		if i >= 4 { // Limit to first 5 messages
			break
		}
	}
	inputSummary["messages"] = messageSummaries

	b.logger.Debugf("Bedrock Converse API INPUT - %+v", inputSummary)
}

// logErrorDetailsConverse logs error details for Converse API
func (b *BedrockAdapter) logErrorDetailsConverse(modelID string, messages []llmtypes.MessageContent, input *bedrockruntime.ConverseInput, opts *llmtypes.CallOptions, err error, result *bedrockruntime.ConverseOutput) {
	if b.logger == nil {
		return
	}

	// Log error with input context
	errorInfo := map[string]interface{}{
		"error":                err.Error(),
		"error_type":           fmt.Sprintf("%T", err),
		"model_id":             modelID,
		"message_count":        len(input.Messages),
		"error_classification": "unknown",
	}

	// Extract detailed error information if it's an AWS SDK error
	var awsErrCode, awsErrMessage, awsRequestID string
	var awsHTTPStatusCode int

	// Try to extract AWS error details using type assertions
	if errWithCode, ok := err.(interface{ Code() string }); ok {
		awsErrCode = errWithCode.Code()
		errorInfo["aws_error_code"] = awsErrCode
	}
	if errWithMsg, ok := err.(interface{ Message() string }); ok {
		awsErrMessage = errWithMsg.Message()
		errorInfo["aws_error_message"] = awsErrMessage
	}
	if errWithRequestID, ok := err.(interface{ RequestID() string }); ok {
		awsRequestID = errWithRequestID.RequestID()
		errorInfo["aws_request_id"] = awsRequestID
	}
	if errWithStatusCode, ok := err.(interface{ StatusCode() int }); ok {
		awsHTTPStatusCode = errWithStatusCode.StatusCode()
		errorInfo["http_status_code"] = awsHTTPStatusCode
	}

	// Classify error based on AWS error code and HTTP status
	errMsg := err.Error()
	classified := false

	if awsHTTPStatusCode > 0 {
		switch awsHTTPStatusCode {
		case 400:
			errorInfo["error_classification"] = "bad_request"
			b.logger.Debugf("🔄 Validation error - Check request parameters")
			classified = true
		case 401:
			errorInfo["error_classification"] = "unauthorized"
			b.logger.Debugf("🔄 401 Unauthorized error - Check AWS credentials and permissions")
			classified = true
		case 403:
			errorInfo["error_classification"] = "access_denied"
			b.logger.Debugf("🔄 403 Access Denied error - Check AWS credentials and permissions")
			classified = true
		case 429:
			errorInfo["error_classification"] = "rate_limit"
			b.logger.Debugf("🔄 429 Rate Limit/Throttling error detected, will trigger fallback mechanism")
			classified = true
		case 500:
			errorInfo["error_classification"] = "server_error"
			b.logger.Debugf("🔄 500 Internal Server Error detected, will trigger fallback mechanism")
			classified = true
		case 502:
			errorInfo["error_classification"] = "bad_gateway"
			b.logger.Debugf("🔄 502 Bad Gateway error detected, will trigger fallback mechanism")
			classified = true
		case 503:
			errorInfo["error_classification"] = "service_unavailable"
			b.logger.Debugf("🔄 503 Service Unavailable error detected, will trigger fallback mechanism")
			classified = true
		case 504:
			errorInfo["error_classification"] = "gateway_timeout"
			b.logger.Debugf("🔄 504 Gateway Timeout error detected, will trigger fallback mechanism")
			classified = true
		}
	}

	// Fallback to error code classification if HTTP status wasn't available
	if !classified {
		if awsErrCode != "" {
			switch awsErrCode {
			case "AccessDeniedException", "AccessDenied":
				errorInfo["error_classification"] = "access_denied"
				b.logger.Debugf("🔄 Access Denied error - Check AWS credentials and permissions")
				classified = true
			case "ValidationException", "InvalidParameterException":
				errorInfo["error_classification"] = "validation_error"
				b.logger.Debugf("🔄 Validation error - Check request parameters")
				classified = true
			case "ThrottlingException", "TooManyRequestsException":
				errorInfo["error_classification"] = "rate_limit"
				b.logger.Debugf("🔄 Rate Limit/Throttling error detected, will trigger fallback mechanism")
				classified = true
			case "ModelNotReadyException", "ModelStreamErrorException":
				errorInfo["error_classification"] = "model_error"
				b.logger.Debugf("🔄 Model Error - Model may not be ready or encountered an error")
				classified = true
			case "InternalServerException":
				errorInfo["error_classification"] = "server_error"
				b.logger.Debugf("🔄 Internal Server Error detected, will trigger fallback mechanism")
				classified = true
			case "ServiceQuotaExceededException":
				errorInfo["error_classification"] = "quota_exceeded"
				b.logger.Debugf("🔄 Service Quota Exceeded - Check AWS service limits")
				classified = true
			}
		}

		// Final fallback to message-based classification
		if !classified {
			if strings.Contains(errMsg, "AccessDenied") || strings.Contains(errMsg, "access denied") || strings.Contains(errMsg, "403") {
				errorInfo["error_classification"] = "access_denied"
				b.logger.Debugf("🔄 Access Denied error - Check AWS credentials and permissions")
			} else if strings.Contains(errMsg, "ValidationException") || strings.Contains(errMsg, "validation") || strings.Contains(errMsg, "400") {
				errorInfo["error_classification"] = "validation_error"
				b.logger.Debugf("🔄 Validation error - Check request parameters")
			} else if strings.Contains(errMsg, "ThrottlingException") || strings.Contains(errMsg, "throttl") || strings.Contains(errMsg, "429") {
				errorInfo["error_classification"] = "rate_limit"
				b.logger.Debugf("🔄 Rate Limit/Throttling error detected, will trigger fallback mechanism")
			} else if strings.Contains(errMsg, "500") || strings.Contains(errMsg, "internal server error") {
				errorInfo["error_classification"] = "server_error"
				b.logger.Debugf("🔄 Internal Server Error detected, will trigger fallback mechanism")
			} else if strings.Contains(errMsg, "503") || strings.Contains(errMsg, "service unavailable") {
				errorInfo["error_classification"] = "service_unavailable"
				b.logger.Debugf("🔄 Service Unavailable error detected, will trigger fallback mechanism")
			} else if strings.Contains(errMsg, "502") || strings.Contains(errMsg, "bad gateway") {
				errorInfo["error_classification"] = "bad_gateway"
				b.logger.Debugf("🔄 Bad Gateway error detected, will trigger fallback mechanism")
			} else if strings.Contains(errMsg, "504") || strings.Contains(errMsg, "gateway timeout") {
				errorInfo["error_classification"] = "gateway_timeout"
				b.logger.Debugf("🔄 Gateway Timeout error detected, will trigger fallback mechanism")
			}
		}
	}

	// Add inference config details
	if input.InferenceConfig != nil {
		if input.InferenceConfig.Temperature != nil {
			errorInfo["temperature"] = *input.InferenceConfig.Temperature
		}
		if input.InferenceConfig.MaxTokens != nil {
			errorInfo["max_tokens"] = *input.InferenceConfig.MaxTokens
		}
	}

	// Add tool config details
	if input.ToolConfig != nil && input.ToolConfig.Tools != nil {
		errorInfo["tools_count"] = len(input.ToolConfig.Tools)
		// Log tool names for debugging
		toolNames := make([]string, 0, len(input.ToolConfig.Tools))
		for _, tool := range input.ToolConfig.Tools {
			if toolSpec, ok := tool.(*types.ToolMemberToolSpec); ok {
				if toolSpec.Value.Name != nil {
					toolNames = append(toolNames, *toolSpec.Value.Name)
				}
			}
		}
		if len(toolNames) > 0 {
			errorInfo["tool_names"] = toolNames
		}
	}

	// Add message details for debugging
	errorInfo["messages"] = make([]map[string]interface{}, 0, len(messages))
	for i, msg := range messages {
		msgInfo := map[string]interface{}{
			"role":  string(msg.Role),
			"parts": len(msg.Parts),
		}
		// Calculate content length
		contentLength := 0
		for _, part := range msg.Parts {
			if textPart, ok := part.(llmtypes.TextContent); ok {
				contentLength += len(textPart.Text)
			}
		}
		msgInfo["content_length"] = contentLength
		if i < 5 { // Limit to first 5 messages
			errorInfo["messages"] = append(errorInfo["messages"].([]map[string]interface{}), msgInfo)
		}
	}

	// Add response details if available (even though there was an error)
	if result != nil {
		responseInfo := map[string]interface{}{}
		if result.Output != nil {
			if msgOutput, ok := result.Output.(*types.ConverseOutputMemberMessage); ok {
				message := msgOutput.Value
				if len(message.Content) > 0 {
					if textBlock, ok := message.Content[0].(*types.ContentBlockMemberText); ok {
						content := textBlock.Value
						if len(content) > 500 {
							content = content[:500] + "..."
						}
						responseInfo["content_preview"] = content
						responseInfo["content_length"] = len(textBlock.Value)
					}
				}
			}
		}
		if result.StopReason != "" {
			responseInfo["stop_reason"] = string(result.StopReason)
		}
		if result.Usage != nil {
			usageInfo := map[string]interface{}{}
			if result.Usage.InputTokens != nil {
				usageInfo["input_tokens"] = int(*result.Usage.InputTokens)
				errorInfo["usage_input_tokens"] = int(*result.Usage.InputTokens)
			}
			if result.Usage.OutputTokens != nil {
				usageInfo["output_tokens"] = int(*result.Usage.OutputTokens)
				errorInfo["usage_output_tokens"] = int(*result.Usage.OutputTokens)
			}
			if result.Usage.TotalTokens != nil {
				usageInfo["total_tokens"] = int(*result.Usage.TotalTokens)
			}
			if len(usageInfo) > 0 {
				responseInfo["usage"] = usageInfo
			}
		}
		if len(responseInfo) > 0 {
			errorInfo["response"] = responseInfo
		}
	}

	// Log comprehensive error information
	b.logger.Errorf("Bedrock Converse API ERROR - %+v", errorInfo)

	// Log additional error details for debugging
	b.logger.Infof("❌ Bedrock LLM generation failed - model: %s, error: %v", modelID, err)
	b.logger.Infof("❌ Error details - type: %T, message: %s", err, err.Error())

	// Log AWS-specific error details if available
	if awsErrCode != "" {
		b.logger.Infof("❌ AWS Error Code: %s", awsErrCode)
	}
	if awsErrMessage != "" {
		b.logger.Infof("❌ AWS Error Message: %s", awsErrMessage)
	}
	if awsRequestID != "" {
		b.logger.Infof("❌ AWS Request ID: %s", awsRequestID)
	}
	if awsHTTPStatusCode > 0 {
		b.logger.Infof("❌ HTTP Status Code: %d", awsHTTPStatusCode)
	}

	// Log request parameters
	b.logger.Infof("📝 Request Parameters:")
	if input.InferenceConfig != nil {
		if input.InferenceConfig.Temperature != nil {
			b.logger.Infof("   Temperature: %v", *input.InferenceConfig.Temperature)
		}
		if input.InferenceConfig.MaxTokens != nil {
			b.logger.Infof("   Max Tokens: %d", *input.InferenceConfig.MaxTokens)
		}
	}
	if input.ToolConfig != nil && input.ToolConfig.Tools != nil {
		b.logger.Infof("   Tools Count: %d", len(input.ToolConfig.Tools))
	}
	if input.System != nil {
		b.logger.Infof("   System Blocks: %d", len(input.System))
	}

	// Log messages sent for debugging
	b.logger.Infof("📤 Messages sent to Bedrock LLM - count: %d", len(messages))
	for i, msg := range messages {
		// Calculate actual content length from message parts
		contentLength := 0
		for _, part := range msg.Parts {
			if textPart, ok := part.(llmtypes.TextContent); ok {
				contentLength += len(textPart.Text)
			}
		}
		b.logger.Infof("📤 Message %d - Role: %s, Content length: %d", i+1, msg.Role, contentLength)
		if i >= 4 { // Limit to first 5 messages
			break
		}
	}
}
