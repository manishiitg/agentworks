// Package minimax provides an adapter for MiniMax's Anthropic-compatible API.
// MiniMax exposes an Anthropic-compatible endpoint at https://api.minimax.io
// We use the Anthropic Go SDK with a custom base URL.
package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

const (
	// MiniMaxAnthropicBaseURL is the base URL for MiniMax's Anthropic-compatible API.
	// The Anthropic SDK appends /v1/messages, resulting in https://api.minimax.io/anthropic/v1/messages.
	MiniMaxAnthropicBaseURL = "https://api.minimax.io/anthropic"
)

// MiniMaxAdapter implements the llmtypes.Model interface using MiniMax's
// Anthropic-compatible API.
type MiniMaxAdapter struct {
	client     anthropic.Client
	apiKey     string
	modelID    string
	logger     interfaces.Logger
	codingPlan bool
}

// NewMiniMaxAdapter creates a new MiniMax adapter using the Anthropic SDK.
func NewMiniMaxAdapter(apiKey, modelID string, logger interfaces.Logger) *MiniMaxAdapter {
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey(apiKey),
		anthropicoption.WithBaseURL(MiniMaxAnthropicBaseURL),
	)
	return &MiniMaxAdapter{
		client:     client,
		apiKey:     apiKey,
		modelID:    modelID,
		logger:     logger,
		codingPlan: false,
	}
}

// NewMiniMaxCodingPlanAdapter creates a MiniMax adapter for the coding plan.
// It sets a claude-code User-Agent header so MiniMax routes the request correctly
// through the coding plan endpoint and applies the appropriate billing.
func NewMiniMaxCodingPlanAdapter(apiKey, modelID string, logger interfaces.Logger) *MiniMaxAdapter {
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey(apiKey),
		anthropicoption.WithBaseURL(MiniMaxAnthropicBaseURL),
		anthropicoption.WithHeader("User-Agent", "claude-code/2.1.71"),
	)
	return &MiniMaxAdapter{
		client:     client,
		apiKey:     apiKey,
		modelID:    modelID,
		logger:     logger,
		codingPlan: true,
	}
}

// GetModelID implements the llmtypes.Model interface
func (m *MiniMaxAdapter) GetModelID() string {
	return m.modelID
}

// GetModelMetadata implements the llmtypes.Model interface
// attachCostEstimate fills GenerationInfo.Additional["cost_usd_estimated"]
// from tokens × registry rates. MiniMax responses carry usage but no
// USD field.
func (m *MiniMaxAdapter) attachCostEstimate(resp *llmtypes.ContentResponse, modelID string) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return
	}
	meta, err := m.GetModelMetadata(modelID)
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

func (m *MiniMaxAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	// Try coding plan models first (Anthropic model names)
	if meta, err := GetMiniMaxCodingPlanModelMetadata(modelID); err == nil {
		return meta, nil
	}
	// Fall back to standard MiniMax model names
	return GetMiniMaxModelMetadata(modelID)
}

// GenerateContent implements the llmtypes.Model interface
func (m *MiniMaxAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	if !m.codingPlan && containsImageParts(messages) {
		return nil, fmt.Errorf("MiniMax image understanding requires provider minimax-coding-plan")
	}

	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}

	modelID := m.modelID
	if opts.Model != "" {
		modelID = opts.Model
	}

	return llmtypes.WithObservability(ctx, llmtypes.ObservabilityConfig{
		Provider:     "minimax",
		Model:        modelID,
		Opts:         opts,
		MessageCount: len(messages),
		Messages:     messages,
		HeaderLine:   fmt.Sprintf("minimax.messages model=%s msgs=%d tools=%d", modelID, len(messages), len(opts.Tools)),
		// Not yet reachable from AgentWorks workflow steps, but the CLI
		// adapters already declare "tmux" / "structured_cli" here so their
		// transport-class chip renders correctly; this is the same
		// declaration for a direct API call, so the label is correct the
		// day this adapter IS wired in.
		RequestMetaExtra: map[string]interface{}{"transport": "api"},
	}, func(sink *llmtypes.StreamSink) (*llmtypes.ContentResponse, error) {
		_ = sink
		resp, err := m.generateContentInner(ctx, opts, modelID, messages)
		if err == nil {
			m.attachCostEstimate(resp, modelID)
		}
		return resp, err
	})
}

