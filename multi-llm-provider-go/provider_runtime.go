package llmproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type ProviderAwareLLM struct {
	llmtypes.Model
	provider     Provider
	modelID      string
	eventEmitter interfaces.EventEmitter
	traceID      interfaces.TraceID
	logger       interfaces.Logger
}

const (
	providerAwareMessageLogMaxChars    = 500
	providerAwarePromptLogMaxChars     = 2000
	providerAwareToolNamesLogMaxChars  = 4000
	providerAwareToolSchemaLogMaxChars = 2000
)

// NewProviderAwareLLM creates a new provider-aware LLM wrapper
func NewProviderAwareLLM(llm llmtypes.Model, provider Provider, modelID string, eventEmitter interfaces.EventEmitter, traceID interfaces.TraceID, logger interfaces.Logger) *ProviderAwareLLM {
	// Use no-op logger if nil is provided
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	return &ProviderAwareLLM{
		Model:        llm,
		provider:     provider,
		modelID:      modelID,
		eventEmitter: eventEmitter,
		traceID:      traceID,
		logger:       logger,
	}
}

// GetProvider returns the provider of this LLM
func (p *ProviderAwareLLM) GetProvider() Provider {
	return p.provider
}

// GetModelID returns the model ID of this LLM
func (p *ProviderAwareLLM) GetModelID() string {
	return p.modelID
}

// GenerateContent wraps the underlying LLM's GenerateContent method to automatically capture token usage
// extractTextFromParts extracts text content from message parts
func extractTextFromParts(parts []llmtypes.ContentPart) string {
	var textParts []string
	for _, part := range parts {
		if textPart, ok := part.(llmtypes.TextContent); ok {
			textParts = append(textParts, textPart.Text)
		}
	}
	return strings.Join(textParts, " ")
}

func truncateProviderAwareLogText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) == 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + fmt.Sprintf("... [truncated, total length: %d chars]", len(runes))
}

func providerAwareVerboseRequestLogging() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MULTI_LLM_VERBOSE_REQUEST_LOGS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func providerAwareRequestPayloadLoggingEnabled() bool {
	if providerAwareVerboseRequestLogging() {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MULTI_LLM_REQUEST_LOGS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (p *ProviderAwareLLM) logRequestPayload(messages []llmtypes.MessageContent, options ...llmtypes.CallOption) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}

	// Extract and log system prompts
	var systemPrompts []string
	for _, msg := range messages {
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			text := extractTextFromParts(msg.Parts)
			if text != "" {
				systemPrompts = append(systemPrompts, text)
			}
		}
	}
	if len(systemPrompts) > 0 {
		p.logger.Infof("📋 SYSTEM PROMPTS (%d):", len(systemPrompts))
		for i, prompt := range systemPrompts {
			p.logger.Infof("   [%d] length=%d preview=%s", i+1, len([]rune(prompt)), truncateProviderAwareLogText(prompt, providerAwarePromptLogMaxChars))
		}
	} else {
		p.logger.Infof("📋 SYSTEM PROMPTS: None")
	}

	// Log all messages
	p.logger.Infof("💬 MESSAGES (%d):", len(messages))
	for i, msg := range messages {
		text := extractTextFromParts(msg.Parts)
		displayText := truncateProviderAwareLogText(text, providerAwareMessageLogMaxChars)
		p.logger.Infof("   [%d] Role: %s, Content: %s", i+1, msg.Role, displayText)
	}

	// Log tools if provided
	if len(opts.Tools) > 0 {
		p.logger.Infof("🔧 TOOLS (%d):", len(opts.Tools))
		toolNames := make([]string, 0, len(opts.Tools))
		for i, tool := range opts.Tools {
			if tool.Function != nil {
				toolNames = append(toolNames, tool.Function.Name)
			} else {
				toolNames = append(toolNames, fmt.Sprintf("tool[%d]=<nil function>", i+1))
			}
		}
		p.logger.Infof("   Names: %s", truncateProviderAwareLogText(strings.Join(toolNames, ", "), providerAwareToolNamesLogMaxChars))

		if providerAwareVerboseRequestLogging() {
			for i, tool := range opts.Tools {
				if tool.Function != nil {
					toolJSON, err := json.MarshalIndent(tool, "      ", "  ")
					if err != nil {
						p.logger.Infof("   [%d] %s (error marshaling: %v)", i+1, tool.Function.Name, err)
					} else {
						p.logger.Infof("   [%d] %s schema preview:\n%s", i+1, tool.Function.Name, truncateProviderAwareLogText(string(toolJSON), providerAwareToolSchemaLogMaxChars))
					}
				} else {
					p.logger.Infof("   [%d] Tool with nil Function", i+1)
				}
			}
		}
	} else {
		p.logger.Infof("🔧 TOOLS: None")
	}
}

