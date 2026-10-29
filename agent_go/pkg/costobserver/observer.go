// Package costobserver turns agent LLM and MCP activity events into ledger entries.
//
// It lives outside cmd/server because more than one launch path spends
// tokens: the chat/query path and delegate() run inside package server, while
// workflow steps and background sub-agents are created by the orchestrator.
// Those orchestrator-created agents previously had no ledger attachment at
// all, so their spend was invisible. Both sides now construct the same
// Observer from here — a second copy of attribution logic would drift.
package costobserver

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"

	unifiedevents "github.com/manishiitg/mcpagent/events"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// Cost scopes. Every launch path must name one; see New for what happens when
// it cannot.
const (
	ScopeChat              = "chat"
	ScopeBuilder           = "builder"
	ScopePulse             = "pulse"
	ScopeWorkflowExecution = "workflow_execution"
	// ScopeSchedule is a turn started by a product (Code/Crew) schedule,
	// reminder or trigger, in its chat or its own isolated conversation,
	// and every sub-agent it delegates to (PLAT-702). Goals workflow
	// schedules stay workflow_execution/pulse.
	ScopeSchedule = "schedule"
	// ScopeUnknown is the last-resort value recorded when a launch path did
	// not name its scope. Reaching it is a defect and is logged as one.
	ScopeUnknown = "unknown"
)

// Cost phases (PLAT-166). A workflow step's execution turn and its separate
// post-completion reflection turn reuse the same agent and the same Observer
// instance — SetPhase toggles which one every entry from here on is
// attributed to. These deliberately reuse the exact phase-string vocabulary
// the older per-step token-usage-file system already established
// (pkg/orchestrator/context_aware_bridge.go's attributedPhase =
// "execution_only", pkg/orchestrator/agents/workflow/step_based_workflow's
// reflectionCostPhase = "reflection") so an operator cross-referencing both
// systems isn't learning a second vocabulary for the same concept.
const (
	PhaseExecutionOnly = "execution_only"
	PhaseReflection    = "reflection"
)

// Observer persists immutable LLM calls and in-process MCP activity. The cumulative token_usage
// event remains a compatibility fallback for older providers, but is ignored
// once a per-call completion has been observed.
type Observer struct {
	ledger         *costledger.Ledger
	sessionID      string
	userID         string
	agentMode      string
	provider       string
	modelID        string
	accountID      string
	billingUserID  string
	workflowID     string
	runID          string
	executionID    string
	scope          string
	sourcePlatform string
	sourceID       string
	sourceLabel    string
	sourceRunID    string
	launchPath     string
	attribution    costledger.Attribution
	native         NativeAccounting

	mu         sync.Mutex
	sawPerCall bool
	// phase is the one attribution field that legitimately changes mid-
	// lifetime for the same Observer instance (PLAT-166) — everything else in
	// WithAttribution is fixed at construction because a reflection turn
	// reuses the step's own agent/observer rather than getting a new one.
	phase string
}

// Option configures an Observer at construction time.
type Option func(*Observer)

// WithProductAttribution receives a server-admitted attribution snapshot.
// Callers propagate the same context to children rather than accepting fields
// from model arguments or client JSON.
func WithProductAttribution(a costledger.Attribution) Option {
	return func(o *Observer) { o.attribution = a }
}

type attributionContextKey struct{}

// Source identifies the admitted automation and occurrence that launched work.
// Keep it on the context so child agents join the same named cost group.
type Source struct {
	ID, Label, RunID string
}

type sourceContextKey struct{}

func ContextWithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, source)
}

func SourceFromContext(ctx context.Context) Source {
	if ctx == nil {
		return Source{}
	}
	source, _ := ctx.Value(sourceContextKey{}).(Source)
	return source
}

func WithSourceFromContext(ctx context.Context) Option {
	source := SourceFromContext(ctx)
	return WithSource(source.ID, source.Label, source.RunID)
}

func ContextWithAttribution(ctx context.Context, a costledger.Attribution) context.Context {
	return context.WithValue(ctx, attributionContextKey{}, a)
}

