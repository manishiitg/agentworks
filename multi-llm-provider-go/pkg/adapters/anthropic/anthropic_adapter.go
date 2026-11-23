package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicAdapter is an adapter that implements llmtypes.Model interface
// using the Anthropic SDK directly
type AnthropicAdapter struct {
	client  anthropic.Client
	modelID string
	logger  interfaces.Logger
}

// NewAnthropicAdapter creates a new adapter instance
func NewAnthropicAdapter(client anthropic.Client, modelID string, logger interfaces.Logger) *AnthropicAdapter {
	return &AnthropicAdapter{
		client:  client,
		modelID: modelID,
		logger:  logger,
	}
}

// GetModelID implements the llmtypes.Model interface
func (a *AnthropicAdapter) GetModelID() string {
	return a.modelID
}

// GetModelMetadata implements the llmtypes.Model interface
func (a *AnthropicAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	return GetAnthropicModelMetadata(modelID)
}

// GenerateContent implements the llmtypes.Model interface
func (a *AnthropicAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Parse call options
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}

	// Determine model ID (from option or default)
	modelID := a.modelID
	if opts.Model != "" {
		modelID = opts.Model
	}

	// Inspector emitter — no-op when opts.InspectorSink is nil. The
	// emitter is the single point of contact for debug-event emission;
	// adapter code never builds InspectorEvent structs directly.
	// Cross-cutting observability is delegated to WithObservability via
	// the closure return path. We capture anthropic-specific completion
	// extras (cache stats) into this map, which EnrichCompletionMeta
	// merges after the body returns.
	var anthropicCompletionExtras map[string]interface{}
	return llmtypes.WithObservability(ctx, llmtypes.ObservabilityConfig{
		Provider:     "anthropic",
		Model:        modelID,
		Opts:         opts,
		MessageCount: len(messages),
		Messages:     messages,
		HeaderLine:   fmt.Sprintf("anthropic.messages.create model=%s msgs=%d", modelID, len(messages)),
		EnrichCompletionMeta: func(_ *llmtypes.ContentResponse, meta map[string]interface{}) {
			for k, v := range anthropicCompletionExtras {
				meta[k] = v
			}
		},
		// Not yet reachable from AgentWorks workflow steps, but the CLI
		// adapters already declare "tmux" / "structured_cli" here so
		// their transport-class chip renders correctly; this is the
		// same declaration for a direct API call, so the label is
		// correct the day this adapter IS wired in.
		RequestMetaExtra: map[string]interface{}{"transport": "api"},
	}, func(sink *llmtypes.StreamSink) (*llmtypes.ContentResponse, error) {
		return a.generateContentInner(ctx, opts, modelID, messages, sink.Term, sink.Inspector, &anthropicCompletionExtras)
	})
}

