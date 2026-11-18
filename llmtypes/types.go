package llmtypes

import (
	"context"
	"encoding/json"
	"time"
)

// Model is the core interface for LLM implementations
type Model interface {
	GenerateContent(ctx context.Context, messages []MessageContent, options ...CallOption) (*ContentResponse, error)
	// GetModelID returns the model ID for this LLM instance
	// Returns empty string if the model ID is not available
	GetModelID() string
	// GetModelMetadata returns metadata for the specified model ID (token limits, pricing, etc.)
	// If modelID is empty, returns metadata for the default model
	// Returns (nil, error) if metadata is not available for the model (e.g., model not found).
	// The error should be descriptive and indicate why the metadata is unavailable.
	GetModelMetadata(modelID string) (*ModelMetadata, error)
}

// ChatMessageType represents the role of a chat message
type ChatMessageType string

const (
	ChatMessageTypeSystem   ChatMessageType = "system"
	ChatMessageTypeHuman    ChatMessageType = "human"
	ChatMessageTypeAI       ChatMessageType = "ai"
	ChatMessageTypeTool     ChatMessageType = "tool"
	ChatMessageTypeGeneric  ChatMessageType = "generic"
	ChatMessageTypeFunction ChatMessageType = "function"
)

// ContentPart is an interface for different types of message parts
type ContentPart interface{}

// TextContent represents a text content part
type TextContent struct {
	Text string
}

// ThinkingContent is a model's reasoning, kept apart from its answer text so
// consumers can render it as thinking and never forward it as a reply.
type ThinkingContent struct {
	Thinking string
}

// ImageContent represents an image content part
// Supports both base64-encoded images and image URLs
type ImageContent struct {
	// SourceType is either "base64" or "url"
	SourceType string
	// MediaType is the MIME type (e.g., "image/jpeg", "image/png", "image/gif", "image/webp")
	// Required for base64 source type
	MediaType string
	// Data contains either:
	// - Base64-encoded image data (without data: URL prefix) for SourceType "base64"
	// - Image URL for SourceType "url"
	Data string
}

// DocumentContent represents a document content part (PDFs today; other
// document types may be added later). Currently consumed by adapters
// whose underlying provider supports document input natively, such as
// Anthropic Claude 3.5+ which accepts PDFs in content blocks.
type DocumentContent struct {
	// SourceType is either "base64" or "url".
	SourceType string
	// MediaType is the MIME type. Only "application/pdf" is universally
	// supported today; other types may pass through but provider
	// acceptance varies.
	MediaType string
	// Data contains either:
	//   - Base64-encoded document bytes (no `data:` prefix) for SourceType "base64"
	//   - Public URL for SourceType "url"
	Data string
	// Title is an optional document label (Anthropic surfaces this as
	// `title` on the content block; useful for citation extraction).
	Title string
	// Context is an optional human-readable description of the document's
	// role in the conversation. Anthropic uses it to improve citation
	// relevance.
	Context string
	// EnableCitations turns on Anthropic's citation extraction so the
	// model returns explicit `citations` annotations grounded in this
	// document. Other providers ignore this field.
	EnableCitations bool
}

// StreamChunkType represents the type of a streaming chunk
type StreamChunkType string

