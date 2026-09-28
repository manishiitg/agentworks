// Package costledger persists immutable LLM and paid-tool cost events and
// exposes date/model aggregates. SQLite is the authoritative production store;
// the workspace-API JSONL implementation remains only for migration-era tests.
package costledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const ledgerWorkspacePath = "_system/costs.jsonl"

type workspaceAPIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

// Entry is one LLM call, paid tool charge, or unpriced MCP activity record.
type Entry struct {
	EventID        string    `json:"event_id,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	Timestamp      time.Time `json:"ts"`
	SessionID      string    `json:"session_id,omitempty"`
	UserID         string    `json:"user_id,omitempty"`
	WorkflowID     string    `json:"workflow_id,omitempty"`
	RunID          string    `json:"run_id,omitempty"`
	ExecutionID    string    `json:"execution_id,omitempty"`
	Scope          string    `json:"scope,omitempty"`
	SourcePlatform string    `json:"source_platform,omitempty"`
	// Phase distinguishes what kind of turn produced this entry within one
	// ExecutionID — "execution_only" or "reflection" for a workflow step
	// (PLAT-166), matching the same phase-string vocabulary the older
	// per-step token-usage-file system already uses
	// (pkg/orchestrator/context_aware_bridge.go's attributedPhase). Empty for
	// every non-workflow scope and every entry written before this shipped;
	// addEntryToSummary only builds a ByPhase breakdown when this is set.
	Phase         string `json:"phase,omitempty"`
	AgentMode     string `json:"agent_mode,omitempty"`
	Component     string `json:"component,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Provider      string `json:"provider,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	// EffectiveModelID is the model the CLI/provider ACTUALLY served
	// the turn with — may drift from ModelID when the user picked an
	// alias like "auto" or "cursor-cli", or when a /model swap happened
	// mid-session. Empty when the provider doesn't surface it.
	EffectiveProvider string `json:"effective_provider,omitempty"`
	EffectiveModelID  string `json:"effective_model_id,omitempty"`
	TurnCount         int    `json:"turn_count,omitempty"`
	LLMCallCount      int    `json:"llm_call_count,omitempty"`
	// LLMGenerationDurationMS is time spent waiting for model responses. It is
	// intentionally not an agent wall-clock duration: tool work and queueing
	// are not present on each cost event and must not be implied by this value.
	LLMGenerationDurationMS int64   `json:"llm_generation_duration_ms,omitempty"`
	PromptTokens            int     `json:"prompt_tokens,omitempty"`
	CompletionTokens        int     `json:"completion_tokens,omitempty"`
	ReasoningTokens         int     `json:"reasoning_tokens,omitempty"`
	CacheReadTokens         int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens        int     `json:"cache_write_tokens,omitempty"`
	TotalCostUSD            float64 `json:"total_cost_usd,omitempty"`
	// CostUSDSource flags whether TotalCostUSD came from the provider
	// ("provider", e.g. claude's total_cost_usd) or was computed
	// downstream from tokens × registry rates ("estimated"). For
	// subscription-billed CLIs (Cursor, Codex Pro) "estimated" is a
	// SHADOW cost — what the same workload would cost via the
	// underlying per-token API, NOT the flat-plan bill.
	CostUSDSource     string                 `json:"cost_usd_source,omitempty"`
	Currency          string                 `json:"currency,omitempty"`
	BillingBasis      string                 `json:"billing_basis,omitempty"`
	PricingSource     string                 `json:"pricing_source,omitempty"`
	PricingVersion    string                 `json:"pricing_version,omitempty"`
	ToolName          string                 `json:"tool_name,omitempty"`
	OperationMetadata map[string]interface{} `json:"operation_metadata,omitempty"`
}