func AttributionFromContext(ctx context.Context) costledger.Attribution {
	if ctx == nil {
		return costledger.Attribution{}
	}
	a, _ := ctx.Value(attributionContextKey{}).(costledger.Attribution)
	return a
}

// WithModel records the provider and model the caller requested. The provider
// may still be overridden per event by what the adapter actually reports.
func WithModel(provider, modelID string) Option {
	return func(o *Observer) {
		o.provider = strings.TrimSpace(provider)
		o.modelID = strings.TrimSpace(modelID)
	}
}

// WithAccount records the provider account the agent runs on:
// global:<provider> for the server account, else a user account ID.
func WithAccount(accountID string) Option {
	return func(o *Observer) {
		o.accountID = strings.TrimSpace(accountID)
	}
}

// WithBillingUser names the person whose shared-account token limits this
// agent's usage counts toward when it is not the agent's user: a Slack
// channel turn runs as a bot identity and bills the target's owner.
func WithBillingUser(userID string) Option {
	return func(o *Observer) {
		o.billingUserID = strings.TrimSpace(userID)
	}
}

// AccountIDFor names the account a model config runs on, for WithAccount.
func AccountIDFor(provider, connectionID string) string {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" || strings.HasPrefix(connectionID, "server-default:") {
		if strings.TrimSpace(provider) == "" {
			return ""
		}
		return "global:" + strings.TrimSpace(provider)
	}
	return connectionID
}

// WithAttribution names the cost scope and the workflow/run/execution this
// agent's spend belongs to. scope is required; see New.
func WithAttribution(scope, workflowID, runID, executionID string) Option {
	return func(o *Observer) {
		o.scope = strings.TrimSpace(scope)
		o.workflowID = strings.TrimSpace(workflowID)
		o.runID = strings.TrimSpace(runID)
		o.executionID = strings.TrimSpace(executionID)
	}
}

// WithSourcePlatform records the external channel that initiated the turn.
// It is intentionally separate from scope: bot-originated turns can still be
// chat, builder, workflow execution, or Pulse work.
func WithSourcePlatform(platform string) Option {
	return func(o *Observer) {
		o.sourcePlatform = strings.ToLower(strings.TrimSpace(platform))
	}
}

// WithSource names the product schedule, reminder or trigger that started
// the turn: its job id, display name and the one automation run (PLAT-702).
func WithSource(id, label, runID string) Option {
	return func(o *Observer) {
		o.sourceID = strings.TrimSpace(id)
		o.sourceLabel = strings.TrimSpace(label)
		o.sourceRunID = strings.TrimSpace(runID)
	}
}

// ScopeForAutomationSource returns ScopeSchedule for a chat-path turn that a
// product schedule, reminder or trigger started (it names a source id), and
// "" otherwise so the caller keeps its own inference.
func ScopeForAutomationSource(sourceID string) string {
	if strings.TrimSpace(sourceID) != "" {
		return ScopeSchedule
	}
	return ""
}

type sourcePlatformContextKey struct{}

// ContextWithSourcePlatform carries a bot channel into child agents and paid
// tool calls without changing their cost scope or user attribution.
func ContextWithSourcePlatform(ctx context.Context, platform string) context.Context {
	if strings.TrimSpace(platform) == "" {
		return ctx
	}
	return context.WithValue(ctx, sourcePlatformContextKey{}, strings.ToLower(strings.TrimSpace(platform)))
}

func SourcePlatformFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	platform, _ := ctx.Value(sourcePlatformContextKey{}).(string)
	return platform
}

// WithLaunchPath names the code path that created this agent. It is only used
// to make an unattributed observer identifiable in the logs.
func WithLaunchPath(launchPath string) Option {
	return func(o *Observer) {
		o.launchPath = strings.TrimSpace(launchPath)
	}
}