func (a *AnthropicAdapter) generateContentInner(ctx context.Context, opts *llmtypes.CallOptions, modelID string, messages []llmtypes.MessageContent, term *llmtypes.SyntheticTerminal, inspector *llmtypes.InspectorEmitter, completionExtras *map[string]interface{}) (*llmtypes.ContentResponse, error) {

	// Convert messages from llm format to Anthropic format
	anthropicMessages, systemMessage := convertMessages(messages)

	// Build MessageNewParams from options
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(modelID),
		Messages:  anthropicMessages,
		MaxTokens: 32768, // High default - API will cap to model's max if exceeded
	}

	// Set system message if present
	if systemMessage != "" {
		// Handle JSON mode by appending instruction to system message
		if opts.JSONMode {
			systemMessage = systemMessage + "\n\nYou must respond with valid JSON only, no other text. Return a JSON object."
		}

		// Apply cache control to system prompts if they're large enough
		// Anthropic requires at least 1024 tokens for Claude 3.5 Sonnet/Opus, but 2048 tokens for Claude Haiku
		// We estimate ~4 chars per token, so 8000+ chars ≈ 2000+ tokens (safe for all models)
		estimatedTokens := len(systemMessage) / 4
		shouldCache := estimatedTokens >= 2000 // Ensure we meet Anthropic's 2048 token minimum for Haiku

		systemBlock := anthropic.TextBlockParam{
			Text: systemMessage,
		}

		if shouldCache {
			// Apply cache control to system prompt for large contexts
			cacheControl := anthropic.NewCacheControlEphemeralParam()
			cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL5m // Set TTL to 5 minutes
			systemBlock.CacheControl = cacheControl
		}

		params.System = []anthropic.TextBlockParam{
			systemBlock,
		}
	} else if opts.JSONMode && len(anthropicMessages) > 0 {
		// If no system message, prepend JSON instruction to first user message
		jsonInstruction := anthropic.NewTextBlock("You must respond with valid JSON only, no other text. Return a JSON object.")
		if len(anthropicMessages) > 0 && anthropicMessages[0].Role == anthropic.MessageParamRoleUser {
			anthropicMessages[0].Content = append([]anthropic.ContentBlockParamUnion{jsonInstruction}, anthropicMessages[0].Content...)
		}
	}

	// Set temperature
	if opts.Temperature > 0 {
		params.Temperature = anthropic.Float(opts.Temperature)
	}

	// Set max tokens
	if opts.MaxTokens > 0 {
		params.MaxTokens = int64(opts.MaxTokens)
	}

	// Optional sampling controls. We only forward when the caller
	// explicitly populated the field — leaving the provider's own
	// default in place otherwise.
	if opts.TopP > 0 {
		params.TopP = anthropic.Float(opts.TopP)
	}
	if opts.TopK > 0 {
		params.TopK = anthropic.Int(int64(opts.TopK))
	}
	if len(opts.StopSequences) > 0 {
		// Defensive copy so the SDK's mutation rules don't surprise us.
		seqs := make([]string, len(opts.StopSequences))
		copy(seqs, opts.StopSequences)
		params.StopSequences = seqs
	}

	// Convert tools if provided
	if len(opts.Tools) > 0 {
		tools := convertTools(opts.Tools)
		params.Tools = tools

		// Handle tool choice
		if opts.ToolChoice != nil {
			toolChoice := convertToolChoice(opts.ToolChoice)
			params.ToolChoice = toolChoice
		}
	}

	// Extended thinking (Claude 4 family). Anthropic requires
	//   budget_tokens >= 1024 AND budget_tokens < max_tokens.
	// Callers express their intent two ways:
	//   1. opts.ThinkingBudget  — explicit token budget (wins if set)
	//   2. opts.ThinkingLevel   — "low" / "medium" / "high" labels, which
	//      we map to concrete budgets that satisfy the >=1024 floor.
	if budget := resolveAnthropicThinkingBudget(opts, params.MaxTokens); budget >= 1024 {
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: int64(budget)},
		}
		// Anthropic rejects `temperature != 1.0` together with thinking
		// on the Messages API. Drop the override silently rather than
		// failing the request — callers may set temperature for other
		// providers in the same call site without realizing.
		params.Temperature = anthropic.Float(1.0)
	}

	// Generate unique request ID for tracking request/response correlation
	requestID := fmt.Sprintf("req_%d", time.Now().UnixNano())

	// Log raw input details for all requests (not just errors)
	if a.logger != nil {
		a.logInputDetails(modelID, messages, params, opts)
		// Also log the actual params that will be sent to API (full, no truncation)
		a.logRawInput(requestID, modelID, params)
	}

	// Always use streaming API for Anthropic to avoid "streaming is required" error.
	// Anthropic requires streaming for operations that may take longer than 10 minutes;
	// NewStreaming() disables that error check regardless of actual request size.
	//
	// Beta headers are composed per-request from the params themselves so we
	// only opt into features we are actually using. Prompt caching is GA in
	// the Messages API, so we no longer send the legacy
	// `prompt-caching-2024-07-31` token unconditionally; it remains available
	// behind a sentinel for adapters that want to pin the legacy contract.
	betaTokens := buildAnthropicBetaTokens(params, opts)
	if a.logger != nil {
		a.logger.Debugf("[ANTHROPIC DEBUG] Model: %s, Messages: %d, System blocks: %d, beta=%q",
			params.Model, len(params.Messages), len(params.System), strings.Join(betaTokens, ","))
	}
	streamOpts := []anthropicoption.RequestOption{}
	if len(betaTokens) > 0 {
		streamOpts = append(streamOpts, anthropicoption.WithHeader("anthropic-beta", strings.Join(betaTokens, ",")))
	}

	// Inspector: emit the request envelope before dispatching. Keep
	// metadata to lightweight counters — full message bodies live in
	// the chat history already.
	inspector.EmitRequest(map[string]interface{}{
		"message_count":        len(params.Messages),
		"system_block_count":   len(params.System),
		"system_prompt_length": anthropicSystemPromptLength(params.System),
		"max_tokens":           params.MaxTokens,
		"tool_count":           len(params.Tools),
		"json_mode":            opts.JSONMode,
		"beta_tokens":          betaTokens,
		"streaming":            true,
	})

	stream := a.client.Messages.NewStreaming(ctx, params, streamOpts...)
	// opts.StreamChan close is owned by WithObservability so term.Done's
	// terminal snapshot (cost + tokens) lands before the channel is shut.

	// Use Message.Accumulate to build the final message
	message := anthropic.Message{}
	var contentChunksSent int
	for stream.Next() {
		event := stream.Current()

		// Accumulate event into message
		if err := message.Accumulate(event); err != nil {
			stream.Close()
			if a.logger != nil {
				a.logErrorDetails(modelID, messages, params, opts, err, &message)
				a.logRawResponse(requestID, modelID, &message, err)
			}
			inspector.EmitEvent("error_phase", map[string]interface{}{"phase": "stream_accumulate"})
			return nil, fmt.Errorf("anthropic streaming accumulate error: %w", err)
		}

		// Inspector: emit one event per streaming chunk. Keep it cheap;
		// just the event-name tag + a delta length on text deltas.
		if inspector.Enabled() {
			eventName, deltaLen := anthropicEventTag(event)
			eventMeta := map[string]interface{}{}
			if deltaLen > 0 {
				eventMeta["delta_text_length"] = deltaLen
			}
			inspector.EmitEvent(eventName, eventMeta)
		}

		// If streaming channel is provided, extract and send text chunks
		if opts.StreamChan != nil {
			switch eventVariant := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				// Check if this is a text delta
				switch deltaVariant := eventVariant.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					if deltaVariant.Text != "" {
						contentChunksSent++
						select {
						case opts.StreamChan <- llmtypes.StreamChunk{
							Type:    llmtypes.StreamChunkTypeContent,
							Content: deltaVariant.Text,
						}:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						term.AssistantText(deltaVariant.Text)
					}
				}
			}
		}
	}

	// Check for stream errors
	if err := stream.Err(); err != nil {
		if a.logger != nil {
			a.logErrorDetails(modelID, messages, params, opts, err, &message)
			a.logRawResponse(requestID, modelID, &message, err)
		}
		inspector.EmitEvent("error_phase", map[string]interface{}{"phase": "stream_err"})
		return nil, fmt.Errorf("anthropic streaming error: %w", err)
	}
	stream.Close()

	// After streaming completes, extract and stream any tool calls from the accumulated message
	var toolCallsSent int
	if opts.StreamChan != nil {
		// FALLBACK: If no content chunks were sent during streaming but message has text content,
		// stream it now (this handles cases where deltas weren't captured)
		if contentChunksSent == 0 {
			// Extract text content from accumulated message
			var textContent strings.Builder
			for _, block := range message.Content {
				if block.Type == "text" && block.Text != "" {
					textContent.WriteString(block.Text)
				}
			}

			// If we found text content, stream it as a single chunk
			if textContent.Len() > 0 {
				contentChunksSent++
				select {
				case opts.StreamChan <- llmtypes.StreamChunk{
					Type:    llmtypes.StreamChunkTypeContent,
					Content: textContent.String(),
				}:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}

		// Extract tool calls from accumulated message
		for _, block := range message.Content {
			if block.Type == "tool_use" {
				var argsJSON []byte
				if len(block.Input) > 0 {
					argsJSON = block.Input
				} else {
					argsJSON = []byte("{}")
				}

				toolCall := llmtypes.ToolCall{
					ID:   block.ID,
					Type: "function",
					FunctionCall: &llmtypes.FunctionCall{
						Name:      block.Name,
						Arguments: string(argsJSON),
					},
				}

				// Stream the complete tool call
				toolCallsSent++
				select {
				case opts.StreamChan <- llmtypes.StreamChunk{
					Type:     llmtypes.StreamChunkTypeToolCall,
					ToolCall: &toolCall,
				}:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				term.ToolStart(block.Name, string(argsJSON))
			}
		}

		// Debug: Log if no chunks were sent (this indicates a potential issue)
		if a.logger != nil && contentChunksSent == 0 && toolCallsSent == 0 {
			// Check if message has content
			var hasContent bool
			for _, block := range message.Content {
				if block.Type == "text" {
					hasContent = true
					break
				}
			}
			if hasContent {
				a.logger.Debugf("[ANTHROPIC DEBUG] WARNING: StreamChan is set but no chunks were sent! Content chunks: %d, Tool calls: %d, Message has content: %v", contentChunksSent, toolCallsSent, hasContent)
			}
		}
	}

	// Log raw response for successful requests
	if a.logger != nil {
		a.logRawResponse(requestID, modelID, &message, nil)
	}

	// Convert the accumulated message to llm format
	resp := convertResponse(&message)
	a.attachCostEstimate(resp, modelID)

	// Provider-specific tool-call events; the bookend completion envelope
	// is emitted by WithObservability after this body returns.
	if inspector.Enabled() {
		for _, block := range message.Content {
			if block.Type == "tool_use" {
				inspector.EmitToolCall(map[string]interface{}{
					"tool_name":    block.Name,
					"tool_call_id": block.ID,
					"args_length":  len(block.Input),
				})
			}
		}
	}

	extras := map[string]interface{}{"content_chunks": contentChunksSent}
	if message.Usage.CacheReadInputTokens > 0 {
		extras["cache_read_input_tokens"] = int(message.Usage.CacheReadInputTokens)
	}
	if message.Usage.CacheCreationInputTokens > 0 {
		extras["cache_creation_input_tokens"] = int(message.Usage.CacheCreationInputTokens)
	}
	if completionExtras != nil {
		*completionExtras = extras
	}

	return resp, nil
}

// anthropicSystemPromptLength reports the total characters across all
// system content blocks. Used by the inspector emitter to surface a
// PII-free signal of system-prompt scale.
func anthropicSystemPromptLength(blocks []anthropic.TextBlockParam) int {
	total := 0
	for _, b := range blocks {
		total += len(b.Text)
	}
	return total
}

// anthropicEventTag derives a stable event-name string for inspector
// emission from an Anthropic stream event. Returns (name, delta_text_len).
func anthropicEventTag(event anthropic.MessageStreamEventUnion) (string, int) {
	switch v := event.AsAny().(type) {
	case anthropic.MessageStartEvent:
		return "message_start", 0
	case anthropic.ContentBlockStartEvent:
		return "content_block_start", 0
	case anthropic.ContentBlockDeltaEvent:
		switch d := v.Delta.AsAny().(type) {
		case anthropic.TextDelta:
			return "text_delta", len(d.Text)
		case anthropic.InputJSONDelta:
			return "input_json_delta", len(d.PartialJSON)
		case anthropic.ThinkingDelta:
			return "thinking_delta", len(d.Thinking)
		default:
			return "content_block_delta", 0
		}
	case anthropic.ContentBlockStopEvent:
		return "content_block_stop", 0
	case anthropic.MessageDeltaEvent:
		return "message_delta", 0
	case anthropic.MessageStopEvent:
		return "message_stop", 0
	}
	return "unknown", 0
}

// attachCostEstimate fills in GenerationInfo.Additional["cost_usd_estimated"]
// from tokens × registry rates so downstream cost ledgers don't have to
// redo the math. Anthropic's Messages API does not return a cost field
// in the response (unlike Claude Code CLI's --print result event), so
// this is the only way to surface a number at the adapter layer.
func (a *AnthropicAdapter) attachCostEstimate(resp *llmtypes.ContentResponse, modelID string) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return
	}
	meta, err := a.GetModelMetadata(modelID)
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

// Call implements a convenience method that wraps GenerateContent for simple text generation
func (a *AnthropicAdapter) Call(ctx context.Context, prompt string, options ...llmtypes.CallOption) (string, error) {
	messages := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: prompt},
			},
		},
	}

	resp, err := a.GenerateContent(ctx, messages, options...)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return resp.Choices[0].Content, nil
}

