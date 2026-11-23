package cursorcli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// EnvCursorCLITestModel is deliberately test-scoped. Authenticated Cursor E2E
// contracts set it to "composer-2.5" so their result does not depend on whichever
// paid model a developer last selected in the local Cursor UI.
const EnvCursorCLITestModel = "CURSOR_CLI_TEST_MODEL"

func cursorCLIModelForLaunch(modelID string) string {
	if testModel := strings.TrimSpace(os.Getenv(EnvCursorCLITestModel)); testModel != "" && isCursorLegacyModelSelector(modelID) {
		return resolveCursorCLIModelID(testModel)
	}
	return resolveCursorCLIModelID(modelID)
}

// cursorModelWithReasoning applies effort only to models that declare supported
// levels. Preserve the caller's context and speed settings when replacing effort.
// Auto and Composer route/manage reasoning themselves; live CLI variants without
// metadata keep their exact selector (including any effort encoded in that ID).
func cursorModelWithReasoning(modelID string, opts *llmtypes.CallOptions) string {
	if opts == nil || strings.TrimSpace(opts.ReasoningEffort) == "" {
		return modelID
	}
	meta, err := (&CursorCLIAdapter{}).GetModelMetadata(modelID)
	effort := strings.ToLower(strings.TrimSpace(opts.ReasoningEffort))
	if err != nil || meta == nil || !slices.Contains(meta.ReasoningEffortLevels, effort) {
		return modelID
	}
	base, parameters, hasParameters := strings.Cut(modelID, "[")
	settings := []string{}
	if hasParameters {
		for _, setting := range strings.Split(strings.TrimSuffix(parameters, "]"), ",") {
			key, _, _ := strings.Cut(strings.TrimSpace(setting), "=")
			if key != "effort" && strings.TrimSpace(setting) != "" {
				settings = append(settings, strings.TrimSpace(setting))
			}
		}
	}
	settings = append(settings, "effort="+effort)
	return base + "[" + strings.Join(settings, ",") + "]"
}

func isCursorLegacyModelSelector(modelID string) bool {
	switch strings.TrimSpace(modelID) {
	case "", "cursor-cli", "high", "medium", "low":
		return true
	default:
		return false
	}
}

// CursorCLIAdapter runs every turn on the Cursor SDK bridge, or in a tmux terminal
// when the server selects CURSOR_CLI_TRANSPORT=tmux.
type CursorCLIAdapter struct {
	tmuxTransport bool
	apiKey        string
	modelID       string
	logger        interfaces.Logger
}

// NewCursorCLIAdapter creates a new CursorCLIAdapter.
func NewCursorCLIAdapter(apiKey string, modelID string, logger interfaces.Logger) *CursorCLIAdapter {
	return &CursorCLIAdapter{
		apiKey:  apiKey,
		modelID: modelID,
		logger:  logger,
	}
}

// GenerateContent streams the Cursor turn.
func (c *CursorCLIAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	// Cursor runs inside the configured CLI confinement.
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}

	tmux := c.tmuxTransport
	switch opts.CodingProviderTransport {
	case "":
	case "sdk":
		tmux = false
	case "tmux":
		tmux = true
	default:
		return nil, fmt.Errorf("unsupported Cursor native transport %q", opts.CodingProviderTransport)
	}
	if tmux {
		if containsCursorImageContent(messages) {
			if opts.StreamChan != nil {
				close(opts.StreamChan)
			}
			return nil, fmt.Errorf("Cursor terminal transport accepts image paths only")
		}
		return c.generateContentTmux(ctx, messages, opts)
	}
	return c.generateContentSDK(ctx, messages, opts)
}

// SearchWeb asks Cursor Agent CLI to use its web search capability and returns
// the final text response.
func (c *CursorCLIAdapter) SearchWeb(ctx context.Context, query string, options ...llmtypes.CallOption) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	searchPrompt := "Use web search to answer the following query.\n\n" + query
	searchOptions := append([]llmtypes.CallOption{}, options...)
	searchOptions = append(searchOptions, WithAutoApproveWebSearch())
	resp, err := c.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: searchPrompt},
			},
		},
	}, searchOptions...)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("cursor cli web search returned no response")
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	if content == "" {
		return "", fmt.Errorf("cursor cli web search returned empty response")
	}
	return content, nil
}

// GetModelID returns the configured model ID.
func (c *CursorCLIAdapter) GetModelID() string {
	return c.modelID
}