// New builds an observer for one agent.
//
// The scope is not defaulted. A launch path that cannot name its scope is a
// bug, and silently writing "unknown" is how 15k rows ended up unattributable:
// nothing downstream can tell a genuinely unclassifiable event from a caller
// that forgot. Such a path is still recorded — losing the tokens would be
// worse than misfiling them — but it logs an error naming itself first.
func New(ledger *costledger.Ledger, sessionID, userID, agentMode string, opts ...Option) *Observer {
	observer := &Observer{
		ledger:    ledger,
		sessionID: sessionID,
		userID:    userID,
		agentMode: agentMode,
		// phase deliberately starts empty, not PhaseExecutionOnly (PLAT-166
		// scope-fix). Every observer used to default to PhaseExecutionOnly,
		// which meant EVERY execution — chat, builder, Pulse, every plain
		// workflow step — grew a `by_phase.execution_only` entry
		// that just duplicated its own top-level total, in every Cost
		// Analysis API response, forever. Only a phase the caller explicitly
		// sets via SetPhase (reflection, a agent item) is worth
		// naming; addEntryToExecutionBucket already skips ByPhase entirely
		// for an empty phase, so leaving this unset costs nothing.
	}
	for _, opt := range opts {
		opt(observer)
	}
	if observer.scope == "" {
		launchPath := observer.launchPath
		if launchPath == "" {
			launchPath = "<unnamed launch path>"
		}
		log.Printf("[COST_LEDGER] %s did not name a cost scope (session=%q agent_mode=%q); recording as %q",
			launchPath, sessionID, agentMode, ScopeUnknown)
		observer.scope = ScopeUnknown
	}
	observer.native = newNativeAccounting(observer)
	return observer
}

// Scope reports the scope every entry from this observer carries.
func (o *Observer) Scope() string {
	if o == nil {
		return ""
	}
	return o.scope
}

// ExecutionID reports the execution every entry from this observer is grouped
// under.
func (o *Observer) ExecutionID() string {
	if o == nil {
		return ""
	}
	return o.executionID
}

// SetPhase updates the phase every entry this observer writes from now on
// carries, until changed again (PLAT-166). A reflection turn — or, per
// PLAT-167, a agent item's own turn — reuses the same agent, and
// therefore the same Observer instance, as whatever ran before it. Bracket
// the turn with SetPhase(PhaseReflection) (or "item:<id>") and a deferred
// SetPhase(""), mirroring ContextAwareEventBridge.PushContext/PopContext's
// bracket right next to it. Reset to "" (not PhaseExecutionOnly): an empty
// phase writes no ByPhase entry at all (addEntryToExecutionBucket), so any
// turn that follows and was never explicitly tagged stays invisible in
// by_phase instead of manufacturing a redundant duplicate-of-the-total entry
// for it.
func (o *Observer) SetPhase(phase string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.phase = strings.TrimSpace(phase)
	o.mu.Unlock()
}

// Phase reports the phase currently attributed to entries from this observer.
func (o *Observer) Phase() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.phase
}

// Name identifies this listener to the agent runtime.
func (o *Observer) Name() string { return "costObserver" }

// HandleEvent implements the agent event listener contract.
func (o *Observer) HandleEvent(_ context.Context, event *unifiedevents.AgentEvent) error {
	if o == nil || o.ledger == nil || event == nil {
		return nil
	}
	switch event.Type {
	case unifiedevents.LLMGenerationStart:
		if o.native != nil {
			o.native.Begin(o.baseEntry(event))
		}
		if start, ok := event.Data.(*unifiedevents.LLMGenerationStartEvent); ok && start != nil {
			o.logModelRequest(event, "started", o.provider, FirstNonEmpty(start.ModelID, o.modelID), "not_completed", 0)
		}
	case unifiedevents.LLMGenerationError:
		if failure, ok := event.Data.(*unifiedevents.LLMGenerationErrorEvent); ok && failure != nil {
			o.logModelRequest(event, "failed", o.provider, FirstNonEmpty(failure.ModelID, o.modelID), "not_completed", 0)
		}
	case unifiedevents.LLMGenerationEnd:
		generation, ok := event.Data.(*unifiedevents.LLMGenerationEndEvent)
		if !ok || generation == nil {
			return nil
		}
		o.mu.Lock()
		o.sawPerCall = true
		o.mu.Unlock()
		o.recordLLMGeneration(event, generation)
	case unifiedevents.TokenUsage:
		o.mu.Lock()
		sawPerCall := o.sawPerCall
		o.mu.Unlock()
		if sawPerCall {
			return nil
		}
		tu, ok := event.Data.(*unifiedevents.TokenUsageEvent)
		if !ok || tu == nil {
			return nil
		}
		o.recordLegacyTokenUsage(event, tu)
	case unifiedevents.ToolCallEnd:
		if tool, ok := event.Data.(*unifiedevents.ToolCallEndEvent); ok && tool != nil && !tool.SyntheticSettle {
			o.recordMCPCall(event, tool.ServerName, tool.ToolName, tool.ToolCallID)
		}
	case unifiedevents.ToolCallError:
		if tool, ok := event.Data.(*unifiedevents.ToolCallErrorEvent); ok && tool != nil {
			o.recordMCPCall(event, tool.ServerName, tool.ToolName, tool.ToolCallID)
		}
	}
	return nil
}