const (
	StreamChunkTypeContent StreamChunkType = "content" // Text content chunk
	// AssistantBlock carries native text-block start/end framing, not a second
	// content delta. Only transports with native identity emit it. End.Content
	// is the exact completed block, for durable presentation, not model input.
	StreamChunkTypeAssistantBlock StreamChunkType = "assistant_block"
	// Reasoning is provider-supplied, user-visible reasoning/progress text. It
	// is intentionally distinct from assistant content so product UIs can render
	// it as a collapsible progress card without treating it as the final answer.
	StreamChunkTypeReasoning     StreamChunkType = "reasoning"
	StreamChunkTypeToolCall      StreamChunkType = "tool_call" // Complete tool call
	StreamChunkTypeToolCallStart StreamChunkType = "tool_call_start"
	StreamChunkTypeToolCallEnd   StreamChunkType = "tool_call_end"
	// StreamChunkTypeToolCallDelta carries a tool call's arguments while the
	// model is still writing them (ToolArgs holds everything so far), so a
	// surface can render a tool's input as it forms. ToolCallStart follows
	// with the complete arguments.
	StreamChunkTypeToolCallDelta StreamChunkType = "tool_call_delta"
	StreamChunkTypeTerminal      StreamChunkType = "terminal"    // Live terminal/screen snapshot
	StreamChunkTypeStatusLine    StreamChunkType = "status_line" // Generic real-time statusline snapshot
	// StreamChunkTypePromptAccepted says the coding CLI took the prompt (the launch is over and the turn really
	// began). It carries no content; the app uses it to stop saying "Starting <agent>".
	StreamChunkTypePromptAccepted StreamChunkType = "prompt_accepted"
)

// StatusLine represents a generic unified statusline payload from interactive CLIs
type StatusLine struct {
	Provider                 string                 `json:"provider"`                // e.g. "cursor", "claudecode"
	Model                    string                 `json:"model,omitempty"`         // e.g. "claude-3-5-sonnet"
	InputTokens              int                    `json:"input_tokens,omitempty"`  // Session input tokens for the current turn/session
	OutputTokens             int                    `json:"output_tokens,omitempty"` // Session output tokens for the current turn/session
	CacheCreationInputTokens int                    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                    `json:"cache_read_input_tokens,omitempty"`
	TotalInputTokens         int                    `json:"total_input_tokens,omitempty"`
	TotalOutputTokens        int                    `json:"total_output_tokens,omitempty"`
	CostUSD                  float64                `json:"cost_usd,omitempty"` // Cumulative estimated cost in USD
	Metadata                 map[string]interface{} `json:"metadata,omitempty"` // Generic/custom provider fields (e.g., git branch, effort, rate limits)
}

// StatusLineProvider is an interface that interactive adapters can implement
// to support fetching real-time statusline snapshots.
type StatusLineProvider interface {
	GetStatusLine(ctx context.Context, sessionID string) (*StatusLine, error)
}

// StreamChunk represents a single chunk in a streaming response
// It can contain either content text or a complete tool call
type StreamChunk struct {
	NativeRetry     *NativeRetry      // Status of the native CLI’s own retry loop.
	NativeGoal      *NativeGoalUpdate // Private goal state, never a token/account status snapshot.
	CodingAgentPlan *CodingAgentPlan  // Full provider-authored checklist snapshot.
	Type            StreamChunkType   // Type of chunk: "content", "reasoning", "tool_call", "terminal", or "status_line"
	Content         string            // Text content (when Type is "content" or "reasoning")
	Metadata        map[string]interface{}
	ToolCall        *ToolCall     // Complete tool call (when Type is "tool_call")
	ToolName        string        // Name of the tool (when Type is "tool_call_start" or "tool_call_end")
	ToolCallID      string        // ID of the tool call (when Type is "tool_call_start" or "tool_call_end")
	ToolArgs        string        // JSON arguments of the tool call (when Type is "tool_call_end")
	ToolResult      string        // Tool execution result (when Type is "tool_call_end")
	ToolDuration    time.Duration // Duration of the tool call (when Type is "tool_call_end")
	StatusLine      *StatusLine   // Generic statusline snapshot (when Type is "status_line")
	// ContextCompaction is the CLI's compaction record (when Type is
	// "context_compaction").
	ContextCompaction *ContextCompaction
	// BackgroundOutput is live output of a running command (when Type is
	// "background_output").
	BackgroundOutput *BackgroundOutput
}

// ToolCall represents a tool/function call request
type ToolCall struct {
	ID               string
	Type             string
	FunctionCall     *FunctionCall
	ThoughtSignature string // For Gemini 3 Pro: thought signature from extra_content.google.thought_signature
}

// FunctionCall represents a function call with name and arguments
type FunctionCall struct {
	Name      string
	Arguments string // JSON string
}

