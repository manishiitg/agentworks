package costledger

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// VisitEntries streams immutable SQLite events in a UTC date window. The
// server supplies current visibility and response projection; this is not an
// externally exposed ledger export. Context cancellation bounds large scans.
func (l *Ledger) VisitEntries(ctx context.Context, from, to string, visit func(Entry) error) error {
	if l == nil || visit == nil {
		return fmt.Errorf("costledger: ledger and visitor required")
	}
	store, ok := l.db.(*sqliteLedger)
	if !ok || store == nil {
		return fmt.Errorf("costledger: attribution queries require SQLite")
	}
	start, end, err := costDateBounds(from, to)
	if err != nil {
		return err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
		event_id, user_id, workflow_id, session_id, run_id, execution_id, scope,
		source_platform, phase, agent_mode, component, requested_provider, requested_model_id,
		effective_provider, effective_model_id, account_id, llm_call_count,
		llm_generation_duration_ms, prompt_tokens, completion_tokens, reasoning_tokens,
		cache_read_tokens, cache_write_tokens, total_cost_usd, billing_basis, occurred_at,
		operation_metadata_json, source_id, source_label, source_run_id,
		attribution_version, actor_id, product_kind, product_id, product_owner_id, product_name, activity,
		origin_dashboard_id, origin_dashboard_name, function_call_id, parent_session_id,
		parent_execution_id
		FROM cost_events WHERE occurred_at >= ? AND occurred_at < ?
		ORDER BY occurred_at, event_id`, start, end)
	if err != nil {
		return fmt.Errorf("costledger: query attributed events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var timestamp, metadata string
		if err := rows.Scan(&e.EventID, &e.UserID, &e.WorkflowID, &e.SessionID,
			&e.RunID, &e.ExecutionID, &e.Scope, &e.SourcePlatform, &e.Phase, &e.AgentMode,
			&e.Component, &e.Provider, &e.ModelID, &e.EffectiveProvider, &e.EffectiveModelID,
			&e.AccountID, &e.LLMCallCount, &e.LLMGenerationDurationMS, &e.PromptTokens,
			&e.CompletionTokens, &e.ReasoningTokens, &e.CacheReadTokens, &e.CacheWriteTokens,
			&e.TotalCostUSD, &e.BillingBasis, &timestamp, &metadata, &e.SourceID,
			&e.SourceLabel, &e.SourceRunID, &e.AttributionVersion, &e.ActorID,
			&e.ProductKind, &e.ProductID, &e.ProductOwnerID, &e.ProductName, &e.Activity,
			&e.OriginDashboardID, &e.OriginDashboardName, &e.FunctionCallID,
			&e.ParentSessionID, &e.ParentExecutionID); err != nil {
			return fmt.Errorf("costledger: scan attributed event: %w", err)
		}
		e.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return fmt.Errorf("costledger: invalid event time: %w", err)
		}
		// Cost responses need cache semantics, never arbitrary paid-tool payloads.
		var flags struct {
			IncludesCache json.RawMessage `json:"prompt_tokens_include_cache"`
		}
		if err := json.Unmarshal([]byte(metadata), &flags); err != nil {
			return fmt.Errorf("costledger: invalid accounting metadata: %w", err)
		}
		if string(flags.IncludesCache) == "true" || string(flags.IncludesCache) == "false" {
			e.OperationMetadata = map[string]interface{}{"prompt_tokens_include_cache": string(flags.IncludesCache) == "true"}
		}
		if err := visit(e); err != nil {
			return err
		}
	}
	return rows.Err()
}
