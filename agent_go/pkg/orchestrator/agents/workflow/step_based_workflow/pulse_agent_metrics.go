package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
)

// PulseAgentMetricRecord is one durable measurement for Pulse work.
type PulseAgentMetricRecord struct {
	ID               int64                            `json:"id"`
	ExecutionID      string                           `json:"execution_id"`
	AgentSessionID   string                           `json:"agent_session_id,omitempty"`
	PulseRunID       string                           `json:"pulse_run_id,omitempty"`
	ReviewRunID      string                           `json:"review_run_id,omitempty"`
	Module           string                           `json:"module"`
	Role             string                           `json:"role"`
	Status           string                           `json:"status"`
	QueuedAt         string                           `json:"queued_at,omitempty"`
	StartedAt        string                           `json:"started_at,omitempty"`
	CompletedAt      string                           `json:"completed_at,omitempty"`
	QueueDurationMS  int64                            `json:"queue_duration_ms"`
	DurationMS       int64                            `json:"duration_ms"`
	LLMCallCount     int                              `json:"llm_call_count"`
	PromptTokens     int                              `json:"prompt_tokens"`
	CompletionTokens int                              `json:"completion_tokens"`
	ReasoningTokens  int                              `json:"reasoning_tokens"`
	CacheReadTokens  int                              `json:"cache_read_tokens"`
	CacheWriteTokens int                              `json:"cache_write_tokens"`
	TotalCostUSD     float64                          `json:"total_cost_usd"`
	Models           map[string]*costledger.Aggregate `json:"models,omitempty"`
	UsageStatus      string                           `json:"usage_status"`
	UsageError       string                           `json:"usage_error,omitempty"`
	RecordedAt       string                           `json:"recorded_at"`
}

// LoadPulseAgentMetrics returns newest-first measurements. Empty filters match
// all values; a negative limit returns the complete history.
func LoadPulseAgentMetrics(ctx context.Context, workspacePath, pulseRunID, module, role string, limit int) ([]PulseAgentMetricRecord, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulseAgentMetricRecord{}, err
	}
	defer db.Close()
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='pulse_agent_metrics'`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return []PulseAgentMetricRecord{}, nil
	}
	query := `SELECT _id, execution_id, agent_session_id, pulse_run_id, review_run_id,
		module, role, status, queued_at, started_at, completed_at, queue_duration_ms,
		duration_ms, llm_call_count, prompt_tokens, completion_tokens, reasoning_tokens,
		cache_read_tokens, cache_write_tokens, total_cost_usd, models_json,
		usage_status, usage_error, recorded_at FROM pulse_agent_metrics WHERE 1=1`
	args := []interface{}{}
	for _, filter := range []struct {
		column string
		value  string
	}{{"pulse_run_id", pulseRunID}, {"module", module}, {"role", role}} {
		if value := strings.TrimSpace(filter.value); value != "" {
			if filter.column == "module" {
				value = pulsemodules.Normalize(value)
			}
			query += " AND " + filter.column + "=?"
			args = append(args, value)
		}
	}
	query += ` ORDER BY completed_at DESC, _id DESC`
	if limit == 0 {
		limit = 200
	}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PulseAgentMetricRecord{}
	for rows.Next() {
		var metric PulseAgentMetricRecord
		var modelsJSON string
		if err := rows.Scan(
			&metric.ID, &metric.ExecutionID, &metric.AgentSessionID, &metric.PulseRunID,
			&metric.ReviewRunID, &metric.Module, &metric.Role, &metric.Status,
			&metric.QueuedAt, &metric.StartedAt, &metric.CompletedAt, &metric.QueueDurationMS,
			&metric.DurationMS, &metric.LLMCallCount, &metric.PromptTokens,
			&metric.CompletionTokens, &metric.ReasoningTokens, &metric.CacheReadTokens,
			&metric.CacheWriteTokens, &metric.TotalCostUSD, &modelsJSON,
			&metric.UsageStatus, &metric.UsageError, &metric.RecordedAt,
		); err != nil {
			return nil, err
		}
		if modelsJSON != "" && modelsJSON != "{}" {
			if err := json.Unmarshal([]byte(modelsJSON), &metric.Models); err != nil {
				return nil, fmt.Errorf("decode Pulse agent models for %s: %w", metric.ExecutionID, err)
			}
		}
		out = append(out, metric)
	}
	return out, rows.Err()
}