// ToolCallResponse represents a tool/function call response
type ToolCallResponse struct {
	ToolCallID string
	Name       string // Name of the tool/function that was called
	Content    string
	IsError    bool // True when the tool execution failed
	// Images, when set, are appended to the tool result block so the
	// model can reason over them on the next turn. Supported on
	// Anthropic Claude 3.5+ (vision-in-tool-output); silently ignored
	// by providers that don't support image content in tool results.
	Images []ImageContent
}

// MessageContent represents a message in the conversation
type MessageContent struct {
	Role  ChatMessageType
	Parts []ContentPart
}

// UnmarshalJSON restores concrete content part types when conversations are
// rehydrated from JSON. Without this, encoding/json decodes []ContentPart into
// []map[string]interface{}, because ContentPart is intentionally interface{}.
func (m *MessageContent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role  ChatMessageType   `json:"Role"`
		Parts []json.RawMessage `json:"Parts"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.Role = raw.Role
	m.Parts = make([]ContentPart, 0, len(raw.Parts))
	for _, part := range raw.Parts {
		m.Parts = append(m.Parts, unmarshalContentPart(part))
	}
	return nil
}

func unmarshalContentPart(data json.RawMessage) ContentPart {
	if len(data) == 0 {
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return TextContent{Text: text}
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		var generic interface{}
		if json.Unmarshal(data, &generic) == nil {
			return generic
		}
		return string(data)
	}

	if text, ok := stringField(fields, "Text", "text"); ok {
		return TextContent{Text: text}
	}
	if contentType, ok := stringField(fields, "type", "Type"); ok && contentType == "text" {
		if text, ok := stringField(fields, "content", "Content"); ok {
			return TextContent{Text: text}
		}
	}

	if _, ok := firstExistingField(fields, "SourceType", "source_type", "sourceType"); ok {
		return ImageContent{
			SourceType: stringFieldOrEmpty(fields, "SourceType", "source_type", "sourceType"),
			MediaType:  stringFieldOrEmpty(fields, "MediaType", "media_type", "mediaType"),
			Data:       stringFieldOrEmpty(fields, "Data", "data"),
		}
	}

	if _, ok := firstExistingField(fields, "ToolCallID", "tool_call_id", "toolCallID"); ok {
		return ToolCallResponse{
			ToolCallID: stringFieldOrEmpty(fields, "ToolCallID", "tool_call_id", "toolCallID"),
			Name:       stringFieldOrEmpty(fields, "Name", "name"),
			Content:    stringFieldOrEmpty(fields, "Content", "content"),
			IsError:    boolFieldOrFalse(fields, "IsError", "is_error", "isError"),
		}
	}

	if _, ok := firstExistingField(fields, "FunctionCall", "function_call", "functionCall"); ok {
		return ToolCall{
			ID:               stringFieldOrEmpty(fields, "ID", "id"),
			Type:             stringFieldOrEmpty(fields, "Type", "type"),
			FunctionCall:     functionCallField(fields, "FunctionCall", "function_call", "functionCall"),
			ThoughtSignature: stringFieldOrEmpty(fields, "ThoughtSignature", "thought_signature", "thoughtSignature"),
		}
	}

	var generic map[string]interface{}
	if json.Unmarshal(data, &generic) == nil {
		return generic
	}
	return string(data)
}

func stringField(fields map[string]json.RawMessage, keys ...string) (string, bool) {
	raw, ok := firstExistingField(fields, keys...)
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func stringFieldOrEmpty(fields map[string]json.RawMessage, keys ...string) string {
	value, _ := stringField(fields, keys...)
	return value
}

func boolFieldOrFalse(fields map[string]json.RawMessage, keys ...string) bool {
	raw, ok := firstExistingField(fields, keys...)
	if !ok {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}

func functionCallField(fields map[string]json.RawMessage, keys ...string) *FunctionCall {
	raw, ok := firstExistingField(fields, keys...)
	if !ok || string(raw) == "null" {
		return nil
	}
	var fn FunctionCall
	if err := json.Unmarshal(raw, &fn); err != nil {
		return nil
	}
	return &fn
}

func firstExistingField(fields map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, key := range keys {
		if raw, ok := fields[key]; ok {
			return raw, true
		}
	}
	return nil, false
}

// ContentResponse represents the response from an LLM
type ContentResponse struct {
	Choices []*ContentChoice
	Usage   *Usage `json:"usage,omitempty"` // Token usage information (LLM-agnostic)
}

// ContentChoice represents a single choice in the response
type ContentChoice struct {
	Content        string
	StopReason     string
	ToolCalls      []ToolCall
	GenerationInfo *GenerationInfo `json:"generation_info,omitempty"`
	// FuncCall is a legacy field for backwards compatibility (deprecated, use ToolCalls instead)
	FuncCall *FunctionCall
}

// Usage represents token usage information
type Usage struct {
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	ReasoningTokens *int `json:"reasoning_tokens,omitempty"` // Reasoning tokens (OpenAI gpt-5.1, etc.)
	ThoughtsTokens  *int `json:"thoughts_tokens,omitempty"`  // Thoughts tokens (Gemini 3 Pro, etc.)
	CacheTokens     *int `json:"cache_tokens,omitempty"`     // Cache tokens (sum of all cache-related tokens from various providers)
}

// GenerationInfo contains token usage and generation metadata from LLM providers.
// It supports multiple naming conventions used by different providers.
type GenerationInfo struct {
	// Primary token fields (used by most providers)
	InputTokens  *int `json:"input_tokens,omitempty"`
	OutputTokens *int `json:"output_tokens,omitempty"`
	TotalTokens  *int `json:"total_tokens,omitempty"`

	// Alternative naming conventions (OpenAI-style)
	PromptTokens     *int `json:"prompt_tokens,omitempty"`
	CompletionTokens *int `json:"completion_tokens,omitempty"`

	// Capitalized variants (some providers use capitalized keys)
	PromptTokensCap     *int `json:"PromptTokens,omitempty"`
	CompletionTokensCap *int `json:"CompletionTokens,omitempty"`
	InputTokensCap      *int `json:"InputTokens,omitempty"`
	OutputTokensCap     *int `json:"OutputTokens,omitempty"`
	TotalTokensCap      *int `json:"TotalTokens,omitempty"`

	// Optional/cache-related fields
	CachedContentTokens *int     `json:"cached_content_tokens,omitempty"`
	ToolUsePromptTokens *int     `json:"tool_use_prompt_tokens,omitempty"`
	ThoughtsTokens      *int     `json:"thoughts_tokens,omitempty"`
	ReasoningTokens     *int     `json:"ReasoningTokens,omitempty"`
	CacheDiscount       *float64 `json:"cache_discount,omitempty"`

	// Additional fields for extensibility (provider-specific)
	Additional map[string]interface{} `json:"-"`

	// CodingProviderSessionHandle records provider-native continuation state for
	// coding-agent transports. It is serialized explicitly so callers can persist
	// the handle instead of re-parsing provider-specific Additional metadata.
	CodingProviderSessionHandle *CodingProviderSessionHandle `json:"coding_provider_session_handle,omitempty"`

	// CodingProviderIntermediateMessages captures the agent's internal
	// turn-by-turn trail (text chunks, tool_use calls, tool_result
	// responses) that occurred inside the provider during this
	// GenerateContent. Populated by adapters whose underlying
	// transport hides the loop from the caller (tmux CLIs read their
	// sidecar transcript; structured/API transports typically leave
	// this empty since the agent layer already sees those turns).
	// See coding_provider_intermediate_messages.go for helpers.
	CodingProviderIntermediateMessages *CodingProviderIntermediateMessages `json:"coding_provider_intermediate_messages,omitempty"`
}

// ExtractUsageFromGenerationInfo extracts token usage from GenerationInfo in an LLM-agnostic way.
// It handles different field naming conventions used by various providers (OpenAI, Anthropic, Bedrock, etc.)
// and returns a unified Usage struct. Returns nil if no token information is available.
func ExtractUsageFromGenerationInfo(genInfo *GenerationInfo) *Usage {
	if genInfo == nil {
		return nil
	}

	usage := &Usage{}

	// Extract input tokens (check multiple naming conventions in priority order)
	if genInfo.InputTokens != nil {
		usage.InputTokens = *genInfo.InputTokens
	} else if genInfo.InputTokensCap != nil {
		usage.InputTokens = *genInfo.InputTokensCap
	} else if genInfo.PromptTokens != nil {
		usage.InputTokens = *genInfo.PromptTokens
	} else if genInfo.PromptTokensCap != nil {
		usage.InputTokens = *genInfo.PromptTokensCap
	}

	// Extract output tokens (check multiple naming conventions in priority order)
	if genInfo.OutputTokens != nil {
		usage.OutputTokens = *genInfo.OutputTokens
	} else if genInfo.OutputTokensCap != nil {
		usage.OutputTokens = *genInfo.OutputTokensCap
	} else if genInfo.CompletionTokens != nil {
		usage.OutputTokens = *genInfo.CompletionTokens
	} else if genInfo.CompletionTokensCap != nil {
		usage.OutputTokens = *genInfo.CompletionTokensCap
	}

	// Extract total tokens (check multiple naming conventions in priority order)
	if genInfo.TotalTokens != nil {
		usage.TotalTokens = *genInfo.TotalTokens
	} else if genInfo.TotalTokensCap != nil {
		usage.TotalTokens = *genInfo.TotalTokensCap
	}

	// Extract reasoning tokens (OpenAI gpt-5.1 and similar models)
	if genInfo.ReasoningTokens != nil {
		usage.ReasoningTokens = genInfo.ReasoningTokens
	}

	// Extract thoughts tokens (Gemini 3 Pro and similar models)
	if genInfo.ThoughtsTokens != nil {
		usage.ThoughtsTokens = genInfo.ThoughtsTokens
	}

	// Extract cache tokens by semantic bucket, not by field name. Adapters may
	// expose the same provider value through both a typed field and raw
	// compatibility aliases; adding every alias would double- or triple-count
	// one cache read/write.
	cacheReadTokens := firstNonZeroIntPtr(genInfo.CachedContentTokens)
	if cacheReadTokens == 0 {
		cacheReadTokens = generationInfoAdditionalInt(
			genInfo.Additional,
			"cache_read_input_tokens",
			"CacheReadInputTokens",
		)
	}
	cacheWriteTokens := generationInfoAdditionalInt(
		genInfo.Additional,
		"cache_creation_input_tokens",
		"CacheCreationInputTokens",
	)
	cacheTokens := cacheReadTokens + cacheWriteTokens

	// Set cache tokens if any were found
	if cacheTokens > 0 {
		usage.CacheTokens = &cacheTokens
	}

	// Calculate total tokens if not provided by the provider
	// Note: TotalTokens from provider may already include reasoning/thoughts tokens
	if usage.TotalTokens == 0 && usage.InputTokens > 0 && usage.OutputTokens > 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		// If we have reasoning/thoughts tokens, they're typically already included in TotalTokens
		// from the provider, so we don't add them again here
	}

	// Return nil if no token information was found
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}

	return usage
}

// PropertySchema represents a single property in a JSON schema
type PropertySchema struct {
	Type        string                 `json:"type,omitempty"`
	Description string                 `json:"description,omitempty"`
	Properties  map[string]interface{} `json:"properties,omitempty"`
	Items       interface{}            `json:"items,omitempty"`
	Enum        []interface{}          `json:"enum,omitempty"`
	Default     interface{}            `json:"default,omitempty"`
	Minimum     *float64               `json:"minimum,omitempty"`
	Maximum     *float64               `json:"maximum,omitempty"`
	MinLength   *int                   `json:"minLength,omitempty"`
	MaxLength   *int                   `json:"maxLength,omitempty"`
	Pattern     string                 `json:"pattern,omitempty"`
	Format      string                 `json:"format,omitempty"`
	// Additional fields for extensibility
	Additional map[string]interface{} `json:"-"`
}

// Parameters represents a JSON schema for function parameters.
// This follows the JSON Schema specification used by LLM providers for function definitions.
type Parameters struct {
	Type                 string                 `json:"type,omitempty"` // Typically "object"
	Properties           map[string]interface{} `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties interface{}            `json:"additionalProperties,omitempty"`
	PatternProperties    map[string]interface{} `json:"patternProperties,omitempty"`
	MinProperties        *int                   `json:"minProperties,omitempty"`
	MaxProperties        *int                   `json:"maxProperties,omitempty"`
	// Additional fields for extensibility
	Additional map[string]interface{} `json:"-"`
}