// GetModelMetadata returns metadata for Cursor Agent CLI model selectors. Cursor
// account model availability and pricing can vary, so unknown selectors are
// exposed with conservative generic metadata.
// The estimated average price of Cursor's Auto mode, in USD per million tokens
// (its former flat rate; cache writes count as input).
const (
	cursorAutoEstimateInputPer1M     = 1.25
	cursorAutoEstimateOutputPer1M    = 6.0
	cursorAutoEstimateCacheReadPer1M = 0.25
)

func (c *CursorCLIAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if modelID == "" {
		modelID = c.modelID
	}
	metadataModelID := strings.TrimSpace(modelID)
	if metadataModelID == "" {
		metadataModelID = "cursor-cli"
	}

	resolvedModelID := resolveCursorCLIModelID(modelID)
	metadataSelector := resolvedModelID
	for _, base := range []string{"grok-4.7", "grok-4.6", "glm-5.3", "glm-5.3-flash"} {
		if strings.HasPrefix(metadataSelector, base+"[") {
			metadataSelector = base
			break
		}
	}
	switch metadataSelector {
	case "":
		return &llmtypes.ModelMetadata{
			ModelID:           metadataModelID,
			Provider:          "cursor-cli",
			ModelName:         "Auto (Cursor Agent CLI)",
			ContextWindow:     200000,
			SupportsToolCalls: true,
		}, nil
	case "auto":
		// Auto bills each request at the list price of whichever model it
		// routes to, and the CLI does not say which. Until it does, this is an
		// ESTIMATED average: Cursor's former flat Auto rate. It is reported as
		// a plan-equivalent estimate, never as an invoice.
		return &llmtypes.ModelMetadata{
			ModelID:                    metadataModelID,
			Provider:                   "cursor-cli",
			ModelName:                  "Auto (Cursor Agent CLI, estimated average price)",
			ContextWindow:              200000,
			InputCostPer1MTokens:       cursorAutoEstimateInputPer1M,
			OutputCostPer1MTokens:      cursorAutoEstimateOutputPer1M,
			CachedInputCostPer1MTokens: cursorAutoEstimateCacheReadPer1M,
			SupportsToolCalls:          true,
		}, nil
	case "composer-2.5":
		return &llmtypes.ModelMetadata{
			ModelID:           metadataModelID,
			Provider:          "cursor-cli",
			ModelName:         "Composer 2.5 (Cursor Agent CLI)",
			ContextWindow:     200000,
			SupportsToolCalls: true,
		}, nil
	case "grok-4.7", "grok-4.6":
		fast := strings.Contains(strings.ToLower(strings.ReplaceAll(resolvedModelID, " ", "")), "fast=true")
		inputRate, outputRate, cacheReadRate := 2.0, 6.0, 0.5
		longContextMultiplier := 2.0
		longContextThreshold := 256000
		if metadataSelector == "grok-4.6" {
			longContextMultiplier = 0
			longContextThreshold = 0
		}
		if fast {
			inputRate, outputRate, cacheReadRate = 4, 12, 1
			if metadataSelector == "grok-4.7" {
				longContextMultiplier = 1.5
			}
		}
		return &llmtypes.ModelMetadata{
			ModelID:                     metadataModelID,
			Provider:                    "cursor-cli",
			ModelName:                   "Grok " + strings.TrimPrefix(metadataSelector, "grok-") + " (Cursor Agent CLI)",
			ContextWindow:               256000,
			InputCostPer1MTokens:        inputRate,
			OutputCostPer1MTokens:       outputRate,
			CachedInputCostPer1MTokens:  cacheReadRate,
			LongContextThresholdTokens:  longContextThreshold,
			LongContextInputMultiplier:  longContextMultiplier,
			LongContextOutputMultiplier: longContextMultiplier,
			SupportsToolCalls:           true,
			SupportsReasoningEffort:     true,
			ReasoningEffortLevels:       []string{"low", "medium", "high", "xhigh"},
		}, nil
	case "glm-5.3", "glm-5.3-flash":
		// Cursor's official model pages, verified 2026-10-03:
		// https://cursor.com/docs/models/glm-5-3
		// https://cursor.com/docs/models/glm-5-3-flash
		name, inputRate, outputRate, cacheReadRate := "GLM 5.3", 1.4, 4.4, 0.26
		if metadataSelector == "glm-5.3-flash" {
			name, inputRate, outputRate, cacheReadRate = "GLM 5.3 Flash", 0.15, 0.5, 0.029
		}
		return &llmtypes.ModelMetadata{
			ModelID:                    metadataModelID,
			Provider:                   "cursor-cli",
			ModelName:                  name + " (Cursor Agent CLI)",
			ContextWindow:              1000000,
			InputCostPer1MTokens:       inputRate,
			OutputCostPer1MTokens:      outputRate,
			CachedInputCostPer1MTokens: cacheReadRate,
			SupportsToolCalls:          true,
			SupportsReasoningEffort:    true,
			ReasoningEffortLevels:      []string{"low", "high", "max"},
		}, nil
	case "gpt-5":
		return &llmtypes.ModelMetadata{
			ModelID:                 metadataModelID,
			Provider:                "cursor-cli",
			ModelName:               "GPT-5 (Cursor Agent CLI)",
			ContextWindow:           400000,
			SupportsToolCalls:       true,
			SupportsReasoningEffort: true,
		}, nil
	case "sonnet-4":
		return &llmtypes.ModelMetadata{
			ModelID:           metadataModelID,
			Provider:          "cursor-cli",
			ModelName:         "Claude Sonnet 4 (Cursor Agent CLI)",
			ContextWindow:     200000,
			SupportsToolCalls: true,
		}, nil
	case "sonnet-4-thinking":
		return &llmtypes.ModelMetadata{
			ModelID:                 metadataModelID,
			Provider:                "cursor-cli",
			ModelName:               "Claude Sonnet 4 Thinking (Cursor Agent CLI)",
			ContextWindow:           200000,
			SupportsToolCalls:       true,
			SupportsReasoningEffort: true,
		}, nil
	default:
		return &llmtypes.ModelMetadata{
			ModelID:           metadataModelID,
			Provider:          "cursor-cli",
			ModelName:         "Cursor Agent CLI (default, pricing varies)",
			ContextWindow:     200000,
			SupportsToolCalls: true,
		}, nil
	}
}