func (o *Observer) recordMCPCall(event *unifiedevents.AgentEvent, server, tool, callID string) {
	server = strings.TrimSpace(server)
	if server == "" || server == "custom" || server == "virtual-tools" || server == "direct_execution" ||
		tool == "get_api_spec" || isCodingCLIProvider(o.provider) {
		return
	}
	entry := o.baseEntry(event)
	entry.Component = "mcp:" + server
	entry.ToolName = tool
	entry.BillingBasis = "unpriced"
	if callID != "" {
		entry.EventID = o.sessionID + ":mcp:" + callID
		entry.IdempotencyKey = entry.EventID
	} else {
		// An agent span can contain several tool calls. Let the ledger derive
		// a key from the complete event instead of collapsing them by span.
		entry.EventID = ""
		entry.IdempotencyKey = ""
	}
	o.append(entry)
}

func isCodingCLIProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude-code", "claude_code", "codex-cli", "codex_cli", "cursor-cli", "cursor_cli", "pi-cli", "pi_cli", "muse-cli", "muse_cli", "agy-cli", "agy_cli":
		return true
	default:
		return false
	}
}

func (o *Observer) recordLLMGeneration(event *unifiedevents.AgentEvent, generation *unifiedevents.LLMGenerationEndEvent) {
	metadata := generation.Metadata
	cacheRead, cacheWrite := ExtractCacheTokens(metadata)
	effectiveModel, estimatedCost := ExtractCostAndEffectiveModel(metadata)
	provider := FirstNonEmpty(stringValue(metadata["provider"]), o.provider)
	modelID := o.modelID
	if modelID == "" {
		modelID = stringValue(metadata["requested_model_id"])
	}
	totalCostUSD := numberValue(metadata["cost_usd"])
	billingBasis := "provider_actual"
	pricingSource := "provider"
	if totalCostUSD <= 0 && estimatedCost > 0 {
		totalCostUSD = estimatedCost
		billingBasis = "token_estimate"
		pricingSource = "model_registry"
		if isSubscriptionCodingProvider(provider) {
			billingBasis = "subscription_shadow"
		}
	}
	if totalCostUSD <= 0 {
		billingBasis = "unpriced"
		pricingSource = ""
	} else if isSubscriptionCodingProvider(provider) {
		// CLI dollar telemetry is API-equivalent usage even when supplied by
		// the provider; it is not a charge on a subscription login.
		billingBasis = "subscription_shadow"
	}
	if unidentifiedAgyModel(provider, effectiveModel) {
		totalCostUSD = 0
		billingBasis = "unpriced"
		pricingSource = ""
		effectiveModel = ""
	}
	if effectiveModel == "" && !unidentifiedAgyModel(provider, effectiveModel) {
		effectiveModel = modelID
	}
	entry := o.baseEntry(event)
	entry.Provider = provider
	entry.ModelID = modelID
	entry.EffectiveProvider = provider
	entry.EffectiveModelID = effectiveModel
	entry.TurnCount = 1
	entry.LLMCallCount = 1
	// Coding CLIs can make several model generations inside one bounded turn.
	// Only an explicitly native per-turn count can replace the default one;
	// cumulative counters from other event paths must not multiply usage.
	if stringValue(metadata["llm_call_count_source"]) == "native_model_generations" {
		if count := ToInt(metadata["llm_call_count"]); count > 0 {
			entry.LLMCallCount = count
		}
	}
	entry.LLMGenerationDurationMS = generation.Duration.Milliseconds()
	entry.PromptTokens = generation.UsageMetrics.PromptTokens
	entry.CompletionTokens = generation.UsageMetrics.CompletionTokens
	entry.ReasoningTokens = generation.UsageMetrics.ReasoningTokens
	entry.CacheReadTokens = firstPositive(cacheRead, generation.UsageMetrics.CacheTokens)
	entry.CacheWriteTokens = cacheWrite
	if includes, ok := metadata["prompt_tokens_include_cache"].(bool); ok {
		entry.OperationMetadata = map[string]interface{}{"prompt_tokens_include_cache": includes}
	}
	if source := stringValue(metadata["llm_call_count_source"]); source == "native_model_generations" {
		if entry.OperationMetadata == nil {
			entry.OperationMetadata = map[string]interface{}{}
		}
		entry.OperationMetadata["llm_call_count_source"] = source
	}
	// A usage row read from the CLI's own transcript (a turn typed into the running terminal) names that run/message,
	// so the row can be traced to it and a later backfill can skip what is already recorded.
	if source, id := stringValue(metadata["usage_source"]), stringValue(metadata["usage_source_id"]); source != "" && id != "" {
		if entry.OperationMetadata == nil {
			entry.OperationMetadata = map[string]interface{}{}
		}
		entry.OperationMetadata["usage_source"], entry.OperationMetadata["usage_source_id"] = source, id
		// The chat's owner pays; say when another agent's message started the turn.
		if source == "retained" {
			if by, label := retainedInputSource(o.sessionID); by != "" {
				entry.OperationMetadata["triggered_by"], entry.OperationMetadata["triggered_by_label"] = by, label
				if entry.SourceLabel == "" {
					entry.SourceLabel = label
				}
			}
		}
	}
	entry.TotalCostUSD = totalCostUSD
	entry.BillingBasis = billingBasis
	entry.PricingSource = pricingSource
	countBasis := "completed_generation"
	if isCodingCLIProvider(provider) {
		countBasis = "native_turn_fallback"
	}
	if stringValue(metadata["llm_call_count_source"]) == "native_model_generations" {
		countBasis = "native_model_generations"
	} else if stringValue(metadata["usage_source"]) == "retained" {
		countBasis = "retained_native_step"
	}
	o.logModelRequest(event, "completed", provider, effectiveModel, countBasis, entry.LLMCallCount)
	if o.native != nil && o.native.Record(entry) {
		return
	}
	o.append(entry)
}

