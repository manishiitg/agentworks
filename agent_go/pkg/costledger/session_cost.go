package costledger

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SessionTokens counts a chat's tokens by kind. NewInput excludes cached input,
// which is counted once in CacheRead or CacheWrite.
type SessionTokens struct {
	NewInput   int64 `json:"new_input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Thinking   int64 `json:"thinking"`
}

func (t *SessionTokens) add(o SessionTokens) {
	t.NewInput += o.NewInput
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Output += o.Output
	t.Thinking += o.Thinking
}

// SessionModelCost is one model's share of a chat's spend in one period.
type SessionModelCost struct {
	Model string `json:"model"`
	// Provider is the app or API that ran the model (e.g. claude-code, codex-cli).
	Provider string `json:"provider"`
	// Name is the model's display name and App the provider's, both filled by the caller.
	Name   string        `json:"name,omitempty"`
	App    string        `json:"app,omitempty"`
	USD    float64       `json:"usd"`
	Calls  int           `json:"calls"`
	Tokens SessionTokens `json:"tokens"`
	// CacheSavedUSD is filled by callers that know the model's list prices.
	CacheSavedUSD float64 `json:"cache_saved_usd,omitempty"`
}

// SessionCostPeriod is a chat's spend while it ran on one app. A chat that
// moved apps (say from Codex to Claude Code) keeps one session, so its spend is
// split at the move: what it costs now is not mixed with what it cost before.
type SessionCostPeriod struct {
	// Providers lists the apps in this period, most expensive first.
	Providers []string `json:"providers"`
	// Apps are the providers' display names, filled by the caller.
	Apps      []string           `json:"apps,omitempty"`
	From      time.Time          `json:"from"`
	To        time.Time          `json:"to"`
	TotalUSD  float64            `json:"total_usd"`
	HelperUSD float64            `json:"helper_usd"`
	Calls     int                `json:"calls"`
	Tokens    SessionTokens      `json:"tokens"`
	Models    []SessionModelCost `json:"models"`
	// CachePercent is the share of input tokens served from cache.
	CachePercent int `json:"cache_percent"`
	// CacheSavedUSD is filled by callers that know list prices; 0 when unknown.
	CacheSavedUSD float64 `json:"cache_saved_usd,omitempty"`
	// UnpricedCalls counts this period's calls without a price.
	UnpricedCalls int `json:"unpriced_calls"`
}

// SessionCost is what one chat has cost so far, including the helper agents
// it started (events whose parent session is this chat).
type SessionCost struct {
	// TotalUSD covers the whole chat, every period.
	TotalUSD float64 `json:"total_usd"`
	// Current is the spend since the chat last moved to another app (or since
	// it started); Earlier is everything before that move, absent when none.
	Current SessionCostPeriod  `json:"current"`
	Earlier *SessionCostPeriod `json:"earlier,omitempty"`
	// Basis is how real TotalUSD is: "actual" (provider-reported), "estimate"
	// (subscription equivalent or token estimate) or "mixed". Calls without a
	// price are counted in UnpricedCalls and add nothing to TotalUSD.
	Basis         string `json:"basis"`
	UnpricedCalls int    `json:"unpriced_calls"`
	// TodayUSD is the priced spend since local midnight, helpers included.
	TodayUSD float64 `json:"today_usd"`
}

const sessionProviderSQL = `COALESCE(NULLIF(effective_provider, ''), requested_provider)`

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
	split, err := sessionSwitchPoint(store.db, sessionID)
	if err != nil {
		return nil, err
	}
	rows, err := store.db.Query(`SELECT
		CASE WHEN ? < 0 OR julianday(occurred_at) > ? THEN 0 ELSE 1 END,
		`+sessionProviderSQL+`,
		COALESCE(NULLIF(effective_model_id, ''), requested_model_id),
		billing_basis,
		CASE WHEN session_id = ? THEN 0 ELSE 1 END,
		SUM(total_cost_usd),
		SUM(llm_call_count),
		SUM(MAX(0, CASE WHEN json_extract(operation_metadata_json, '$.prompt_tokens_include_cache') = 1
			THEN prompt_tokens - cache_read_tokens - cache_write_tokens ELSE prompt_tokens END)),
		SUM(cache_read_tokens),
		SUM(cache_write_tokens),
		SUM(completion_tokens),
		SUM(reasoning_tokens),
		MIN(occurred_at),
		MAX(occurred_at)
		FROM cost_events
		WHERE session_id = ? OR parent_session_id = ?
		GROUP BY 1, 2, 3, 4, 5`, split, split, sessionID, sessionID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("costledger: session cost: %w", err)
	}
	defer rows.Close()
	out := &SessionCost{}
	periods := [2]*SessionCostPeriod{{}, {}}
	type modelKey struct{ provider, model string }
	models := [2]map[modelKey]*SessionModelCost{{}, {}}
	providerUSD := [2]map[string]float64{{}, {}}
	var actual, estimated bool
	for rows.Next() {
		var period, helper int
		var provider, model, basis, first, last string
		var usd float64
		var calls int
		var tokens SessionTokens
		if err := rows.Scan(&period, &provider, &model, &basis, &helper, &usd, &calls,
			&tokens.NewInput, &tokens.CacheRead, &tokens.CacheWrite, &tokens.Output, &tokens.Thinking, &first, &last); err != nil {
			return nil, fmt.Errorf("costledger: session cost: %w", err)
		}
		p := periods[period]
		p.Tokens.add(tokens)
		p.Calls += calls
		widenPeriod(p, first, last)
		key := modelKey{provider, model}
		m := models[period][key]
		if m == nil {
			m = &SessionModelCost{Model: model, Provider: provider}
			models[period][key] = m
		}
		m.Calls += calls
		m.Tokens.add(tokens)
		if basis == "unpriced" {
			// The app still counts as used in this period even before it has a price.
			providerUSD[period][provider] += 0
			out.UnpricedCalls += calls
			p.UnpricedCalls += calls
			continue
		}
		m.USD += usd
		p.TotalUSD += usd
		out.TotalUSD += usd
		providerUSD[period][provider] += usd
		if helper == 1 {
			p.HelperUSD += usd
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
	now := time.Now().In(DayLocation())
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UTC().Format(time.RFC3339Nano)
	var today sql.NullFloat64
	if err := store.db.QueryRow(`SELECT SUM(total_cost_usd) FROM cost_events
		WHERE (session_id = ? OR parent_session_id = ?) AND billing_basis != 'unpriced'
		AND julianday(occurred_at) >= julianday(?)`, sessionID, sessionID, midnight).Scan(&today); err != nil {
		return nil, fmt.Errorf("costledger: session cost: %w", err)
	}
	out.TodayUSD = today.Float64
	for i, p := range periods {
		// Always lists, never null: the Context panel reads them directly.
		p.Providers, p.Models = []string{}, []SessionModelCost{}
		for _, m := range models[i] {
			p.Models = append(p.Models, *m)
		}
		sort.Slice(p.Models, func(a, b int) bool { return p.Models[a].USD > p.Models[b].USD })
		for provider := range providerUSD[i] {
			p.Providers = append(p.Providers, provider)
		}
		usd := providerUSD[i]
		sort.Slice(p.Providers, func(a, b int) bool { return usd[p.Providers[a]] > usd[p.Providers[b]] })
		if input := p.Tokens.NewInput + p.Tokens.CacheRead + p.Tokens.CacheWrite; input > 0 {
			p.CachePercent = int(float64(p.Tokens.CacheRead) / float64(input) * 100)
		}
	}
	out.Current = *periods[0]
	if periods[1].Calls > 0 {
		out.Earlier = periods[1]
	}
	switch {
	case actual && estimated:
		out.Basis = "mixed"
	case estimated:
		out.Basis = "estimate"
	default:
		out.Basis = "actual"
	}
	return out, nil
}

// sessionSwitchPoint returns the julian day of the chat's last own call on an
// app other than the one it uses now, or -1 when it never moved. Helper agents
// do not count: they may run elsewhere without the chat having moved.
func sessionSwitchPoint(db *sql.DB, sessionID string) (float64, error) {
	var current sql.NullString
	err := db.QueryRow(`SELECT `+sessionProviderSQL+` FROM cost_events WHERE session_id = ?
		ORDER BY julianday(occurred_at) DESC LIMIT 1`, sessionID).Scan(&current)
	if err == sql.ErrNoRows || !current.Valid || current.String == "" {
		return -1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("costledger: session cost: %w", err)
	}
	var last sql.NullFloat64
	if err := db.QueryRow(`SELECT MAX(julianday(occurred_at)) FROM cost_events WHERE session_id = ?
		AND `+sessionProviderSQL+` NOT IN ('', ?)`, sessionID, current.String).Scan(&last); err != nil {
		return 0, fmt.Errorf("costledger: session cost: %w", err)
	}
	if !last.Valid {
		return -1, nil
	}
	return last.Float64, nil
}

func widenPeriod(p *SessionCostPeriod, first, last string) {
	if at, err := time.Parse(time.RFC3339Nano, first); err == nil && (p.From.IsZero() || at.Before(p.From)) {
		p.From = at
	}
	if at, err := time.Parse(time.RFC3339Nano, last); err == nil && at.After(p.To) {
		p.To = at
	}
}