func splitCursorSystemPrompt(messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent) {
	var systems []string
	conversation := make([]llmtypes.MessageContent, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			for _, part := range msg.Parts {
				if textPart, ok := part.(llmtypes.TextContent); ok {
					systems = append(systems, textPart.Text)
				}
			}
			continue
		}
		conversation = append(conversation, msg)
	}
	return strings.Join(systems, "\n\n"), conversation
}

func buildCursorPrompt(messages []llmtypes.MessageContent, resume bool) string {
	if resume {
		var parts []string
		for _, message := range llmtypes.PendingHumanMessages(messages) {
			parts = append(parts, extractTextFromMessage(message))
		}
		return strings.Join(parts, "\n\n")
	}
	if len(messages) <= 1 {
		return latestCursorHumanMessage(messages)
	}

	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		text := extractTextFromMessage(msg)
		if strings.TrimSpace(text) == "" {
			continue
		}
		role := "User"
		if msg.Role == llmtypes.ChatMessageTypeAI {
			role = "Assistant"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", role, text))
	}
	return strings.Join(parts, "\n\n")
}

// buildCursorStructuredPrompt applies Cursor's native-continuation contract
// to the structured transport. On a fresh chat the system instructions and
// supplied history bootstrap Cursor. On --resume, Cursor already has both, so
// only the newest human message belongs in the new process invocation.
func buildCursorStructuredPrompt(systemPrompt string, messages []llmtypes.MessageContent, resumeID string) string {
	resume := strings.TrimSpace(resumeID) != ""
	prompt := buildCursorPrompt(messages, resume)
	if !resume && strings.TrimSpace(systemPrompt) != "" {
		return "[System Instructions]\n" + systemPrompt + "\n\n[User Message]\n" + prompt
	}
	return prompt
}

func latestCursorHumanMessage(messages []llmtypes.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llmtypes.ChatMessageTypeHuman {
			return extractTextFromMessage(messages[i])
		}
	}
	return ""
}

func cursorAssistantHistory(messages []llmtypes.MessageContent) []string {
	history := make([]string, 0)
	for _, msg := range messages {
		if msg.Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		text := strings.TrimSpace(extractTextFromMessage(msg))
		if text != "" {
			history = append(history, text)
		}
	}
	return history
}

func extractTextFromMessage(msg llmtypes.MessageContent) string {
	var parts []string
	for _, part := range msg.Parts {
		if textPart, ok := part.(llmtypes.TextContent); ok {
			parts = append(parts, textPart.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func containsCursorImageContent(messages []llmtypes.MessageContent) bool {
	for _, msg := range messages {
		for _, part := range msg.Parts {
			switch part.(type) {
			case llmtypes.ImageContent, *llmtypes.ImageContent:
				return true
			}
		}
	}
	return false
}

func (c *CursorCLIAdapter) logInfof(format string, args ...interface{}) {
	if c.logger != nil {
		c.logger.Infof(format, args...)
	}
}

func (c *CursorCLIAdapter) SetTmuxTransport(enabled bool) { c.tmuxTransport = enabled }