func (o *Observer) recordLegacyTokenUsage(event *unifiedevents.AgentEvent, tu *unifiedevents.TokenUsageEvent) {
	cacheRead, cacheWrite := ExtractCacheTokens(tu.GenerationInfo)
	effectiveModel, estimatedCost := ExtractCostAndEffectiveModel(tu.GenerationInfo)
	provider := FirstNonEmpty(tu.Provider, o.provider)
	modelID := FirstNonEmpty(tu.ModelID, o.modelID)
	totalCostUSD := numberValue(tu.GenerationInfo["cost_usd"])
	billingBasis := "provider_actual"
	pricingSource := "provider"
	if totalCostUSD <= 0 && estimatedCost > 0 {
		totalCostUSD = estimatedCost
		billingBasis = "token_estimate"
		pricingSource = "model_registry"
		if isSubscriptionCodingProvider(provider) {
			billingBasis = "subscription_shadow"
		}
	} else if totalCostUSD <= 0 && tu.TotalCost > 0 {
		// TotalCost on the cumulative event is generally computed from model
		// metadata. It must not be labeled as a provider invoice.
		totalCostUSD = tu.TotalCost
		billingBasis = "token_estimate"
		pricingSource = "agent_token_pricing"
		if isSubscriptionCodingProvider(provider) {
			billingBasis = "subscription_shadow"
		}
	}
	if totalCostUSD <= 0 {
		billingBasis = "unpriced"
		pricingSource = ""
	} else if isSubscriptionCodingProvider(provider) {
		billingBasis = "subscription_shadow"
	}
	if unidentifiedAgyModel(provider, effectiveModel) {
		totalCostUSD = 0
		billingBasis = "unpriced"
		pricingSource = ""
		effectiveModel = ""
	}
	if effectiveModel == "" && !unidentifiedAgyModel(provider, effectiveModel) {
		effectiveModel = modelID
	}
	entry := o.baseEntry(event)
	entry.Provider = provider
	entry.ModelID = modelID
	entry.EffectiveProvider = provider
	entry.EffectiveModelID = effectiveModel
	entry.TurnCount = 1
	entry.LLMCallCount = ToInt(tu.GenerationInfo["llm_call_count"])
	if entry.LLMCallCount == 0 && (tu.PromptTokens > 0 || tu.CompletionTokens > 0 || tu.ReasoningTokens > 0 || totalCostUSD > 0) {
		entry.LLMCallCount = 1
	}
	entry.PromptTokens = tu.PromptTokens
	entry.CompletionTokens = tu.CompletionTokens
	entry.ReasoningTokens = tu.ReasoningTokens
	entry.CacheReadTokens = cacheRead
	entry.CacheWriteTokens = cacheWrite
	if includes, ok := tu.GenerationInfo["prompt_tokens_include_cache"].(bool); ok {
		entry.OperationMetadata = map[string]interface{}{"prompt_tokens_include_cache": includes}
	}
	entry.TotalCostUSD = totalCostUSD
	entry.BillingBasis = billingBasis
	entry.PricingSource = pricingSource
	o.append(entry)
}

