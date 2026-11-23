package azure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/internal/recorder"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

// AzureAdapter is an adapter that implements llmtypes.Model interface
// using Azure AI Services / Azure AI Foundry with OpenAI-compatible API
type AzureAdapter struct {
	client     *openai.Client
	modelID    string
	logger     interfaces.Logger
	endpoint   string
	apiKey     string
	apiVersion string
}

// AzureConfig holds Azure-specific configuration
type AzureConfig struct {
	Endpoint   string // Azure AI endpoint (e.g., https://xxx.services.ai.azure.com/api/projects/xxx)
	APIKey     string // Azure API key
	APIVersion string // API version (e.g., 2024-02-01)
	Region     string // Azure region (optional, for logging)
}

// NewAzureAdapter creates a new Azure adapter instance
func NewAzureAdapter(config AzureConfig, modelID string, logger interfaces.Logger) *AzureAdapter {
	// Construct base URL for Azure AI
	baseURL := config.Endpoint

	// Azure AI Services endpoint format (services.ai.azure.com):
	// The endpoint may contain /api/projects/{project-name} but we need the base resource URL
	// Extract base URL: https://{resource}.services.ai.azure.com
	if strings.Contains(baseURL, "services.ai.azure.com") {
		// Extract the base resource URL (everything up to and including .services.ai.azure.com)
		idx := strings.Index(baseURL, "services.ai.azure.com")
		if idx != -1 {
			baseURL = baseURL[:idx+len("services.ai.azure.com")]
		}
		// Use OpenAI v1 API path
		baseURL = baseURL + "/openai/v1/"
	} else if strings.Contains(baseURL, "cognitiveservices.azure.com") {
		// Azure Cognitive Services with OpenAI-compatible API
		if !strings.HasSuffix(baseURL, "/") {
			baseURL = baseURL + "/"
		}
		if !strings.Contains(baseURL, "/openai/v1") {
			baseURL = baseURL + "openai/v1/"
		}
	} else if strings.Contains(baseURL, "openai.azure.com") {
		// Azure OpenAI - use /openai/ path with deployments
		if !strings.HasSuffix(baseURL, "/") {
			baseURL = baseURL + "/"
		}
		if !strings.Contains(baseURL, "/openai") {
			baseURL = baseURL + "openai/"
		}
	} else {
		// Generic endpoint - add trailing slash
		if !strings.HasSuffix(baseURL, "/") {
			baseURL = baseURL + "/"
		}
	}

	// Add API version as query parameter (Azure requires this)
	apiVersion := config.APIVersion
	if apiVersion == "" {
		// Use different default versions based on endpoint type
		if strings.Contains(baseURL, "services.ai.azure.com") {
			apiVersion = "v1" // Azure AI Services with OpenAI v1 API
		} else if strings.Contains(baseURL, "cognitiveservices.azure.com") {
			apiVersion = "v1" // Responses API requires v1
		} else {
			apiVersion = "2024-10-21" // Azure OpenAI API
		}
	}

	// Create OpenAI SDK client with Azure configuration
	// Azure uses api-key header instead of Authorization Bearer
	// Use QueryAdd to add api-version query parameter to all requests (except for OpenAI v1 compatible endpoints)
	clientOptions := []option.RequestOption{
		option.WithHeader("api-key", config.APIKey),
		option.WithBaseURL(baseURL),
	}

	// Only add api-version if not a v1-compatible endpoint and not explicitly empty
	if !strings.Contains(baseURL, "/openai/v1") && apiVersion != "" {
		clientOptions = append(clientOptions, option.WithQueryAdd("api-version", apiVersion))
	}

	client := openai.NewClient(clientOptions...)

	return &AzureAdapter{
		client:     &client,
		modelID:    modelID,
		logger:     logger,
		endpoint:   config.Endpoint,
		apiKey:     config.APIKey,
		apiVersion: apiVersion,
	}
}

// GetModelID implements the llmtypes.Model interface
func (a *AzureAdapter) GetModelID() string {
	return a.modelID
}