// convertMessages converts llmtypes messages to Anthropic message format
// Returns messages and system message (if present)
func convertMessages(langMessages []llmtypes.MessageContent) ([]anthropic.MessageParam, string) {
	anthropicMessages := make([]anthropic.MessageParam, 0, len(langMessages))
	var systemMessage string

	// Track if we've seen any tool calls/responses - if so, don't cache user messages
	// Only cache the first static user message (before any tool interactions)
	hasSeenToolInteraction := false
	isFirstUserMessage := true

	for _, msg := range langMessages {
		// Extract content parts
		var contentParts []string
		var imageParts []llmtypes.ImageContent
		var documentParts []llmtypes.DocumentContent
		var toolCallID string
		var toolResponseContent string
		var toolResponseIsError bool
		var toolResponseImages []llmtypes.ImageContent
		var toolCalls []llmtypes.ToolCall

		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				contentParts = append(contentParts, p.Text)
			case llmtypes.ImageContent:
				imageParts = append(imageParts, p)
			case llmtypes.DocumentContent:
				documentParts = append(documentParts, p)
			case llmtypes.ToolCallResponse:
				// Tool response - extract tool call ID, text content,
				// and any image attachments. Claude 3.5+ supports
				// images in tool_result blocks (vision-in-tool-output).
				toolCallID = p.ToolCallID
				toolResponseContent = p.Content
				toolResponseIsError = p.IsError
				toolResponseImages = append(toolResponseImages, p.Images...)
			case llmtypes.ToolCall:
				// Tool call in assistant message
				toolCalls = append(toolCalls, p)
			}
		}

		// Handle different message roles
		switch string(msg.Role) {
		case string(llmtypes.ChatMessageTypeSystem):
			// System messages go to the system parameter, not messages array
			if len(contentParts) > 0 {
				systemMessage = strings.Join(contentParts, "\n")
			}
		case string(llmtypes.ChatMessageTypeHuman):
			// User message - can have text and/or images
			contentBlocks := []anthropic.ContentBlockParamUnion{}

			// Add text content if present
			if len(contentParts) > 0 {
				content := strings.Join(contentParts, "\n")

				// Only cache user messages if:
				// 1. It's the first user message in the conversation
				// 2. No tool interactions have occurred yet (static conversation)
				// 3. The content is large enough (>= 2000 tokens)
				// Never cache user messages after tool calls/responses (they're part of dynamic conversation flow)
				estimatedTokens := len(content) / 4
				shouldCache := !hasSeenToolInteraction && isFirstUserMessage && estimatedTokens >= 2000

				if shouldCache {
					// For large static content, we apply cache control to the entire block
					// Cache control marks the END of cacheable content
					// This tells Anthropic to cache everything up to this point
					// IMPORTANT: The cache_control parameter must be on a text block that contains
					// at least 2048 tokens for Claude Haiku (or 1024 for other models)
					// Use the constructor function to properly initialize CacheControlEphemeralParam
					cacheControl := anthropic.NewCacheControlEphemeralParam()
					cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL5m // Set TTL to 5 minutes
					textBlock := anthropic.TextBlockParam{
						Text:         content,
						CacheControl: cacheControl,
					}
					contentBlocks = append(contentBlocks, anthropic.ContentBlockParamUnion{OfText: &textBlock})
					// Cache control is now applied - this will be sent to Anthropic API
					// The entire content block will be cached and can be reused in subsequent requests
				} else {
					// Use standard text block (no caching for dynamic content or small messages)
					contentBlocks = append(contentBlocks, anthropic.NewTextBlock(content))
				}
			}

			// Mark that we've seen a user message (so subsequent ones won't be cached)
			isFirstUserMessage = false

			// Add image content blocks if present
			for _, img := range imageParts {
				imageBlock := createImageBlock(img)
				if imageBlock != nil {
					contentBlocks = append(contentBlocks, *imageBlock)
				}
			}

			// Add document content blocks (e.g. PDFs) if present.
			// Anthropic Claude 3.5+ natively reads PDFs and other
			// document content; unsupported media types are silently
			// dropped by createDocumentBlock.
			for _, doc := range documentParts {
				docBlock := createDocumentBlock(doc)
				if docBlock != nil {
					contentBlocks = append(contentBlocks, *docBlock)
				}
			}

			// Only add message if there's content
			if len(contentBlocks) > 0 {
				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: contentBlocks,
				})
			}
		case string(llmtypes.ChatMessageTypeAI):
			// Assistant message can have text content or tool calls
			// If there are tool calls, mark that we've seen tool interactions
			if len(toolCalls) > 0 {
				hasSeenToolInteraction = true
			}

			content := ""
			if len(contentParts) > 0 {
				content = strings.Join(contentParts, "\n")
			}

			// If there are tool calls, include them
			if len(toolCalls) > 0 {
				// Convert tool calls to Anthropic format
				contentBlocks := []anthropic.ContentBlockParamUnion{}
				if content != "" {
					contentBlocks = append(contentBlocks, anthropic.NewTextBlock(content))
				}
				for _, tc := range toolCalls {
					// Parse arguments
					var args map[string]interface{}
					if tc.FunctionCall.Arguments != "" {
						if err := json.Unmarshal([]byte(tc.FunctionCall.Arguments), &args); err != nil {
							// If parsing fails, create empty map
							args = make(map[string]interface{})
						}
					} else {
						args = make(map[string]interface{})
					}

					// Create tool use block using helper
					toolUseBlock := anthropic.NewToolUseBlock(tc.ID, args, tc.FunctionCall.Name)
					contentBlocks = append(contentBlocks, toolUseBlock)
				}

				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: contentBlocks,
				})
			} else {
				// Assistant message with just text
				contentBlock := anthropic.NewTextBlock(content)

				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{contentBlock},
				})
			}
		case string(llmtypes.ChatMessageTypeTool):
			// Tool message - handle tool responses
			// Mark that we've seen tool interactions (tool responses are dynamic)
			if toolCallID != "" {
				hasSeenToolInteraction = true
				contentBlock := buildToolResultBlock(toolCallID, toolResponseContent, toolResponseIsError, toolResponseImages)
				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{contentBlock},
				})
			}
		default:
			// Default to user message - can have text and/or images
			contentBlocks := []anthropic.ContentBlockParamUnion{}

			// Add text content if present
			if len(contentParts) > 0 {
				content := strings.Join(contentParts, "\n")
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(content))
			}

			// Add image content blocks if present
			for _, img := range imageParts {
				imageBlock := createImageBlock(img)
				if imageBlock != nil {
					contentBlocks = append(contentBlocks, *imageBlock)
				}
			}

			// Add document content blocks (e.g. PDFs) if present.
			for _, doc := range documentParts {
				docBlock := createDocumentBlock(doc)
				if docBlock != nil {
					contentBlocks = append(contentBlocks, *docBlock)
				}
			}

			// Only add message if there's content
			if len(contentBlocks) > 0 {
				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: contentBlocks,
				})
			}
		}
	}

	return anthropicMessages, systemMessage
}

