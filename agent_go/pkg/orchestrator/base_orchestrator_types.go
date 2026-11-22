package orchestrator

import (
	"time"

	"github.com/manishiitg/mcpagent/llm"
)

// LLMModel represents a single LLM configuration (orchestrator level)
type LLMModel struct {
	ConnectionID string                 `json:"connection_id,omitempty"`
	Provider     string                 `json:"provider"`
	ModelID      string                 `json:"model_id"`
	APIKey       *string                `json:"api_key,omitempty"` // Per-model API key
	Region       *string                `json:"region,omitempty"`  // For Bedrock
	Options      map[string]interface{} `json:"options,omitempty"` // Provider-specific runtime options
}

// LLMConfig represents the unified LLM configuration
type LLMConfig struct {
	Primary LLMModel `json:"primary"`

	APIKeys *APIKeys `json:"api_keys,omitempty"` // Global API keys (fallback if per-model not set)
}

// APIKeys is an alias for llm.ProviderAPIKeys (canonical type).
type APIKeys = llm.ProviderAPIKeys

// BedrockKey is an alias for llm.BedrockConfig (canonical type).
type BedrockKey = llm.BedrockConfig

// AzureKey is an alias for llm.AzureAPIConfig (canonical type).
type AzureKey = llm.AzureAPIConfig

// OrchestratorType represents the type of orchestrator
type OrchestratorType string

const (
	OrchestratorTypePlanner  OrchestratorType = "planner"
	OrchestratorTypeWorkflow OrchestratorType = "workflow"
)

// StepTokenUsage represents accumulated token usage for a workflow step
type StepTokenUsage struct {
	InputTokens           int
	OutputTokens          int
	CacheTokens           int // Total cache tokens (read + write)
	CacheReadTokens       int // Tokens read from cache (discounted)
	CacheWriteTokens      int // Tokens written to cache (premium)
	ReasoningTokens       int
	LLMCallCount          int
	CacheEnabledCallCount int
	CacheDiscountSum      float64 // Sum of cache discounts for averaging
	// Pricing fields (aggregated across all models)
	InputCost      float64
	OutputCost     float64
	ReasoningCost  float64
	CacheCost      float64 // Total cache cost (read + write)
	CacheReadCost  float64 // Cache read cost (discounted rate)
	CacheWriteCost float64 // Cache write cost (premium rate)
	TotalCost      float64
	// Context window usage (max across all models)
	ContextUsagePercent float64
}

// StepTokenData represents token data for a step to be persisted
type StepTokenData struct {
	Phase            string
	Step             int    // Step index (deprecated, use StepID)
	StepID           string // Step ID (e.g., "fetch-data", "process-results")
	StepTitle        string
	InputTokens      int
	OutputTokens     int
	CacheTokens      int // Total cache tokens (read + write) - kept for backward compatibility
	CacheReadTokens  int // Tokens read from cache (charged at discount rate)
	CacheWriteTokens int // Tokens written to cache (charged at premium rate, 1.25x)
	ReasoningTokens  int
	LLMCallCount     int
	// ExecutionID is one immutable identifier minted per orchestrator/bridge
	// instance (PLAT-031). It stays constant for the whole run, including
	// across a UTC-date shard rotation, so cost rows split across two date
	// files can still be recognized as one execution.
	ExecutionID string
}

// ModelTokenData represents token data for a model to be persisted
type ModelTokenData struct {
	ModelID          string
	Provider         string
	InputTokens      int
	OutputTokens     int
	CacheTokens      int // Total cache tokens (read + write) - kept for backward compatibility
	CacheReadTokens  int // Tokens read from cache (charged at discount rate)
	CacheWriteTokens int // Tokens written to cache (charged at premium rate, 1.25x)
	ReasoningTokens  int
	LLMCallCount     int
	// Pricing fields (calculated from model metadata)
	InputCost      float64
	OutputCost     float64
	ReasoningCost  float64
	CacheCost      float64 // Total cache cost (read + write) - kept for backward compatibility
	CacheReadCost  float64 // Cost for cache reads (discounted)
	CacheWriteCost float64 // Cost for cache writes (premium)
	TotalCost      float64
	// Context window tracking
	ContextWindowUsage int // Current tokens used in context window
	ModelContextWindow int // Model's context window size
}

