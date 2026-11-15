package llmtypes

import "time"

// NativeRecord is a committed observation from one explicitly selected coding
// runtime. It is not a command, a permission grant, or platform-run completion.
// Text is present only for assistant replies and provider-authored questions;
// analytics consumers must not copy it into operational reports.
type NativeRecord struct {
	// Plan is transient chat projection data, never part of observer checkpoints.
	Plan              *CodingAgentPlan `json:"-"`
	HelperType        string           `json:"helper_type,omitempty"`
	HelperDescription string           `json:"helper_description,omitempty"`
	CurrentStep       string           `json:"current_step,omitempty"`
	HelperMode        string           `json:"helper_mode,omitempty"`
	CommandLabel      string           `json:"command_label,omitempty"`
	BackgroundMode    string           `json:"background_mode,omitempty"`
	// Background task facts are supplied by the owning runtime/host, never
	// estimated from a parent turn's usage. Nil Tokens means unreported.
	TokenSource string    `json:"token_source,omitempty"`
	WorkID      string    `json:"work_id,omitempty"`
	TaskRunID   string    `json:"task_run_id,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	EndedAt     time.Time `json:"ended_at,omitempty"`
	Tokens      *int64    `json:"tokens,omitempty"`
	// AssistantBlockID is the native presentation identity shared with a live
	// assistant block, not the transcript row UUID or a completion verdict.
	AssistantBlockID string       `json:"assistant_block_id,omitempty"`
	ID               string       `json:"id"`
	Provider         string       `json:"provider"`
	SessionID        string       `json:"native_session_id"`
	TurnID           string       `json:"native_turn_id,omitempty"`
	ParentID         string       `json:"parent_id,omitempty"`
	Kind             string       `json:"kind"`
	State            string       `json:"state,omitempty"`
	Origin           string       `json:"origin,omitempty"`
	Timestamp        time.Time    `json:"timestamp,omitempty"`
	Text             string       `json:"text,omitempty"`
	Name             string       `json:"name,omitempty"`
	ToolID           string       `json:"tool_id,omitempty"`
	TaskID           string       `json:"task_id,omitempty"`
	FollowupID       string       `json:"followup_turn_id,omitempty"`
	Model            string       `json:"model,omitempty"`
	DurationMS       int64        `json:"duration_ms,omitempty"`
	Usage            *NativeUsage `json:"usage,omitempty"`
	// Output is a finished command's output, used only to close that
	// command's own tool card in its chat. It is never serialized, so saved
	// activity, observer state and reports do not copy it.
	Output string `json:"-"`
}

// NativeUsage keeps the producer's scope explicit. Cumulative observations
// replace a previous observation of that scope; they must never be summed.
// A list-price estimate is not an invoice. Missing prices remain absent.
type NativeUsage struct {
	Scope       string   `json:"scope"`
	Cumulative  bool     `json:"cumulative"`
	Input       int64    `json:"input_tokens,omitempty"`
	Output      int64    `json:"output_tokens,omitempty"`
	CacheRead   int64    `json:"cache_read_tokens,omitempty"`
	CacheWrite  int64    `json:"cache_write_tokens,omitempty"`
	EstimateUSD *float64 `json:"estimated_usd,omitempty"`
}