func (o *Observer) baseEntry(event *unifiedevents.AgentEvent) costledger.Entry {
	eventID := strings.TrimSpace(event.SpanID)
	if eventID == "" {
		eventID = strings.TrimSpace(event.CorrelationID)
	}
	storedEventID := ""
	idempotencyKey := ""
	if eventID != "" {
		storedEventID = strings.Join([]string{o.sessionID, eventID}, ":")
		idempotencyKey = storedEventID
	}
	o.mu.Lock()
	phase := o.phase
	o.mu.Unlock()
	return costledger.Entry{
		Attribution:    o.attribution,
		EventID:        storedEventID,
		IdempotencyKey: idempotencyKey,
		Timestamp:      event.Timestamp,
		SessionID:      o.sessionID,
		UserID:         o.userID,
		WorkflowID:     o.workflowID,
		RunID:          o.runID,
		ExecutionID:    o.executionID,
		Scope:          o.scope,
		SourcePlatform: o.sourcePlatform,
		SourceID:       o.sourceID,
		SourceLabel:    o.sourceLabel,
		SourceRunID:    o.sourceRunID,
		AccountID:      o.accountID,
		BillingUserID:  o.billingUserID,
		Phase:          phase,
		AgentMode:      o.agentMode,
		Component:      event.Component,
		CorrelationID:  event.CorrelationID,
	}
}

func (o *Observer) append(entry costledger.Entry) {
	if err := o.ledger.Append(entry); err != nil {
		log.Printf("[COST_LEDGER] Failed to append entry: %v", err)
	}
	o.appendToWorkspaceLedger(entry)
}

