package claudecode

import "testing"

// TestParseClaudeStatusLineJSON_NativeModelAndCost covers Claude Code's native
// statusLine input shape: a nested model object and a cost object. Both must be
// surfaced (real display name + cost), overriding the passed-in default model.
func TestParseClaudeStatusLineJSON_NativeModelAndCost(t *testing.T) {
	raw := []byte(`{
		"model": {"id": "claude-opus-4-8", "display_name": "Opus 4.8"},
		"cost": {"total_cost_usd": 0.0421},
		"input_tokens": 15000,
		"output_tokens": 273,
		"cache_read_input_tokens": 48000
	}`)

	status, err := parseClaudeStatusLineJSON(raw, "fallback-model")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if status.Model != "Opus 4.8" {
		t.Errorf("Model = %q, want 'Opus 4.8' (display_name overrides default)", status.Model)
	}
	if status.CostUSD != 0.0421 {
		t.Errorf("CostUSD = %v, want 0.0421", status.CostUSD)
	}
	if status.InputTokens != 15000 || status.OutputTokens != 273 || status.CacheReadInputTokens != 48000 {
		t.Errorf("tokens not parsed: %+v", status)
	}
	if status.Provider != "claudecode" {
		t.Errorf("Provider = %q, want claudecode", status.Provider)
	}
}

// TestParseClaudeStatusLineJSON_PlaceholderModelStripped ensures the
// "claude-code" placeholder (equal to the provider name) is dropped so the label
// doesn't render a duplicate "claudecode · claude-code".
func TestParseClaudeStatusLineJSON_PlaceholderModelStripped(t *testing.T) {
	// No model in the JSON; default is the placeholder id.
	status, err := parseClaudeStatusLineJSON([]byte(`{"input_tokens": 10}`), "claude-code")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if status.Model != "" {
		t.Errorf("Model = %q, want empty (placeholder stripped)", status.Model)
	}
}

// TestParseClaudeStatusLineJSON_CustomTokenFormat ensures the older token-only
// format (no model/cost objects) still parses with no regression.
func TestParseClaudeStatusLineJSON_CustomTokenFormat(t *testing.T) {
	raw := []byte(`{"inputTokens": 100, "outputTokens": 50, "cacheReadInputTokens": 200}`)
	status, err := parseClaudeStatusLineJSON(raw, "")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if status.InputTokens != 100 || status.OutputTokens != 50 || status.CacheReadInputTokens != 200 {
		t.Errorf("camelCase tokens not parsed: %+v", status)
	}
	if status.Model != "" {
		t.Errorf("Model = %q, want empty (no model in payload, no default)", status.Model)
	}
	if status.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 (no cost in payload)", status.CostUSD)
	}
}

// TestParseClaudeStatusLineJSON_ModelAsString covers the variant where "model"
// is a bare string rather than an object.
func TestParseClaudeStatusLineJSON_ModelAsString(t *testing.T) {
	status, err := parseClaudeStatusLineJSON([]byte(`{"model": "claude-sonnet-4-6"}`), "fallback")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if status.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q, want claude-sonnet-4-6", status.Model)
	}
}

// TestParseClaudeStatusLineJSON_RateLimitExtras covers Claude Code's rate_limits
// block (Pro/Max plan usage). Both windows must surface as display-ready usage
// segments under Metadata["status_extras"], the generic per-provider contract.
func TestParseClaudeStatusLineJSON_RateLimitExtras(t *testing.T) {
	raw := []byte(`{
		"model": {"display_name": "Opus 4.8"},
		"input_tokens": 472,
		"output_tokens": 76,
		"cost": {"total_cost_usd": 151.13},
		"rate_limits": {
			"five_hour": {"used_percentage": 24.0, "resets_at": 0},
			"seven_day": {"used_percentage": 41.0, "resets_at": 0}
		}
	}`)
	status, err := parseClaudeStatusLineJSON(raw, "")
	if err != nil {
		t.Fatalf("parseClaudeStatusLineJSON: %v", err)
	}
	extras, ok := status.Metadata["status_extras"].([]string)
	if !ok {
		t.Fatalf("Metadata[status_extras] = %#v, want []string", status.Metadata["status_extras"])
	}
	want := []string{"5h 24%", "7d 41%"}
	if len(extras) != len(want) || extras[0] != want[0] || extras[1] != want[1] {
		t.Fatalf("extras = %v, want %v", extras, want)
	}
}

// TestParseClaudeStatusLineJSON_NoRateLimitsNoExtras proves the extras key is
// absent (not an empty list) when no rate_limits are present — e.g. before the
// first API response, or for non-subscription auth.
func TestParseClaudeStatusLineJSON_NoRateLimitsNoExtras(t *testing.T) {
	status, err := parseClaudeStatusLineJSON([]byte(`{"input_tokens": 10}`), "")
	if err != nil {
		t.Fatalf("parseClaudeStatusLineJSON: %v", err)
	}
	if _, present := status.Metadata["status_extras"]; present {
		t.Fatalf("status_extras present, want absent: %#v", status.Metadata["status_extras"])
	}
}

// TestParseClaudeStatusLineJSON_ContextAndEffortExtras proves context-window fill
// (ctx%) and reasoning effort surface as extras alongside rate limits, in order.
func TestParseClaudeStatusLineJSON_ContextAndEffortExtras(t *testing.T) {
	raw := []byte(`{
		"model": {"display_name": "Opus 4.8"},
		"input_tokens": 100,
		"rate_limits": {
			"five_hour": {"used_percentage": 24.0},
			"seven_day": {"used_percentage": 41.0}
		},
		"context_window": {"used_percentage": 7},
		"effort": {"level": "high"}
	}`)
	status, err := parseClaudeStatusLineJSON(raw, "")
	if err != nil {
		t.Fatalf("parseClaudeStatusLineJSON: %v", err)
	}
	extras, _ := status.Metadata["status_extras"].([]string)
	want := []string{"5h 24%", "7d 41%", "ctx 7%", "high"}
	if len(extras) != len(want) {
		t.Fatalf("extras = %v, want %v", extras, want)
	}
	for i := range want {
		if extras[i] != want[i] {
			t.Fatalf("extras[%d] = %q, want %q (full: %v)", i, extras[i], want[i], extras)
		}
	}
}

func TestParseClaudeStatusLineJSON_NativeContextUsage(t *testing.T) {
	raw := []byte(`{
		"context_window": {
			"total_input_tokens": 73002,
			"total_output_tokens": 843,
			"current_usage": {
				"input_tokens": 2,
				"output_tokens": 843,
				"cache_creation_input_tokens": 2430,
				"cache_read_input_tokens": 70570
			},
			"used_percentage": 7
		}
	}`)
	status, err := parseClaudeStatusLineJSON(raw, "")
	if err != nil {
		t.Fatalf("parseClaudeStatusLineJSON: %v", err)
	}
	if status.TotalInputTokens != 73002 || status.TotalOutputTokens != 843 {
		t.Fatalf("total tokens = %d/%d, want 73002/843", status.TotalInputTokens, status.TotalOutputTokens)
	}
	if status.InputTokens != 2 || status.OutputTokens != 843 {
		t.Fatalf("current tokens = %d/%d, want 2/843", status.InputTokens, status.OutputTokens)
	}
	if status.CacheCreationInputTokens != 2430 || status.CacheReadInputTokens != 70570 {
		t.Fatalf("cache tokens = %d/%d, want 2430/70570", status.CacheCreationInputTokens, status.CacheReadInputTokens)
	}
}