// Aggregate is the rolled-up token + cost total for a date/model bucket.
type Aggregate struct {
	Provider                 string  `json:"provider,omitempty"`
	PricingModelID           string  `json:"pricing_model_id,omitempty"`
	PricingVersion           string  `json:"pricing_version,omitempty"`
	PromptTokens             int     `json:"prompt_tokens"`
	CompletionTokens         int     `json:"completion_tokens"`
	ReasoningTokens          int     `json:"reasoning_tokens"`
	CacheReadTokens          int     `json:"cache_read_tokens"`
	CacheWriteTokens         int     `json:"cache_write_tokens"`
	TotalCostUSD             float64 `json:"total_cost_usd"`
	CallCount                int     `json:"call_count"`
	LLMGenerationDurationMS  int64   `json:"llm_generation_duration_ms"`
	AccountingEventCount     int     `json:"accounting_event_count"`
	UnpricedCallCount        int     `json:"unpriced_call_count"`
	ProviderActualCostUSD    float64 `json:"provider_actual_cost_usd"`
	TokenEstimateCostUSD     float64 `json:"token_estimate_cost_usd"`
	SubscriptionShadowUSD    float64 `json:"subscription_shadow_cost_usd"`
	InputCostUSD             float64 `json:"input_cost_usd,omitempty"`
	OutputCostUSD            float64 `json:"output_cost_usd,omitempty"`
	ReasoningCostUSD         float64 `json:"reasoning_cost_usd,omitempty"`
	CacheReadCostUSD         float64 `json:"cache_read_cost_usd,omitempty"`
	CacheWriteCostUSD        float64 `json:"cache_write_cost_usd,omitempty"`
	UnpricedPromptTokens     int     `json:"unpriced_prompt_tokens,omitempty"`
	UnpricedCompletionTokens int     `json:"unpriced_completion_tokens,omitempty"`
	UnpricedReasoningTokens  int     `json:"unpriced_reasoning_tokens,omitempty"`
	UnpricedCacheReadTokens  int     `json:"unpriced_cache_read_tokens,omitempty"`
	UnpricedCacheWriteTokens int     `json:"unpriced_cache_write_tokens,omitempty"`
}

func (a *Aggregate) add(e Entry) {
	provider := e.EffectiveProvider
	if provider == "" {
		provider = e.Provider
	}
	if a.Provider == "" {
		a.Provider = provider
	} else if provider != "" && a.Provider != provider {
		// Mixed-provider totals cannot safely inherit one provider identity. A
		// per-model aggregate normally stays single-provider; the overall total
		// is intentionally left blank when providers differ.
		a.Provider = ""
	}
	a.PromptTokens += e.PromptTokens
	a.CompletionTokens += e.CompletionTokens
	a.ReasoningTokens += e.ReasoningTokens
	a.CacheReadTokens += e.CacheReadTokens
	a.CacheWriteTokens += e.CacheWriteTokens
	a.TotalCostUSD += e.TotalCostUSD
	a.CallCount += e.LLMCallCount
	a.LLMGenerationDurationMS += e.LLMGenerationDurationMS
	a.AccountingEventCount++
	if e.LLMCallCount > 0 && e.BillingBasis == "unpriced" {
		a.UnpricedCallCount += e.LLMCallCount
		a.UnpricedPromptTokens += e.PromptTokens
		a.UnpricedCompletionTokens += e.CompletionTokens
		a.UnpricedReasoningTokens += e.ReasoningTokens
		a.UnpricedCacheReadTokens += e.CacheReadTokens
		a.UnpricedCacheWriteTokens += e.CacheWriteTokens
	}
	switch e.BillingBasis {
	case "provider_actual":
		a.ProviderActualCostUSD += e.TotalCostUSD
	case "subscription_shadow":
		a.SubscriptionShadowUSD += e.TotalCostUSD
	case "token_estimate":
		a.TokenEstimateCostUSD += e.TotalCostUSD
	}
}

// DateAggregate is one row in the per-date rollup. It embeds Aggregate
// so its JSON shape stays flat (existing consumers reading
// `prompt_tokens`/`call_count`/etc. at the date level keep working),
// and adds a per-model breakdown for clients that want to expand the
// row.
type DateAggregate struct {
	Aggregate
	ByModel          map[string]*Aggregate      `json:"by_model,omitempty"`
	ByScope          map[string]*ScopeAggregate `json:"by_scope,omitempty"`
	BySourcePlatform map[string]*Aggregate      `json:"by_source_platform,omitempty"`
	WorkflowRunCount int                        `json:"workflow_run_count,omitempty"`
	workflowRunIDs   map[string]struct{}
}

