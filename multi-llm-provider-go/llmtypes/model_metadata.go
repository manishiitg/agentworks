package llmtypes

// ModelMetadata contains comprehensive metadata about an LLM model including
// token limits, pricing, and high-level capabilities (reasoning, thinking level, tools, etc.)
type ModelMetadata struct {
	// ModelID is the unique identifier for the model (e.g., "gpt-4o", "claude-3-5-sonnet-20241022")
	ModelID string `json:"model_id"`

	// ModelName is the human-readable name of the model
	ModelName string `json:"model_name"`

	// ContextWindow is the maximum number of tokens the model can process in a single request
	ContextWindow int `json:"context_window"`

	// InputCostPer1MTokens is the cost per 1 million input tokens (in USD)
	InputCostPer1MTokens float64 `json:"input_cost_per_1m"`

	// OutputCostPer1MTokens is the cost per 1 million output tokens (in USD)
	OutputCostPer1MTokens float64 `json:"output_cost_per_1m"`

	// ReasoningCostPer1MTokens is the cost per 1 million reasoning tokens (in USD)
	// This applies to reasoning models like o1, o3, gpt-5.1
	// Set to 0 if the model doesn't support reasoning tokens
	ReasoningCostPer1MTokens float64 `json:"reasoning_cost_per_1m"`

	// CachedInputCostPer1MTokens is the cost per 1 million cached input tokens read from cache (in USD)
	// Cached tokens typically have a 50-90% discount compared to regular input tokens
	// Set to 0 if the model doesn't support prompt caching
	CachedInputCostPer1MTokens float64 `json:"cached_input_cost_per_1m"`

	// CachedInputCostWritePer1MTokens is the cost per 1 million cached input tokens written to cache (in USD)
	// Cache write costs are typically higher than regular input tokens (e.g., Anthropic charges 25% more)
	// Set to 0 if the model doesn't support cache write tracking or if cache writes are charged at regular input rate
	CachedInputCostWritePer1MTokens float64 `json:"cached_input_cost_write_per_1m"`

	// LongContextThresholdTokens activates request-wide input and output rate
	// multipliers when the total input footprint exceeds this many tokens.
	// Cached reads and writes use the input multiplier.
	LongContextThresholdTokens  int     `json:"long_context_threshold_tokens,omitempty"`
	LongContextInputMultiplier  float64 `json:"long_context_input_multiplier,omitempty"`
	LongContextOutputMultiplier float64 `json:"long_context_output_multiplier,omitempty"`

	// CostPerImage is the cost per generated image (in USD).
	// Applies to image generation models (e.g., Imagen). Set to 0 for text/embedding models.
	CostPerImage float64 `json:"cost_per_image,omitempty"`

	// Provider is the name of the provider (e.g., "openai", "anthropic", "bedrock", "vertex")
	Provider string `json:"provider"`

	// ===== Capabilities (optional; zero values mean "unknown/unsupported" unless documented) =====

	// SupportsToolCalls indicates whether the model can call tools/functions
	SupportsToolCalls bool `json:"supports_tool_calls"`

	// SupportsJSONMode indicates whether the model can enforce JSON responses (JSON mode / response_format)
	SupportsJSONMode bool `json:"supports_json_mode"`

	// SupportsThinkingLevel indicates whether the model supports a thinking-level knob (e.g., Gemini 3 Pro)
	// When true, ThinkingLevels lists the allowed string values (e.g., ["low","high"])
	SupportsThinkingLevel bool     `json:"supports_thinking_level"`
	ThinkingLevels        []string `json:"thinking_levels"`

	// SupportsReasoningEffort indicates whether the model supports a reasoning-effort knob (e.g., gpt-5.1)
	// When true, ReasoningEffortLevels lists the allowed string values (e.g., ["minimal","low","medium","high"])
	SupportsReasoningEffort bool     `json:"supports_reasoning_effort"`
	ReasoningEffortLevels   []string `json:"reasoning_effort_levels"`

	// SupportsThinkingBudget indicates whether the model supports a numeric thinking budget
	// (e.g., Gemini 2.5 Pro's thinkingBudget, which controls reasoning token budget).
	// Specific ranges/units are provider-specific and documented externally.
	SupportsThinkingBudget bool `json:"supports_thinking_budget"`

	// ModelSelectionMode tells the frontend how to present model selection for
	// this provider. "fixed_tier" (default) means a small set of curated
	// tiers/models shown as cards. "dynamic" means the provider supports many
	// models that should be fetched on demand via a CLI command or API.
	ModelSelectionMode string `json:"model_selection_mode,omitempty"`
}

// ModelMetadataProvider is an optional interface that models can implement
// to provide metadata about themselves
type ModelMetadataProvider interface {
	// GetModelMetadata returns metadata for the specified model ID
	// If modelID is empty, returns metadata for the default model
	GetModelMetadata(modelID string) (*ModelMetadata, error)
}