// appendToWorkspaceLedger records the same entry into the attributed
// workflow's own per-workspace ledger (PLAT-184), alongside the global one
// above. The global ledger sits outside every workflow's own folder and is
// not reachable by any agent's normal read/tool access at all; this copy is
// what makes a workflow's own cost data -- including the phase/item
// breakdown PLAT-166/167 added -- actually queryable by that workflow's own
// Pulse review and Workflow Builder sessions. Best-effort: a failure here
// must never affect the global ledger write above, which remains the
// authoritative record the Cost Analysis UI reads.
func (o *Observer) appendToWorkspaceLedger(entry costledger.Entry) {
	workspacePath := strings.TrimSpace(o.workflowID)
	if workspacePath == "" {
		return
	}
	for _, destination := range costledger.WorkspaceCostCopies(workspacePath) {
		ledger, err := costledger.WorkspaceLedger(destination)
		if err != nil {
			log.Printf("[COST_LEDGER] Failed to open workspace ledger for %s: %v", destination, err)
			continue
		}
		if ledger == nil {
			continue
		}
		if err := ledger.Append(entry); err != nil {
			log.Printf("[COST_LEDGER] Failed to append entry to workspace ledger for %s: %v", destination, err)
		}
	}
}

// InferScope names the scope for a chat-session agent — the chat/query path
// and delegate() sub-agents. Anything not otherwise identified is chat.
func InferScope(agentMode, phaseID string) string {
	mode := strings.ToLower(strings.TrimSpace(agentMode))
	switch {
	case strings.Contains(mode, "workflow"):
		return ScopeBuilder
	default:
		return ScopeChat
	}
}

// InferWorkflowScope names the scope for an agent created by the workflow
// orchestrator: a workflow step or a background sub-agent.
//
// hasRunFolder is the signal that separates a live workflow execution from
// builder-side work: the orchestrator only sets an iteration/run folder once
// it is executing into runs/. It is a real distinction rather than a guess at
// the agent's name, because both are driven by the same controller type and
// the same agent mode.
func InferWorkflowScope(agentMode string, hasRunFolder bool, identifiers ...string) string {
	if hasRunFolder {
		return ScopeWorkflowExecution
	}
	return ScopeBuilder
}

func isSubscriptionCodingProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude-code", "claude_code", "codex-cli", "codex_cli", "cursor-cli", "cursor_cli":
		return true
	default:
		return false
	}
}

func stringValue(v interface{}) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func numberValue(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		value, _ := n.Float64()
		return value
	default:
		return 0
	}
}

// FirstNonEmpty returns the first trimmed non-empty value.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// ExtractCostAndEffectiveModel pulls the unified cost+model fields the
// CLI adapters now emit on GenerationInfo.Additional:
//
//	cost_usd_estimated  — float, computed from tokens × registry rates
//	cost_model_id       — string, model used for the rate lookup
//	<provider>_effective_model / claude_code_model / cursor_model /
//	  gemini_effective_model / codex_effective_model — string, the model
//	  the CLI actually served the turn with
//
// Returns ("", 0) when none of those are present.
func ExtractCostAndEffectiveModel(info map[string]interface{}) (effectiveModel string, estimatedCost float64) {
	if info == nil {
		return "", 0
	}
	if v, ok := info["cost_usd_estimated"]; ok {
		switch n := v.(type) {
		case float64:
			estimatedCost = n
		case float32:
			estimatedCost = float64(n)
		}
	}
	for _, key := range []string{
		"cost_model_id",
		"claude_code_model",
		"codex_effective_model",
		"gemini_effective_model",
		"cursor_model",
	} {
		if v, ok := info[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				effectiveModel = s
				break
			}
		}
	}
	return effectiveModel, estimatedCost
}

// ExtractCacheTokens pulls cache_read_input_tokens / cache_creation_input_tokens
// out of the provider's raw generation info blob. Providers that don't report
// cache usage just return zeros.
func ExtractCacheTokens(info map[string]interface{}) (read, write int) {
	if info == nil {
		return 0, 0
	}
	if v, ok := info["cache_read_input_tokens"]; ok {
		read = ToInt(v)
	}
	if v, ok := info["cache_creation_input_tokens"]; ok {
		write = ToInt(v)
	}
	return read, write
}

// ToInt coerces the numeric shapes provider metadata arrives in to an int.
func ToInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
	}
	return 0
}