// ScopeAggregate rolls a scope up while retaining the individual runtime
// executions that produced it. This is the canonical hierarchy used by the
// cost UI: builder/pulse/workflow, then their child agents/steps.
type ScopeAggregate struct {
	Aggregate
	ByExecution map[string]*ExecutionAggregate `json:"by_execution,omitempty"`
}

// ExecutionAggregate rolls one runtime execution's cost up while retaining a
// phase breakdown (PLAT-166): a workflow step's own execution-turn cost vs
// its separate post-completion reflection-turn cost, when the entries making
// up this execution carry a Phase. Aggregate is embedded so the JSON shape
// stays flat for every existing field (`total_cost_usd`,
// `llm_generation_duration_ms`, etc. all read exactly as before); `by_phase`
// is new and omitted entirely for an execution whose entries never set Phase.
type ExecutionAggregate struct {
	Aggregate
	ByPhase map[string]*Aggregate `json:"by_phase,omitempty"`
}

// Summary is the aggregated view returned by Summarize.
type Summary struct {
	From             string                     `json:"from,omitempty"`
	To               string                     `json:"to,omitempty"`
	Total            Aggregate                  `json:"total"`
	ByDate           map[string]*DateAggregate  `json:"by_date"`  // YYYY-MM-DD UTC
	ByModel          map[string]*Aggregate      `json:"by_model"` // model_id
	ByScope          map[string]*ScopeAggregate `json:"by_scope,omitempty"`
	BySourcePlatform map[string]*Aggregate      `json:"by_source_platform,omitempty"`
	// ByWorkflow keys spend by the raw workflow_id it was attributed to
	// (a Workflow/<name> folder, a crew root, a chat folder). Events with no
	// workflow_id land under "". Callers that present it fold subpaths and
	// filter by the viewer's access.
	ByWorkflow map[string]*WorkflowAggregate `json:"by_workflow,omitempty"`
	// ByWorkflowUser preserves actor attribution for views that first enforce
	// workflow access. Keep it out of the general summary response: that route
	// has no per-workflow permission filter.
	ByWorkflowUser map[string]map[string]*UserAggregate `json:"-"`
	// These dimensions are internal until the cost overview has applied the
	// caller's workflow access rules.
	ByWorkflowBot map[string]map[string]*BotAggregate `json:"-"`
	ByWorkflowMCP map[string]map[string]*MCPAggregate `json:"-"`
	Coverage      Coverage                            `json:"coverage"`
}

// UserAggregate is one actor's spend within one raw workflow ID.
type UserAggregate struct {
	Aggregate
	ByScope map[string]*Aggregate `json:"by_scope,omitempty"`
	ByModel map[string]*Aggregate `json:"by_model,omitempty"`
}

type BotAggregate struct {
	Aggregate
	Platform string `json:"platform"`
	UserID   string `json:"user_id"`
}

type MCPAggregate struct {
	Calls         int `json:"calls"`
	UnpricedCalls int `json:"unpriced_calls"`
	// Cost remains unknown when an MCP service does not report a price.
	RecordedCostUSD float64 `json:"recorded_cost_usd"`
}

// WorkflowAggregate is one workflow_id's spend with the scope
// (run/pulse/builder/chat) and model splits the consolidated cost view needs.
type WorkflowAggregate struct {
	Aggregate
	ByScope map[string]*Aggregate `json:"by_scope,omitempty"`
	ByModel map[string]*Aggregate `json:"by_model,omitempty"`
}

// Merge folds source into a. The provider identity survives only while
// every merged aggregate agrees on it, matching add.
func (a *Aggregate) Merge(source Aggregate) {
	if a.AccountingEventCount == 0 {
		a.Provider = source.Provider
	} else if a.Provider != source.Provider {
		a.Provider = ""
	}
	mergeAggregate(a, source)
}

// Coverage reports whether the aggregate omitted or could not price evidence.
type Coverage struct {
	Source                string `json:"source"`
	MalformedEventCount   int    `json:"malformed_event_count"`
	QuarantinedEventCount int    `json:"quarantined_event_count"`
}