// UsageMetadata represents usage-related metadata for LLM requests
type UsageMetadata struct {
	Include bool `json:"include,omitempty"`
}

// Metadata contains provider-specific metadata for LLM generation requests.
// It supports structured fields for common use cases and extensibility for provider-specific needs.
type Metadata struct {
	// Structured fields for common metadata
	Usage *UsageMetadata `json:"usage,omitempty"`

	// Custom fields for provider-specific metadata
	Custom map[string]interface{} `json:"custom,omitempty"`
}

// Tool represents a tool/function definition that can be called
type Tool struct {
	Type     string
	Function *FunctionDefinition
}

// FunctionDefinition represents a function definition with schema
type FunctionDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  *Parameters `json:"parameters,omitempty"`
}

// ToolChoice represents tool choice configuration
type ToolChoice struct {
	Type     string // "auto", "none", "required"
	Function *FunctionName
	Any      bool
	None     bool
}

// FunctionName represents a specific function to call
type FunctionName struct {
	Name string
}

// JSONSchemaConfig holds configuration for JSON Schema structured outputs
type JSONSchemaConfig struct {
	Name        string                 // Schema name
	Description string                 // Schema description
	Schema      map[string]interface{} // JSON Schema definition
	Strict      bool                   // Whether to enforce strict schema compliance
}