// TokenUsageFile represents persisted token usage data per iteration
type TokenUsageFile struct {
	CreatedAt      time.Time                              `json:"created_at"`
	UpdatedAt      time.Time                              `json:"updated_at"`
	ByModel        map[string]*ModelTokenUsage            `json:"by_model"`          // Aggregated by model (across all steps)
	ByStepAndModel map[string]map[string]*ModelTokenUsage `json:"by_step_and_model"` // Nested map: stepKey -> modelID -> token usage
	ByTool         map[string]*ToolCostUsage              `json:"by_tool,omitempty"` // Aggregated non-token tool costs
	ByStepAndTool  map[string]map[string]*ToolCostUsage   `json:"by_step_and_tool,omitempty"`
	// ExecutionID is the run-instance identity for this run folder's aggregate
	// (PLAT-031). Sticky first-write: set once by whichever call first creates
	// this run folder's entry, then carried forward through every clone/merge
	// so it survives a UTC-midnight date-shard rotation.
	ExecutionID string `json:"execution_id,omitempty"`
	// PriorExecutionIDs records earlier ExecutionID values displaced by a
	// later execution reusing this same run folder, so two runs that share a
	// run folder remain distinguishable instead of silently merging identity.
	PriorExecutionIDs []string `json:"prior_execution_ids,omitempty"`
}

// TokenUsageSummary represents total token usage across all models and steps
type TokenUsageSummary struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	CacheTokens     int `json:"cache_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
	LLMCallCount    int `json:"llm_call_count"`
}

// ModelTokenUsage represents token usage for a specific model (JSON format)
// Stores both raw integers and string-formatted millions (with "M" suffix)
type ModelTokenUsage struct {
	Provider          string `json:"provider"`
	PricingModelID    string `json:"pricing_model_id,omitempty"` // immutable model identity used for rates
	PricingVersion    string `json:"pricing_version,omitempty"`  // versioned rate-card contract
	InputTokens       int    `json:"input_tokens"`               // raw count
	OutputTokens      int    `json:"output_tokens"`              // raw count
	InputTokensM      string `json:"input_tokens_m"`             // formatted as "17.016M"
	OutputTokensM     string `json:"output_tokens_m"`            // formatted as "0.116M"
	CacheTokens       int    `json:"cache_tokens"`               // raw count (total = read + write)
	CacheTokensM      string `json:"cache_tokens_m"`             // formatted as "4.546M"
	CacheReadTokens   int    `json:"cache_read_tokens"`          // tokens read from cache (discounted)
	CacheReadTokensM  string `json:"cache_read_tokens_m"`        // formatted
	CacheWriteTokens  int    `json:"cache_write_tokens"`         // tokens written to cache (premium)
	CacheWriteTokensM string `json:"cache_write_tokens_m"`       // formatted
	ReasoningTokens   int    `json:"reasoning_tokens"`           // raw count
	ReasoningTokensM  string `json:"reasoning_tokens_m"`         // formatted as "0.000M"
	LLMCallCount      int    `json:"llm_call_count"`             // count
	// Pricing fields (in USD)
	InputCost      float64 `json:"input_cost_usd,omitempty"`
	OutputCost     float64 `json:"output_cost_usd,omitempty"`
	ReasoningCost  float64 `json:"reasoning_cost_usd,omitempty"`
	CacheCost      float64 `json:"cache_cost_usd,omitempty"`       // Total cache cost (read + write)
	CacheReadCost  float64 `json:"cache_read_cost_usd,omitempty"`  // Cache read cost (discounted rate)
	CacheWriteCost float64 `json:"cache_write_cost_usd,omitempty"` // Cache write cost (premium rate, 1.25x)
	// TotalCost has no omitempty: a genuinely zero-cost call and "we have no
	// rate card for this model" must not collapse into the same absent key.
	// See Unpriced below for which one this is.
	TotalCost float64 `json:"total_cost_usd"`
	// Unpriced is true when no provider rate card was found for this model
	// (pi-cli's adapter, for example, returns metadata with every
	// *CostPer1MTokens field unset for every model it serves). All cost
	// fields above are 0 in that case, but that 0 is "unknown," not "free" --
	// omitted (false) whenever real pricing was found, so existing readers
	// that only check for the field's presence see no change for priced
	// calls.
	Unpriced bool `json:"unpriced,omitempty"`
	// SubscriptionEstimate is true for plan logins (Claude Code, Codex,
	// Cursor): the dollars above are what the same tokens would cost on the
	// API, not a charge.
	SubscriptionEstimate bool `json:"subscription_estimate,omitempty"`
	// Context window tracking
	ContextWindowUsage  int     `json:"context_window_usage,omitempty"`  // Current tokens used
	ModelContextWindow  int     `json:"model_context_window,omitempty"`  // Model's context window size
	ContextUsagePercent float64 `json:"context_usage_percent,omitempty"` // Percentage of context window used
}

// ToolCostUsage represents non-token cost for paid tools such as image, video,
// speech, transcription, and music generation.
type ToolCostUsage struct {
	ToolName    string                 `json:"tool_name"`
	Capability  string                 `json:"capability,omitempty"`
	Provider    string                 `json:"provider,omitempty"`
	ModelID     string                 `json:"model_id,omitempty"`
	Unit        string                 `json:"unit,omitempty"`
	Quantity    float64                `json:"quantity,omitempty"`
	Count       int                    `json:"count,omitempty"`
	TotalCost   float64                `json:"total_cost_usd,omitempty"`
	Estimated   bool                   `json:"estimated,omitempty"`
	OutputPaths []string               `json:"output_paths,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"created_at,omitempty"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
}