// GetModelMetadata implements the llmtypes.Model interface
// attachCostEstimate fills GenerationInfo.Additional["cost_usd_estimated"]
// from tokens × registry rates. Azure OpenAI responses carry usage
// but no USD field.
func (a *AzureAdapter) attachCostEstimate(resp *llmtypes.ContentResponse, modelID string) {
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

func (a *AzureAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if modelID == "" {
		modelID = a.modelID
	}
	return GetAzureModelMetadata(modelID)
}

// GenerateContent implements the llmtypes.Model interface
func (a *AzureAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
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

	return llmtypes.WithObservability(ctx, llmtypes.ObservabilityConfig{
		Provider:     "azure",
		Model:        modelID,
		Opts:         opts,
		MessageCount: len(messages),
		Messages:     messages,
		HeaderLine:   fmt.Sprintf("azure.chat.completions model=%s msgs=%d tools=%d", modelID, len(messages), len(opts.Tools)),
		// Not yet reachable from AgentWorks workflow steps, but the CLI
		// adapters already declare "tmux" / "structured_cli" here so their
		// transport-class chip renders correctly; this is the same
		// declaration for a direct API call, so the label is correct the
		// day this adapter IS wired in.
		RequestMetaExtra: map[string]interface{}{"transport": "api"},
	}, func(sink *llmtypes.StreamSink) (*llmtypes.ContentResponse, error) {
		_ = sink
		resp, err := a.generateContentInner(ctx, opts, modelID, messages)
		if err == nil {
			a.attachCostEstimate(resp, modelID)
		}
		return resp, err
	})
}

func (a *AzureAdapter) generateContentInner(ctx context.Context, opts *llmtypes.CallOptions, modelID string, messages []llmtypes.MessageContent) (*llmtypes.ContentResponse, error) {

	// Convert messages from llmtypes format to OpenAI format
	openaiMessages := convertMessages(messages, a.logger)

	// Build ChatCompletionNewParams from options
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(modelID),
		Messages: openaiMessages,
	}

	// Set temperature - some models (gpt-5, o1, o3, o4) only support default temperature (1.0)
	if opts.Temperature > 0 && !hasTemperatureRestrictions(modelID) {
		params.Temperature = param.NewOpt(opts.Temperature)
	}

	// Handle JSON Schema structured outputs
	if opts.JSONSchema != nil {
		schemaParam := openai.ResponseFormatJSONSchemaJSONSchemaParam{
			Name:        opts.JSONSchema.Name,
			Description: param.NewOpt(opts.JSONSchema.Description),
			Schema:      opts.JSONSchema.Schema,
			Strict:      param.NewOpt(opts.JSONSchema.Strict),
		}
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: schemaParam},
		}
	}

	// Convert tools if provided (required for SDK path — mirrors OpenAI adapter)
	if len(opts.Tools) > 0 {
		params.Tools = convertTools(opts.Tools)

		// Handle tool choice - only set when tools are provided
		if opts.ToolChoice != nil {
			toolChoice := convertToolChoice(opts.ToolChoice)
			if toolChoice != nil {
				params.ToolChoice = *toolChoice
			}
		}
	}

	// Handle reasoning effort (for gpt-5.1 and similar models)
	if opts.ReasoningEffort != "" {
		if a.logger != nil {
			a.logger.Debugf("Setting reasoning_effort to: %s", opts.ReasoningEffort)
		}
		// Convert string to shared.ReasoningEffort type and set it
		params.ReasoningEffort = shared.ReasoningEffort(opts.ReasoningEffort)
	}

	// Handle verbosity (for reasoning models)
	if opts.Verbosity != "" {
		if a.logger != nil {
			a.logger.Debugf("Setting verbosity to: %s", opts.Verbosity)
		}
		// Convert string to ChatCompletionNewParamsVerbosity type and set it
		params.Verbosity = openai.ChatCompletionNewParamsVerbosity(opts.Verbosity)
	}

	// Log input details if logger is available
	if a.logger != nil {
		a.logger.Debugf("Azure GenerateContent INPUT - model: %s, messages: %d, endpoint: %s",
			modelID, len(messages), a.endpoint)
	}

	// Check if this is an agentic model that requires the Responses API
	if isAgenticModel(modelID) {
		// Check for recorder in context
		rec, _ := recorder.FromContext(ctx)
		if rec != nil {
			if rec.IsReplayEnabled() {
				// Build request info for matching
				requestInfo := buildRequestInfo(messages, modelID, opts)

				// Load recorded response
				recordedResponse, err := rec.LoadResponsesAPIResponse(requestInfo)
				if err != nil {
					if a.logger != nil {
						a.logger.Errorf("Failed to load recorded Responses API response: %v", err)
					}
					return nil, fmt.Errorf("failed to load recorded Responses API response: %w", err)
				}

				if a.logger != nil {
					a.logger.Infof("▶️  [RECORDER] Replaying recorded Azure Responses API response")
				}

				return a.convertResponsesAPIResponse(recordedResponse)
			}
		}

		resp, err := a.runRawResponsesAPI(ctx, modelID, messages, opts)
		if err != nil {
			return nil, err
		}

		// Record response if recording is enabled
		if rec != nil && rec.IsRecordingEnabled() {
			// We need the raw response map for recording
			// For simplicity in this implementation, we re-parse it in runRawResponsesAPI or here
			// I will modify runRawResponsesAPI to return both or handle recording inside
		}

		return resp, nil
	}

	// Check if streaming is requested
	if opts.StreamChan != nil {
		// Enable usage in streaming responses
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		}
		return a.generateContentStreaming(ctx, modelID, params, opts, messages)
	}

	// Call Azure API (non-streaming)
	result, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		if a.logger != nil {
			a.logger.Errorf("Azure GenerateContent ERROR - model: %s, error: %v", modelID, err)
		}
		return nil, fmt.Errorf("azure generate content: %w", err)
	}

	// Convert response from OpenAI format to llmtypes format
	return convertResponse(result, a.logger), nil
}