func (m *MiniMaxAdapter) generateContentInner(ctx context.Context, opts *llmtypes.CallOptions, modelID string, messages []llmtypes.MessageContent) (*llmtypes.ContentResponse, error) {
	anthropicMessages, systemMessage := convertMessages(messages)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(modelID),
		Messages:  anthropicMessages,
		MaxTokens: 40960,
	}

	if systemMessage != "" {
		if opts.JSONMode {
			systemMessage = systemMessage + "\n\nYou must respond with valid JSON only, no other text. Return a JSON object."
		}
		params.System = []anthropic.TextBlockParam{
			{Text: systemMessage},
		}
	} else if opts.JSONMode && len(anthropicMessages) > 0 {
		jsonInstruction := anthropic.NewTextBlock("You must respond with valid JSON only, no other text. Return a JSON object.")
		if len(anthropicMessages) > 0 && anthropicMessages[0].Role == anthropic.MessageParamRoleUser {
			anthropicMessages[0].Content = append([]anthropic.ContentBlockParamUnion{jsonInstruction}, anthropicMessages[0].Content...)
		}
	}

	if opts.Temperature > 0 {
		params.Temperature = anthropic.Float(opts.Temperature)
	}
	if opts.MaxTokens > 0 {
		params.MaxTokens = int64(opts.MaxTokens)
	}

	if len(opts.Tools) > 0 {
		params.Tools = convertTools(opts.Tools)
		if opts.ToolChoice != nil {
			params.ToolChoice = convertToolChoice(opts.ToolChoice)
		}
	}

	if m.logger != nil {
		m.logger.Debugf("[MINIMAX] GenerateContent model=%s messages=%d tools=%d", modelID, len(messages), len(params.Tools))
	}

	stream := m.client.Messages.NewStreaming(ctx, params)

	defer func() {
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
	}()

	message := anthropic.Message{}
	var contentChunksSent int
	// Track usage from MessageDeltaEvent since the Anthropic SDK's Accumulate
	// only copies OutputTokens from delta events, not InputTokens.
	// MiniMax may report input_tokens in the message_delta event rather than
	// (or in addition to) the message_start event.
	var deltaInputTokens int64
	var deltaCacheCreationInputTokens int64
	var deltaCacheReadInputTokens int64
	for stream.Next() {
		event := stream.Current()

		// Log every event at debug level to trace MiniMax streaming sequence
		if m.logger != nil {
			switch ev := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				m.logger.Debugf("[MINIMAX] EVENT content_block_start: index=%d block_type=%s raw=%s",
					ev.Index, ev.ContentBlock.Type, truncate(ev.ContentBlock.RawJSON(), 500))
			case anthropic.ContentBlockDeltaEvent:
				switch d := ev.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					m.logger.Debugf("[MINIMAX] EVENT content_block_delta: index=%d delta_type=text_delta text_len=%d",
						ev.Index, len(d.Text))
				case anthropic.InputJSONDelta:
					m.logger.Debugf("[MINIMAX] EVENT content_block_delta: index=%d delta_type=input_json_delta partial_json=%s",
						ev.Index, truncate(d.PartialJSON, 200))
				default:
					m.logger.Debugf("[MINIMAX] EVENT content_block_delta: index=%d delta_type=%s raw=%s",
						ev.Index, ev.Delta.Type, truncate(ev.Delta.RawJSON(), 200))
				}
			case anthropic.ContentBlockStopEvent:
				// Log the state of the block being closed before Accumulate processes it
				if int(ev.Index) < len(message.Content) {
					cb := message.Content[ev.Index]
					m.logger.Debugf("[MINIMAX] EVENT content_block_stop: index=%d block_type=%s text_len=%d input_len=%d input_bytes=%s",
						ev.Index, cb.Type, len(cb.Text), len(cb.Input), truncate(string(cb.Input), 500))
				} else {
					m.logger.Debugf("[MINIMAX] EVENT content_block_stop: index=%d (block not yet in accumulator)", ev.Index)
				}
			case anthropic.MessageDeltaEvent:
				m.logger.Debugf("[MINIMAX] EVENT message_delta: stop_reason=%s", ev.Delta.StopReason)
			case anthropic.MessageStopEvent:
				m.logger.Debugf("[MINIMAX] EVENT message_stop")
			case anthropic.MessageStartEvent:
				m.logger.Debugf("[MINIMAX] EVENT message_start: model=%s", ev.Message.Model)
			}
		}

		if err := message.Accumulate(event); err != nil {
			// "error converting content block to JSON" happens on ContentBlockStopEvent
			// or MessageStopEvent when the SDK tries to cache JSON.raw from the
			// accumulated struct. MiniMax sometimes sends malformed/incomplete JSON
			// in content block fields. The actual struct data (Text, Input, etc.) is
			// already accumulated and intact — only the JSON.raw cache step fails.
			// Log all block details and continue rather than aborting the entire stream.
			if strings.Contains(err.Error(), "error converting content block to JSON") {
				if m.logger != nil {
					m.logger.Errorf("[MINIMAX] JSON cache error on %s (data intact), dumping all %d blocks:",
						event.Type, len(message.Content))
					for i, cb := range message.Content {
						m.logger.Errorf("[MINIMAX]   block[%d]: type=%s text_len=%d input_len=%d input_bytes=%s",
							i, cb.Type, len(cb.Text), len(cb.Input), truncate(string(cb.Input), 1000))
					}
				}
			} else {
				stream.Close()
				if m.logger != nil {
					var accumulated strings.Builder
					for _, block := range message.Content {
						if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
							accumulated.WriteString(tb.Text)
						}
					}
					m.logger.Errorf("[MINIMAX] Accumulate error after %d chunks, stop_reason=%s, accumulated_text_len=%d, accumulated_content_blocks=%d, event_type=%s, raw_event=%s, partial_text=%q, error=%v",
						contentChunksSent, message.StopReason, accumulated.Len(), len(message.Content), event.Type, truncate(event.RawJSON(), 1000), truncate(accumulated.String(), 500), err)
				}
				return nil, fmt.Errorf("minimax streaming accumulate error: %w", err)
			}
		}

		// Capture usage from MessageDeltaEvent that Accumulate doesn't copy
		if deltaEvent, ok := event.AsAny().(anthropic.MessageDeltaEvent); ok {
			if deltaEvent.Usage.InputTokens > 0 {
				deltaInputTokens = deltaEvent.Usage.InputTokens
			}
			if deltaEvent.Usage.CacheCreationInputTokens > 0 {
				deltaCacheCreationInputTokens = deltaEvent.Usage.CacheCreationInputTokens
			}
			if deltaEvent.Usage.CacheReadInputTokens > 0 {
				deltaCacheReadInputTokens = deltaEvent.Usage.CacheReadInputTokens
			}
		}

		if opts.StreamChan != nil {
			switch eventVariant := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
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
					}
				}
			}
		}
	}

	// Patch accumulated message with usage data from delta events that
	// the SDK's Accumulate doesn't copy (it only copies OutputTokens).
	if deltaInputTokens > 0 && message.Usage.InputTokens == 0 {
		message.Usage.InputTokens = deltaInputTokens
	}
	if deltaCacheCreationInputTokens > 0 && message.Usage.CacheCreationInputTokens == 0 {
		message.Usage.CacheCreationInputTokens = deltaCacheCreationInputTokens
	}
	if deltaCacheReadInputTokens > 0 && message.Usage.CacheReadInputTokens == 0 {
		message.Usage.CacheReadInputTokens = deltaCacheReadInputTokens
	}

	if err := stream.Err(); err != nil {
		if m.logger != nil {
			m.logger.Errorf("[MINIMAX] Streaming error: %v", err)
		}
		return nil, fmt.Errorf("minimax streaming error: %w", err)
	}
	stream.Close()

	if m.logger != nil {
		m.logger.Debugf("[MINIMAX] Stream complete: content_chunks=%d stop_reason=%s input_tokens=%d output_tokens=%d cache_read=%d cache_creation=%d delta_input=%d delta_cache_read=%d delta_cache_creation=%d",
			contentChunksSent, message.StopReason,
			message.Usage.InputTokens, message.Usage.OutputTokens,
			message.Usage.CacheReadInputTokens, message.Usage.CacheCreationInputTokens,
			deltaInputTokens, deltaCacheReadInputTokens, deltaCacheCreationInputTokens)
		m.logger.Debugf("[MINIMAX] Accumulated message has %d content blocks:", len(message.Content))
		for i, cb := range message.Content {
			m.logger.Debugf("[MINIMAX]   block[%d]: type=%s id=%s name=%s text_len=%d input_len=%d input_bytes=%s",
				i, cb.Type, cb.ID, cb.Name, len(cb.Text), len(cb.Input), truncate(string(cb.Input), 500))
		}
	}

	// If MiniMax hit max_tokens while generating a tool call, the Input JSON will be
	// truncated. Detect this and return an error so the retry/fallback fires instead
	// of passing broken JSON downstream to tool execution.
	if message.StopReason == "max_tokens" {
		for _, block := range message.Content {
			if block.Type == "tool_use" && len(block.Input) > 0 {
				if !json.Valid(block.Input) {
					if m.logger != nil {
						m.logger.Errorf("[MINIMAX] Tool call truncated at max_tokens: tool=%s input_len=%d input_tail=%s",
							block.Name, len(block.Input), truncate(string(block.Input[max(0, len(block.Input)-200):]), 200))
					}
					return nil, fmt.Errorf("minimax output truncated: max_tokens reached with incomplete tool call arguments for %q (input_len=%d)", block.Name, len(block.Input))
				}
			}
		}
	}

	// Fallback: if no chunks sent during streaming but message has text, emit it now
	if opts.StreamChan != nil {
		if contentChunksSent == 0 {
			var textContent strings.Builder
			for _, block := range message.Content {
				if block.Type == "text" && block.Text != "" {
					textContent.WriteString(block.Text)
				}
			}
			if textContent.Len() > 0 {
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

		// Stream tool calls
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
				select {
				case opts.StreamChan <- llmtypes.StreamChunk{
					Type:     llmtypes.StreamChunkTypeToolCall,
					ToolCall: &toolCall,
				}:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
	}

	return convertResponse(&message), nil
}

func containsImageParts(messages []llmtypes.MessageContent) bool {
	for _, message := range messages {
		for _, part := range message.Parts {
			switch part.(type) {
			case llmtypes.ImageContent:
				return true
			}
		}
	}
	return false
}

// Call implements a convenience method for simple text generation
func (m *MiniMaxAdapter) Call(ctx context.Context, prompt string, options ...llmtypes.CallOption) (string, error) {
	messages := []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}},
		},
	}
	resp, err := m.GenerateContent(ctx, messages, options...)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return resp.Choices[0].Content, nil
}