// StepTokenSummary represents token usage summary for a workflow step
// Stores both raw integers and string-formatted millions (with "M" suffix)
type StepTokenSummary struct {
	StepType         string `json:"step_type"` // e.g., "execution", "validation", "learning"
	StepTitle        string `json:"step_title,omitempty"`
	InputTokens      int    `json:"input_tokens"`       // raw count
	OutputTokens     int    `json:"output_tokens"`      // raw count
	InputTokensM     string `json:"input_tokens_m"`     // formatted as "17.016M"
	OutputTokensM    string `json:"output_tokens_m"`    // formatted as "0.116M"
	CacheTokens      int    `json:"cache_tokens"`       // raw count
	CacheTokensM     string `json:"cache_tokens_m"`     // formatted as "4.546M"
	ReasoningTokens  int    `json:"reasoning_tokens"`   // raw count
	ReasoningTokensM string `json:"reasoning_tokens_m"` // formatted as "0.000M"
	LLMCallCount     int    `json:"llm_call_count"`     // count
}

// StepTypeTokenUsage represents aggregated token usage for a step type across all steps
// Stores both raw integers and string-formatted millions (with "M" suffix)
type StepTypeTokenUsage struct {
	StepType         string `json:"step_type"`          // e.g., "execution", "validation", "learning"
	InputTokens      int    `json:"input_tokens"`       // raw count
	OutputTokens     int    `json:"output_tokens"`      // raw count
	InputTokensM     string `json:"input_tokens_m"`     // formatted as "17.016M"
	OutputTokensM    string `json:"output_tokens_m"`    // formatted as "0.116M"
	CacheTokens      int    `json:"cache_tokens"`       // raw count
	CacheTokensM     string `json:"cache_tokens_m"`     // formatted as "4.546M"
	ReasoningTokens  int    `json:"reasoning_tokens"`   // raw count
	ReasoningTokensM string `json:"reasoning_tokens_m"` // formatted as "0.000M"
	LLMCallCount     int    `json:"llm_call_count"`     // count
}

// PhaseTokenData represents token data for a phase to be persisted
type PhaseTokenData struct {
	Phase            string
	InputTokens      int
	OutputTokens     int
	CacheTokens      int // Total cache tokens (read + write) - kept for backward compatibility
	CacheReadTokens  int // Tokens read from cache (charged at discount rate)
	CacheWriteTokens int // Tokens written to cache (charged at premium rate, 1.25x)
	ReasoningTokens  int
	LLMCallCount     int
}

// PhaseTokenUsageFile represents persisted token usage data per phase under costs/phase/token_usage.json.
type PhaseTokenUsageFile struct {
	CreatedAt       time.Time                              `json:"created_at"`
	UpdatedAt       time.Time                              `json:"updated_at"`
	ByPhaseAndModel map[string]map[string]*ModelTokenUsage `json:"by_phase_and_model"` // Nested map: phase -> modelID -> token usage
	ByModel         map[string]*ModelTokenUsage            `json:"by_model"`           // Aggregated by model (across all phases)
	// SessionCumulative records the last cumulative diagnostic snapshot seen for
	// each chat. It is bookkeeping, not another cost total: the next turn is
	// reduced to a delta before it is added to the workflow-wide aggregates.
	SessionCumulative map[string]*ModelTokenUsage `json:"session_cumulative,omitempty"`
}
