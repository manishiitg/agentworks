package picli

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
)

const (
	DefaultModelID = "google/gemini-3.8-flash"
)

// PiCLIAdapter implements the LLM interface for Pi Coding Agent's native RPC runtime.
type PiCLIAdapter struct {
	apiKey  string
	modelID string
	logger  interfaces.Logger
	// customProvider is the account's own OpenAI-compatible endpoint, staged
	// into each session's models.json (SetCustomProvider).
	customProvider *PiCustomProvider
}

// NewPiCLIAdapter creates a new Pi CLI adapter.
func NewPiCLIAdapter(apiKey string, modelID string, logger interfaces.Logger) *PiCLIAdapter {
	return &PiCLIAdapter{
		apiKey:  strings.TrimSpace(apiKey),
		modelID: strings.TrimSpace(modelID),
		logger:  logger,
	}
}

// GenerateContent generates content using Pi RPC.
func (p *PiCLIAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	// Pi starts under the Landlock launcher
	// when the policy confines the CLI.
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}

	return p.generateContentRPC(ctx, messages, opts)
}

// SearchWeb asks Pi to use available web/search capability if the selected
// model/tools support it, and returns the final text response.
func (p *PiCLIAdapter) SearchWeb(ctx context.Context, query string, options ...llmtypes.CallOption) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	resp, err := p.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use available web search capability to answer this query.\n\n"+query),
	}, options...)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("pi-cli web search returned no response")
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if content == "" {
		return "", fmt.Errorf("pi-cli web search returned empty response")
	}
	return content, nil
}

// GetModelID returns the configured model ID.
func (p *PiCLIAdapter) GetModelID() string {
	if strings.TrimSpace(p.modelID) == "" {
		return DefaultModelID
	}
	return p.modelID
}

// GetModelMetadata returns conservative metadata for Pi-routed models.
func (p *PiCLIAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if strings.TrimSpace(modelID) == "" {
		modelID = p.GetModelID()
	}
	selectedProvider := ""
	if !strings.Contains(modelID, "/") {
		selectedProvider, _ = resolvePiProviderModel(p.GetModelID(), "")
	}
	provider, model := resolvePiProviderModel(modelID, selectedProvider)
	metadata := &llmtypes.ModelMetadata{
		ModelID:                 provider + "/" + model,
		Provider:                "pi-cli",
		ModelName:               "Pi CLI (" + provider + "/" + model + ")",
		ContextWindow:           1048576,
		SupportsToolCalls:       true,
		SupportsReasoningEffort: true,
		ReasoningEffortLevels:   []string{"low", "medium", "high", "xhigh"},
	}
	// Pi's native cost takes precedence in the response. When it supplies only
	// tokens, the runtime can estimate known Google routes from the same Gemini
	// registry used by the API adapter. Other routes remain unpriced.
	if p.customProvider == nil && (provider == "google" || provider == "google-vertex") {
		if pricing, err := vertex.GetVertexGeminiModelMetadata(model); err == nil {
			metadata.InputCostPer1MTokens = pricing.InputCostPer1MTokens
			metadata.OutputCostPer1MTokens = pricing.OutputCostPer1MTokens
			metadata.CachedInputCostPer1MTokens = pricing.CachedInputCostPer1MTokens
			metadata.CachedInputCostWritePer1MTokens = pricing.CachedInputCostWritePer1MTokens
		}
	}
	return metadata, nil
}

func splitPiSystemPrompt(messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent) {
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

func buildPiPrompt(messages []llmtypes.MessageContent) string {
	var parts []string
	for _, message := range llmtypes.PendingHumanMessages(messages) {
		parts = append(parts, extractPiTextFromMessage(message))
	}
	return strings.Join(parts, "\n\n")
}

func extractPiTextFromMessage(msg llmtypes.MessageContent) string {
	var parts []string
	for _, part := range msg.Parts {
		if textPart, ok := part.(llmtypes.TextContent); ok {
			parts = append(parts, textPart.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (p *PiCLIAdapter) logInfof(format string, args ...interface{}) {
	if p.logger != nil {
		p.logger.Infof(format, args...)
	}
}