// CallOptions holds all call options for LLM generation
type CallOptions struct {
	Model           string
	Temperature     float64
	MaxTokens       int
	JSONMode        bool
	JSONSchema      *JSONSchemaConfig // JSON Schema for structured outputs
	Tools           []Tool
	ToolChoice      *ToolChoice
	StreamChan      chan<- StreamChunk // Channel for streaming chunks (content and tool calls)
	Metadata        *Metadata          `json:"metadata,omitempty"` // Provider-specific metadata
	ReasoningEffort string             // Reasoning effort level: "minimal", "low", "medium", "high" (for gpt-5.1 and similar models)
	Verbosity       string             // Response verbosity level: "low", "medium", "high" (for reasoning models)
	ThinkingLevel   string             // Thinking level: "low", "high" (for Gemini 3 Pro)
	ThinkingBudget  int                // Thinking budget (token limit) for reasoning models (e.g., Gemini 2.5 Flash Thinking)
	AllowedTools    []string           // Explicitly allowed tools for agentic models (e.g., gpt-5.2-codex)
	CLISecurity     *CLISecurityPolicy // Resolved coding-CLI launch policy; nil means compatibility mode.
	// Host-owned recovery context, used only when a native session cannot load.
	CodingProviderHistory []MessageContent `json:"-"`
	// Called synchronously after session creation, before submitting a prompt.
	OnCodingProviderSession func(CodingProviderSessionHandle) `json:"-"`
	// CLIRuntimeDir keeps generated artifacts out of a caller-owned working
	// directory. Native cwd and session identity still refer to that directory.
	CLIRuntimeDir string
	// Host-owned native continuation transport. Never inferred from chat content.
	CodingProviderTransport string `json:"-"`

	// Sampling controls supported by most modern providers. Zero values
	// mean "do not set" — adapters should only forward these to the
	// provider when explicitly populated, so the provider's own default
	// (typically top_p=1, top_k=0=disabled) keeps applying.
	TopP          float64  // Nucleus sampling probability (0 < p ≤ 1)
	TopK          int      // Top-k sampling cutoff (provider must support; Anthropic accepts; OpenAI does not)
	StopSequences []string // Up to N strings that, if generated, halt sampling immediately

	// InspectorSink is an opt-in debug-event consumer. When non-nil,
	// adapters emit structured InspectorEvents (request → events →
	// tool_calls → completion/error) describing the call lifecycle.
	// When nil (the default), adapters skip emission entirely — zero
	// allocation, single nil-compare per event hook.
	// See docs/inspector_contract.md for the wire contract.
	InspectorSink InspectorSink
}