// generateContentStreaming handles streaming responses from Azure API
func (a *AzureAdapter) generateContentStreaming(ctx context.Context, modelID string, params openai.ChatCompletionNewParams, opts *llmtypes.CallOptions, messages []llmtypes.MessageContent) (*llmtypes.ContentResponse, error) {
	// Create streaming request
	stream := a.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()
	// opts.StreamChan close is owned by WithObservability.

	// Accumulate response data
	var accumulatedContent strings.Builder
	var accumulatedToolCalls []llmtypes.ToolCall
	var finishReason string
	var streamModel string
	var usage *openai.CompletionUsage

	// Track tool calls by index
	toolCallMap := make(map[int64]*llmtypes.ToolCall)
	completedToolCallIndices := make(map[int64]bool)

	// Process streaming chunks
	for stream.Next() {
		chunk := stream.Current()

		// Store model from first chunk
		if streamModel == "" {
			streamModel = chunk.Model
		}

		// Extract usage from chunk if available
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = &chunk.Usage
		}

		// Process each choice in the chunk
		for _, choice := range chunk.Choices {
			// Extract text delta and accumulate
			if choice.Delta.Content != "" {
				deltaText := choice.Delta.Content
				accumulatedContent.WriteString(deltaText)

				// Stream content chunks immediately
				if opts.StreamChan != nil {
					select {
					case opts.StreamChan <- llmtypes.StreamChunk{
						Type:    llmtypes.StreamChunkTypeContent,
						Content: deltaText,
					}:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}

			// Handle tool call deltas
			if len(choice.Delta.ToolCalls) > 0 {
				for _, toolCallDelta := range choice.Delta.ToolCalls {
					index := toolCallDelta.Index

					// Initialize tool call if not exists
					if toolCallMap[index] == nil {
						toolCallMap[index] = &llmtypes.ToolCall{
							ID:   toolCallDelta.ID,
							Type: toolCallDelta.Type,
							FunctionCall: &llmtypes.FunctionCall{
								Name:      toolCallDelta.Function.Name,
								Arguments: "",
							},
						}
					}

					// Update ID if provided
					if toolCallDelta.ID != "" {
						toolCallMap[index].ID = toolCallDelta.ID
					}

					// Update type if provided
					if toolCallDelta.Type != "" {
						toolCallMap[index].Type = toolCallDelta.Type
					}

					// Update function name if provided
					if toolCallDelta.Function.Name != "" {
						toolCallMap[index].FunctionCall.Name = toolCallDelta.Function.Name
					}

					// Accumulate function arguments
					if toolCallDelta.Function.Arguments != "" {
						currentArgs := toolCallMap[index].FunctionCall.Arguments
						toolCallMap[index].FunctionCall.Arguments = currentArgs + toolCallDelta.Function.Arguments
					}
				}
			}

			// Store finish reason from last chunk
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason

				// When finish_reason is "tool_calls", all tool calls are complete
				if choice.FinishReason == "tool_calls" {
					for index := range toolCallMap {
						if !completedToolCallIndices[index] {
							completedToolCallIndices[index] = true
							if opts.StreamChan != nil {
								toolCall := toolCallMap[index]
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

	// Check for stream errors
	if err := stream.Err(); err != nil {
		if a.logger != nil {
			a.logger.Errorf("Azure streaming error - model: %s, error: %v", modelID, err)
		}
		return nil, fmt.Errorf("azure streaming error: %w", err)
	}

	// Convert accumulated tool calls to slice
	for index, toolCall := range toolCallMap {
		accumulatedToolCalls = append(accumulatedToolCalls, *toolCall)
		if !completedToolCallIndices[index] && finishReason == "tool_calls" && opts.StreamChan != nil {
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
	choice := &llmtypes.ContentChoice{
		Content:    accumulatedContent.String(),
		StopReason: finishReason,
		ToolCalls:  accumulatedToolCalls,
	}

	// Add usage information if available
	if usage != nil {
		inputTokens := int(usage.PromptTokens)
		outputTokens := int(usage.CompletionTokens)
		totalTokens := int(usage.TotalTokens)

		choice.GenerationInfo = &llmtypes.GenerationInfo{
			InputTokens:         &inputTokens,
			OutputTokens:        &outputTokens,
			TotalTokens:         &totalTokens,
			PromptTokens:        &inputTokens,
			CompletionTokens:    &outputTokens,
			PromptTokensCap:     &inputTokens,
			CompletionTokensCap: &outputTokens,
			TotalTokensCap:      &totalTokens,
		}
	}

	// Extract token usage from GenerationInfo
	tokenUsage := llmtypes.ExtractUsageFromGenerationInfo(choice.GenerationInfo)
	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{choice},
		Usage:   tokenUsage,
	}, nil
}

// convertMessages converts llmtypes messages to OpenAI message format
func convertMessages(langMessages []llmtypes.MessageContent, logger interfaces.Logger) []openai.ChatCompletionMessageParamUnion {
	openaiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(langMessages))

	for _, msg := range langMessages {
		// Extract content parts
		var contentParts []string
		var imageParts []llmtypes.ImageContent
		var toolResponses []llmtypes.ToolCallResponse
		var toolCalls []llmtypes.ToolCall

		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				contentParts = append(contentParts, p.Text)
			case llmtypes.ImageContent:
				imageParts = append(imageParts, p)
			case llmtypes.ToolCallResponse:
				toolResponses = append(toolResponses, p)
			case llmtypes.ToolCall:
				toolCalls = append(toolCalls, p)
			}
		}

		// Create appropriate message type based on role
		switch string(msg.Role) {
		case string(llmtypes.ChatMessageTypeSystem):
			content := strings.Join(contentParts, "\n")
			openaiMessages = append(openaiMessages, openai.SystemMessage(content))

		case string(llmtypes.ChatMessageTypeHuman):
			if len(imageParts) > 0 {
				contentPartsArray := make([]openai.ChatCompletionContentPartUnionParam, 0)
				for _, text := range contentParts {
					if text != "" {
						contentPartsArray = append(contentPartsArray, openai.TextContentPart(text))
					}
				}
				for _, img := range imageParts {
					imagePart := createImageContentPart(img)
					if imagePart != nil {
						contentPartsArray = append(contentPartsArray, *imagePart)
					}
				}
				if len(contentPartsArray) > 0 {
					openaiMessages = append(openaiMessages, openai.UserMessage(contentPartsArray))
				}
			} else {
				content := strings.Join(contentParts, "\n")
				openaiMessages = append(openaiMessages, openai.UserMessage(content))
			}

		case string(llmtypes.ChatMessageTypeAI):
			content := strings.Join(contentParts, "\n")
			if len(toolCalls) > 0 {
				openaiToolCalls := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(toolCalls))
				for _, tc := range toolCalls {
					functionToolCall := openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      tc.FunctionCall.Name,
						Arguments: tc.FunctionCall.Arguments,
					}
					openaiToolCalls = append(openaiToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID:       tc.ID,
							Type:     "function",
							Function: functionToolCall,
						},
					})
				}
				assistantMsg := openai.ChatCompletionAssistantMessageParam{
					ToolCalls: openaiToolCalls,
				}
				if content != "" {
					assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.NewOpt(content),
					}
				}
				openaiMessages = append(openaiMessages, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &assistantMsg,
				})
			} else {
				openaiMessages = append(openaiMessages, openai.AssistantMessage(content))
			}

		case string(llmtypes.ChatMessageTypeTool):
			if len(toolResponses) > 0 {
				for _, toolResp := range toolResponses {
					if toolResp.ToolCallID == "" {
						continue
					}
					openaiMessages = append(openaiMessages, openai.ToolMessage(toolResp.Content, toolResp.ToolCallID))
				}
			}

		default:
			content := strings.Join(contentParts, "\n")
			openaiMessages = append(openaiMessages, openai.UserMessage(content))
		}
	}

	return openaiMessages
}

