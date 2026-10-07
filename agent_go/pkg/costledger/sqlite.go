package costledger

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// scopeUnknown is the last-resort scope for an entry whose writer did not name
// one. It matches the column default and pkg/costobserver.ScopeUnknown.
const scopeUnknown = "unknown"

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS cost_events (
    event_id TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    occurred_at TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    workflow_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    run_id TEXT NOT NULL DEFAULT '',
    execution_id TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL DEFAULT 'unknown',
    source_platform TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL DEFAULT '',
    agent_mode TEXT NOT NULL DEFAULT '',
    component TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    requested_provider TEXT NOT NULL DEFAULT '',
    requested_model_id TEXT NOT NULL DEFAULT '',
    account_id TEXT NOT NULL DEFAULT '',
    effective_provider TEXT NOT NULL DEFAULT '',
    effective_model_id TEXT NOT NULL DEFAULT '',
    turn_count INTEGER NOT NULL DEFAULT 0,
    llm_call_count INTEGER NOT NULL DEFAULT 0,
	llm_generation_duration_ms INTEGER NOT NULL DEFAULT 0,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    total_cost_usd REAL NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    billing_basis TEXT NOT NULL DEFAULT 'unpriced',
    pricing_source TEXT NOT NULL DEFAULT '',
    pricing_version TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    operation_metadata_json TEXT NOT NULL DEFAULT '{}',
    source_id TEXT NOT NULL DEFAULT '',
    source_label TEXT NOT NULL DEFAULT '',
    source_run_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_cost_events_occurred_at ON cost_events(occurred_at);
CREATE INDEX IF NOT EXISTS idx_cost_events_user_time ON cost_events(user_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_cost_events_effective_model ON cost_events(effective_model_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_cost_events_workflow_scope ON cost_events(workflow_id, scope, occurred_at);
CREATE TABLE IF NOT EXISTS cost_event_quarantine (
    source_hash TEXT PRIMARY KEY,
    source_path TEXT NOT NULL,
    line_number INTEGER NOT NULL,
    raw_record TEXT NOT NULL,
    parse_error TEXT NOT NULL,
    quarantined_at TEXT NOT NULL
);`

type sqliteLedger struct {
	db *sql.DB
}

// MigrationReport describes one idempotent legacy JSONL import.
type MigrationReport struct {
	Imported    int `json:"imported"`
	Duplicates  int `json:"duplicates"`
	Quarantined int `json:"quarantined"`
}

// NewSQLiteLedger opens the authoritative local cost event database.
func NewSQLiteLedger(dbPath string) (*Ledger, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil, fmt.Errorf("costledger: SQLite path is required")
	}
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("costledger: resolve SQLite path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("costledger: create SQLite directory: %w", err)
	}
	u := &url.URL{Scheme: "file", Path: absPath}
	dsn := u.String() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("costledger: open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: ping SQLite database: %w", err)
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: initialize SQLite schema: %w", err)
	}
	if err := ensureCostEventColumn(db, "llm_generation_duration_ms", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: migrate duration column: %w", err)
	}
	if err := ensureCostEventColumn(db, "phase", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: migrate phase column: %w", err)
	}
	if err := ensureCostEventColumn(db, "source_platform", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: migrate source_platform column: %w", err)
	}
	if err := ensureCostEventColumn(db, "account_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: migrate account_id column: %w", err)
	}
	if err := ensureCostEventColumn(db, "billing_user_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: migrate billing_user_id column: %w", err)
	}
	for _, column := range []string{"source_id", "source_label", "source_run_id"} {
		if err := ensureCostEventColumn(db, column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			db.Close()
			return nil, fmt.Errorf("costledger: migrate %s column: %w", column, err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_cost_events_billing_user_time ON cost_events(billing_user_id, occurred_at)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("costledger: index billing_user_id: %w", err)
	}
	return &Ledger{db: &sqliteLedger{db: db}}, nil
}

// ensureCostEventColumn keeps existing local ledgers forward-compatible. The
// cost ledger is intentionally long-lived, so CREATE TABLE IF NOT EXISTS alone
// cannot add a field to databases created by an older server.
func ensureCostEventColumn(db *sql.DB, column, definition string) error {
	var found int
	err := db.QueryRow(`SELECT 1 FROM pragma_table_info('cost_events') WHERE name = ?`, column).Scan(&found)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("inspect cost_events schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("ALTER TABLE cost_events ADD COLUMN %s %s", column, definition)); err != nil {
		return fmt.Errorf("add %s: %w", column, err)
	}
	return nil
}

func (s *sqliteLedger) append(e Entry) error {
	// normalizeEntry backfills a blank scope with "unknown" so the NOT NULL
	// column and the legacy import both stay valid. For a live write that
	// backfill is a defect being swallowed, so name the writer before it
	// happens — an unattributable row is nearly worthless once it lands.
	if strings.TrimSpace(e.Scope) == "" {
		log.Printf("[COST_LEDGER] cost event named no scope (component=%q tool=%q session=%q execution=%q); recording as %q",
			e.Component, e.ToolName, e.SessionID, e.ExecutionID, scopeUnknown)
	}
	normalizeEntry(&e)
	metadata, err := json.Marshal(e.OperationMetadata)
	if err != nil {
		return fmt.Errorf("costledger: marshal operation metadata: %w", err)
	}
	const insertEvent = `
INSERT OR IGNORE INTO cost_events (
    event_id, idempotency_key, occurred_at, user_id, workflow_id, session_id,
    run_id, execution_id, scope, source_platform, phase, agent_mode, component, correlation_id,
    requested_provider, requested_model_id, effective_provider, effective_model_id,
    turn_count, llm_call_count, llm_generation_duration_ms, prompt_tokens, completion_tokens, reasoning_tokens,
    cache_read_tokens, cache_write_tokens, total_cost_usd, currency, billing_basis,
    pricing_source, pricing_version, tool_name, operation_metadata_json, account_id, billing_user_id,
    source_id, source_label, source_run_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []interface{}{
		e.EventID, e.IdempotencyKey, e.Timestamp.UTC().Format(time.RFC3339Nano),
		e.UserID, e.WorkflowID, e.SessionID, e.RunID, e.ExecutionID, e.Scope, e.SourcePlatform, e.Phase,
		e.AgentMode, e.Component, e.CorrelationID, e.Provider, e.ModelID,
		e.EffectiveProvider, e.EffectiveModelID, e.TurnCount, e.LLMCallCount, e.LLMGenerationDurationMS,
		e.PromptTokens, e.CompletionTokens, e.ReasoningTokens, e.CacheReadTokens,
		e.CacheWriteTokens, e.TotalCostUSD, e.Currency, e.BillingBasis,
		e.PricingSource, e.PricingVersion, e.ToolName, string(metadata), e.AccountID, e.BillingUserID,
		e.SourceID, e.SourceLabel, e.SourceRunID,
	}
	for attempt := 0; ; attempt++ {
		_, err = s.db.Exec(insertEvent, args...)
		if err == nil {
			return nil
		}
		if attempt >= 2 || !isSQLiteBusy(err) {
			return fmt.Errorf("costledger: insert SQLite event: %w", err)
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
}

func (s *sqliteLedger) summarize(from, to, executionID, workflowID string) (*Summary, error) {
	fromInclusive, toExclusive, err := costDateBounds(from, to)
	if err != nil {
		return nil, err
	}
	return s.summarizeWindow(fromInclusive, toExclusive, executionID, workflowID, "")
}

func (s *sqliteLedger) summarizeWorkflowOverview(from, to, workflowID string) (*Summary, bool, error) {
	recent, err := s.summarize(from, to, "", workflowID)
	if err != nil {
		return nil, false, err
	}
	allTime, err := s.summarizeWorkflowTotals(workflowID)
	if err != nil {
		return nil, false, err
	}
	compactWorkflowOverview(recent, allTime)

	fromInclusive, _, err := costDateBounds(from, "")
	if err != nil {
		return nil, false, err
	}
	hasMore := false
	if fromInclusive != "" {
		filter, args := workflowCostSQL(workflowID)
		args = append(args, fromInclusive)
		err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM cost_events WHERE `+filter+` AND occurred_at < ? LIMIT 1)`, args...).Scan(&hasMore)
		if err != nil {
			return nil, false, fmt.Errorf("costledger: check older workflow events: %w", err)
		}
	}
	return recent, hasMore, nil
}

func (s *sqliteLedger) summarizeWorkflowTotals(workflowID string) (*Summary, error) {
	summary := &Summary{
		ByDate:           make(map[string]*DateAggregate),
		ByModel:          make(map[string]*Aggregate),
		ByScope:          make(map[string]*ScopeAggregate),
		BySourcePlatform: make(map[string]*Aggregate),
		Coverage:         Coverage{Source: "sqlite"},
	}
	filter, args := workflowCostSQL(workflowID)
	query := `
SELECT scope,
       COALESCE(SUM(prompt_tokens + CASE
         WHEN json_type(operation_metadata_json, '$.prompt_tokens_include_cache') = 'true' THEN 0
         WHEN json_type(operation_metadata_json, '$.prompt_tokens_include_cache') = 'false' THEN cache_read_tokens + cache_write_tokens
         WHEN LOWER(TRIM(COALESCE(NULLIF(TRIM(effective_provider), ''), requested_provider))) IN ('muse-cli', 'muse_cli') THEN 0
         ELSE cache_read_tokens + cache_write_tokens END), 0),
       COALESCE(SUM(CASE WHEN llm_call_count > 0 AND billing_basis = 'unpriced'
         AND prompt_tokens = 0 AND completion_tokens = 0 AND reasoning_tokens = 0
         AND cache_read_tokens = 0 AND cache_write_tokens = 0 AND total_cost_usd = 0
         THEN llm_call_count ELSE 0 END), 0),
       COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0),
       COALESCE(SUM(reasoning_tokens), 0), COALESCE(SUM(cache_read_tokens), 0),
       COALESCE(SUM(cache_write_tokens), 0), COALESCE(SUM(total_cost_usd), 0),
       COALESCE(SUM(llm_call_count), 0), COALESCE(SUM(llm_generation_duration_ms), 0), COUNT(*),
       COALESCE(SUM(CASE WHEN llm_call_count > 0 AND billing_basis = 'unpriced' THEN llm_call_count ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'provider_actual' THEN total_cost_usd ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'token_estimate' THEN total_cost_usd ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'subscription_shadow' THEN total_cost_usd ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'unpriced' THEN prompt_tokens ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'unpriced' THEN completion_tokens ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'unpriced' THEN reasoning_tokens ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'unpriced' THEN cache_read_tokens ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN billing_basis = 'unpriced' THEN cache_write_tokens ELSE 0 END), 0)
FROM cost_events
WHERE ` + filter + `
GROUP BY scope`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("costledger: summarize workflow totals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		var aggregate Aggregate
		if err := rows.Scan(
			&scope, &aggregate.InputTokens, &aggregate.MissingUsageCallCount,
			&aggregate.PromptTokens, &aggregate.CompletionTokens,
			&aggregate.ReasoningTokens, &aggregate.CacheReadTokens,
			&aggregate.CacheWriteTokens, &aggregate.TotalCostUSD,
			&aggregate.CallCount, &aggregate.LLMGenerationDurationMS, &aggregate.AccountingEventCount,
			&aggregate.UnpricedCallCount,
			&aggregate.ProviderActualCostUSD, &aggregate.TokenEstimateCostUSD, &aggregate.SubscriptionShadowUSD,
			&aggregate.UnpricedPromptTokens, &aggregate.UnpricedCompletionTokens,
			&aggregate.UnpricedReasoningTokens, &aggregate.UnpricedCacheReadTokens,
			&aggregate.UnpricedCacheWriteTokens,
		); err != nil {
			return nil, fmt.Errorf("costledger: scan workflow totals: %w", err)
		}
		summary.ByScope[scope] = &ScopeAggregate{Aggregate: aggregate}
		mergeAggregate(&summary.Total, aggregate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("costledger: iterate workflow totals: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cost_event_quarantine`).Scan(&summary.Coverage.QuarantinedEventCount); err != nil {
		return nil, fmt.Errorf("costledger: count quarantined events: %w", err)
	}
	return summary, nil
}

func mergeAggregate(target *Aggregate, source Aggregate) {
	target.InputTokens += source.InputTokens
	target.PromptTokens += source.PromptTokens
	target.CompletionTokens += source.CompletionTokens
	target.ReasoningTokens += source.ReasoningTokens
	target.CacheReadTokens += source.CacheReadTokens
	target.CacheWriteTokens += source.CacheWriteTokens
	target.TotalCostUSD += source.TotalCostUSD
	target.CallCount += source.CallCount
	target.LLMGenerationDurationMS += source.LLMGenerationDurationMS
	target.AccountingEventCount += source.AccountingEventCount
	target.UnpricedCallCount += source.UnpricedCallCount
	target.MissingUsageCallCount += source.MissingUsageCallCount
	target.ProviderActualCostUSD += source.ProviderActualCostUSD
	target.TokenEstimateCostUSD += source.TokenEstimateCostUSD
	target.SubscriptionShadowUSD += source.SubscriptionShadowUSD
	target.UnpricedPromptTokens += source.UnpricedPromptTokens
	target.UnpricedCompletionTokens += source.UnpricedCompletionTokens
	target.UnpricedReasoningTokens += source.UnpricedReasoningTokens
	target.UnpricedCacheReadTokens += source.UnpricedCacheReadTokens
	target.UnpricedCacheWriteTokens += source.UnpricedCacheWriteTokens
}

func (s *sqliteLedger) summarizeWindow(fromInclusive, toExclusive, executionID, workflowID, scope string) (*Summary, error) {
	summary := &Summary{
		From: fromInclusive, To: toExclusive,
		ByDate: make(map[string]*DateAggregate), ByModel: make(map[string]*Aggregate),
		ByScope:          make(map[string]*ScopeAggregate),
		BySourcePlatform: make(map[string]*Aggregate),
		Coverage:         Coverage{Source: "sqlite"},
	}
	query := `
SELECT event_id, idempotency_key, occurred_at, user_id, workflow_id, session_id,
       run_id, execution_id, scope, source_platform, phase, agent_mode, component, correlation_id,
       requested_provider, requested_model_id, effective_provider, effective_model_id,
       turn_count, llm_call_count, llm_generation_duration_ms, prompt_tokens, completion_tokens, reasoning_tokens,
       cache_read_tokens, cache_write_tokens, total_cost_usd, currency, billing_basis,
       pricing_source, pricing_version, tool_name, operation_metadata_json, account_id, billing_user_id,
       source_id, source_label, source_run_id
FROM cost_events`
	where := make([]string, 0, 4)
	args := make([]interface{}, 0, 4)
	if fromInclusive != "" {
		where = append(where, "occurred_at >= ?")
		args = append(args, fromInclusive)
	}
	if toExclusive != "" {
		where = append(where, "occurred_at < ?")
		args = append(args, toExclusive)
	}
	if executionID = strings.TrimSpace(executionID); executionID != "" {
		where = append(where, "execution_id = ?")
		args = append(args, executionID)
	}
	if workflowID = strings.TrimSpace(workflowID); workflowID != "" {
		filter, workflowArgs := workflowCostSQL(workflowID)
		where = append(where, filter)
		args = append(args, workflowArgs...)
	}
	if scope = strings.TrimSpace(scope); scope != "" {
		where = append(where, "scope = ?")
		args = append(args, scope)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY occurred_at"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("costledger: query SQLite events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var occurredAt, metadataJSON string
		if err := rows.Scan(
			&e.EventID, &e.IdempotencyKey, &occurredAt, &e.UserID, &e.WorkflowID,
			&e.SessionID, &e.RunID, &e.ExecutionID, &e.Scope, &e.SourcePlatform, &e.Phase, &e.AgentMode,
			&e.Component, &e.CorrelationID, &e.Provider, &e.ModelID,
			&e.EffectiveProvider, &e.EffectiveModelID, &e.TurnCount, &e.LLMCallCount, &e.LLMGenerationDurationMS,
			&e.PromptTokens, &e.CompletionTokens, &e.ReasoningTokens,
			&e.CacheReadTokens, &e.CacheWriteTokens, &e.TotalCostUSD, &e.Currency,
			&e.BillingBasis, &e.PricingSource, &e.PricingVersion, &e.ToolName,
			&metadataJSON, &e.AccountID, &e.BillingUserID,
			&e.SourceID, &e.SourceLabel, &e.SourceRunID,
		); err != nil {
			return nil, fmt.Errorf("costledger: scan SQLite event: %w", err)
		}
		ts, err := time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return nil, fmt.Errorf("costledger: parse stored timestamp %q: %w", occurredAt, err)
		}
		e.Timestamp = ts
		if err := json.Unmarshal([]byte(metadataJSON), &e.OperationMetadata); err != nil {
			return nil, fmt.Errorf("costledger: decode stored operation metadata for %q: %w", e.EventID, err)
		}
		date := ts.UTC().Format("2006-01-02")
		addEntryToSummary(summary, date, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("costledger: iterate SQLite events: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cost_event_quarantine`).Scan(&summary.Coverage.QuarantinedEventCount); err != nil {
		return nil, fmt.Errorf("costledger: count quarantined events: %w", err)
	}
	return summary, nil
}

func costDateBounds(from, to string) (string, string, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	parse := func(name, value string) (time.Time, error) {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return time.Time{}, fmt.Errorf("costledger: invalid %s date %q (expected YYYY-MM-DD): %w", name, value, err)
		}
		return parsed.UTC(), nil
	}
	fromInclusive := ""
	if from != "" {
		parsed, err := parse("from", from)
		if err != nil {
			return "", "", err
		}
		fromInclusive = parsed.Format(time.RFC3339Nano)
	}
	toExclusive := ""
	if to != "" {
		parsed, err := parse("to", to)
		if err != nil {
			return "", "", err
		}
		toExclusive = parsed.AddDate(0, 0, 1).Format(time.RFC3339Nano)
	}
	if fromInclusive != "" && toExclusive != "" && fromInclusive >= toExclusive {
		return "", "", fmt.Errorf("costledger: from date %q must not be after to date %q", from, to)
	}
	return fromInclusive, toExclusive, nil
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked")
}

func (s *sqliteLedger) migrateLegacyJSONL(path string) (MigrationReport, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return MigrationReport{}, nil
	}
	if err != nil {
		return MigrationReport{}, fmt.Errorf("costledger: open legacy JSONL: %w", err)
	}
	defer file.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return MigrationReport{}, fmt.Errorf("costledger: begin legacy migration: %w", err)
	}
	defer tx.Rollback()
	report := MigrationReport{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		raw := append([]byte(nil), scanner.Bytes()...)
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			result, qErr := tx.Exec(`INSERT OR IGNORE INTO cost_event_quarantine
                (source_hash, source_path, line_number, raw_record, parse_error, quarantined_at)
                VALUES (?, ?, ?, ?, ?, ?)`, hash, path, lineNumber, string(raw), err.Error(), time.Now().UTC().Format(time.RFC3339Nano))
			if qErr != nil {
				return MigrationReport{}, fmt.Errorf("costledger: quarantine malformed row %d: %w", lineNumber, qErr)
			}
			if affected, _ := result.RowsAffected(); affected > 0 {
				report.Quarantined++
			}
			continue
		}
		e.EventID = "legacy-" + hash
		e.IdempotencyKey = "legacy-jsonl:" + hash
		normalizeEntry(&e)
		metadata, err := json.Marshal(e.OperationMetadata)
		if err != nil {
			return MigrationReport{}, fmt.Errorf("costledger: marshal legacy metadata row %d: %w", lineNumber, err)
		}
		result, err := tx.Exec(`
INSERT OR IGNORE INTO cost_events (
    event_id, idempotency_key, occurred_at, user_id, workflow_id, session_id,
    run_id, execution_id, scope, source_platform, phase, agent_mode, component, correlation_id,
    requested_provider, requested_model_id, effective_provider, effective_model_id,
    turn_count, llm_call_count, llm_generation_duration_ms, prompt_tokens, completion_tokens, reasoning_tokens,
    cache_read_tokens, cache_write_tokens, total_cost_usd, currency, billing_basis,
    pricing_source, pricing_version, tool_name, operation_metadata_json, account_id, billing_user_id,
    source_id, source_label, source_run_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.EventID, e.IdempotencyKey, e.Timestamp.UTC().Format(time.RFC3339Nano),
			e.UserID, e.WorkflowID, e.SessionID, e.RunID, e.ExecutionID, e.Scope, e.SourcePlatform, e.Phase,
			e.AgentMode, e.Component, e.CorrelationID, e.Provider, e.ModelID,
			e.EffectiveProvider, e.EffectiveModelID, e.TurnCount, e.LLMCallCount, e.LLMGenerationDurationMS,
			e.PromptTokens, e.CompletionTokens, e.ReasoningTokens, e.CacheReadTokens,
			e.CacheWriteTokens, e.TotalCostUSD, e.Currency, e.BillingBasis,
			e.PricingSource, e.PricingVersion, e.ToolName, string(metadata), e.AccountID, e.BillingUserID,
			e.SourceID, e.SourceLabel, e.SourceRunID,
		)
		if err != nil {
			return MigrationReport{}, fmt.Errorf("costledger: migrate legacy row %d: %w", lineNumber, err)
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			report.Imported++
		} else {
			report.Duplicates++
		}
	}
	if err := scanner.Err(); err != nil {
		return MigrationReport{}, fmt.Errorf("costledger: scan legacy JSONL: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MigrationReport{}, fmt.Errorf("costledger: commit legacy migration: %w", err)
	}
	return report, nil
}

// accountTokens sums one person's tokens per account, on accounts whose ID
// starts with accountPrefix, since weekStart and the part since dayStart. An
// entry is the person's when it is billed to them (billing_user_id), or when
// it names no billing user and they ran it (user_id). Rows written before
// billing_user_id existed count to user_id, as they always did.
func (s *sqliteLedger) accountTokens(userID, accountPrefix string, dayStart, weekStart time.Time) (map[string]AccountTokenUsage, error) {
	usage := map[string]AccountTokenUsage{}
	rows, err := s.db.Query(`
SELECT account_id, occurred_at, prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens,
       requested_provider, effective_provider, operation_metadata_json, billing_user_id != ''
FROM cost_events
WHERE (billing_user_id = ? OR (billing_user_id = '' AND user_id = ?)) AND substr(account_id, 1, ?) = ? AND occurred_at >= ?`,
		// No zone suffix: a prefix of every stored RFC3339Nano time in that
		// second, so an event at 00:00:00.5Z is not sorted before the bound.
		userID, userID, len(accountPrefix), accountPrefix, weekStart.UTC().Format("2006-01-02T15:04:05"))
	if err != nil {
		return usage, fmt.Errorf("costledger: query account tokens: %w", err)
	}
	defer rows.Close()
	day := dayStart.UTC()
	for rows.Next() {
		var e Entry
		var accountID, occurredAt, metadataJSON string
		var viaBot bool
		if err := rows.Scan(&accountID, &occurredAt, &e.PromptTokens, &e.CompletionTokens, &e.CacheReadTokens, &e.CacheWriteTokens,
			&e.Provider, &e.EffectiveProvider, &metadataJSON, &viaBot); err != nil {
			return usage, fmt.Errorf("costledger: scan account tokens: %w", err)
		}
		if strings.Contains(metadataJSON, "prompt_tokens_include_cache") {
			_ = json.Unmarshal([]byte(metadataJSON), &e.OperationMetadata)
		}
		tokens := int64(entryInputTokens(e) + e.CompletionTokens)
		account := usage[accountID]
		account.Week += tokens
		if viaBot {
			account.ViaBotWeek += tokens
		}
		if at, err := time.Parse(time.RFC3339Nano, occurredAt); err == nil && !at.Before(day) {
			account.Day += tokens
			if viaBot {
				account.ViaBotDay += tokens
			}
		}
		usage[accountID] = account
	}
	return usage, rows.Err()
}

func (s *sqliteLedger) close() error {
	return s.db.Close()
}

func normalizeEntry(e *Entry) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	} else {
		e.Timestamp = e.Timestamp.UTC()
	}
	if e.Currency == "" {
		e.Currency = "USD"
	}
	if e.Scope == "" {
		e.Scope = scopeUnknown
	}
	if e.BillingUserID = strings.TrimSpace(e.BillingUserID); e.BillingUserID == strings.TrimSpace(e.UserID) {
		e.BillingUserID = ""
	}
	e.SourcePlatform = normalizeSourcePlatform(e.SourcePlatform)
	if e.SourcePlatform == "" {
		e.SourcePlatform = sourcePlatformFromSessionID(e.SessionID)
	}
	if e.BillingBasis == "" {
		switch e.CostUSDSource {
		case "provider":
			e.BillingBasis = "provider_actual"
		case "estimated":
			e.BillingBasis = "token_estimate"
		default:
			if e.TotalCostUSD > 0 {
				e.BillingBasis = "token_estimate"
			} else {
				e.BillingBasis = "unpriced"
			}
		}
	}
	if e.LLMCallCount == 0 && !strings.HasPrefix(e.Component, "tool:") &&
		(e.Provider != "" || e.ModelID != "" || e.PromptTokens > 0 || e.CompletionTokens > 0) {
		e.LLMCallCount = 1
	}
	if e.IdempotencyKey == "" {
		copy := *e
		copy.EventID = ""
		copy.IdempotencyKey = ""
		data, _ := json.Marshal(copy)
		e.IdempotencyKey = fmt.Sprintf("event:%x", sha256.Sum256(data))
	}
	if e.EventID == "" {
		e.EventID = e.IdempotencyKey
	}
}

func normalizeSourcePlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	switch platform {
	case "slack", "whatsapp":
		return platform
	default:
		return platform
	}
}

func sourcePlatformFromSessionID(sessionID string) string {
	rest := strings.TrimPrefix(strings.TrimSpace(sessionID), "bot-")
	if rest == sessionID {
		return ""
	}
	platform, _, ok := strings.Cut(rest, "--")
	if !ok {
		return ""
	}
	return normalizeSourcePlatform(platform)
}

func (s *sqliteLedger) repriceUnpriced(estimate UnpricedEstimator) (int, error) {
	rows, err := s.db.Query(`SELECT event_id, requested_provider, requested_model_id, effective_provider, effective_model_id,
       prompt_tokens, completion_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens
FROM cost_events WHERE billing_basis = 'unpriced' AND llm_call_count > 0`)
	if err != nil {
		return 0, fmt.Errorf("costledger: list unpriced calls: %w", err)
	}
	type priced struct {
		id     string
		cost   float64
		source string
	}
	var updates []priced
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.EventID, &e.Provider, &e.ModelID, &e.EffectiveProvider, &e.EffectiveModelID,
			&e.PromptTokens, &e.CompletionTokens, &e.ReasoningTokens, &e.CacheReadTokens, &e.CacheWriteTokens); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("costledger: scan unpriced call: %w", err)
		}
		if cost, source := estimate(e); cost > 0 {
			updates = append(updates, priced{e.EventID, cost, source})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("costledger: iterate unpriced calls: %w", err)
	}
	_ = rows.Close()
	if len(updates) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("costledger: begin reprice: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	count := 0
	for _, u := range updates {
		res, err := tx.Exec(`UPDATE cost_events SET total_cost_usd = ?, billing_basis = 'subscription_shadow', pricing_source = ?
WHERE event_id = ? AND billing_basis = 'unpriced'`, u.cost, u.source, u.id)
		if err != nil {
			return 0, fmt.Errorf("costledger: reprice call: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			count++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("costledger: commit reprice: %w", err)
	}
	return count, nil
}