// Ledger writes immutable cost events and produces aggregate summaries. The
// SQLite implementation is safe across independent Ledger instances and uses
// idempotency keys to make retries harmless.
type Ledger struct {
	baseURL string
	client  *http.Client
	db      sqliteStore
	mu      sync.Mutex
}

var (
	defaultLedgerMu sync.RWMutex
	defaultLedger   *Ledger
)

// SetDefaultLedger publishes the process-wide production ledger used by paid
// virtual tools. The server owns its lifecycle; callers must not close it.
func SetDefaultLedger(ledger *Ledger) {
	defaultLedgerMu.Lock()
	defaultLedger = ledger
	defaultLedgerMu.Unlock()
}

// DefaultLedger returns the process-wide ledger, when the server initialized
// one. Tests and standalone tools may leave it unset and open their own store.
func DefaultLedger() *Ledger {
	defaultLedgerMu.RLock()
	defer defaultLedgerMu.RUnlock()
	return defaultLedger
}

type sqliteStore interface {
	append(Entry) error
	summarize(from, to, executionID, workflowID string) (*Summary, error)
	summarizeWindow(fromInclusive, toExclusive, executionID, workflowID, scope string) (*Summary, error)
	summarizeWorkflowOverview(from, to, workflowID string) (*Summary, bool, error)
	migrateLegacyJSONL(path string) (MigrationReport, error)
	close() error
}

// NewLedger creates a ledger that persists to _system/costs.jsonl via the
// workspace API.
func NewLedger(workspaceAPIURL string) *Ledger {
	return &Ledger{
		baseURL: strings.TrimRight(strings.TrimSpace(workspaceAPIURL), "/"),
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: wsauth.Transport(&http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			}),
		},
	}
}

func (l *Ledger) workspacePathURL(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return l.baseURL + "/api/documents/" + strings.Join(segments, "/")
}

func (l *Ledger) readFile(path string) ([]byte, bool, error) {
	req, err := http.NewRequest(http.MethodGet, l.workspacePathURL(path), nil)
	if err != nil {
		return nil, false, fmt.Errorf("costledger: create read request for %s: %w", path, err)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("costledger: read %s via workspace API: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("costledger: read response body for %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("costledger: workspace API returned status %d for %s: %s", resp.StatusCode, path, string(body))
	}

	var apiResp workspaceAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, false, fmt.Errorf("costledger: parse workspace API response for %s: %w", path, err)
	}
	if strings.Contains(apiResp.Message, "File does not exist") || strings.Contains(apiResp.Error, "File not found") {
		return nil, false, nil
	}
	if !apiResp.Success {
		return nil, false, fmt.Errorf("costledger: workspace API error for %s: %s", path, apiResp.Error)
	}

	var data struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, false, fmt.Errorf("costledger: parse content for %s: %w", path, err)
	}
	return []byte(data.Content), true, nil
}