// SearchWeb uses the MiniMax CLI's native web search command and returns the final text response.
func (m *MiniMaxAdapter) SearchWeb(ctx context.Context, query string, options ...llmtypes.CallOption) (string, error) {
	if !m.codingPlan {
		return "", fmt.Errorf("MiniMax web search requires provider minimax-coding-plan")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	mmxPath, err := exec.LookPath("mmx")
	if err != nil {
		return "", fmt.Errorf("MiniMax CLI (mmx) is required for MiniMax web search but was not found on PATH")
	}

	cmd := exec.CommandContext(ctx, mmxPath, "search", "query", "--q", query)
	cmd.Env = buildMiniMaxSearchEnv(m.apiKey)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		if stderrOutput := strings.TrimSpace(stderrBuf.String()); stderrOutput != "" {
			return "", fmt.Errorf("MiniMax CLI web search failed: %s", stderrOutput)
		}
		return "", fmt.Errorf("MiniMax CLI web search failed: %w", err)
	}

	result := strings.TrimSpace(stdoutBuf.String())
	if result == "" {
		if stderrOutput := strings.TrimSpace(stderrBuf.String()); stderrOutput != "" {
			return "", fmt.Errorf("MiniMax CLI web search returned no output: %s", stderrOutput)
		}
		return "", fmt.Errorf("MiniMax CLI web search returned no output")
	}

	return result, nil
}