// createImageContentPart creates an OpenAI image content part from ImageContent
func createImageContentPart(img llmtypes.ImageContent) *openai.ChatCompletionContentPartUnionParam {
	if img.SourceType == "base64" {
		dataURL := fmt.Sprintf("data:%s;base64,%s", img.MediaType, img.Data)
		imageURLParam := openai.ChatCompletionContentPartImageImageURLParam{
			URL: dataURL,
		}
		imagePart := openai.ImageContentPart(imageURLParam)
		return &imagePart
	} else if img.SourceType == "url" {
		imageURLParam := openai.ChatCompletionContentPartImageImageURLParam{
			URL: img.Data,
		}
		imagePart := openai.ImageContentPart(imageURLParam)
		return &imagePart
	}
	return nil
}

// convertToolChoice converts llmtypes tool choice to OpenAI tool choice format
func convertToolChoice(toolChoice interface{}) *openai.ChatCompletionToolChoiceOptionUnionParam {
	if toolChoice == nil {
		return nil
	}

	if choiceStr, ok := toolChoice.(string); ok {
		switch choiceStr {
		case "auto":
			result := openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: param.NewOpt("auto"),
			}
			return &result
		case "none":
			result := openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: param.NewOpt("none"),
			}
			return &result
		case "required":
			result := openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: param.NewOpt("required"),
			}
			return &result
		default:
			result := openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: param.NewOpt("auto"),
			}
			return &result
		}
	}

	if tc, ok := toolChoice.(*llmtypes.ToolChoice); ok && tc != nil {
		result := openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: param.NewOpt("auto"),
		}
		return &result
	}

	if choiceMap, ok := toolChoice.(map[string]interface{}); ok {
		if typ, ok := choiceMap["type"].(string); ok && typ == "function" {
			if fnMap, ok := choiceMap["function"].(map[string]interface{}); ok {
				if name, ok := fnMap["name"].(string); ok {
					result := openai.ToolChoiceOptionFunctionToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{
						Name: name,
					})
					return &result
				}
			}
		}
	}

	result := openai.ChatCompletionToolChoiceOptionUnionParam{
		OfAuto: param.NewOpt("auto"),
	}
	return &result
}

// convertTools converts llmtypes tools to OpenAI SDK tools format for the Azure SDK path.
// This mirrors the OpenAI adapter's convertTools function since both use the same SDK.
func convertTools(llmTools []llmtypes.Tool) []openai.ChatCompletionToolUnionParam {
	openaiTools := make([]openai.ChatCompletionToolUnionParam, 0, len(llmTools))

	for _, tool := range llmTools {
		if tool.Function == nil {
			continue
		}

		var parameters shared.FunctionParameters
		if tool.Function.Parameters != nil {
			paramsMap := make(map[string]interface{})
			if tool.Function.Parameters.Type != "" {
				paramsMap["type"] = tool.Function.Parameters.Type
			}
			if len(tool.Function.Parameters.Properties) > 0 {
				paramsMap["properties"] = tool.Function.Parameters.Properties
			}
			if len(tool.Function.Parameters.Required) > 0 {
				paramsMap["required"] = tool.Function.Parameters.Required
			}
			if tool.Function.Parameters.AdditionalProperties != nil {
				paramsMap["additionalProperties"] = tool.Function.Parameters.AdditionalProperties
			}
			if tool.Function.Parameters.PatternProperties != nil {
				paramsMap["patternProperties"] = tool.Function.Parameters.PatternProperties
			}
			if tool.Function.Parameters.Additional != nil {
				for k, v := range tool.Function.Parameters.Additional {
					paramsMap[k] = v
				}
			}
			paramsMap = llmtypes.ModelToolSchema(paramsMap)

			// OpenAI requires properties to be present when type is "object"
			if paramsMap["type"] == "object" {
				if _, hasProperties := paramsMap["properties"]; !hasProperties {
					paramsMap["properties"] = map[string]interface{}{
						"_": map[string]interface{}{
							"type":        "string",
							"description": "Unused parameter",
						},
					}
				}
			}
			parameters = shared.FunctionParameters(paramsMap)
		}

		functionDef := shared.FunctionDefinitionParam{
			Name:        tool.Function.Name,
			Description: param.NewOpt(tool.Function.Description),
			Parameters:  parameters,
		}

		openaiTools = append(openaiTools, openai.ChatCompletionFunctionTool(functionDef))
	}

	return openaiTools
}

// hasTemperatureRestrictions checks if a model only supports default temperature (1.0)
func hasTemperatureRestrictions(modelID string) bool {
	modelIDLower := strings.ToLower(modelID)
	restrictedModels := []string{
		"gpt-5",
		"gpt-5.1",
		"gpt-5.2",
		"o1",
		"o1-mini",
		"o1-preview",
		"o3",
		"o3-mini",
		"o4",
		"o4-mini",
	}

	for _, restricted := range restrictedModels {
		if strings.Contains(modelIDLower, restricted) {
			return true
		}
	}
	return false
}

// isAgenticModel checks if a model requires the Responses API
// This includes GPT-5 series models and codex variants
func isAgenticModel(modelID string) bool {
	modelIDLower := strings.ToLower(modelID)
	// GPT-5.x models use Responses API
	if strings.Contains(modelIDLower, "gpt-5") {
		return true
	}
	// Codex models use Responses API
	if strings.Contains(modelIDLower, "codex") {
		return true
	}
	return false
}