func (l *Ledger) writeFile(path string, content []byte) error {
	requestBody, err := json.Marshal(map[string]string{"content": string(content)})
	if err != nil {
		return fmt.Errorf("costledger: marshal content for %s: %w", path, err)
	}
	req, err := http.NewRequest(http.MethodPut, l.workspacePathURL(path), bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("costledger: create write request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("costledger: write %s via workspace API: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("costledger: workspace API returned status %d for %s: %s", resp.StatusCode, path, string(body))
	}
	return nil
}

// Append writes one entry as a JSONL line. Missing Timestamp is filled in.
func (l *Ledger) Append(e Entry) error {
	if l == nil {
		return fmt.Errorf("costledger: nil ledger")
	}
	if l.db != nil {
		return l.db.append(e)
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	normalizeEntry(&e)
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("costledger: marshal entry: %w", err)
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	content, exists, err := l.readFile(ledgerWorkspacePath)
	if err != nil {
		return err
	}
	if exists && len(content) > 0 {
		line = append(content, line...)
	}
	return l.writeFile(ledgerWorkspacePath, line)
}

// Summarize scans the ledger and rolls entries up by date and model. from/to
// are inclusive date bounds in YYYY-MM-DD (UTC); empty strings mean unbounded.
func (l *Ledger) Summarize(from, to string) (*Summary, error) {
	if l == nil {
		return nil, fmt.Errorf("costledger: nil ledger")
	}
	if l.db != nil {
		return l.db.summarize(from, to, "", "")
	}
	return l.summarizeLegacy(from, to, "", "")
}

// SummarizeExecution returns the exact cost and token rows attributed to one
// runtime execution. Pulse reviewers and fixers use this to snapshot their
// own usage into the workflow-local Pulse database instead of sharing a broad
// phase or todo-task bucket.
func (l *Ledger) SummarizeExecution(executionID string) (*Summary, error) {
	if l == nil {
		return nil, fmt.Errorf("costledger: nil ledger")
	}
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return nil, fmt.Errorf("costledger: execution id is required")
	}
	if l.db != nil {
		return l.db.summarize("", "", executionID, "")
	}
	return l.summarizeLegacy("", "", executionID, "")
}

// SummarizeWorkflow returns only events attributed to one workflow. The exact
// workflow id is the workspace-relative path recorded on every cost event.
func (l *Ledger) SummarizeWorkflow(workflowID string) (*Summary, error) {
	if l == nil {
		return nil, fmt.Errorf("costledger: nil ledger")
	}
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return nil, fmt.Errorf("costledger: workflow id is required")
	}
	if l.db != nil {
		return l.db.summarize("", "", "", workflowID)
	}
	return l.summarizeLegacy("", "", "", workflowID)
}

// SummarizeWorkflowOverview returns exact all-time headline totals together
// with a bounded daily/detail window. The SQLite path aggregates the all-time
// totals in SQL and decodes only rows inside the requested window, so UI first
// paint does not grow with retained history. hasMore reports whether an older
// event exists before the requested from date.
func (l *Ledger) SummarizeWorkflowOverview(workflowID, from, to string) (*Summary, bool, error) {
	if l == nil {
		return nil, false, fmt.Errorf("costledger: nil ledger")
	}
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return nil, false, fmt.Errorf("costledger: workflow id is required")
	}
	if l.db != nil {
		return l.db.summarizeWorkflowOverview(from, to, workflowID)
	}

	recent, err := l.summarizeLegacy(from, to, "", workflowID)
	if err != nil {
		return nil, false, err
	}
	allTime, err := l.summarizeLegacy("", "", "", workflowID)
	if err != nil {
		return nil, false, err
	}
	compactWorkflowOverview(recent, allTime)
	hasMore := false
	if strings.TrimSpace(from) != "" {
		for date := range allTime.ByDate {
			if date < from {
				hasMore = true
				break
			}
		}
	}
	return recent, hasMore, nil
}

// SummarizeWorkflowScopeWindow returns cost events for one workflow and scope
// inside an exact UTC time window. Unlike Summarize, this is not date-bucket
// filtering: callers such as Pulse can isolate one scheduled pass even when a
// workflow runs several times on the same day.
func (l *Ledger) SummarizeWorkflowScopeWindow(workflowID, scope string, fromInclusive, toExclusive time.Time) (*Summary, error) {
	if l == nil {
		return nil, fmt.Errorf("costledger: nil ledger")
	}
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return nil, fmt.Errorf("costledger: workflow id is required")
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("costledger: scope is required")
	}
	if fromInclusive.IsZero() {
		return nil, fmt.Errorf("costledger: window start is required")
	}
	if !toExclusive.IsZero() && !toExclusive.After(fromInclusive) {
		return nil, fmt.Errorf("costledger: window end must be after start")
	}
	from := fromInclusive.UTC().Format(time.RFC3339Nano)
	to := ""
	if !toExclusive.IsZero() {
		to = toExclusive.UTC().Format(time.RFC3339Nano)
	}
	if l.db != nil {
		return l.db.summarizeWindow(from, to, "", workflowID, scope)
	}
	return l.summarizeLegacyWindow(from, to, "", workflowID, scope)
}

func (l *Ledger) summarizeLegacy(from, to, executionID, workflowID string) (*Summary, error) {
	return l.summarizeLegacyFiltered(from, to, executionID, workflowID, "", false)
}