// CallOption is a function type for setting call options
type CallOption func(*CallOptions)

// NewParameters creates a new Parameters struct from a map.
// This is a convenience function for converting maps to typed Parameters.
func NewParameters(paramsMap map[string]interface{}) *Parameters {
	if paramsMap == nil {
		return nil
	}

	params := &Parameters{}
	if typ, ok := paramsMap["type"].(string); ok {
		params.Type = typ
	}
	if properties, ok := paramsMap["properties"].(map[string]interface{}); ok {
		params.Properties = properties
	}
	if required, ok := paramsMap["required"].([]interface{}); ok {
		requiredStr := make([]string, 0, len(required))
		for _, r := range required {
			if s, ok := r.(string); ok {
				requiredStr = append(requiredStr, s)
			}
		}
		params.Required = requiredStr
	} else if required, ok := paramsMap["required"].([]string); ok {
		params.Required = required
	}
	if additionalProps, ok := paramsMap["additionalProperties"]; ok {
		params.AdditionalProperties = additionalProps
	}
	if patternProps, ok := paramsMap["patternProperties"].(map[string]interface{}); ok {
		params.PatternProperties = patternProps
	}
	if minProps, ok := paramsMap["minProperties"].(float64); ok {
		min := int(minProps)
		params.MinProperties = &min
	} else if minProps, ok := paramsMap["minProperties"].(int); ok {
		params.MinProperties = &minProps
	}
	if maxProps, ok := paramsMap["maxProperties"].(float64); ok {
		max := int(maxProps)
		params.MaxProperties = &max
	} else if maxProps, ok := paramsMap["maxProperties"].(int); ok {
		params.MaxProperties = &maxProps
	}
	// Store any additional fields
	params.Additional = make(map[string]interface{})
	for k, v := range paramsMap {
		switch k {
		case "type", "properties", "required", "additionalProperties", "patternProperties", "minProperties", "maxProperties":
			// Already handled
		default:
			params.Additional[k] = v
		}
	}
	return params
}