// convertResponse converts OpenAI response to llmtypes ContentResponse
func convertResponse(result *openai.ChatCompletion, logger interfaces.Logger) *llmtypes.ContentResponse {
	if result == nil {
		return &llmtypes.ContentResponse{
			Choices: []*llmtypes.ContentChoice{},
			Usage:   nil,
		}
	}

	choices := make([]*llmtypes.ContentChoice, 0, len(result.Choices))

	for _, choice := range result.Choices {
		langChoice := &llmtypes.ContentChoice{}

		if choice.Message.Content != "" {
			langChoice.Content = choice.Message.Content
		}

		if len(choice.Message.ToolCalls) > 0 {
			toolCalls := make([]llmtypes.ToolCall, 0, len(choice.Message.ToolCalls))
			for _, tc := range choice.Message.ToolCalls {
				langToolCall := llmtypes.ToolCall{
					ID:   tc.ID,
					Type: string(tc.Type),
				}
				langToolCall.FunctionCall = &llmtypes.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: convertArgumentsToString(tc.Function.Arguments),
				}
				toolCalls = append(toolCalls, langToolCall)
			}
			langChoice.ToolCalls = toolCalls
		}

		if choice.FinishReason != "" {
			langChoice.StopReason = choice.FinishReason
		}

		inputTokens := int(result.Usage.PromptTokens)
		outputTokens := int(result.Usage.CompletionTokens)
		totalTokens := int(result.Usage.TotalTokens)

		langChoice.GenerationInfo = &llmtypes.GenerationInfo{
			InputTokens:         &inputTokens,
			OutputTokens:        &outputTokens,
			TotalTokens:         &totalTokens,
			PromptTokens:        &inputTokens,
			CompletionTokens:    &outputTokens,
			PromptTokensCap:     &inputTokens,
			CompletionTokensCap: &outputTokens,
			TotalTokensCap:      &totalTokens,
		}

		choices = append(choices, langChoice)
	}

	var usage *llmtypes.Usage
	if len(choices) > 0 && choices[0].GenerationInfo != nil {
		usage = llmtypes.ExtractUsageFromGenerationInfo(choices[0].GenerationInfo)
	}

	return &llmtypes.ContentResponse{
		Choices: choices,
		Usage:   usage,
	}
}

// runRawResponsesAPI makes a direct HTTP call to the /responses API for agentic models
func (a *AzureAdapter) runRawResponsesAPI(ctx context.Context, modelID string, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	// Handle native streaming if requested
	if opts.StreamChan != nil {
		return a.executeResponsesStreamingRequest(ctx, modelID, messages, opts)
	}

	return a.executeResponsesRequest(ctx, modelID, messages, opts)
}