func (l *Ledger) summarizeLegacyWindow(from, to, executionID, workflowID, scope string) (*Summary, error) {
	return l.summarizeLegacyFiltered(from, to, executionID, workflowID, scope, true)
}

func (l *Ledger) summarizeLegacyFiltered(from, to, executionID, workflowID, scope string, exactWindow bool) (*Summary, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	summary := &Summary{
		From:             from,
		To:               to,
		ByDate:           make(map[string]*DateAggregate),
		ByModel:          make(map[string]*Aggregate),
		ByScope:          make(map[string]*ScopeAggregate),
		BySourcePlatform: make(map[string]*Aggregate),
		Coverage:         Coverage{Source: "legacy_jsonl"},
	}

	content, exists, err := l.readFile(ledgerWorkspacePath)
	if err != nil {
		return nil, err
	}
	if !exists || len(content) == 0 {
		return summary, nil
	}

	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			summary.Coverage.MalformedEventCount++
			continue
		}
		normalizeEntry(&e)
		date := e.Timestamp.UTC().Format("2006-01-02")
		comparisonValue := date
		if exactWindow {
			comparisonValue = e.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		if from != "" && comparisonValue < from {
			continue
		}
		if to != "" && ((!exactWindow && comparisonValue > to) || (exactWindow && comparisonValue >= to)) {
			continue
		}
		if executionID != "" && e.ExecutionID != executionID {
			continue
		}
		if workflowID != "" && e.WorkflowID != workflowID {
			continue
		}
		if scope != "" && e.Scope != scope {
			continue
		}
		addEntryToSummary(summary, date, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("costledger: scan %s: %w", ledgerWorkspacePath, err)
	}
	return summary, nil
}

// Close releases the SQLite connection. It is a no-op for the legacy API ledger.
func (l *Ledger) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.close()
}

// MigrateLegacyJSONL imports valid rows idempotently and quarantines malformed
// rows. It is supported only by the SQLite ledger.
func (l *Ledger) MigrateLegacyJSONL(path string) (MigrationReport, error) {
	if l == nil || l.db == nil {
		return MigrationReport{}, fmt.Errorf("costledger: legacy migration requires SQLite ledger")
	}
	return l.db.migrateLegacyJSONL(path)
}

// addEntryToExecutionBucket rolls one entry into an execution's combined
// total (unchanged from before PLAT-166) and, only when the entry names a
// Phase, also into that execution's ByPhase breakdown. An execution whose
// entries never set Phase never gets a ByPhase map at all, so `by_phase` is
// omitted from its JSON exactly as it was before this field existed.
func addEntryToExecutionBucket(bucket *ExecutionAggregate, e Entry) {
	bucket.Aggregate.add(e)
	phase := strings.TrimSpace(e.Phase)
	if phase == "" {
		return
	}
	if bucket.ByPhase == nil {
		bucket.ByPhase = make(map[string]*Aggregate)
	}
	phaseBucket, ok := bucket.ByPhase[phase]
	if !ok {
		phaseBucket = &Aggregate{}
		bucket.ByPhase[phase] = phaseBucket
	}
	phaseBucket.add(e)
}