// buildToolResultBlock constructs a tool_result content block, optionally
// embedding image content for Claude 3.5+ vision-in-tool-output. When no
// images are attached we fall back to the SDK's text-only convenience
// constructor so the wire format matches existing tests bit-for-bit.
func buildToolResultBlock(toolCallID, text string, isError bool, images []llmtypes.ImageContent) anthropic.ContentBlockParamUnion {
	if len(images) == 0 {
		return anthropic.NewToolResultBlock(toolCallID, text, isError)
	}
	result := anthropic.ToolResultBlockParam{
		ToolUseID: toolCallID,
	}
	if isError {
		result.IsError = anthropic.Bool(true)
	}
	if strings.TrimSpace(text) != "" {
		result.Content = append(result.Content, anthropic.ToolResultBlockParamContentUnion{
			OfText: &anthropic.TextBlockParam{Text: text},
		})
	}
	for _, img := range images {
		block := createImageBlock(img)
		if block == nil || block.OfImage == nil {
			continue
		}
		result.Content = append(result.Content, anthropic.ToolResultBlockParamContentUnion{
			OfImage: block.OfImage,
		})
	}
	return anthropic.ContentBlockParamUnion{OfToolResult: &result}
}

// createDocumentBlock converts a DocumentContent into the Anthropic
// document content-block. Two media types are supported today:
//
//   - application/pdf — wrapped in Base64PDFSourceParam (base64 source)
//     or URLPDFSourceParam (URL source).
//   - text/plain      — wrapped in PlainTextSourceParam (base64 source
//     only; PlainTextSourceParam carries the raw text inline).
//
// Anything else returns nil so the caller can degrade gracefully
// instead of forwarding a malformed request.
func createDocumentBlock(doc llmtypes.DocumentContent) *anthropic.ContentBlockParamUnion {
	mediaType := strings.ToLower(strings.TrimSpace(doc.MediaType))
	source := strings.ToLower(strings.TrimSpace(doc.SourceType))
	data := strings.TrimSpace(doc.Data)
	if data == "" {
		return nil
	}

	var block anthropic.ContentBlockParamUnion
	switch mediaType {
	case "application/pdf":
		if source == "url" {
			block = anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: doc.Data})
		} else {
			block = anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: doc.Data})
		}
	case "text/plain":
		// PlainTextSourceParam carries the raw decoded text. If the
		// caller passed base64 data, decode here; if they passed the
		// raw text, just hand it through unchanged.
		text := doc.Data
		if source == "base64" {
			if decoded, err := base64.StdEncoding.DecodeString(doc.Data); err == nil {
				text = string(decoded)
			}
		}
		block = anthropic.NewDocumentBlock(anthropic.PlainTextSourceParam{Data: text})
	default:
		return nil
	}
	if block.OfDocument != nil {
		if doc.Title != "" {
			block.OfDocument.Title = anthropic.String(doc.Title)
		}
		if doc.Context != "" {
			block.OfDocument.Context = anthropic.String(doc.Context)
		}
		if doc.EnableCitations {
			block.OfDocument.Citations = anthropic.CitationsConfigParam{Enabled: anthropic.Bool(true)}
		}
	}
	return &block
}