// Embedding represents a single embedding vector with metadata
type Embedding struct {
	Index     int       `json:"index"`            // Index of the embedding in the batch
	Embedding []float32 `json:"embedding"`        // The embedding vector
	Object    string    `json:"object,omitempty"` // Object type (usually "embedding")
}

// EmbeddingUsage represents token usage information for embedding generation
type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"` // Number of tokens in the input
	TotalTokens  int `json:"total_tokens"`  // Total tokens used
}

// EmbeddingResponse represents the response from an embedding generation request
type EmbeddingResponse struct {
	Embeddings []Embedding     `json:"embeddings"`       // Array of embeddings
	Model      string          `json:"model"`            // Model used for generation
	Usage      *EmbeddingUsage `json:"usage,omitempty"`  // Token usage information
	Object     string          `json:"object,omitempty"` // Object type (usually "list")
}

// EmbeddingOptions holds all options for embedding generation
type EmbeddingOptions struct {
	Model      string // Model ID (e.g., "text-embedding-3-small")
	Dimensions *int   // Optional dimensions parameter (for text-embedding-3 models)
}

// EmbeddingOption is a function type for setting embedding options
type EmbeddingOption func(*EmbeddingOptions)

// SignalPromptAccepted tells the caller the coding CLI took the prompt. It never blocks and does nothing without a
// stream channel; it must be called before the adapter closes the channel.
func SignalPromptAccepted(ch chan<- StreamChunk) {
	if ch == nil {
		return
	}
	select {
	case ch <- StreamChunk{Type: StreamChunkTypePromptAccepted}:
	default:
	}
}