func addEntryToSummary(summary *Summary, date string, e Entry) {
	normalizeEntry(&e)
	summary.Total.add(e)
	if summary.ByScope == nil {
		summary.ByScope = make(map[string]*ScopeAggregate)
	}
	sourcePlatform := strings.TrimSpace(e.SourcePlatform)
	if sourcePlatform != "" {
		if summary.BySourcePlatform == nil {
			summary.BySourcePlatform = make(map[string]*Aggregate)
		}
		sourceBucket, ok := summary.BySourcePlatform[sourcePlatform]
		if !ok {
			sourceBucket = &Aggregate{}
			summary.BySourcePlatform[sourcePlatform] = sourceBucket
		}
		sourceBucket.add(e)
	}
	scope := strings.TrimSpace(e.Scope)
	if scope == "" {
		scope = "unknown"
	}
	scopeBucket, ok := summary.ByScope[scope]
	if !ok {
		scopeBucket = &ScopeAggregate{ByExecution: make(map[string]*ExecutionAggregate)}
		summary.ByScope[scope] = scopeBucket
	}
	scopeBucket.Aggregate.add(e)
	addEntryToWorkflowBucket(summary, scope, e)
	addEntryToWorkflowUserBucket(summary, scope, e)
	addEntryToBotAndMCPBuckets(summary, e)
	executionID := strings.TrimSpace(e.ExecutionID)
	if executionID == "" && strings.TrimSpace(e.SessionID) != "" {
		executionID = "session:" + strings.TrimSpace(e.SessionID)
	}
	if executionID == "" {
		executionID = "unattributed"
	}
	executionBucket, ok := scopeBucket.ByExecution[executionID]
	if !ok {
		executionBucket = &ExecutionAggregate{}
		scopeBucket.ByExecution[executionID] = executionBucket
	}
	addEntryToExecutionBucket(executionBucket, e)
	bucket, ok := summary.ByDate[date]
	if !ok {
		bucket = &DateAggregate{
			ByModel:          make(map[string]*Aggregate),
			ByScope:          make(map[string]*ScopeAggregate),
			BySourcePlatform: make(map[string]*Aggregate),
			workflowRunIDs:   make(map[string]struct{}),
		}
		summary.ByDate[date] = bucket
	}
	bucket.Aggregate.add(e)
	if sourcePlatform != "" {
		dateSourceBucket, ok := bucket.BySourcePlatform[sourcePlatform]
		if !ok {
			dateSourceBucket = &Aggregate{}
			bucket.BySourcePlatform[sourcePlatform] = dateSourceBucket
		}
		dateSourceBucket.add(e)
	}
	dateScopeBucket, ok := bucket.ByScope[scope]
	if !ok {
		dateScopeBucket = &ScopeAggregate{ByExecution: make(map[string]*ExecutionAggregate)}
		bucket.ByScope[scope] = dateScopeBucket
	}
	dateScopeBucket.Aggregate.add(e)
	dateExecutionBucket, ok := dateScopeBucket.ByExecution[executionID]
	if !ok {
		dateExecutionBucket = &ExecutionAggregate{}
		dateScopeBucket.ByExecution[executionID] = dateExecutionBucket
	}
	addEntryToExecutionBucket(dateExecutionBucket, e)
	if scope == "workflow_execution" && strings.TrimSpace(e.RunID) != "" {
		bucket.workflowRunIDs[e.RunID] = struct{}{}
		bucket.WorkflowRunCount = len(bucket.workflowRunIDs)
	}
	modelID := e.EffectiveModelID
	if modelID == "" {
		modelID = e.ModelID
	}
	if modelID == "" {
		return
	}
	dm, ok := bucket.ByModel[modelID]
	if !ok {
		dm = &Aggregate{}
		bucket.ByModel[modelID] = dm
	}
	dm.add(e)
	mb, ok := summary.ByModel[modelID]
	if !ok {
		mb = &Aggregate{}
		summary.ByModel[modelID] = mb
	}
	mb.add(e)
}

func addEntryToBotAndMCPBuckets(summary *Summary, e Entry) {
	workflowID := strings.TrimSpace(e.WorkflowID)
	platform := strings.TrimSpace(e.SourcePlatform)
	if platform != "" {
		if summary.ByWorkflowBot == nil {
			summary.ByWorkflowBot = make(map[string]map[string]*BotAggregate)
		}
		bots := summary.ByWorkflowBot[workflowID]
		if bots == nil {
			bots = make(map[string]*BotAggregate)
			summary.ByWorkflowBot[workflowID] = bots
		}
		key := platform + "\x00" + strings.TrimSpace(e.UserID)
		bot := bots[key]
		if bot == nil {
			bot = &BotAggregate{Platform: platform, UserID: strings.TrimSpace(e.UserID)}
			bots[key] = bot
		}
		bot.Aggregate.add(e)
	}
	if server, ok := strings.CutPrefix(strings.TrimSpace(e.Component), "mcp:"); ok && server != "" {
		if summary.ByWorkflowMCP == nil {
			summary.ByWorkflowMCP = make(map[string]map[string]*MCPAggregate)
		}
		servers := summary.ByWorkflowMCP[workflowID]
		if servers == nil {
			servers = make(map[string]*MCPAggregate)
			summary.ByWorkflowMCP[workflowID] = servers
		}
		bucket := servers[server]
		if bucket == nil {
			bucket = &MCPAggregate{}
			servers[server] = bucket
		}
		bucket.Calls++
		if e.BillingBasis == "unpriced" {
			bucket.UnpricedCalls++
		}
		bucket.RecordedCostUSD += e.TotalCostUSD
	}
}