// createImageBlock creates an Anthropic image content block from ImageContent
func createImageBlock(img llmtypes.ImageContent) *anthropic.ContentBlockParamUnion {
	if img.SourceType == "base64" {
		// Use helper function for base64 images
		imageBlock := anthropic.NewImageBlockBase64(img.MediaType, img.Data)
		return &imageBlock
	} else if img.SourceType == "url" {
		// Create URL image source and use NewImageBlock
		urlSource := anthropic.URLImageSourceParam{
			URL: img.Data,
		}
		imageBlock := anthropic.NewImageBlock(urlSource)
		return &imageBlock
	}
	// Invalid source type
	return nil
}

// convertTools converts llmtypes tools to Anthropic tool format
func convertTools(llmTools []llmtypes.Tool) []anthropic.ToolUnionParam {
	anthropicTools := make([]anthropic.ToolUnionParam, 0, len(llmTools))

	for _, tool := range llmTools {
		if tool.Function == nil {
			continue
		}

		// Extract function parameters as JSON schema
		var parameters map[string]interface{}
		if tool.Function.Parameters != nil {
			// Convert from typed Parameters to map
			// Parameters is now *llmtypes.Parameters, so convert it to map
			paramsBytes, err := json.Marshal(tool.Function.Parameters)
			if err == nil {
				var paramsMap map[string]interface{}
				if err := json.Unmarshal(paramsBytes, &paramsMap); err == nil {
					parameters = paramsMap
				}
			}
		}

		if parameters == nil {
			parameters = make(map[string]interface{})
		}

		parameters = llmtypes.ModelToolSchema(parameters)

		// Extract required fields from parameters if available
		var required []string
		if req, ok := parameters["required"].([]interface{}); ok {
			required = make([]string, 0, len(req))
			for _, r := range req {
				if str, ok := r.(string); ok {
					required = append(required, str)
				}
			}
		}

		// Extract properties (remove type and required from parameters for InputSchema)
		properties := make(map[string]interface{})
		if props, ok := parameters["properties"].(map[string]interface{}); ok {
			properties = props
		}

		// Create Anthropic tool with InputSchema using helper
		// Type defaults to "object" if elided
		inputSchema := anthropic.ToolInputSchemaParam{
			Properties: properties,
			Required:   required,
		}
		anthropicTool := anthropic.ToolUnionParamOfTool(inputSchema, tool.Function.Name)

		// Set description on the inner ToolParam variant. The Anthropic docs
		// explicitly state: "Tool descriptions should be as detailed as
		// possible. The more information that the model has about what the
		// tool is and how to use it, the better it will perform." Dropping
		// the description forces the model to disambiguate tools by name
		// alone, which degrades selection quality on any multi-tool prompt.
		if desc := strings.TrimSpace(tool.Function.Description); desc != "" && anthropicTool.OfTool != nil {
			anthropicTool.OfTool.Description = anthropic.String(desc)
		}

		anthropicTools = append(anthropicTools, anthropicTool)
	}

	return anthropicTools
}