func (p *ProviderAwareLLM) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Note: LLM generation start event is now emitted at the agent level to avoid duplication

	// Automatically add usage parameter for OpenRouter requests to get cache token information
	if p.provider == ProviderOpenRouter {
		options = append(options, WithOpenRouterUsage())
	}

	if providerAwareRequestPayloadLoggingEnabled() {
		p.logRequestPayload(messages, options...)
	}

	// Log request timing
	requestStartTime := time.Now()
	p.logger.Infof("⏱️  LLM REQUEST START - Time: %s", requestStartTime.Format(time.RFC3339))

	// Call the underlying LLM
	resp, err := p.Model.GenerateContent(ctx, messages, options...)

	// Log response timing
	requestEndTime := time.Now()
	duration := requestEndTime.Sub(requestStartTime)
	p.logger.Infof("⏱️  LLM RESPONSE RECEIVED - Time: %s, Duration: %v", requestEndTime.Format(time.RFC3339), duration)

	// Check if we have a valid response
	if err != nil {
		// Classify so consumers can branch on cause (rate limit vs auth vs
		// outage) via llmerrors.KindOf instead of string-matching. The
		// original error stays in the chain (Unwrap), so existing
		// string-based handling keeps working.
		err = llmerrors.Classify(string(p.provider), p.modelID, err)
		p.logger.Infof("❌ LLM generation failed - provider: %s, model: %s, kind: %s, error: %v", string(p.provider), p.modelID, llmerrors.KindOf(err), err)

		// Emit LLM generation error event with rich debugging information
		errorMetadata := LLMMetadata{
			User: "llm_generation_user",
			CustomFields: map[string]string{
				"provider":        string(p.provider),
				"model_id":        p.modelID,
				"messages":        fmt.Sprintf("%d", len(messages)),
				"temperature":     fmt.Sprintf("%f", getTemperatureFromOptions(options)),
				"message_content": extractMessageContentAsString(messages),
				"error":           err.Error(),
				"error_type":      fmt.Sprintf("%T", err),
				"error_kind":      string(llmerrors.KindOf(err)),
				"debug_note":      "Enhanced error logging for turn 2 debugging",
			},
		}
		emitLLMGenerationError(p.eventEmitter, string(p.provider), p.modelID, OperationLLMGeneration, len(messages), getTemperatureFromOptions(options), extractMessageContentAsString(messages), err, p.traceID, errorMetadata)

		return nil, err
	}

	// Validate response structure
	if resp == nil {
		p.logger.Infof("❌ Response is nil")

		// Emit LLM generation error event for nil response
		errorMetadata := LLMMetadata{
			User: "llm_generation_user",
			CustomFields: map[string]string{
				"debug_note": "Response validation failed - nil response",
			},
		}
		emitLLMGenerationError(p.eventEmitter, string(p.provider), p.modelID, OperationLLMGeneration, len(messages), getTemperatureFromOptions(options), extractMessageContentAsString(messages), fmt.Errorf("response validation failed - nil response"), p.traceID, errorMetadata)

		return nil, fmt.Errorf("response is nil")
	}

	if resp.Choices == nil {
		p.logger.Infof("❌ Response.Choices is nil")

		// Emit LLM generation error event for nil choices
		errorMetadata := LLMMetadata{
			User: "llm_generation_user",
			CustomFields: map[string]string{
				"provider":        string(p.provider),
				"model_id":        p.modelID,
				"messages":        fmt.Sprintf("%d", len(messages)),
				"temperature":     fmt.Sprintf("%f", getTemperatureFromOptions(options)),
				"message_content": extractMessageContentAsString(messages),
				"error":           "Response.Choices is nil",
				"debug_note":      "Response validation failed - nil choices",
			},
		}
		emitLLMGenerationError(p.eventEmitter, string(p.provider), p.modelID, OperationLLMGeneration, len(messages), getTemperatureFromOptions(options), extractMessageContentAsString(messages), fmt.Errorf("response.Choices is nil"), p.traceID, errorMetadata)

		return nil, fmt.Errorf("response.Choices is nil")
	}

	if len(resp.Choices) == 0 {
		p.logger.Infof("❌ Response.Choices is empty array - this will cause 'no results' error")

		// Enhanced logging for ALL providers when choices array is empty
		p.logger.Errorf("🔍 Empty Choices Array Debug Information for %s:", string(p.provider))
		p.logger.Errorf("   Model ID: %s", p.modelID)
		p.logger.Errorf("   Provider: %s", string(p.provider))
		p.logger.Errorf("   Response Type: %T", resp)
		p.logger.Errorf("   Response Pointer: %p", resp)
		p.logger.Errorf("   Choices Array Length: %d", len(resp.Choices))
		p.logger.Errorf("   Choices Array Nil: %v", resp.Choices == nil)
		p.logger.Errorf("   Choices Array Cap: %d", cap(resp.Choices))

		// Log the ENTIRE response structure for comprehensive debugging
		p.logger.Errorf("🔍 COMPLETE LLM RESPONSE STRUCTURE:")
		p.logger.Errorf("   Full Response: %+v", resp)

		// Log the options that were passed to the LLM
		p.logger.Errorf("🔍 LLM CALL OPTIONS:")
		for i, opt := range options {
			p.logger.Errorf("   Option %d: %T = %+v", i+1, opt, opt)
		}

		// Log the messages that were sent to the LLM
		p.logger.Errorf("🔍 MESSAGES SENT TO LLM:")
		for i, msg := range messages {
			p.logger.Errorf("   Message %d - Role: %s, Parts: %d", i+1, msg.Role, len(msg.Parts))
			for j, part := range msg.Parts {
				p.logger.Errorf("     Part %d - Type: %T, Content: %+v", j+1, part, part)
			}
		}

		// Emit LLM generation error event for empty choices
		errorMetadata := LLMMetadata{
			User: "llm_generation_user",
			CustomFields: map[string]string{
				"provider":        string(p.provider),
				"model_id":        p.modelID,
				"messages":        fmt.Sprintf("%d", len(messages)),
				"temperature":     fmt.Sprintf("%f", getTemperatureFromOptions(options)),
				"message_content": extractMessageContentAsString(messages),
				"error":           "Response.Choices is empty",
				"debug_note":      "Response validation failed - empty choices array",
			},
		}
		emitLLMGenerationError(p.eventEmitter, string(p.provider), p.modelID, OperationLLMGeneration, len(messages), getTemperatureFromOptions(options), extractMessageContentAsString(messages), fmt.Errorf("response.Choices is empty"), p.traceID, errorMetadata)

		return nil, fmt.Errorf("response.Choices is empty")
	}

	// Validate first choice has content
	firstChoice := resp.Choices[0]
	if firstChoice.Content == "" {
		// Check if this is a valid tool call response
		if len(firstChoice.ToolCalls) > 0 {
			p.logger.Infof("✅ Valid tool call response detected - Content is empty but ToolCalls present")
			p.logger.Infof("   Tool Calls: %d", len(firstChoice.ToolCalls))
			for i, toolCall := range firstChoice.ToolCalls {
				functionName := "N/A"
				arguments := "{}"
				if toolCall.FunctionCall != nil {
					functionName = toolCall.FunctionCall.Name
					if toolCall.FunctionCall.Arguments != "" {
						arguments = toolCall.FunctionCall.Arguments
					}
				}
				p.logger.Infof("   Tool Call %d: ID=%s, Type=%s, Function=%s, Arguments=%s",
					i+1, toolCall.ID, toolCall.Type, functionName, arguments)
			}
			// Note: Tool call events are emitted later in the function (line ~1594) to avoid duplication
			// This is a valid response, continue processing
		} else if firstChoice.FuncCall != nil { // Legacy function call handling
			p.logger.Infof("✅ Valid function call response detected - Content is empty but FuncCall present")
			p.logger.Infof("   Function Call: Name=%s", firstChoice.FuncCall.Name)
			// This is a valid response, continue processing
		} else if handle, ok := llmtypes.ExtractCodingProviderSessionHandle(firstChoice.GenerationInfo); ok && !handle.Empty() {
			// LaunchOnly response: the adapter successfully started/reacquired the tmux transport
			// and embedded the session handle in GenerationInfo. Empty Content is intentional here.
			p.logger.Infof("✅ Valid coding-agent launch-only response - Content is empty but session handle present (provider=%s, tmux=%s)", handle.Provider, handle.TmuxSession)
			// This is a valid response, continue processing
		} else {
			// This is actually an empty content error
			p.logger.Infof("❌ Choice.Content is empty - this will cause 'no results' error")

			// Enhanced logging for ALL providers when choice content is empty
			p.logger.Errorf("🔍 Empty Choice Content Debug Information for %s:", string(p.provider))
			p.logger.Errorf("   Model ID: %s", p.modelID)
			p.logger.Errorf("   Provider: %s", string(p.provider))
			p.logger.Errorf("   Response Type: %T", resp)
			p.logger.Errorf("   Response Pointer: %p", resp)
			p.logger.Errorf("   Choices Count: %d", len(resp.Choices))
			p.logger.Errorf("   First Choice Type: %T", firstChoice)
			p.logger.Errorf("   First Choice Content Empty: %v", firstChoice.Content == "")

			p.logger.Errorf("   First Choice Content Length: %d", len(firstChoice.Content))

			// Detailed choice structure logging
			p.logger.Errorf("🔍 DETAILED CHOICE STRUCTURE:")
			p.logger.Errorf("   Choice.StopReason: %v", firstChoice.StopReason)
			toolCallsCount := 0
			if firstChoice.ToolCalls != nil {
				toolCallsCount = len(firstChoice.ToolCalls)
			}
			p.logger.Errorf("   Choice.ToolCalls: %v (nil: %v, count: %d)", firstChoice.ToolCalls != nil, firstChoice.ToolCalls == nil, toolCallsCount)
			if len(firstChoice.ToolCalls) > 0 {
				for i, tc := range firstChoice.ToolCalls {
					p.logger.Errorf("     ToolCall %d: ID=%s, Type=%s, FunctionName=%s, Arguments=%s",
						i+1, tc.ID, tc.Type, tc.FunctionCall.Name, truncateString(tc.FunctionCall.Arguments, 200))
				}
			}
			p.logger.Errorf("   Choice.FuncCall: %v", firstChoice.FuncCall != nil)
			if firstChoice.FuncCall != nil {
				p.logger.Errorf("     FuncCall Name: %s, Arguments: %s",
					firstChoice.FuncCall.Name, truncateString(firstChoice.FuncCall.Arguments, 200))
			}
			p.logger.Errorf("   Choice.GenerationInfo: %v (nil: %v)", firstChoice.GenerationInfo != nil, firstChoice.GenerationInfo == nil)
			if firstChoice.GenerationInfo != nil {
				info := firstChoice.GenerationInfo
				p.logger.Errorf("     GenerationInfo: InputTokens=%v, OutputTokens=%v, TotalTokens=%v",
					info.InputTokens, info.OutputTokens, info.TotalTokens)
				// Log additional fields if present
				if info.Additional != nil {
					for key, value := range info.Additional {
						valueStr := fmt.Sprintf("%v", value)
						if len(valueStr) > 200 {
							valueStr = truncateString(valueStr, 200)
						}
						p.logger.Errorf("       %s: %s (type: %T)", key, valueStr, value)
					}
				}
			}

			// Log the ENTIRE response structure for comprehensive debugging
			p.logger.Errorf("🔍 COMPLETE LLM RESPONSE STRUCTURE:")
			p.logger.Errorf("   Full Response: %+v", resp)

			// Serialize response to JSON for raw-like representation
			// Note: This is the processed response from langchaingo, not the raw HTTP response
			// but it gives us a JSON representation of what we received
			if respJSON, err := json.MarshalIndent(resp, "   ", "  "); err == nil {
				jsonStr := string(respJSON)
				// Truncate if too long to avoid massive log files
				if len(jsonStr) > 5000 {
					jsonStr = jsonStr[:5000] + "\n   ... (truncated, total length: " + fmt.Sprintf("%d", len(jsonStr)) + " bytes)"
				}
				p.logger.Errorf("🔍 RAW RESPONSE AS JSON (processed by langchaingo):")
				p.logger.Errorf("%s", jsonStr)
			} else {
				p.logger.Errorf("   ⚠️ Failed to serialize response to JSON: %w", err)
			}

			// Log the options that were passed to the LLM
			p.logger.Errorf("🔍 LLM CALL OPTIONS:")
			for i, opt := range options {
				p.logger.Errorf("   Option %d: %T = %+v", i+1, opt, opt)
			}

			// Log the messages that were sent to the LLM
			p.logger.Errorf("🔍 MESSAGES SENT TO LLM:")
			for i, msg := range messages {
				p.logger.Errorf("   Message %d - Role: %s, Parts: %d", i+1, msg.Role, len(msg.Parts))
				for j, part := range msg.Parts {
					p.logger.Errorf("     Part %d - Type: %T, Content: %+v", j+1, part, part)
				}
			}

			// Emit LLM generation error event for empty choice content
			errorMetadata := LLMMetadata{
				User: "llm_generation_user",
				CustomFields: map[string]string{
					"provider":        string(p.provider),
					"model_id":        p.modelID,
					"messages":        fmt.Sprintf("%d", len(messages)),
					"temperature":     fmt.Sprintf("%f", getTemperatureFromOptions(options)),
					"message_content": extractMessageContentAsString(messages),
					"error":           "Choice.Content is empty",
					"debug_note":      "Response validation failed - empty content",
				},
			}
			emitLLMGenerationError(p.eventEmitter, string(p.provider), p.modelID, OperationLLMGeneration, len(messages), getTemperatureFromOptions(options), extractMessageContentAsString(messages), fmt.Errorf("choice.Content is empty"), p.traceID, errorMetadata)

			// Include a provider-specific API error when available.
			emptyContentErr := "choice.Content is empty"
			if firstChoice.GenerationInfo != nil && firstChoice.GenerationInfo.Additional != nil {
				if apiErr, ok := firstChoice.GenerationInfo.Additional["gemini_api_error"].(string); ok && apiErr != "" {
					emptyContentErr = fmt.Sprintf("choice.Content is empty: %s", apiErr)
				}
			}
			return nil, fmt.Errorf("%s", emptyContentErr)
		}
	}

	// 🆕 ENHANCED SUCCESS LOGGING
	p.logger.Infof("✅ LLM generation validation passed - provider: %s, model: %s", string(p.provider), p.modelID)
	p.logger.Infof("✅ Response structure - Choices: %v, Choices count: %d", resp.Choices != nil, len(resp.Choices))
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		p.logger.Infof("✅ First choice - Content: %v, Content length: %d, GenerationInfo: %v",
			choice.Content != "", len(choice.Content), choice.GenerationInfo != nil)
		if choice.GenerationInfo != nil {
			p.logger.Infof("✅ GenerationInfo available: InputTokens=%v, OutputTokens=%v, TotalTokens=%v",
				choice.GenerationInfo.InputTokens, choice.GenerationInfo.OutputTokens, choice.GenerationInfo.TotalTokens)
		}

		// Log tool calls if present (even when content is also present)
		if len(choice.ToolCalls) > 0 {
			p.logger.Infof("🔧 TOOL CALLS IN RESPONSE (%d):", len(choice.ToolCalls))
			for i, toolCall := range choice.ToolCalls {
				functionName := "N/A"
				arguments := "{}"
				if toolCall.FunctionCall != nil {
					functionName = toolCall.FunctionCall.Name
					if toolCall.FunctionCall.Arguments != "" {
						arguments = toolCall.FunctionCall.Arguments
					}
				}
				p.logger.Infof("   Tool Call %d: ID=%s, Type=%s, Function=%s, Arguments=%s",
					i+1, toolCall.ID, toolCall.Type, functionName, arguments)
			}
		}

		// Emit tool call events for all tool calls (even when content is present)
		if len(choice.ToolCalls) > 0 {
			for _, toolCall := range choice.ToolCalls {
				toolName := ""
				arguments := "{}"
				if toolCall.FunctionCall != nil {
					toolName = toolCall.FunctionCall.Name
					if toolCall.FunctionCall.Arguments != "" {
						arguments = toolCall.FunctionCall.Arguments
					}
				}

				toolCallMetadata := LLMMetadata{
					User: "tool_call_user",
					CustomFields: map[string]string{
						"provider":     string(p.provider),
						"model_id":     p.modelID,
						"tool_call_id": toolCall.ID,
						"tool_type":    toolCall.Type,
						"tool_name":    toolName,
					},
				}
				emitToolCallDetected(p.eventEmitter, string(p.provider), p.modelID, toolCall.ID, toolName, arguments, p.traceID, toolCallMetadata)
			}
		}
	}

	// Extract token usage using unified Usage struct (comprehensive extraction)
	var usage *llmtypes.Usage
	if resp.Usage != nil {
		// Use unified Usage field (already populated by adapters with all token types)
		usage = resp.Usage
	} else if len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		// Fallback: Extract from GenerationInfo using comprehensive extraction
		usage = llmtypes.ExtractUsageFromGenerationInfo(resp.Choices[0].GenerationInfo)
	}

	if usage != nil {
		// Calculate total tokens if not provided by the provider
		if usage.TotalTokens == 0 && usage.InputTokens > 0 && usage.OutputTokens > 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}

		// Build comprehensive log message with all token types
		logMsg := fmt.Sprintf("Token usage extracted: Input=%d, Output=%d, Total=%d", usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
		if usage.CacheTokens != nil && *usage.CacheTokens > 0 {
			logMsg += fmt.Sprintf(", Cached=%d", *usage.CacheTokens)
		}
		if usage.ThoughtsTokens != nil && *usage.ThoughtsTokens > 0 {
			logMsg += fmt.Sprintf(", Thoughts=%d", *usage.ThoughtsTokens)
		}
		if usage.ReasoningTokens != nil && *usage.ReasoningTokens > 0 {
			logMsg += fmt.Sprintf(", Reasoning=%d", *usage.ReasoningTokens)
		}
		p.logger.Infof(logMsg)

		// Emit LLM generation success event with comprehensive token usage
		successMetadata := LLMMetadata{
			User: "llm_generation_user",
			CustomFields: map[string]string{
				"provider":        string(p.provider),
				"model_id":        p.modelID,
				"messages":        fmt.Sprintf("%d", len(messages)),
				"temperature":     fmt.Sprintf("%f", getTemperatureFromOptions(options)),
				"message_content": extractMessageContentAsString(messages),
				"response_length": fmt.Sprintf("%d", len(resp.Choices[0].Content)),
				"choices_count":   fmt.Sprintf("%d", len(resp.Choices)),
				"input_tokens":    fmt.Sprintf("%d", usage.InputTokens),
				"output_tokens":   fmt.Sprintf("%d", usage.OutputTokens),
				"total_tokens":    fmt.Sprintf("%d", usage.TotalTokens),
			},
		}

		// Add optional token types to metadata if present
		if usage.CacheTokens != nil && *usage.CacheTokens > 0 {
			successMetadata.CustomFields["cache_tokens"] = fmt.Sprintf("%d", *usage.CacheTokens)
		}
		if usage.ThoughtsTokens != nil && *usage.ThoughtsTokens > 0 {
			successMetadata.CustomFields["thoughts_tokens"] = fmt.Sprintf("%d", *usage.ThoughtsTokens)
		}
		if usage.ReasoningTokens != nil && *usage.ReasoningTokens > 0 {
			successMetadata.CustomFields["reasoning_tokens"] = fmt.Sprintf("%d", *usage.ReasoningTokens)
		}

		successMetadata.CustomFields["note"] = "Token usage extracted from unified Usage struct"
		emitLLMGenerationSuccess(p.eventEmitter, string(p.provider), p.modelID, OperationLLMGeneration, len(messages), getTemperatureFromOptions(options), extractMessageContentAsString(messages), len(resp.Choices[0].Content), len(resp.Choices), p.traceID, successMetadata)
	} else {
		// No token usage available, emit success event without usage
		p.logger.Infof("No token usage available (neither resp.Usage nor GenerationInfo)")

		// Emit LLM generation success event without token usage
		successMetadata := LLMMetadata{
			User: "llm_generation_user",
			CustomFields: map[string]string{
				"provider":        string(p.provider),
				"model_id":        p.modelID,
				"messages":        fmt.Sprintf("%d", len(messages)),
				"temperature":     fmt.Sprintf("%f", getTemperatureFromOptions(options)),
				"message_content": extractMessageContentAsString(messages),
				"response_length": fmt.Sprintf("%d", len(resp.Choices[0].Content)),
				"choices_count":   fmt.Sprintf("%d", len(resp.Choices)),
				"note":            "No GenerationInfo available for token usage",
			},
		}
		emitLLMGenerationSuccess(p.eventEmitter, string(p.provider), p.modelID, OperationLLMGeneration, len(messages), getTemperatureFromOptions(options), extractMessageContentAsString(messages), len(resp.Choices[0].Content), len(resp.Choices), p.traceID, successMetadata)
	}

	return resp, nil
}

// extractMessageContentAsString converts message content to a readable string
func extractMessageContentAsString(messages []llmtypes.MessageContent) string {
	if len(messages) == 0 {
		return "no messages"
	}

	var result strings.Builder
	for i, msg := range messages {
		if i > 0 {
			result.WriteString(" | ")
		}
		result.WriteString(fmt.Sprintf("Role:%s", msg.Role))

		for j, part := range msg.Parts {
			if j > 0 {
				result.WriteString(",")
			}
			if textPart, ok := part.(llmtypes.TextContent); ok {
				content := textPart.Text
				if len(content) > 100 {
					content = content[:100] + "..."
				}
				result.WriteString(fmt.Sprintf("Text:%s", content))
			} else {
				result.WriteString(fmt.Sprintf("Part:%T", part))
			}
		}
	}
	return result.String()
}

// getTemperatureFromOptions extracts temperature from call options
func getTemperatureFromOptions(options []llmtypes.CallOption) float64 {
	// For now, return default temperature since CallOption is a function type
	// and we can't easily extract the temperature value
	return 0.7 // default temperature
}

// truncateString truncates a string to a specified length
func truncateString(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length] + "..."
}

// WithOpenRouterUsage enables usage parameter for OpenRouter requests to get cache token information