func addEntryToWorkflowUserBucket(summary *Summary, scope string, e Entry) {
	if summary.ByWorkflowUser == nil {
		summary.ByWorkflowUser = make(map[string]map[string]*UserAggregate)
	}
	workflowID := strings.TrimSpace(e.WorkflowID)
	users := summary.ByWorkflowUser[workflowID]
	if users == nil {
		users = make(map[string]*UserAggregate)
		summary.ByWorkflowUser[workflowID] = users
	}
	userID := strings.TrimSpace(e.UserID)
	bucket := users[userID]
	if bucket == nil {
		bucket = &UserAggregate{ByScope: make(map[string]*Aggregate), ByModel: make(map[string]*Aggregate)}
		users[userID] = bucket
	}
	bucket.Aggregate.add(e)
	scopeBucket := bucket.ByScope[scope]
	if scopeBucket == nil {
		scopeBucket = &Aggregate{}
		bucket.ByScope[scope] = scopeBucket
	}
	scopeBucket.add(e)
	modelID := e.EffectiveModelID
	if modelID == "" {
		modelID = e.ModelID
	}
	if modelID != "" {
		modelBucket := bucket.ByModel[modelID]
		if modelBucket == nil {
			modelBucket = &Aggregate{}
			bucket.ByModel[modelID] = modelBucket
		}
		modelBucket.add(e)
	}
}

func addEntryToWorkflowBucket(summary *Summary, scope string, e Entry) {
	if summary.ByWorkflow == nil {
		summary.ByWorkflow = make(map[string]*WorkflowAggregate)
	}
	workflowID := strings.TrimSpace(e.WorkflowID)
	bucket, ok := summary.ByWorkflow[workflowID]
	if !ok {
		bucket = &WorkflowAggregate{ByScope: make(map[string]*Aggregate), ByModel: make(map[string]*Aggregate)}
		summary.ByWorkflow[workflowID] = bucket
	}
	bucket.Aggregate.add(e)
	scopeBucket, ok := bucket.ByScope[scope]
	if !ok {
		scopeBucket = &Aggregate{}
		bucket.ByScope[scope] = scopeBucket
	}
	scopeBucket.add(e)
	modelID := e.EffectiveModelID
	if modelID == "" {
		modelID = e.ModelID
	}
	if modelID == "" {
		return
	}
	modelBucket, ok := bucket.ByModel[modelID]
	if !ok {
		modelBucket = &Aggregate{}
		bucket.ByModel[modelID] = modelBucket
	}
	modelBucket.add(e)
}

func compactWorkflowOverview(recent, allTime *Summary) {
	if recent == nil || allTime == nil {
		return
	}
	recent.Total = allTime.Total
	recent.Coverage = allTime.Coverage
	// A single-workflow overview has nothing to split by workflow.
	recent.ByWorkflow = nil
	recent.ByScope = make(map[string]*ScopeAggregate, len(allTime.ByScope))
	for scope, aggregate := range allTime.ByScope {
		if aggregate == nil {
			continue
		}
		recent.ByScope[scope] = &ScopeAggregate{Aggregate: aggregate.Aggregate}
	}
}

// SortedDates returns the date keys from a summary in ascending order.
// Kept here so handlers don't have to re-implement the sort.
func (s *Summary) SortedDates() []string {
	out := make([]string, 0, len(s.ByDate))
	for d := range s.ByDate {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// SortedModels returns model keys sorted by total cost descending.
func (s *Summary) SortedModels() []string {
	out := make([]string, 0, len(s.ByModel))
	for m := range s.ByModel {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return s.ByModel[out[i]].TotalCostUSD > s.ByModel[out[j]].TotalCostUSD
	})
	return out
}