// convertToolChoice converts langchaingo tool choice to Anthropic tool choice format
func convertToolChoice(toolChoice interface{}) anthropic.ToolChoiceUnionParam {
	// Handle string-based tool choice
	if choiceStr, ok := toolChoice.(string); ok {
		switch choiceStr {
		case "auto":
			return anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		case "none":
			return anthropic.ToolChoiceUnionParam{
				OfNone: &anthropic.ToolChoiceNoneParam{},
			}
		case "required":
			return anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			}
		default:
			// Default to auto
			return anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		}
	}

	// Handle ToolChoice struct if it's that type
	if tc, ok := toolChoice.(*llmtypes.ToolChoice); ok && tc != nil {
		// Handle function-specific tool choice
		if tc.Function != nil && tc.Function.Name != "" {
			return anthropic.ToolChoiceParamOfTool(tc.Function.Name)
		}
		// Handle type-based tool choice
		switch tc.Type {
		case "auto":
			return anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		case "none":
			return anthropic.ToolChoiceUnionParam{
				OfNone: &anthropic.ToolChoiceNoneParam{},
			}
		case "required", "any":
			return anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			}
		default:
			// Default to auto
			return anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		}
	}

	// Handle map-based tool choice (from ConvertToolChoice)
	if choiceMap, ok := toolChoice.(map[string]interface{}); ok {
		if typ, ok := choiceMap["type"].(string); ok && typ == "function" {
			if fnMap, ok := choiceMap["function"].(map[string]interface{}); ok {
				if name, ok := fnMap["name"].(string); ok {
					// Function-specific tool choice
					return anthropic.ToolChoiceParamOfTool(name)
				}
			}
		}
	}

	// Default to auto
	return anthropic.ToolChoiceUnionParam{
		OfAuto: &anthropic.ToolChoiceAutoParam{},
	}
}

// convertResponse converts Anthropic response to llmtypes ContentResponse
func convertResponse(result *anthropic.Message) *llmtypes.ContentResponse {
	if result == nil {
		return &llmtypes.ContentResponse{
			Choices: []*llmtypes.ContentChoice{},
			Usage:   nil,
		}
	}

	choices := make([]*llmtypes.ContentChoice, 0, 1) // Anthropic typically returns one choice

	choice := &llmtypes.ContentChoice{}

	// Extract text content, tool calls, and thinking blocks from the
	// response. Thinking blocks are kept out of the assistant content
	// (they're internal reasoning, not the user-facing reply) and
	// surfaced separately via GenerationInfo.Additional so callers that
	// need them — tracing, debugging, follow-up turns that want to
	// re-feed the thinking back into Claude — can opt in.
	var textParts []string
	var toolCalls []llmtypes.ToolCall
	var thinkingParts []string
	var thinkingSignatures []string

	// Content is a slice of ContentBlockUnion
	for _, block := range result.Content {
		// ContentBlockUnion uses Type field to determine the variant
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			// Convert tool use to tool call
			var argsJSON []byte
			if len(block.Input) > 0 {
				argsJSON = block.Input
			} else {
				argsJSON = []byte("{}")
			}

			toolCall := llmtypes.ToolCall{
				ID:   block.ID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			}
			toolCalls = append(toolCalls, toolCall)
		case "thinking":
			// Extended-thinking reasoning. Block.Thinking holds the
			// raw chain-of-thought text; Block.Signature is the
			// integrity stamp Anthropic returns so the same thinking
			// block can be re-sent in a follow-up turn (required for
			// tool_use turns when thinking is on).
			if block.Thinking != "" {
				thinkingParts = append(thinkingParts, block.Thinking)
			}
			if block.Signature != "" {
				thinkingSignatures = append(thinkingSignatures, block.Signature)
			}
		}
	}

	// Combine text parts
	if len(textParts) > 0 {
		choice.Content = strings.Join(textParts, "\n")
	}

	// Set tool calls if any
	if len(toolCalls) > 0 {
		choice.ToolCalls = toolCalls
	}

	// Extract stop reason
	if result.StopReason != "" {
		choice.StopReason = string(result.StopReason)
	}

	// Extract token usage if available
	// Usage is not a pointer in Anthropic SDK
	inputTokens := int(result.Usage.InputTokens)
	outputTokens := int(result.Usage.OutputTokens)
	totalTokens := int(result.Usage.InputTokens + result.Usage.OutputTokens)

	genInfo := &llmtypes.GenerationInfo{
		InputTokens:     &inputTokens,
		OutputTokens:    &outputTokens,
		TotalTokens:     &totalTokens,
		InputTokensCap:  &inputTokens,
		OutputTokensCap: &outputTokens,
	}

	// Cache tokens if available
	// Anthropic returns cache tokens in Usage.CacheReadInputTokens and CacheCreationInputTokens
	// CacheReadInputTokens: tokens read from cache (appears when cache is used)
	// CacheCreationInputTokens: tokens used to create cache (appears when cache is created)

	// Debug: Always store cache token values (even if 0) to help debugging
	// Store them in Additional map so we can see what Anthropic actually returned
	if genInfo.Additional == nil {
		genInfo.Additional = make(map[string]interface{})
	}

	// Always store raw values for debugging (even if 0)
	genInfo.Additional["_debug_cache_read_raw"] = int(result.Usage.CacheReadInputTokens)
	genInfo.Additional["_debug_cache_creation_raw"] = int(result.Usage.CacheCreationInputTokens)

	if result.Usage.CacheReadInputTokens > 0 {
		cacheReadTokens := int(result.Usage.CacheReadInputTokens)
		genInfo.Additional["cache_read_input_tokens"] = cacheReadTokens
		genInfo.Additional["CacheReadInputTokens"] = cacheReadTokens
		// Also populate CachedContentTokens for consistency with other providers
		genInfo.CachedContentTokens = &cacheReadTokens
	}
	if result.Usage.CacheCreationInputTokens > 0 {
		cacheCreationTokens := int(result.Usage.CacheCreationInputTokens)
		genInfo.Additional["cache_creation_input_tokens"] = cacheCreationTokens
		genInfo.Additional["CacheCreationInputTokens"] = cacheCreationTokens
	}

	// Extended-thinking output. We keep it out of the assistant content
	// (it is internal reasoning, not the user-facing reply) but expose
	// the joined chain-of-thought + each block's signature via
	// Additional so callers that need to re-feed the thinking back to
	// Claude on a follow-up turn (required for tool-use continuations
	// when thinking is on) can do so.
	if len(thinkingParts) > 0 {
		genInfo.Additional["thinking"] = strings.Join(thinkingParts, "\n")
		genInfo.Additional["thinking_blocks"] = len(thinkingParts)
	}
	if len(thinkingSignatures) > 0 {
		genInfo.Additional["thinking_signatures"] = thinkingSignatures
	}

	choice.GenerationInfo = genInfo

	choices = append(choices, choice)

	// Extract usage from GenerationInfo
	usage := llmtypes.ExtractUsageFromGenerationInfo(genInfo)
	return &llmtypes.ContentResponse{
		Choices: choices,
		Usage:   usage,
	}
}

