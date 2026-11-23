package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/modelspecs"
)

var codexRateLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b429\b`),
	regexp.MustCompile(`\b503\b`),
	regexp.MustCompile(`(?i)\brate[- ]limit(ed|ing)?\b`),
	regexp.MustCompile(`(?i)\btoo many requests\b`),
	regexp.MustCompile(`(?i)\bservice unavailable\b`),
	regexp.MustCompile(`(?i)\busage limit\b`),
	regexp.MustCompile(`(?i)\boverloaded\b`),
}

func looksLikeCodexRateLimit(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	for _, pattern := range codexRateLimitPatterns {
		if pattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}

// CodexCLIAdapter implements the LLM interface for the OpenAI Codex CLI.
type CodexCLIAdapter struct {
	tmuxTransport bool
	apiKey        string
	modelID       string
	logger        interfaces.Logger
}

// NewCodexCLIAdapter creates a new instance of the CodexCLIAdapter.
func NewCodexCLIAdapter(apiKey string, modelID string, logger interfaces.Logger) *CodexCLIAdapter {
	return &CodexCLIAdapter{
		apiKey:  apiKey,
		modelID: modelID,
		logger:  logger,
	}
}

// GenerateContent drives Codex's native app-server protocol. Persistent callers
// retain a process; bounded calls close it and can resume the returned thread.
func (c *CodexCLIAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	if err := llmtypes.ValidateCLISecurityLaunch(
		opts,
		llmtypes.CLISecurityModeVerified,
		llmtypes.CLISecurityModeIsolated,
	); err != nil {
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
		return nil, err
	}

	tmux := c.tmuxTransport
	switch opts.CodingProviderTransport {
	case "":
	case "app-server":
		tmux = false
	case "tmux":
		tmux = true
	default:
		return nil, fmt.Errorf("unsupported Codex native transport %q", opts.CodingProviderTransport)
	}
	if tmux {
		if len(collectCodexImageContent(messages)) > 0 || len(llmtypes.NativeImageInputsForSession(ctx, codexInteractiveSessionIDFromOptions(opts))) > 0 {
			if opts.StreamChan != nil {
				close(opts.StreamChan)
			}
			return nil, fmt.Errorf("Codex native images are unavailable in terminal transport; use the file path")
		}
		owner := codexInteractiveSessionIDFromOptions(opts)
		_, goalCommand := llmtypes.NativeGoalCommandForSession(ctx, owner)
		_, goalPause := llmtypes.NativeGoalTurnPauseForSession(ctx, owner)
		if goalCommand || goalPause {
			if opts.StreamChan != nil {
				close(opts.StreamChan)
			}
			return nil, llmtypes.NativeGoalUnavailable("Codex in terminal transport")
		}
		return c.generateContentInteractive(ctx, messages, opts)
	}
	return c.generateContentAppServer(ctx, messages, opts)
}

// SearchWeb uses Codex CLI's native web search capability and returns the final text response.
func (c *CodexCLIAdapter) SearchWeb(ctx context.Context, query string, options ...llmtypes.CallOption) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	searchPrompt := "Use web search to answer the following query.\n\n" + query
	// Transcript-based tool-call/content streaming defaults OFF
	// (codexInteractiveStreamTranscriptEnabled) unless a caller opts in --
	// SearchWeb never did, so a caller passing a stream channel saw an empty
	// stream (toolStarts=0, content="") even on a genuine, successful native
	// web search: the final answer came back fine through the rollout/pane
	// parse path, which does not depend on this flag, while the stream simply
	// never got told what the search tool call actually did. Confirmed live:
	// codex answers web searches by running a custom_tool_call (name "exec",
	// invoking tools.web__run) under this transport, an event type the
	// transcript reader already recognizes -- it just wasn't being read.
	searchOptions := append([]llmtypes.CallOption{}, options...)
	searchOptions = append(searchOptions, WithStreamTranscript(true))
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
		return "", fmt.Errorf("codex cli web search returned no response")
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	if content == "" {
		return "", fmt.Errorf("codex cli web search returned empty response")
	}
	return content, nil
}

// GetModelID returns the model ID.
func (c *CodexCLIAdapter) GetModelID() string {
	return c.modelID
}

// GetModelMetadata returns metadata for the model.
func (c *CodexCLIAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if modelID == "" {
		modelID = c.modelID
	}
	originalModelID := modelID
	modelID = resolveCodexCLIModelID(modelID)
	metadataModelID := strings.TrimSpace(originalModelID)
	if metadataModelID == "" {
		metadataModelID = modelID
	}

	// Facts come from the shared table (modelspecs); the first listed ID the
	// resolved model contains wins, so longer IDs come first.
	for _, id := range codexSpecModels {
		if !strings.Contains(modelID, id) {
			continue
		}
		if m, ok := modelspecs.For(id, "codex-cli"); ok {
			m.ModelID = metadataModelID
			if levels, own := codexReasoningLevels[id]; own {
				m.ReasoningEffortLevels = levels
			}
			return m, nil
		}
	}
	// Generic fallback for unknown models
	return &llmtypes.ModelMetadata{
		ModelID:                 metadataModelID,
		Provider:                "codex-cli",
		ModelName:               "OpenAI Codex CLI (pricing varies)",
		ContextWindow:           200000,
		SupportsToolCalls:       true,
		SupportsReasoningEffort: true,
		ReasoningEffortLevels:   []string{"low", "medium", "high", "xhigh"},
	}, nil
}

// codexSpecModels are the models Codex serves, longest first where one ID
// contains another.
var codexSpecModels = []string{
	"gpt-6.2-astra", "gpt-6-astra", "gpt-6.2-sol", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-sol", "gpt-6-luna",
	"gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4-mini", "gpt-5.4", "gpt-5.3-codex-spark", "gpt-5.3-codex",
}

// codexReasoningLevels: where Codex accepts other reasoning efforts than the
// API does for the same model.
var codexReasoningLevels = map[string][]string{
	"gpt-6-sol":  {"low", "medium", "high", "xhigh", "max", "ultra"},
	"gpt-6-luna": {"low", "medium", "high", "xhigh", "max"},
}

// --- Helper Functions ---

func extractTextFromMessage(msg llmtypes.MessageContent) string {
	var parts []string
	for _, part := range msg.Parts {
		if textPart, ok := part.(llmtypes.TextContent); ok {
			parts = append(parts, textPart.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func collectCodexImageContent(messages []llmtypes.MessageContent) []llmtypes.ImageContent {
	var images []llmtypes.ImageContent
	for _, msg := range messages {
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llmtypes.ImageContent:
				images = append(images, p)
			case *llmtypes.ImageContent:
				if p != nil {
					images = append(images, *p)
				}
			}
		}
	}
	return images
}

func codexStringConfigOverride(key string, value string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal codex config override %s: %w", key, err)
	}
	return key + "=" + string(encoded), nil
}

// SetTmuxTransport selects an explicit terminal transport; failures never change transport.
func (c *CodexCLIAdapter) SetTmuxTransport(enabled bool) { c.tmuxTransport = enabled }