func buildMiniMaxSearchEnv(apiKey string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "MINIMAX_API_KEY=") {
			continue
		}
		env = append(env, entry)
	}
	if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
		env = append(env, "MINIMAX_API_KEY="+trimmed)
	}
	env = append(env, "NO_COLOR=1")
	return env
}

// convertMessages converts llmtypes messages to Anthropic message format
func convertMessages(langMessages []llmtypes.MessageContent) ([]anthropic.MessageParam, string) {
	anthropicMessages := make([]anthropic.MessageParam, 0, len(langMessages))
	var systemMessage string

	for _, msg := range langMessages {
		var contentParts []string
		var imageParts []llmtypes.ImageContent
		var toolCallID string
		var toolResponseContent string
		var toolResponseIsError bool
		var toolCalls []llmtypes.ToolCall

		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				contentParts = append(contentParts, p.Text)
			case llmtypes.ImageContent:
				imageParts = append(imageParts, p)
			case llmtypes.ToolCallResponse:
				toolCallID = p.ToolCallID
				toolResponseContent = p.Content
				toolResponseIsError = p.IsError
			case llmtypes.ToolCall:
				toolCalls = append(toolCalls, p)
			}
		}

		switch string(msg.Role) {
		case string(llmtypes.ChatMessageTypeSystem):
			if len(contentParts) > 0 {
				systemMessage = strings.Join(contentParts, "\n")
			}
		case string(llmtypes.ChatMessageTypeHuman):
			contentBlocks := []anthropic.ContentBlockParamUnion{}
			if len(contentParts) > 0 {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(strings.Join(contentParts, "\n")))
			}
			for _, img := range imageParts {
				if block := createImageBlock(img); block != nil {
					contentBlocks = append(contentBlocks, *block)
				}
			}
			if len(contentBlocks) > 0 {
				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: contentBlocks,
				})
			}
		case string(llmtypes.ChatMessageTypeAI):
			contentBlocks := []anthropic.ContentBlockParamUnion{}
			if len(contentParts) > 0 {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(strings.Join(contentParts, "\n")))
			}
			for _, tc := range toolCalls {
				var args map[string]interface{}
				if tc.FunctionCall.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.FunctionCall.Arguments), &args); err != nil {
						args = make(map[string]interface{})
					}
				} else {
					args = make(map[string]interface{})
				}
				contentBlocks = append(contentBlocks, anthropic.NewToolUseBlock(tc.ID, args, tc.FunctionCall.Name))
			}
			if len(contentBlocks) > 0 {
				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleAssistant,
					Content: contentBlocks,
				})
			}
		case string(llmtypes.ChatMessageTypeTool):
			if toolCallID != "" {
				anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
					Role:    anthropic.MessageParamRoleUser,
					Content: []anthropic.ContentBlockParamUnion{anthropic.NewToolResultBlock(toolCallID, toolResponseContent, toolResponseIsError)},
				})
			}
		default:
			contentBlocks := []anthropic.ContentBlockParamUnion{}
			if len(contentParts) > 0 {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(strings.Join(contentParts, "\n")))
			}
			for _, img := range imageParts {
				if block := createImageBlock(img); block != nil {
					contentBlocks = append(contentBlocks, *block)
				}
			}
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