// executeResponsesStreamingRequest handles native SSE streaming for the Responses API
func (a *AzureAdapter) executeResponsesStreamingRequest(ctx context.Context, modelID string, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	// Convert messages to Responses API format
	apiMessages := convertMessagesForResponsesAPI(messages, a.logger)

	// Construct the Responses API payload with stream: true
	payload := map[string]interface{}{
		"model":  modelID,
		"input":  apiMessages,
		"stream": true,
	}

	// Add tools if provided (Responses API format: name, description, parameters at top level)
	if len(opts.Tools) > 0 {
		apiTools := make([]map[string]interface{}, 0, len(opts.Tools))
		for _, t := range opts.Tools {
			if t.Function == nil {
				continue
			}
			// Build parameters with valid schema structure for Responses API
			paramsMap := map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
			if t.Function.Parameters != nil {
				raw, err := json.Marshal(t.Function.Parameters)
				if err != nil {
					return nil, fmt.Errorf("invalid tool schema %s: %w", t.Function.Name, err)
				}
				if err := json.Unmarshal(raw, &paramsMap); err != nil {
					return nil, err
				}
			}
			paramsMap = llmtypes.ModelToolSchema(paramsMap)
			// Responses API expects flat structure, not nested under "function"
			toolObj := map[string]interface{}{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  paramsMap,
			}
			apiTools = append(apiTools, toolObj)
		}
		payload["tools"] = apiTools
	}

	// Handle tool choice - only set when tools are provided
	if opts.ToolChoice != nil && opts.ToolChoice.Type != "" && len(opts.Tools) > 0 {
		payload["tool_choice"] = opts.ToolChoice.Type
	}

	// Handle allowed_tools - only set when tools are provided
	if len(opts.AllowedTools) > 0 && len(opts.Tools) > 0 {
		payload["allowed_tools"] = opts.AllowedTools
	}

	// Construct full URL
	baseURL := a.endpoint
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if !strings.Contains(baseURL, "/openai/v1") {
		baseURL += "openai/v1/"
	}
	responsesURL := baseURL + "responses"

	// Append api-version if available
	useApiVersion := a.apiVersion
	// Force v1 for Responses API on Azure endpoints as other versions are typically not supported for this specialized API
	if strings.Contains(baseURL, ".azure.com") {
		useApiVersion = "v1"
	}

	if useApiVersion != "" {
		separator := "?"
		if strings.Contains(responsesURL, "?") {
			separator = "&"
		}
		responsesURL = fmt.Sprintf("%s%sapi-version=%s", responsesURL, separator, useApiVersion)
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal responses payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", responsesURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create responses request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", a.apiKey)

	client := &http.Client{}
	resp, err := client.Do(req) //nolint:bodyclose // Body closed in goroutine at line 911 or error paths

	// Helper to retry with derived endpoint if initial request fails
	if (err != nil || resp.StatusCode != http.StatusOK) && strings.Contains(a.endpoint, "services.ai.azure.com") {
		// Try to derive alternative endpoint
		parts := strings.Split(a.endpoint, "services.ai.azure.com")
		if len(parts) > 0 {
			prefix := parts[0]
			if strings.HasPrefix(prefix, "https://") {
				resourceName := strings.TrimPrefix(prefix, "https://")
				resourceName = strings.TrimSuffix(resourceName, ".")
				if resourceName != "" {
					derivedEndpoint := fmt.Sprintf("https://%s.cognitiveservices.azure.com/", resourceName)
					derivedURL := derivedEndpoint + "openai/v1/responses?api-version=v1"
					
					if a.logger != nil {
						a.logger.Infof("Azure streaming request failed, retrying with derived endpoint: %s", derivedEndpoint)
					}

					newReq, newErr := http.NewRequestWithContext(ctx, "POST", derivedURL, bytes.NewBuffer(jsonData))
					if newErr == nil {
						newReq.Header.Set("Content-Type", "application/json")
						newReq.Header.Set("api-key", a.apiKey)

						newResp, newErr := client.Do(newReq) //nolint:bodyclose // Body closed on success (replaces resp) or failure (line 883)
						if newErr == nil && newResp.StatusCode == http.StatusOK {
							// Retry succeeded! 
							// Close the ORIGINAL response body before replacing it
							if resp != nil && resp.Body != nil {
								resp.Body.Close()
							}

							resp = newResp
							err = nil
							if a.logger != nil {
								a.logger.Infof("Streaming retry successful!")
							}
						} else {
							// Retry failed. Close the NEW response if it exists.
							// Keep ORIGINAL 'resp' open for downstream error handling.
							if newResp != nil && newResp.Body != nil {
								newResp.Body.Close()
							}
						}
					}
				}
			}
		}
	}

	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, fmt.Errorf("execute responses streaming request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("responses API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Process SSE stream; wait for completion so we can return an accumulated response
	// (callers like the provider expect a non-nil response to avoid "response is nil" error)
	var accumulatedContent strings.Builder
	toolCallMap := make(map[string]*llmtypes.ToolCall)
	done := make(chan struct{})

	go func() {
		defer resp.Body.Close()
		// opts.StreamChan close is owned by WithObservability.
		defer close(done)

		scanner := bufio.NewScanner(resp.Body)
		// Increase buffer size to handle large events (e.g. response.created with many tools)
		// Default is 64KB, increasing to 5MB
		const maxCapacity = 5 * 1024 * 1024
		buf := make([]byte, maxCapacity)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var event map[string]interface{}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			eventType, _ := event["type"].(string)
			switch eventType {
			case "response.output_text.delta":
				if delta, ok := event["delta"].(string); ok {
					accumulatedContent.WriteString(delta)
					opts.StreamChan <- llmtypes.StreamChunk{
						Type:    llmtypes.StreamChunkTypeContent,
						Content: delta,
					}
				}
			case "response.output_text.done":
				// Fallback: some deployments send full text only in .done (e.g. gpt-5.2)
				// Only use if we haven't received any content via deltas
				if text, ok := event["text"].(string); ok && text != "" {
					if accumulatedContent.Len() == 0 {
						accumulatedContent.WriteString(text)
						opts.StreamChan <- llmtypes.StreamChunk{
							Type:    llmtypes.StreamChunkTypeContent,
							Content: text,
						}
					}
				}
			case "response.content_part.done":
				// Fallback: full text in part.text when content part is done
				if part, ok := event["part"].(map[string]interface{}); ok {
					if partType, _ := part["type"].(string); partType == "output_text" {
						if text, ok := part["text"].(string); ok && text != "" {
							if accumulatedContent.Len() == 0 {
								accumulatedContent.WriteString(text)
								opts.StreamChan <- llmtypes.StreamChunk{
									Type:    llmtypes.StreamChunkTypeContent,
									Content: text,
								}
							}
						}
					}
				}
			case "response.completed":
				// Azure may send full output only in response.completed (e.g. gpt-5.2)
				if respObj, ok := event["response"].(map[string]interface{}); ok {
					if outputArr, ok := respObj["output"].([]interface{}); ok {
						for _, outItem := range outputArr {
							item, _ := outItem.(map[string]interface{})
							if item == nil {
								continue
							}
							if itemType, _ := item["type"].(string); itemType != "message" {
								continue
							}
							if contentArr, ok := item["content"].([]interface{}); ok {
								for _, c := range contentArr {
									part, _ := c.(map[string]interface{})
									if part == nil {
										continue
									}
									if partType, _ := part["type"].(string); partType == "output_text" {
										if text, ok := part["text"].(string); ok && text != "" {
											if accumulatedContent.Len() == 0 {
												accumulatedContent.WriteString(text)
												opts.StreamChan <- llmtypes.StreamChunk{
													Type:    llmtypes.StreamChunkTypeContent,
													Content: text,
												}
											}
										}
									}
								}
							}
						}
					}
				}
			case "response.output_item.done":
				// Handle completed item (could be message or tool call)
				if item, ok := event["item"].(map[string]interface{}); ok {
					itemType, _ := item["type"].(string)
					if itemType == "function_call" {
						callID, _ := item["call_id"].(string)
						callName, _ := item["name"].(string)
						// Arguments can be a string or a map - handle both cases
						callArgs := convertArgumentsToString(item["arguments"])

						tc := llmtypes.ToolCall{
							ID:   callID,
							Type: "function",
							FunctionCall: &llmtypes.FunctionCall{
								Name:      callName,
								Arguments: callArgs,
							},
						}
						toolCallMap[callID] = &tc
						opts.StreamChan <- llmtypes.StreamChunk{
							Type:     llmtypes.StreamChunkTypeToolCall,
							ToolCall: &tc,
						}
					}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			if a.logger != nil {
				a.logger.Errorf("Azure SSE scanner error: %v", err)
			}
		}
	}()

	// Wait for stream to finish so we can return a non-nil response (avoids "all LLMs failed: response is nil")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
	}

	// Build response from accumulated content and tool calls
	var toolCalls []llmtypes.ToolCall
	for _, tc := range toolCallMap {
		toolCalls = append(toolCalls, *tc)
	}
	stopReason := "stop"
	if len(toolCalls) > 0 {
		stopReason = "tool_calls"
	}
	choices := []*llmtypes.ContentChoice{{
		Content:    accumulatedContent.String(),
		StopReason: stopReason,
		ToolCalls:  toolCalls,
	}}
	return &llmtypes.ContentResponse{
		Choices: choices,
		Usage:   nil, // Responses API stream may not include usage in SSE events
	}, nil
}

// executeResponsesRequest handles the actual HTTP request to the Responses API
func (a *AzureAdapter) executeResponsesRequest(ctx context.Context, modelID string, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (*llmtypes.ContentResponse, error) {
	// Convert messages to Responses API format
	apiMessages := convertMessagesForResponsesAPI(messages, a.logger)

	// Log the converted messages for debugging
	if a.logger != nil {
		a.logger.Debugf("Responses API - sending %d input messages (from %d original messages)", len(apiMessages), len(messages))
		for i, msg := range apiMessages {
			msgType := "unknown"
			if t, ok := msg["type"].(string); ok {
				msgType = t
			} else if r, ok := msg["role"].(string); ok {
				msgType = "role:" + r
			}
			a.logger.Debugf("  [%d] type=%s", i, msgType)
		}
	}

	// Construct the Responses API payload
	// The input field expects message objects
	payload := map[string]interface{}{
		"model": modelID,
		"input": apiMessages,
	}

	// Add tools if provided (Responses API format: name, description, parameters at top level)
	if len(opts.Tools) > 0 {
		apiTools := make([]map[string]interface{}, 0, len(opts.Tools))
		for _, t := range opts.Tools {
			if t.Function == nil {
				continue
			}
			// Build parameters with valid schema structure for Responses API
			paramsMap := map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
			if t.Function.Parameters != nil {
				raw, err := json.Marshal(t.Function.Parameters)
				if err != nil {
					return nil, fmt.Errorf("invalid tool schema %s: %w", t.Function.Name, err)
				}
				if err := json.Unmarshal(raw, &paramsMap); err != nil {
					return nil, err
				}
			}
			paramsMap = llmtypes.ModelToolSchema(paramsMap)
			// Responses API expects flat structure, not nested under "function"
			toolObj := map[string]interface{}{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  paramsMap,
			}
			apiTools = append(apiTools, toolObj)
		}
		payload["tools"] = apiTools
	}

	// Handle tool choice - only set when tools are provided
	if opts.ToolChoice != nil && len(opts.Tools) > 0 {
		if opts.ToolChoice.Type != "" {
			payload["tool_choice"] = opts.ToolChoice.Type
		}
	}

	// Handle allowed_tools - only set when tools are provided
	if len(opts.AllowedTools) > 0 && len(opts.Tools) > 0 {
		payload["allowed_tools"] = opts.AllowedTools
	}

	// Construct full URL
	baseURL := a.endpoint
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if !strings.Contains(baseURL, "/openai/v1") {
		baseURL += "openai/v1/"
	}
	responsesURL := baseURL + "responses"

	// Append api-version if available
	useApiVersion := a.apiVersion
	// Force v1 for Responses API on Azure endpoints as other versions are typically not supported for this specialized API
	if strings.Contains(baseURL, ".azure.com") {
		useApiVersion = "v1"
	}

	if useApiVersion != "" {
		separator := "?"
		if strings.Contains(responsesURL, "?") {
			separator = "&"
		}
		responsesURL = fmt.Sprintf("%s%sapi-version=%s", responsesURL, separator, useApiVersion)
	}

	// Create request
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal responses payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", responsesURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create responses request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", a.apiKey)

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	
	// Helper to retry with derived endpoint if initial request fails
	if (err != nil || resp.StatusCode != http.StatusOK) && strings.Contains(a.endpoint, "services.ai.azure.com") {
		// Try to derive alternative endpoint
		// Format: https://{resource}.services.ai.azure.com/api/projects/{project}
		// Target: https://{resource}.cognitiveservices.azure.com/
		parts := strings.Split(a.endpoint, "services.ai.azure.com")
		if len(parts) > 0 {
			prefix := parts[0]
			if strings.HasPrefix(prefix, "https://") {
				resourceName := strings.TrimPrefix(prefix, "https://")
				resourceName = strings.TrimSuffix(resourceName, ".")
				if resourceName != "" {
					derivedEndpoint := fmt.Sprintf("https://%s.cognitiveservices.azure.com/", resourceName)
					
					// Construct new URL
					derivedURL := derivedEndpoint + "openai/v1/responses?api-version=v1"
					
					if a.logger != nil {
						a.logger.Infof("Azure request failed, retrying with derived endpoint: %s", derivedEndpoint)
					}

					// Create new request
					newReq, newErr := http.NewRequestWithContext(ctx, "POST", derivedURL, bytes.NewBuffer(jsonData))
					if newErr == nil {
						newReq.Header.Set("Content-Type", "application/json")
						newReq.Header.Set("api-key", a.apiKey)
						
						newResp, newErr := client.Do(newReq)
						if newErr == nil && newResp.StatusCode == http.StatusOK {
							// Retry succeeded! 
							// Close the ORIGINAL response body before replacing it
							if resp != nil && resp.Body != nil {
								resp.Body.Close()
							}
							
							// Use the new response
							resp = newResp
							err = nil
							if a.logger != nil {
								a.logger.Infof("Retry successful!")
							}
						} else {
							// Retry failed. Close the NEW response if it exists.
							// We keep the ORIGINAL 'resp' (which is still open or nil) so downstream error handling sees the original error.
							if newResp != nil && newResp.Body != nil {
								newResp.Body.Close()
							}
							
							if a.logger != nil {
								a.logger.Infof("Retry failed (status %d)", 0)
								if newResp != nil {
									a.logger.Infof("Retry status: %d", newResp.StatusCode)
								}
							}
						}
					}
				}
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("execute responses request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read responses response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("responses API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResponse map[string]interface{}
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("unmarshal responses response: %w", err)
	}

	// Record response if recording is enabled
	rec, _ := recorder.FromContext(ctx)
	if rec != nil && rec.IsRecordingEnabled() {
		requestInfo := buildRequestInfo(messages, modelID, opts)
		filePath, err := rec.RecordResponsesAPIResponse(apiResponse, requestInfo)
		if err != nil {
			if a.logger != nil {
				a.logger.Errorf("Failed to save recorded Responses API response: %v", err)
			}
		} else if a.logger != nil {
			a.logger.Infof("📹 [RECORDER] Saved Responses API response to %s", filePath)
		}
	}

	return a.convertResponsesAPIResponse(apiResponse)
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

// convertMessagesForResponsesAPI converts llmtypes messages to Responses API format
// The Responses API uses a different format than Chat Completions:
// - Regular messages use: {"role": "user/assistant/system", "content": "..."}
// - Function calls use: {"type": "function_call", "call_id": "...", "name": "...", "arguments": "..."}
// - Tool outputs use: {"type": "function_call_output", "call_id": "...", "output": "..."}
func convertMessagesForResponsesAPI(messages []llmtypes.MessageContent, logger interfaces.Logger) []map[string]interface{} {
	apiMessages := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		// Handle tool messages specially - they need function_call_output format
		if msg.Role == llmtypes.ChatMessageTypeTool {
			// Extract tool call ID and content from ToolCallResponse parts
			for _, part := range msg.Parts {
				if toolResp, ok := part.(llmtypes.ToolCallResponse); ok {
					// Tool messages in the Responses API use function_call_output format
					if logger != nil {
						outputPreview := toolResp.Content
						if len(outputPreview) > 200 {
							outputPreview = outputPreview[:200] + "..."
						}
						logger.Debugf("Converting tool response - call_id: %s, output_length: %d, preview: %s",
							toolResp.ToolCallID, len(toolResp.Content), outputPreview)
					}
					apiMessages = append(apiMessages, map[string]interface{}{
						"type":    "function_call_output",
						"call_id": toolResp.ToolCallID,
						"output":  toolResp.Content,
					})
				}
			}
			continue
		}

		// Handle AI messages - need to extract both content and tool calls
		if msg.Role == llmtypes.ChatMessageTypeAI {
			var content string
			var toolCalls []llmtypes.ToolCall

			for _, part := range msg.Parts {
				switch p := part.(type) {
				case llmtypes.TextContent:
					content += p.Text
				case llmtypes.ToolCall:
					toolCalls = append(toolCalls, p)
				}
			}

			// Add assistant message if there's content
			if content != "" {
				apiMessages = append(apiMessages, map[string]interface{}{
					"role":    "assistant",
					"content": content,
				})
			}

			// Add function_call items for each tool call
			// The Responses API requires these to match function_call_output items
			for _, tc := range toolCalls {
				if logger != nil {
					logger.Debugf("Converting AI function_call - call_id: %s, name: %s, arguments: %s",
						tc.ID, tc.FunctionCall.Name, tc.FunctionCall.Arguments)
				}
				apiMessages = append(apiMessages, map[string]interface{}{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.FunctionCall.Name,
					"arguments": tc.FunctionCall.Arguments,
				})
			}
			continue
		}

		role := string(msg.Role)
		// Map internal roles to API roles
		switch msg.Role {
		case llmtypes.ChatMessageTypeHuman:
			role = "user"
		case llmtypes.ChatMessageTypeSystem:
			role = "system"
		}

		// Extract content
		var content string
		for _, part := range msg.Parts {
			if textPart, ok := part.(llmtypes.TextContent); ok {
				content += textPart.Text
			}
		}

		apiMessages = append(apiMessages, map[string]interface{}{
			"role":    role,
			"content": content,
		})
	}

	return apiMessages
}

// convertResponsesAPIResponse converts the Responses API output format to llmtypes.ContentResponse
func (a *AzureAdapter) convertResponsesAPIResponse(apiResponse map[string]interface{}) (*llmtypes.ContentResponse, error) {
	output, ok := apiResponse["output"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("responses output missing or invalid")
	}

	contentResponse := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{},
	}

	choice := &llmtypes.ContentChoice{}
	toolCalls := []llmtypes.ToolCall{}

	for _, item := range output {
		block, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := block["type"].(string)
		switch blockType {
		case "message":
			contentArr, _ := block["content"].([]interface{})
			for _, c := range contentArr {
				contentObj, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				contentType, _ := contentObj["type"].(string)
				if contentType == "output_text" {
					text, _ := contentObj["text"].(string)
					choice.Content += text
				}
			}
		case "function_call": // Tool call block (Responses API uses "function_call")
			callID, _ := block["call_id"].(string) // Note: use call_id, not id
			callName, _ := block["name"].(string)
			// Arguments can be a string or a map - handle both cases
			callArgs := convertArgumentsToString(block["arguments"])
			if a.logger != nil {
				a.logger.Debugf("Responses API function_call - name: %s, call_id: %s, raw_arguments: %v (type: %T), converted: %s",
					callName, callID, block["arguments"], block["arguments"], callArgs)
			}
			toolCalls = append(toolCalls, llmtypes.ToolCall{
				ID:   callID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      callName,
					Arguments: callArgs,
				},
			})
		case "call": // Older/alternative format check
			callID, _ := block["id"].(string)
			callName, _ := block["name"].(string)
			// Arguments can be a string or a map - handle both cases
			callArgs := convertArgumentsToString(block["arguments"])
			toolCalls = append(toolCalls, llmtypes.ToolCall{
				ID:   callID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      callName,
					Arguments: callArgs,
				},
			})
		}
	}

	choice.ToolCalls = toolCalls
	contentResponse.Choices = append(contentResponse.Choices, choice)

	// Extract usage
	if usageMap, ok := apiResponse["usage"].(map[string]interface{}); ok {
		inputTokens := 0
		outputTokens := 0
		if it, ok := usageMap["input_tokens"].(float64); ok {
			inputTokens = int(it)
		}
		if ot, ok := usageMap["output_tokens"].(float64); ok {
			outputTokens = int(ot)
		}

		contentResponse.Usage = &llmtypes.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
		}
	}

	return contentResponse, nil
}

// convertArgumentsToString converts function arguments to JSON string
func convertArgumentsToString(args interface{}) string {
	if args == nil {
		return "{}"
	}

	if argsStr, ok := args.(string); ok {
		return argsStr
	}

	if argsMap, ok := args.(map[string]interface{}); ok {
		bytes, err := json.Marshal(argsMap)
		if err != nil {
			return "{}"
		}
		return string(bytes)
	}

	bytes, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}

	return string(bytes)
}