// logInputDetails logs the input parameters before making the API call
func (a *AnthropicAdapter) logInputDetails(modelID string, messages []llmtypes.MessageContent, params anthropic.MessageNewParams, opts *llmtypes.CallOptions) {
	// Build input summary
	inputSummary := map[string]interface{}{
		"model_id":      modelID,
		"message_count": len(messages),
		"temperature":   opts.Temperature,
		"max_tokens":    opts.MaxTokens,
		"json_mode":     opts.JSONMode,
		"tools_count":   len(opts.Tools),
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

	// Add params details
	// Temperature is param.Opt[float64] - always log if set (param.Opt has IsOmitted check)
	// Since we only set it if opts.Temperature > 0, we can check that
	if opts.Temperature > 0 {
		inputSummary["params_temperature"] = opts.Temperature
	}
	if params.MaxTokens > 0 {
		inputSummary["params_max_tokens"] = params.MaxTokens
	}
	if len(params.System) > 0 {
		inputSummary["params_has_system"] = true
	}
	if len(params.Tools) > 0 {
		inputSummary["params_tools_count"] = len(params.Tools)
	}
	// Check if tool choice is set (check if any field is non-nil)
	if params.ToolChoice.OfAuto != nil || params.ToolChoice.OfAny != nil || params.ToolChoice.OfTool != nil || params.ToolChoice.OfNone != nil {
		inputSummary["params_tool_choice"] = "set"
	}

	// Check for cache control in system prompts and messages (for debugging cache functionality)
	cacheControlCount := 0
	cacheControlDetails := []map[string]interface{}{}

	// Check system prompts for cache control
	for i, systemBlock := range params.System {
		hasCacheControl := systemBlock.CacheControl.TTL != "" || systemBlock.CacheControl.Type != ""
		if hasCacheControl {
			cacheControlCount++
			textLength := len(systemBlock.Text)
			estimatedTokens := textLength / 4
			cacheControlDetails = append(cacheControlDetails, map[string]interface{}{
				"location":         "system",
				"block_index":      i,
				"text_length":      textLength,
				"estimated_tokens": estimatedTokens,
				"ttl":              systemBlock.CacheControl.TTL,
				"type":             systemBlock.CacheControl.Type,
			})
			// DEBUG: Print cache control details using logger
			if a.logger != nil {
				a.logger.Debugf("[ANTHROPIC DEBUG] Found cache control in system prompt - block %d: TTL=%s, Type=%s, TextLength=%d, EstimatedTokens=%d",
					i, systemBlock.CacheControl.TTL, systemBlock.CacheControl.Type, textLength, estimatedTokens)
			}
		}
	}

	// Check user messages for cache control
	for i, msg := range params.Messages {
		if msg.Role == anthropic.MessageParamRoleUser {
			for j, block := range msg.Content {
				if textBlock := block.OfText; textBlock != nil {
					// Check if cache control is set (check both TTL and Type)
					hasCacheControl := textBlock.CacheControl.TTL != "" || textBlock.CacheControl.Type != ""
					if hasCacheControl {
						cacheControlCount++
						// Log detailed cache control info
						textLength := len(textBlock.Text)
						estimatedTokens := textLength / 4
						cacheControlDetails = append(cacheControlDetails, map[string]interface{}{
							"location":         "message",
							"message_index":    i,
							"block_index":      j,
							"text_length":      textLength,
							"estimated_tokens": estimatedTokens,
							"ttl":              textBlock.CacheControl.TTL,
							"type":             textBlock.CacheControl.Type,
						})
						// DEBUG: Print cache control details using logger
						if a.logger != nil {
							a.logger.Debugf("[ANTHROPIC DEBUG] Found cache control in params - message %d, block %d: TTL=%s, Type=%s, TextLength=%d, EstimatedTokens=%d",
								i, j, textBlock.CacheControl.TTL, textBlock.CacheControl.Type, textLength, estimatedTokens)
						}
					}
				}
			}
		}
	}

	if cacheControlCount > 0 {
		inputSummary["cache_control_blocks"] = cacheControlCount
		inputSummary["cache_enabled"] = true
		inputSummary["cache_control_details"] = cacheControlDetails
		if a.logger != nil {
			a.logger.Debugf("[ANTHROPIC DEBUG] Total cache control blocks found: %d (system: %d, messages: %d)",
				cacheControlCount, len(params.System), len(params.Messages))
		}
	} else {
		// Only log as info (not warning) since this is expected for small messages
		if a.logger != nil {
			systemSize := 0
			if len(params.System) > 0 {
				systemSize = len(params.System[0].Text)
			}
			a.logger.Debugf("[ANTHROPIC DEBUG] No cache control blocks found. System prompt size: %d chars (~%d tokens), Messages: %d",
				systemSize, systemSize/4, len(params.Messages))
		}
	}

	a.logger.Debugf("Anthropic GenerateContent INPUT - %+v", inputSummary)
}

// logErrorDetails logs both input and error response details when an error occurs
func (a *AnthropicAdapter) logErrorDetails(modelID string, messages []llmtypes.MessageContent, params anthropic.MessageNewParams, opts *llmtypes.CallOptions, err error, result *anthropic.Message) {
	// Log error with input context
	errorInfo := map[string]interface{}{
		"error":         err.Error(),
		"error_type":    fmt.Sprintf("%T", err),
		"model_id":      modelID,
		"message_count": len(messages),
	}

	// Extract detailed error information if it's an API error
	// Anthropic SDK uses shared.Error types - check for APIErrorObject
	if errMsg := err.Error(); errMsg != "" {
		errorInfo["error_details"] = errMsg
	}

	// Add params summary
	if opts.Temperature > 0 {
		errorInfo["temperature"] = opts.Temperature
	}
	if params.MaxTokens > 0 {
		errorInfo["max_tokens"] = params.MaxTokens
	}
	if len(params.System) > 0 {
		errorInfo["has_system"] = true
	}
	if len(params.Tools) > 0 {
		errorInfo["tools_count"] = len(params.Tools)
	}

	// Add response details if available (even though there was an error)
	if result != nil {
		responseInfo := map[string]interface{}{}

		// Extract content preview
		for _, block := range result.Content {
			if block.Type == "text" && block.Text != "" {
				content := block.Text
				if len(content) > 500 {
					content = content[:500] + "..."
				}
				responseInfo["content_preview"] = content
				responseInfo["content_length"] = len(block.Text)
				break
			}
		}

		if len(result.Content) > 0 {
			responseInfo["content_blocks_count"] = len(result.Content)
		}
		if result.StopReason != "" {
			responseInfo["stop_reason"] = string(result.StopReason)
		}

		if len(responseInfo) > 0 {
			errorInfo["response"] = responseInfo
		}

		// Add usage information (Usage is not a pointer)
		errorInfo["usage"] = map[string]interface{}{
			"input_tokens":  result.Usage.InputTokens,
			"output_tokens": result.Usage.OutputTokens,
		}
	}

	// Log comprehensive error information
	a.logger.Errorf("Anthropic GenerateContent ERROR - %+v", errorInfo)

	// Also log input details for full context
	a.logInputDetails(modelID, messages, params, opts)
}

// logRawInput logs the complete raw params that will be sent to the Anthropic API
func (a *AnthropicAdapter) logRawInput(requestID, modelID string, params anthropic.MessageNewParams) {
	if a.logger == nil {
		return
	}

	// Build complete input structure by marshaling params to JSON
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		a.logger.Errorf("❌ [ANTHROPIC] Failed to marshal params for raw input logging: %v", err)
		return
	}

	var paramsMap map[string]interface{}
	if err := json.Unmarshal(paramsJSON, &paramsMap); err != nil {
		a.logger.Errorf("❌ [ANTHROPIC] Failed to unmarshal params JSON: %v", err)
		return
	}

	rawInput := map[string]interface{}{
		"request_id": requestID,
		"model_id":   modelID,
		"params":     paramsMap,
	}

	rawInputJSON, _ := json.MarshalIndent(rawInput, "", "  ")
	a.logger.Debugf("🔍 [REQUEST_ID: %s] RAW ANTHROPIC API INPUT (FULL JSON):\n%s", requestID, string(rawInputJSON))
}