func createImageBlock(img llmtypes.ImageContent) *anthropic.ContentBlockParamUnion {
	if img.SourceType == "base64" {
		block := anthropic.NewImageBlockBase64(img.MediaType, img.Data)
		return &block
	} else if img.SourceType == "url" {
		block := anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: img.Data})
		return &block
	}
	return nil
}

func convertTools(llmTools []llmtypes.Tool) []anthropic.ToolUnionParam {
	anthropicTools := make([]anthropic.ToolUnionParam, 0, len(llmTools))
	for _, tool := range llmTools {
		if tool.Function == nil {
			continue
		}
		var parameters map[string]interface{}
		if tool.Function.Parameters != nil {
			if b, err := json.Marshal(tool.Function.Parameters); err == nil {
				var m map[string]interface{}
				if err := json.Unmarshal(b, &m); err == nil {
					parameters = m
				}
			}
		}
		if parameters == nil {
			parameters = make(map[string]interface{})
		}

		parameters = llmtypes.ModelToolSchema(parameters)

		var required []string
		if req, ok := parameters["required"].([]interface{}); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					required = append(required, s)
				}
			}
		}
		properties := make(map[string]interface{})
		if props, ok := parameters["properties"].(map[string]interface{}); ok {
			properties = props
		}

		toolParam := anthropic.ToolParam{
			Name:        tool.Function.Name,
			InputSchema: anthropic.ToolInputSchemaParam{Properties: properties, Required: required},
		}
		if tool.Function.Description != "" {
			toolParam.Description = param.NewOpt(tool.Function.Description)
		}
		anthropicTools = append(anthropicTools, anthropic.ToolUnionParam{OfTool: &toolParam})
	}
	return anthropicTools
}

