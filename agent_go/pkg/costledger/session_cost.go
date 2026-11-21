package costledger

import (
	"fmt"
	"sort"
	"strings"
)

// SessionModelCost is one model's share of a chat's spend.
type SessionModelCost struct {
	Model string  `json:"model"`
	USD   float64 `json:"usd"`
}

// SessionCost is what one chat has cost so far, including the helper agents
// it started (events whose parent session is this chat).
type SessionCost struct {
	TotalUSD float64            `json:"total_usd"`
	Models   []SessionModelCost `json:"models"`
	// HelperUSD is the part of TotalUSD spent by helper agents.
	HelperUSD float64 `json:"helper_usd"`
	// Basis is how real TotalUSD is: "actual" (provider-reported), "estimate"
	// (subscription equivalent or token estimate) or "mixed". Calls without a
	// price are counted in UnpricedCalls and add nothing to TotalUSD.
	Basis         string `json:"basis"`
	UnpricedCalls int    `json:"unpriced_calls"`
	// CachePercent is the share of input tokens served from cache, 0 when
	// there was no input.
	CachePercent int `json:"cache_percent"`
}

// SessionCost totals one chat's events. It is read-only and needs the SQLite
// ledger; callers show nothing when it returns an error.
func (l *Ledger) SessionCost(sessionID string) (*SessionCost, error) {
	sessionID = strings.TrimSpace(sessionID)
	if l == nil || sessionID == "" {
		return nil, fmt.Errorf("costledger: ledger and session id required")
	}
	store, ok := l.db.(*sqliteLedger)
	if !ok || store == nil {
		return nil, fmt.Errorf("costledger: session cost requires SQLite")
	}
	rows, err := store.db.Query(`SELECT
		COALESCE(NULLIF(effective_model_id, ''), requested_model_id),
		billing_basis,
		CASE WHEN session_id = ? THEN 0 ELSE 1 END,
		SUM(total_cost_usd),
		SUM(llm_call_count),
		SUM(CASE WHEN json_extract(operation_metadata_json, '$.prompt_tokens_include_cache') = 1
			THEN prompt_tokens ELSE prompt_tokens + cache_read_tokens + cache_write_tokens END),
		SUM(cache_read_tokens)
		FROM cost_events
		WHERE session_id = ? OR parent_session_id = ?
		GROUP BY 1, 2, 3`, sessionID, sessionID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("costledger: session cost: %w", err)
	}
	defer rows.Close()
	out := &SessionCost{}
	perModel := map[string]float64{}
	var input, cached int64
	var actual, estimated bool
	for rows.Next() {
		var model, basis string
		var helper int
		var usd float64
		var calls, in, cache int64
		if err := rows.Scan(&model, &basis, &helper, &usd, &calls, &in, &cache); err != nil {
			return nil, fmt.Errorf("costledger: session cost: %w", err)
		}
		input += in
		cached += cache
		if basis == "unpriced" {
			out.UnpricedCalls += int(calls)
			continue
		}
		out.TotalUSD += usd
		perModel[model] += usd
		if helper == 1 {
			out.HelperUSD += usd
		}
		if basis == "provider_actual" {
			actual = true
		} else {
			estimated = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("costledger: session cost: %w", err)
	}
	for model, usd := range perModel {
		out.Models = append(out.Models, SessionModelCost{Model: model, USD: usd})
	}
	sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].USD > out.Models[j].USD })
	switch {
	case actual && estimated:
		out.Basis = "mixed"
	case estimated:
		out.Basis = "estimate"
	default:
		out.Basis = "actual"
	}
	if input > 0 {
		out.CachePercent = int(float64(cached) / float64(input) * 100)
	}
	return out, nil
}