// logRawResponse logs the complete raw response from the Anthropic API
func (a *AnthropicAdapter) logRawResponse(requestID, modelID string, message *anthropic.Message, err error) {
	if a.logger == nil {
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
		a.logger.Errorf("❌ [REQUEST_ID: %s] RAW ANTHROPIC API ERROR (FULL JSON):\n%s", requestID, string(errorJSON))
		return
	}

	// Build complete response structure
	responseSummary := map[string]interface{}{
		"request_id":  requestID,
		"model_id":    modelID,
		"content":     make([]map[string]interface{}, 0, len(message.Content)),
		"stop_reason": string(message.StopReason),
		"usage": map[string]interface{}{
			"input_tokens":  message.Usage.InputTokens,
			"output_tokens": message.Usage.OutputTokens,
		},
	}

	// Add all content blocks (full, no truncation)
	for _, block := range message.Content {
		blockDetail := make(map[string]interface{})
		blockDetail["type"] = block.Type

		if block.Type == "text" {
			blockDetail["text"] = block.Text
		} else if block.Type == "tool_use" {
			blockDetail["id"] = block.ID
			blockDetail["name"] = block.Name
			// Convert input to JSON string (full, no truncation)
			inputJSON, err := json.Marshal(block.Input)
			if err == nil {
				blockDetail["input"] = string(inputJSON)
			} else {
				blockDetail["input"] = "{}"
			}
		}

		responseSummary["content"] = append(responseSummary["content"].([]map[string]interface{}), blockDetail)
	}

	responseJSON, _ := json.MarshalIndent(responseSummary, "", "  ")
	a.logger.Debugf("🔍 [REQUEST_ID: %s] COMPLETE RAW ANTHROPIC API RESPONSE (FULL JSON):\n%s", requestID, string(responseJSON))
}