func convertToolChoice(toolChoice interface{}) anthropic.ToolChoiceUnionParam {
	if choiceStr, ok := toolChoice.(string); ok {
		switch choiceStr {
		case "none":
			return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
		case "required", "any":
			return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
		default:
			return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
		}
	}
	if tc, ok := toolChoice.(*llmtypes.ToolChoice); ok && tc != nil {
		if tc.Function != nil && tc.Function.Name != "" {
			return anthropic.ToolChoiceParamOfTool(tc.Function.Name)
		}
		switch tc.Type {
		case "none":
			return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
		case "required", "any":
			return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
		}
	}
	return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
}

func convertResponse(result *anthropic.Message) *llmtypes.ContentResponse {
	if result == nil {
		return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{}}
	}

	choice := &llmtypes.ContentChoice{}
	var textParts []string
	var toolCalls []llmtypes.ToolCall

	for _, block := range result.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			argsJSON := []byte("{}")
			if len(block.Input) > 0 {
				argsJSON = block.Input
			}
			toolCalls = append(toolCalls, llmtypes.ToolCall{
				ID:   block.ID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	if len(textParts) > 0 {
		choice.Content = strings.Join(textParts, "\n")
	}
	if len(toolCalls) > 0 {
		choice.ToolCalls = toolCalls
	}
	if result.StopReason != "" {
		choice.StopReason = string(result.StopReason)
	}

	// MiniMax uses automatic prompt caching. When cached, input_tokens only
	// reports non-cached tokens. The true context size includes cached tokens:
	//   total_context = input_tokens + cache_read_input_tokens + cache_creation_input_tokens
	cacheReadTokens := int(result.Usage.CacheReadInputTokens)
	cacheCreationTokens := int(result.Usage.CacheCreationInputTokens)
	// InputTokens represents the full context window size (including cached tokens)
	// This is critical for context window tracking / summarization thresholds.
	inputTokens := int(result.Usage.InputTokens) + cacheReadTokens + cacheCreationTokens
	outputTokens := int(result.Usage.OutputTokens)
	totalTokens := inputTokens + outputTokens

	genInfo := &llmtypes.GenerationInfo{
		InputTokens:  &inputTokens,
		OutputTokens: &outputTokens,
		TotalTokens:  &totalTokens,
		Additional:   make(map[string]interface{}),
	}
	// Store cache breakdown in Additional for proper cost calculation
	if cacheReadTokens > 0 {
		genInfo.Additional["CacheReadInputTokens"] = cacheReadTokens
	}
	if cacheCreationTokens > 0 {
		genInfo.Additional["CacheCreationInputTokens"] = cacheCreationTokens
	}
	// Store the raw (non-cached) input tokens for reference
	genInfo.Additional["RawInputTokens"] = int(result.Usage.InputTokens)

	choice.GenerationInfo = genInfo
	usage := llmtypes.ExtractUsageFromGenerationInfo(genInfo)
	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{choice},
		Usage:   usage,
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
