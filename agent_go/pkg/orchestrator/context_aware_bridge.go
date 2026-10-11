package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// numericOnlyIdentifierPattern matches a bare digit string such as a
// schedule-message counter. Every real planning/plan.json step ID observed
// in this codebase is a descriptive slug (e.g. "fetch-data", "job-search-
// ...-step-4"), never bare digits, so this is used to keep an unvalidated
// numeric index from being attributed as if it were a plan step (PLAT-031).
var numericOnlyIdentifierPattern = regexp.MustCompile(`^[0-9]+$`)

// TokenPersister defines the interface for persisting token usage to file
type TokenPersister interface {
	PersistTokenUsage(ctx context.Context, iterationFolder string, stepTokenData *StepTokenData, modelTokenData *ModelTokenData) error
}

// PhaseTokenPersister defines the interface for persisting phase token usage to file
type PhaseTokenPersister interface {
	PersistPhaseTokenUsage(ctx context.Context, phaseTokenData *PhaseTokenData, modelTokenData *ModelTokenData) error
}

// BackgroundAgentTranscriptWriter persists one normalized event into a
// background agent's durable structured transcript (PLAT-164). agentID is
// the execution-owner id carried by orchevents.ParentExecutionIDKey — the
// same id background_agent_log and the transcript's own file path are keyed
// on, so a bridge-side append lands in the same file cmd/server created at
// agent start and will mark terminal at agent completion.
type BackgroundAgentTranscriptWriter interface {
	AppendBackgroundAgentTranscriptEvent(ctx context.Context, sessionID, agentID string, evt orchevents.BackgroundAgentTranscriptEvent) error
}

// AgentSessionIDKey is re-exported from orchestrator/events for convenience.
// Use events.AgentSessionIDKey to inject agent session ID into context.
// When set, the ContextAwareEventBridge tags events with this correlation ID,
// enabling the frontend to group tool calls under their parent orchestrator_agent_start.
var AgentSessionIDKey = orchevents.AgentSessionIDKey

// orchestratorContext holds a snapshot of orchestrator context for stack operations
type orchestratorContext struct {
	phase     string
	step      int
	stepID    string
	stepType  string
	agentName string
	// Rich step context — see ContextAwareEventBridge fields below for semantics.
	stepName      string
	stepIndex     int
	stepTotal     int
	parentStepID  string
	attempt       int
	executionMode string
	transport     string
	triggeredBy   string
}

type eventContextOverrideKey struct{}

type eventContextOverride struct {
	phase     string
	step      int
	stepID    string
	agentName string
	rich      RichStepContext
}

// WithEventContextOverride binds workflow-step metadata to one execution
// context. Parallel child agents must use this instead of mutating the
// bridge's process-wide context stack.
func WithEventContextOverride(ctx context.Context, phase string, step int, stepID, agentName string, rich RichStepContext) context.Context {
	return context.WithValue(ctx, eventContextOverrideKey{}, eventContextOverride{
		phase:     phase,
		step:      step,
		stepID:    stepID,
		agentName: agentName,
		rich:      rich,
	})
}

// HasEventContextOverride reports whether events emitted with ctx already
// carry an execution-local orchestrator context.
func HasEventContextOverride(ctx context.Context) bool {
	_, ok := ctx.Value(eventContextOverrideKey{}).(eventContextOverride)
	return ok
}

type timingCaptureContextKey struct{}

type timingCaptureState struct {
	toolCalls      map[string]*ToolCallEntry
	toolCallOrder  []string
	llmCalls       []*LLMCallEntry
	activeLLMCalls []*LLMCallEntry
}

// ToolCallEntry represents a captured tool call for logging purposes.
type ToolCallEntry struct {
	ToolCallID  string        `json:"tool_call_id"`
	ToolName    string        `json:"tool_name"`
	Args        string        `json:"args,omitempty"`
	Result      string        `json:"result,omitempty"`
	Error       string        `json:"error,omitempty"`
	Duration    time.Duration `json:"duration,omitempty"`
	StepID      string        `json:"step_id,omitempty"`
	Timestamp   time.Time     `json:"timestamp"`
	StartedAt   time.Time     `json:"started_at,omitempty"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
}

// LLMCallEntry represents a captured LLM call for timing/debug logging.
type LLMCallEntry struct {
	Turn                  int           `json:"turn,omitempty"`
	ModelID               string        `json:"model_id,omitempty"`
	Status                string        `json:"status"`
	Error                 string        `json:"error,omitempty"`
	StartedAt             time.Time     `json:"started_at"`
	CompletedAt           time.Time     `json:"completed_at,omitempty"`
	Duration              time.Duration `json:"duration,omitempty"`
	FirstResponseAt       time.Time     `json:"first_response_at,omitempty"`
	TimeToFirstResponse   time.Duration `json:"time_to_first_response,omitempty"`
	FirstContentAt        time.Time     `json:"first_content_at,omitempty"`
	TimeToFirstContent    time.Duration `json:"time_to_first_content,omitempty"`
	FirstToolCallAt       time.Time     `json:"first_tool_call_at,omitempty"`
	TimeToFirstToolCall   time.Duration `json:"time_to_first_tool_call,omitempty"`
	ToolCalls             int           `json:"tool_calls,omitempty"`
	PromptTokens          int           `json:"prompt_tokens,omitempty"`
	CompletionTokens      int           `json:"completion_tokens,omitempty"`
	TotalTokens           int           `json:"total_tokens,omitempty"`
	CacheTokens           int           `json:"cache_tokens,omitempty"`
	ReasoningTokens       int           `json:"reasoning_tokens,omitempty"`
	ContextUsagePercent   float64       `json:"context_usage_percent,omitempty"`
	ModelContextWindow    int           `json:"model_context_window,omitempty"`
	FixedThresholdPercent float64       `json:"fixed_threshold_percent,omitempty"`
}

// TimingCaptureSnapshot is a per-attempt timing snapshot used by workflow logs.
type TimingCaptureSnapshot struct {
	ToolCalls []ToolCallEntry `json:"tool_calls,omitempty"`
	LLMCalls  []LLMCallEntry  `json:"llm_calls,omitempty"`
}

// ContextAwareEventBridge wraps an existing AgentEventListener and adds orchestrator context
type ContextAwareEventBridge struct {
	underlyingBridge mcpagent.AgentEventListener
	tokenPersister   TokenPersister // Interface for persisting token usage
	iterationFolder  string         // Current iteration folder for persistence
	// transcriptWriter persists background-agent events into their durable
	// PLAT-164 transcript. nil when this bridge is not backing a background
	// execution's orchestrator (e.g. tests) — appends are then a no-op.
	transcriptWriter BackgroundAgentTranscriptWriter
	// executionID is one immutable ID minted for the lifetime of this bridge
	// instance (PLAT-031), i.e. for the whole run — stamped onto every step
	// cost event so the cost ledger keeps one execution's identity intact
	// even when its writes land in two different UTC date-shard files.
	executionID      string
	currentPhase     string
	currentStep      int    // Step index within the current phase
	currentStepID    string // Step ID (e.g., "fetch-data", "process-results")
	currentStepType  string // Plan step type (e.g., "regular", "todo_task", "routing")
	currentAgentName string

	// Rich step-context fields — surfaced on every event's metadata so
	// the terminal pane / inspector / cost ledger can render a useful
	// header instead of the bare step_id. Set together via
	// PushContextRich / SetRichStepContext.
	currentStepName      string // Human-readable step title (from plan.json's "title")
	currentStepIndex     int    // 1-based position of this step in the plan
	currentStepTotal     int    // Total number of steps in the plan
	currentParentStepID  string // ID of the step that triggered this one (nested/sub-agents)
	currentAttempt       int    // 1-based retry counter for this step
	currentExecutionMode string // "scripted" | "agentic" — the declared execution mode (legacy: "learn_code" | "code_exec")
	currentTransport     string // "tmux" | "structured" — coding-agent CLI transport for this step
	currentTriggeredBy   string // What invoked this step: "workflow_executor" | "run_full_workflow" | "execute_step" | "parent_step:X"

	// Batch execution context (for batch progress tracking in frontend)
	currentGroupName string // Current group name being executed
	currentGroupIdx  int    // 0-based index of current group
	totalGroups      int    // Total number of groups in batch
	// Context stack for nested agent execution (e.g., orchestrator -> sub-agent)
	contextStack []orchestratorContext
	// Timing collector — captures tool and LLM timing events for workspace logging
	timingCaptures  map[string]*timingCaptureState
	timingCaptureID atomic.Uint64
	mu              sync.RWMutex
	logger          loggerv2.Logger
	tokenPersistWG  sync.WaitGroup
	tokenPersistMu  sync.Mutex
	tokenPersistErr []error

	// Transcript persistence, mirroring the token-persistence fields above so
	// a caller can independently drain either one.
	transcriptPersistWG  sync.WaitGroup
	transcriptPersistMu  sync.Mutex
	transcriptPersistErr []error
}

const tokenPersistenceTimeout = 30 * time.Second

func (c *ContextAwareEventBridge) persistTokenUsageAsync(label string, persist func(context.Context) error) {
	c.tokenPersistWG.Add(1)
	go func() {
		defer c.tokenPersistWG.Done()
		persistCtx, cancel := context.WithTimeout(context.Background(), tokenPersistenceTimeout)
		defer cancel()
		if err := persist(persistCtx); err != nil {
			wrapped := fmt.Errorf("%s: %w", label, err)
			c.tokenPersistMu.Lock()
			c.tokenPersistErr = append(c.tokenPersistErr, wrapped)
			c.tokenPersistMu.Unlock()
			c.logger.Warn(fmt.Sprintf("Failed to persist token usage: %v", wrapped))
		}
	}()
}

// WaitForTokenPersistence drains token writes before a workflow is finalized.
// It returns any persistence failures separately from the workflow result.
func (c *ContextAwareEventBridge) WaitForTokenPersistence(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		c.tokenPersistWG.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for token persistence: %w", ctx.Err())
	case <-done:
	}
	c.tokenPersistMu.Lock()
	defer c.tokenPersistMu.Unlock()
	if len(c.tokenPersistErr) == 0 {
		return nil
	}
	messages := make([]string, 0, len(c.tokenPersistErr))
	for _, err := range c.tokenPersistErr {
		messages = append(messages, err.Error())
	}
	c.tokenPersistErr = nil
	return fmt.Errorf("token persistence failed: %s", strings.Join(messages, "; "))
}

func (c *ContextAwareEventBridge) persistTranscriptAsync(label string, persist func(context.Context) error) {
	c.transcriptPersistWG.Add(1)
	go func() {
		defer c.transcriptPersistWG.Done()
		persistCtx, cancel := context.WithTimeout(context.Background(), tokenPersistenceTimeout)
		defer cancel()
		if err := persist(persistCtx); err != nil {
			wrapped := fmt.Errorf("%s: %w", label, err)
			c.transcriptPersistMu.Lock()
			c.transcriptPersistErr = append(c.transcriptPersistErr, wrapped)
			c.transcriptPersistMu.Unlock()
			c.logger.Warn(fmt.Sprintf("Failed to persist background agent transcript event: %v", wrapped))
		}
	}()
}

// WaitForBackgroundTranscriptPersistence drains transcript-event writes,
// mirroring WaitForTokenPersistence. Independent of it — a caller that only
// cares about one need not wait on the other.
func (c *ContextAwareEventBridge) WaitForBackgroundTranscriptPersistence(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		c.transcriptPersistWG.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for background transcript persistence: %w", ctx.Err())
	case <-done:
	}
	c.transcriptPersistMu.Lock()
	defer c.transcriptPersistMu.Unlock()
	if len(c.transcriptPersistErr) == 0 {
		return nil
	}
	messages := make([]string, 0, len(c.transcriptPersistErr))
	for _, err := range c.transcriptPersistErr {
		messages = append(messages, err.Error())
	}
	c.transcriptPersistErr = nil
	return fmt.Errorf("background transcript persistence failed: %s", strings.Join(messages, "; "))
}

// Name implements the EventBridge interface
func (c *ContextAwareEventBridge) Name() string {
	return "context_aware_bridge"
}

// NewContextAwareEventBridge creates a new context-aware event bridge
func NewContextAwareEventBridge(underlyingBridge mcpagent.AgentEventListener, logger loggerv2.Logger) *ContextAwareEventBridge {
	return &ContextAwareEventBridge{
		underlyingBridge: underlyingBridge,
		logger:           logger,
		executionID:      uuid.NewString(),
	}
}

// ExecutionID returns the immutable ID minted for this run (PLAT-031).
func (c *ContextAwareEventBridge) ExecutionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.executionID
}

// SetLogger updates the logger used by the bridge so workflow/group scoping can
// follow the active orchestrator execution context.
func (c *ContextAwareEventBridge) SetLogger(logger loggerv2.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

// SetTokenPersister sets the token persister (no longer using accumulators)
func (c *ContextAwareEventBridge) SetTokenPersister(persister TokenPersister) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokenPersister = persister
}

// SetBackgroundAgentTranscriptWriter sets the writer HandleEvent appends
// background-agent events to (PLAT-164).
func (c *ContextAwareEventBridge) SetBackgroundAgentTranscriptWriter(writer BackgroundAgentTranscriptWriter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transcriptWriter = writer
}

// SetIterationFolder sets the current iteration folder for token persistence
func (c *ContextAwareEventBridge) SetIterationFolder(iterationFolder string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.iterationFolder = iterationFolder
	c.logger.Debug(fmt.Sprintf("📁 Set iteration folder for token persistence: %s", iterationFolder))
}

// SetOrchestratorContext sets the current orchestrator context
func (c *ContextAwareEventBridge) SetOrchestratorContext(phase string, step int, stepID string, agentName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentPhase = phase
	c.currentStep = step
	c.currentStepID = stepID
	c.currentStepType = ""
	c.currentAgentName = agentName

	c.logger.Info(fmt.Sprintf("🎯 Set orchestrator context: %s (step %d, ID: %s)", phase, step+1, stepID))
}

// RichStepContext is the bundle of fields the orchestrator can attach
// to the current step so downstream consumers (terminal pane, inspector,
// cost ledger) render an informative header instead of just step_id.
// Pass on PushContextRich; all fields are optional.
type RichStepContext struct {
	StepName      string // Human-readable title from plan.json
	StepType      string // Plan step type: regular, todo_task, routing, etc.
	StepIndex     int    // 1-based position in the plan
	StepTotal     int    // Total steps in the plan
	ParentStepID  string // Triggering step id (nested workflow / sub-agent)
	Attempt       int    // 1-based retry counter
	ExecutionMode string // "scripted" | "agentic"; persisted older values are normalized on read
	Transport     string // "tmux" | "structured"
	TriggeredBy   string // "workflow_executor" | "run_full_workflow" | "execute_step" | "parent_step:X"
}

// SetRichStepContext attaches the richer step envelope to the current
// context. Safe to call any number of times; later calls overwrite. All
// fields are optional — empty/zero values are simply not injected into
// downstream metadata.
func (c *ContextAwareEventBridge) SetRichStepContext(ctx RichStepContext) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentStepName = ctx.StepName
	if ctx.StepType != "" {
		c.currentStepType = strings.TrimSpace(ctx.StepType)
	}
	c.currentStepIndex = ctx.StepIndex
	c.currentStepTotal = ctx.StepTotal
	c.currentParentStepID = ctx.ParentStepID
	c.currentAttempt = ctx.Attempt
	c.currentExecutionMode = ctx.ExecutionMode
	c.currentTransport = ctx.Transport
	c.currentTriggeredBy = ctx.TriggeredBy
}

// MergeRichStepContext is like SetRichStepContext but only overwrites
// fields that are non-zero in ctx. Useful when different layers
// populate different fields (e.g. the controller knows the execution
// mode + transport from step config, while PushContext set the step
// name + parent_step_id earlier).
func (c *ContextAwareEventBridge) MergeRichStepContext(ctx RichStepContext) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.StepName != "" {
		c.currentStepName = ctx.StepName
	}
	if ctx.StepType != "" {
		c.currentStepType = strings.TrimSpace(ctx.StepType)
	}
	if ctx.StepIndex > 0 {
		c.currentStepIndex = ctx.StepIndex
	}
	if ctx.StepTotal > 0 {
		c.currentStepTotal = ctx.StepTotal
	}
	if ctx.ParentStepID != "" {
		c.currentParentStepID = ctx.ParentStepID
	}
	if ctx.Attempt > 0 {
		c.currentAttempt = ctx.Attempt
	}
	if ctx.ExecutionMode != "" {
		c.currentExecutionMode = ctx.ExecutionMode
	}
	if ctx.Transport != "" {
		c.currentTransport = ctx.Transport
	}
	if ctx.TriggeredBy != "" {
		c.currentTriggeredBy = ctx.TriggeredBy
	}
}

// PushContext saves the current context to the stack and sets a new context
// Use this before executing a sub-agent to preserve the parent context.
// The new context's rich-step fields (StepName, StepIndex, etc.) are
// cleared; populate them via SetRichStepContext after pushing, or use
// PushContextRich to do both at once.
func (c *ContextAwareEventBridge) PushContext(phase string, step int, stepID string, agentName string) {
	c.pushContextInternal(phase, step, stepID, agentName, RichStepContext{})
}

// PushContextRich is PushContext + SetRichStepContext in one call,
// avoiding a window where the bridge has the new step id without its
// title/index/parent context.
func (c *ContextAwareEventBridge) PushContextRich(phase string, step int, stepID string, agentName string, rich RichStepContext) {
	c.pushContextInternal(phase, step, stepID, agentName, rich)
}

func (c *ContextAwareEventBridge) pushContextInternal(phase string, step int, stepID string, agentName string, rich RichStepContext) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Save current context (including rich fields) onto the stack so
	// PopContext can restore parent state when a sub-agent returns.
	c.contextStack = append(c.contextStack, orchestratorContext{
		phase:         c.currentPhase,
		step:          c.currentStep,
		stepID:        c.currentStepID,
		stepType:      c.currentStepType,
		agentName:     c.currentAgentName,
		stepName:      c.currentStepName,
		stepIndex:     c.currentStepIndex,
		stepTotal:     c.currentStepTotal,
		parentStepID:  c.currentParentStepID,
		attempt:       c.currentAttempt,
		executionMode: c.currentExecutionMode,
		transport:     c.currentTransport,
		triggeredBy:   c.currentTriggeredBy,
	})

	// Set new context
	c.currentPhase = phase
	c.currentStep = step
	c.currentStepID = stepID
	c.currentStepType = strings.TrimSpace(rich.StepType)
	c.currentAgentName = agentName
	c.currentStepName = rich.StepName
	c.currentStepIndex = rich.StepIndex
	c.currentStepTotal = rich.StepTotal
	c.currentParentStepID = rich.ParentStepID
	c.currentAttempt = rich.Attempt
	c.currentExecutionMode = rich.ExecutionMode
	c.currentTransport = rich.Transport
	c.currentTriggeredBy = rich.TriggeredBy

	c.logger.Info(fmt.Sprintf("📥 Pushed context (stack depth: %d): %s (step %d, ID: %s)", len(c.contextStack), phase, step+1, stepID))
}

// PopContext restores the previous context from the stack
// Use this after a sub-agent completes to restore the parent context
func (c *ContextAwareEventBridge) PopContext() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.contextStack) == 0 {
		c.logger.Warn("⚠️ PopContext called but context stack is empty - no context to restore")
		return
	}

	// Pop the last context from stack
	lastIdx := len(c.contextStack) - 1
	prevContext := c.contextStack[lastIdx]
	c.contextStack = c.contextStack[:lastIdx]

	// Restore previous context (basic + rich)
	c.currentStepName = prevContext.stepName
	c.currentStepIndex = prevContext.stepIndex
	c.currentStepTotal = prevContext.stepTotal
	c.currentParentStepID = prevContext.parentStepID
	c.currentAttempt = prevContext.attempt
	c.currentExecutionMode = prevContext.executionMode
	c.currentTransport = prevContext.transport
	c.currentTriggeredBy = prevContext.triggeredBy
	c.currentPhase = prevContext.phase
	c.currentStep = prevContext.step
	c.currentStepID = prevContext.stepID
	c.currentStepType = prevContext.stepType
	c.currentAgentName = prevContext.agentName

	c.logger.Info(fmt.Sprintf("📤 Popped context (stack depth: %d): restored to %s (step %d, ID: %s)", len(c.contextStack), c.currentPhase, c.currentStep+1, c.currentStepID))
}

// GetCurrentStepID returns the current step ID (for external use)
func (c *ContextAwareEventBridge) GetCurrentStepID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentStepID
}

// ClearOrchestratorContext clears the orchestrator context
func (c *ContextAwareEventBridge) ClearOrchestratorContext() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentPhase = ""
	c.currentStep = 0
	c.currentStepID = ""
	c.currentStepType = ""
	c.currentAgentName = ""

	c.logger.Info("🧹 Cleared orchestrator context")
}

// SetBatchContext sets the current batch execution context
func (c *ContextAwareEventBridge) SetBatchContext(groupName string, groupIndex int, totalGroups int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentGroupName = groupName
	c.currentGroupIdx = groupIndex
	c.totalGroups = totalGroups

	c.logger.Info(fmt.Sprintf("📦 Set batch context: group %s (%d/%d)", groupName, groupIndex+1, totalGroups))
}

// ClearBatchContext clears the batch execution context
func (c *ContextAwareEventBridge) ClearBatchContext() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentGroupName = ""
	c.currentGroupIdx = 0
	c.totalGroups = 0

	c.logger.Info("🧹 Cleared batch context")
}

// SetCurrentStepID sets the current step ID (simple version - just step ID for all events)
func (c *ContextAwareEventBridge) SetCurrentStepID(stepID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentStepID = stepID
	c.currentStepType = ""

	c.logger.Info(fmt.Sprintf("🎯 Set current step ID: %s", stepID))
}

// SetCurrentStepContext sets the current workflow plan step ID and type for all events.
func (c *ContextAwareEventBridge) SetCurrentStepContext(stepID, stepType string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentStepID = stepID
	c.currentStepType = strings.TrimSpace(stepType)

	c.logger.Info(fmt.Sprintf("🎯 Set current step context: %s (%s)", stepID, c.currentStepType))
}

// ClearCurrentStepID clears the current step ID
func (c *ContextAwareEventBridge) ClearCurrentStepID() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentStepID = ""
	c.currentStepType = ""

	c.logger.Info("🧹 Cleared current step ID")
}

func addMetadataToEvent(event *events.AgentEvent, extra map[string]any) {
	if len(extra) == 0 {
		return
	}
	eventData, ok := event.Data.(interface {
		GetBaseEventData() *events.BaseEventData
	})
	if !ok {
		return
	}
	baseData := eventData.GetBaseEventData()
	if baseData == nil {
		return
	}

	newMeta := make(map[string]any, len(baseData.Metadata)+len(extra))
	for k, v := range baseData.Metadata {
		newMeta[k] = v
	}
	for k, v := range extra {
		newMeta[k] = v
	}
	baseData.Metadata = newMeta
}

// HandleEvent implements AgentEventListener interface
func (c *ContextAwareEventBridge) HandleEvent(ctx context.Context, event *events.AgentEvent) error {
	// Tag events with correlation ID from context (for parallel agent grouping).
	// This allows the frontend to parent tool calls under their orchestrator_agent_start.
	// ForceCorrelationIDKey takes priority — it survives child agent context overwrites.
	isSubAgent, _ := ctx.Value(orchevents.IsSubAgentContextKey).(bool)
	if isSubAgent && !strings.HasPrefix(event.CorrelationID, "delegation-") {
		if forcedID, ok := ctx.Value(orchevents.ForceCorrelationIDKey).(string); ok && forcedID != "" {
			event.CorrelationID = forcedID
		} else if agentSessionID, ok := ctx.Value(AgentSessionIDKey).(string); ok && agentSessionID != "" {
			event.CorrelationID = agentSessionID
		}
	}

	if sequenceItem, ok := ctx.Value(orchevents.AgentItemContextKey).(orchevents.AgentItemContext); ok {
		addMetadataToEvent(event, map[string]any{
			"agent_item":      true,
			"agent_step_id":   sequenceItem.StepID,
			"agent_item_id":   sequenceItem.ItemID,
			"agent_item_type": sequenceItem.ItemType,
		})
	}

	// Tag events with workshop_step_id in metadata (without changing CorrelationID).
	// This lets the frontend detect "this event belongs to a workshop step execution"
	// for auto-notifications, without breaking EventHierarchy grouping.
	if forcedID, ok := ctx.Value(orchevents.ForceCorrelationIDKey).(string); ok && forcedID != "" && strings.HasPrefix(forcedID, "workshop-") {
		if baseData, ok := event.Data.(interface {
			GetBaseEventData() *events.BaseEventData
		}); ok {
			if bd := baseData.GetBaseEventData(); bd != nil {
				newMeta := make(map[string]any, len(bd.Metadata)+2)
				for k, v := range bd.Metadata {
					newMeta[k] = v
				}
				newMeta["workshop_step_id"] = forcedID
				if parentExecutionID, ok := ctx.Value(orchevents.ParentExecutionIDKey).(string); ok && strings.TrimSpace(parentExecutionID) != "" {
					newMeta["parent_execution_id"] = strings.TrimSpace(parentExecutionID)
				}
				bd.Metadata = newMeta
			}
		}
	}

	// Copy orchestrator and batch context while holding read lock
	c.mu.RLock()
	currentPhase := c.currentPhase
	currentStep := c.currentStep
	currentStepID := c.currentStepID
	currentStepType := c.currentStepType
	currentAgentName := c.currentAgentName
	currentGroupName := c.currentGroupName
	currentGroupIdx := c.currentGroupIdx
	totalGroups := c.totalGroups
	// Rich step context (populated by SetRichStepContext / PushContextRich)
	currentStepName := c.currentStepName
	currentStepIndex := c.currentStepIndex
	currentStepTotal := c.currentStepTotal
	currentParentStepID := c.currentParentStepID
	currentAttempt := c.currentAttempt
	currentExecutionMode := c.currentExecutionMode
	currentTransport := c.currentTransport
	currentTriggeredBy := c.currentTriggeredBy
	c.mu.RUnlock()

	// Parallel child executions share one bridge, so the bridge's mutable
	// process-wide context cannot identify them safely. Prefer the immutable
	// context carried by the emitting goroutine when present.
	if override, ok := ctx.Value(eventContextOverrideKey{}).(eventContextOverride); ok {
		currentPhase = override.phase
		currentStep = override.step
		currentStepID = override.stepID
		currentStepType = strings.TrimSpace(override.rich.StepType)
		currentAgentName = override.agentName
		currentStepName = override.rich.StepName
		currentStepIndex = override.rich.StepIndex
		currentStepTotal = override.rich.StepTotal
		currentParentStepID = override.rich.ParentStepID
		currentAttempt = override.rich.Attempt
		currentExecutionMode = override.rich.ExecutionMode
		currentTransport = override.rich.Transport
		currentTriggeredBy = override.rich.TriggeredBy
	}

	// Check what context we have
	hasOrchestratorContext := currentPhase != ""
	hasBatchContext := totalGroups > 0
	hasStepID := currentStepID != ""
	executionOwnerID, hasExecutionOwner := ctx.Value(orchevents.ParentExecutionIDKey).(string)
	executionOwnerID = strings.TrimSpace(executionOwnerID)
	hasExecutionOwner = hasExecutionOwner && executionOwnerID != ""

	// Step-scoped agents already get a workflow-specific orchestrator_agent_end
	// event with the final text. Forwarding generic agent_end/unified_completion
	// for the same step duplicates workflow-step output in the UI. Keep both
	// generic completion events for top-level chat/workflow-builder turns, where
	// no workflow step context is active.
	if event.Type == events.EventTypeUnifiedCompletion && hasStepID {
		c.logger.Debug(fmt.Sprintf("🔕 ContextAwareBridge: Suppressed step-scoped unified_completion for step %s", currentStepID))
		return nil
	}
	if event.Type == events.AgentEnd && hasStepID {
		c.logger.Debug(fmt.Sprintf("🔕 ContextAwareBridge: Suppressed step-scoped agent_end for step %s", currentStepID))
		return nil
	}

	// Add context to metadata if we have any context (step ID, batch, or orchestrator)
	if hasOrchestratorContext || hasBatchContext || hasStepID || hasExecutionOwner {
		c.logger.Debug(fmt.Sprintf("🔍 ContextAwareBridge: Processing event %s with step %s, batch %s", event.Type, currentStepID, currentGroupName))

		// Add context to metadata
		// We need to check if the event data has a BaseEventData field
		if eventData, ok := event.Data.(interface {
			GetBaseEventData() *events.BaseEventData
		}); ok {
			baseData := eventData.GetBaseEventData()

			// Nil check before accessing Metadata
			if baseData == nil {
				c.logger.Warn(fmt.Sprintf("⚠️ ContextAwareBridge: GetBaseEventData returned nil for event %s", event.Type))
			} else {
				// Build a NEW metadata map instead of modifying in-place.
				// Modifying an existing map while another goroutine may be iterating it
				// during JSON serialization (SSE) causes "concurrent map iteration and map write".
				// By creating a fresh map and assigning it before forwarding the event,
				// the SSE goroutine always sees a fully-populated, immutable snapshot.
				newMeta := make(map[string]any, len(baseData.Metadata)+8)
				for k, v := range baseData.Metadata {
					newMeta[k] = v
				}

				// Parallel workflow sub-agents share the bridge's step context, so the
				// step id alone is not a reliable terminal-stream owner. The execution
				// owner comes from the request context and stays goroutine-local.
				if hasExecutionOwner {
					newMeta["execution_owner_id"] = executionOwnerID
					newMeta["background_agent_id"] = executionOwnerID
					// Provider events are sometimes born with the parent turn's
					// main_agent label. The explicit goroutine-local owner is the
					// authoritative signal that this is a child execution.
					existingKind, _ := newMeta["execution_kind"].(string)
					existingKind = strings.ToLower(strings.TrimSpace(existingKind))
					if existingKind == "" || existingKind == "main_agent" || existingKind == "main" || existingKind == "chat" {
						if strings.HasPrefix(executionOwnerID, "workflow-step:") {
							newMeta["execution_kind"] = "workflow_step"
							newMeta["scope"] = "workflow_step"
						} else {
							newMeta["execution_kind"] = "background_agent"
							newMeta["scope"] = "background_agent"
						}
					}
				}

				// Add current step ID (simple tracking - which step is running)
				if hasStepID {
					newMeta["current_step_id"] = currentStepID
					if currentStepType != "" {
						newMeta["current_step_type"] = currentStepType
						newMeta["plan_step_type"] = currentStepType
					}
					// Read-only snapshots stay in the rail until the user
					// dismisses them via the X button — no auto-prune. Tmux
					// terminals: the pane scrape captured at task end is the
					// final record; tmux itself is killed quickly via
					// llmtypes.TmuxKillDelay. Synthetic terminals: in-memory
					// text buffers backed by nothing ephemeral. Either way,
					// the user controls when a snapshot leaves the rail, so
					// the bridge no longer injects terminal_retention_seconds.
				}

				// Add batch context (which group is running)
				if hasBatchContext {
					newMeta["batch_group_name"] = currentGroupName
					newMeta["batch_group_index"] = currentGroupIdx
					newMeta["batch_total_groups"] = totalGroups
				}

				// Add full orchestrator context if available (backward compat)
				if hasOrchestratorContext {
					newMeta["orchestrator_phase"] = currentPhase
					newMeta["orchestrator_step"] = currentStep
					newMeta["orchestrator_step_id"] = currentStepID
					newMeta["orchestrator_agent_name"] = currentAgentName
				}

				// Rich step context — only inject keys with non-zero
				// values so consumers can tell "field set" apart from
				// "field intentionally blank". Surfaced under both
				// "step_*" (preferred) and explicit names so existing
				// readers like terminals/store.go work without changes.
				if currentStepName != "" {
					newMeta["step_name"] = currentStepName
				}
				if currentStepIndex > 0 {
					newMeta["step_index"] = currentStepIndex
				}
				if currentStepTotal > 0 {
					newMeta["step_total"] = currentStepTotal
				}
				if currentParentStepID != "" {
					newMeta["parent_step_id"] = currentParentStepID
				}
				if currentAttempt > 0 {
					newMeta["step_attempt"] = currentAttempt
				}
				if currentExecutionMode != "" {
					newMeta["step_execution_mode"] = currentExecutionMode
				}
				if currentTransport != "" {
					newMeta["step_transport"] = currentTransport
				}
				if currentTriggeredBy != "" {
					newMeta["step_triggered_by"] = currentTriggeredBy
				}

				// A step-scoped agent's first user-role message is the generated
				// executor prompt, not a human chat message. Preserve any
				// explicit source (for example live user input) and tag only
				// otherwise-unclassified turn-zero messages. Step scope is the
				// stable signal here: parallel children carry it in the immutable
				// context even before provider transport metadata is available.
				if event.Type == events.UserMessage && hasStepID {
					if _, alreadyClassified := newMeta["source"]; !alreadyClassified {
						if message, ok := event.Data.(*events.UserMessageEvent); ok && message.Turn == 0 {
							newMeta["source"] = "execution_prompt"
						}
					}
				}

				// Atomically replace the metadata map (all writes done before assignment)
				baseData.Metadata = newMeta

				c.logger.Debug(fmt.Sprintf("✅ ContextAwareBridge: Added metadata to event %s, metadata keys count: %d", event.Type, len(baseData.Metadata)))
			}
		} else {
			c.logger.Warn(fmt.Sprintf("⚠️ ContextAwareBridge: Event data %T does not have GetBaseEventData method", event.Data))
		}
	}

	// PLAT-164: append this event to the background agent's durable
	// transcript. Scope matches the "background_agent" execution_kind tagged
	// into metadata above — an execution owner that is not a workflow step.
	// Workflow steps already get a durable per-run record for free
	// (runs/iteration-N/...); top-level chat/workflow-builder turns with no
	// execution owner at all have their own conversation persistence.
	if hasExecutionOwner && !strings.HasPrefix(executionOwnerID, "workflow-step:") {
		c.mu.RLock()
		writer := c.transcriptWriter
		c.mu.RUnlock()
		if writer != nil {
			if transcriptEvent, ok := orchevents.NormalizeBackgroundTranscriptEvent(event); ok {
				sessionID := event.SessionID
				agentID := executionOwnerID
				c.persistTranscriptAsync("background agent transcript", func(persistCtx context.Context) error {
					return writer.AppendBackgroundAgentTranscriptEvent(persistCtx, sessionID, agentID, transcriptEvent)
				})
			}
		}
	}

	// Intercept token_usage events and persist directly to file (no in-memory accumulation)
	if event.Type == events.TokenUsage {
		if tokenEvent, ok := event.Data.(*events.TokenUsageEvent); ok {
			// Extract cache tokens (separated into read and write for accurate pricing)
			// Cache reads are discounted, cache writes are premium (1.25x base rate)
			cacheTokensSeparate := extractCacheTokensSeparate(tokenEvent)

			// Extract LLM call count from event (cumulative for conversation end, 1 for single calls)
			llmCallCount := extractLLMCallCount(tokenEvent)

			// Prefer the effective model the CLI/provider actually served
			// the turn with — when the user picked an alias like "auto"
			// or "cursor-cli", or when /model swap happened mid-session,
			// the effective ID differs from the requested ModelID. Cost
			// aggregation should bucket under the real model so a
			// dashboard column "by_model" is accurate.
			effectiveModelID := effectiveModelIDFromTokenEvent(tokenEvent)
			modelIDForBucket := tokenEvent.ModelID
			if effectiveModelID != "" {
				modelIDForBucket = effectiveModelID
			}

			// Prepare model token data
			var modelTokenData *ModelTokenData
			if modelIDForBucket != "" {
				modelTokenData = &ModelTokenData{
					ModelID:          modelIDForBucket,
					Provider:         tokenEvent.Provider,
					InputTokens:      tokenEvent.PromptTokens,         // input tokens
					OutputTokens:     tokenEvent.CompletionTokens,     // output tokens
					CacheTokens:      cacheTokensSeparate.Total,       // total cache (backward compat)
					CacheReadTokens:  cacheTokensSeparate.ReadTokens,  // cache reads (discounted)
					CacheWriteTokens: cacheTokensSeparate.WriteTokens, // cache writes (premium 1.25x)
					ReasoningTokens:  tokenEvent.ReasoningTokens,
					LLMCallCount:     llmCallCount, // Extract actual call count from event
				}
			}

			// Check if this is a phase-only agent (step == 0 and phase is a phase-only agent)
			isPhaseOnly := currentPhase != "" && currentStep == 0 && IsPhaseOnlyAgent(currentPhase)

			if isPhaseOnly {
				// Phase-only agent: persist to main workspace folder (phase-level tracking)
				var phaseTokenData *PhaseTokenData
				if currentPhase != "" {
					phaseTokenData = &PhaseTokenData{
						Phase:            currentPhase,
						InputTokens:      tokenEvent.PromptTokens,         // input tokens
						OutputTokens:     tokenEvent.CompletionTokens,     // output tokens
						CacheTokens:      cacheTokensSeparate.Total,       // total cache (backward compat)
						CacheReadTokens:  cacheTokensSeparate.ReadTokens,  // cache reads (discounted)
						CacheWriteTokens: cacheTokensSeparate.WriteTokens, // cache writes (premium 1.25x)
						ReasoningTokens:  tokenEvent.ReasoningTokens,
						LLMCallCount:     llmCallCount, // Extract actual call count from event
					}
				}

				// Persist phase token usage directly to file (real-time persistence, no accumulation)
				c.mu.RLock()
				persister := c.tokenPersister
				c.mu.RUnlock()

				if persister != nil {
					// Check if persister implements PhaseTokenPersister interface
					if phasePersister, ok := persister.(PhaseTokenPersister); ok {
						c.persistTokenUsageAsync("phase token usage", func(persistCtx context.Context) error {
							return phasePersister.PersistPhaseTokenUsage(persistCtx, phaseTokenData, modelTokenData)
						})
					} else {
						c.logger.Debug("⚠️ Token persister does not implement PhaseTokenPersister, skipping phase token persistence")
					}
				}
			} else {
				// Step-based agent: persist to iteration folder (existing behavior)
				// A run-level coordinator also emits token usage, but it has no
				// current plan step. Persist it under an explicit attribution key
				// instead of leaving by_step_and_model empty; an empty map made the
				// cost UI misleadingly present these calls as the run folder itself.
				attributedPhase := currentPhase
				attributedStepID := currentStepID
				if strings.TrimSpace(attributedPhase) == "" {
					if strings.TrimSpace(attributedStepID) != "" {
						attributedPhase = "execution_only"
					} else {
						attributedPhase = "workflow_orchestrator"
						attributedStepID = "workflow_orchestrator"
					}
				}
				// A bare numeric ID (e.g. a schedule-message counter) is never a
				// real planning/plan.json step ID in this codebase — plan step
				// IDs are descriptive slugs. Keep it out of a step-shaped phase
				// bucket so the cost ledger never claims plan-step provenance it
				// doesn't have (PLAT-031).
				if numericOnlyIdentifierPattern.MatchString(strings.TrimSpace(attributedStepID)) {
					attributedPhase = "schedule_message"
				}
				c.mu.RLock()
				executionID := c.executionID
				c.mu.RUnlock()
				stepTokenData := &StepTokenData{
					Phase:            attributedPhase,
					Step:             currentStep,
					StepID:           attributedStepID,                // Use step ID instead of index
					InputTokens:      tokenEvent.PromptTokens,         // input tokens
					OutputTokens:     tokenEvent.CompletionTokens,     // output tokens
					CacheTokens:      cacheTokensSeparate.Total,       // total cache (backward compat)
					CacheReadTokens:  cacheTokensSeparate.ReadTokens,  // cache reads (discounted)
					CacheWriteTokens: cacheTokensSeparate.WriteTokens, // cache writes (premium 1.25x)
					ReasoningTokens:  tokenEvent.ReasoningTokens,
					LLMCallCount:     llmCallCount, // Extract actual call count from event
					ExecutionID:      executionID,
				}

				// Persist token usage directly to file (real-time persistence, no accumulation)
				c.mu.RLock()
				persister := c.tokenPersister
				iterationFolder := c.iterationFolder
				c.mu.RUnlock()

				if persister != nil && iterationFolder != "" {
					c.persistTokenUsageAsync("step token usage", func(persistCtx context.Context) error {
						return persister.PersistTokenUsage(persistCtx, iterationFolder, stepTokenData, modelTokenData)
					})
				}
			}
		}
	}

	// Capture tool/LLM timing in the execution-local collector. This prevents
	// parallel children from draining or resetting each other's timing data.
	if event.Data != nil {
		c.captureToolCallEvent(ctx, event, currentStepID)
	}

	if c.underlyingBridge == nil {
		c.logger.Error(fmt.Sprintf("❌ ContextAwareBridge: Underlying bridge is nil, cannot forward event %s", event.Type), nil)
		return fmt.Errorf("underlying bridge is nil")
	}
	err := c.underlyingBridge.HandleEvent(ctx, event)
	if err != nil {
		c.logger.Warn(fmt.Sprintf("⚠️ ContextAwareBridge: Error forwarding event %s: %v", event.Type, err))
	}
	return err
}

const defaultTimingCaptureID = "default"

func timingCaptureIDFromContext(ctx context.Context) string {
	if ctx != nil {
		if captureID, ok := ctx.Value(timingCaptureContextKey{}).(string); ok && captureID != "" {
			return captureID
		}
	}
	return defaultTimingCaptureID
}

func (c *ContextAwareEventBridge) startTimingCapture(captureID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timingCaptures == nil {
		c.timingCaptures = make(map[string]*timingCaptureState)
	}
	c.timingCaptures[captureID] = &timingCaptureState{
		toolCalls: make(map[string]*ToolCallEntry),
	}
}

// StartTimingCapture enables the legacy single-lane timing collector.
// Parallel execution paths should use StartTimingCaptureFor.
func (c *ContextAwareEventBridge) StartTimingCapture() {
	c.startTimingCapture(defaultTimingCaptureID)
}

// StartTimingCaptureFor creates a collector owned by the returned context.
// Events emitted with another context cannot enter or drain this collector.
func (c *ContextAwareEventBridge) StartTimingCaptureFor(ctx context.Context) context.Context {
	captureID := fmt.Sprintf("capture-%d", c.timingCaptureID.Add(1))
	c.startTimingCapture(captureID)
	return context.WithValue(ctx, timingCaptureContextKey{}, captureID)
}

// CopyTimingCaptureContext propagates src's active timing-capture identity
// (if any) onto dst. Use this whenever a child execution context is built
// from something other than the in-flight tool-call context — e.g. an async
// sub-agent's detached context, which is otherwise assembled from the step's
// long-lived base context — so the child's LLM/tool timing events still land
// in the collector the parent step will drain instead of a different one
// (PLAT-032: child calls were silently missing from parent step telemetry).
func CopyTimingCaptureContext(dst, src context.Context) context.Context {
	if dst == nil || src == nil {
		return dst
	}
	if captureID, ok := src.Value(timingCaptureContextKey{}).(string); ok && captureID != "" {
		return context.WithValue(dst, timingCaptureContextKey{}, captureID)
	}
	return dst
}

func (c *ContextAwareEventBridge) drainTimingCapture(captureID string) TimingCaptureSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.timingCaptures[captureID]
	if state == nil {
		return TimingCaptureSnapshot{}
	}

	result := TimingCaptureSnapshot{
		ToolCalls: make([]ToolCallEntry, 0, len(state.toolCallOrder)),
		LLMCalls:  make([]LLMCallEntry, 0, len(state.llmCalls)),
	}
	for _, id := range state.toolCallOrder {
		if tc, ok := state.toolCalls[id]; ok {
			result.ToolCalls = append(result.ToolCalls, *tc)
		}
	}
	for _, llmCall := range state.llmCalls {
		if llmCall != nil {
			result.LLMCalls = append(result.LLMCalls, *llmCall)
		}
	}
	delete(c.timingCaptures, captureID)
	return result
}

// DrainTimingCapture returns the legacy single-lane timing data.
func (c *ContextAwareEventBridge) DrainTimingCapture() TimingCaptureSnapshot {
	return c.drainTimingCapture(defaultTimingCaptureID)
}

// DrainTimingCaptureFor returns only timing data owned by ctx.
func (c *ContextAwareEventBridge) DrainTimingCaptureFor(ctx context.Context) TimingCaptureSnapshot {
	return c.drainTimingCapture(timingCaptureIDFromContext(ctx))
}

func (c *ContextAwareEventBridge) captureToolCallEvent(ctx context.Context, event *events.AgentEvent, stepID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.timingCaptures[timingCaptureIDFromContext(ctx)]
	if state == nil {
		return
	}

	eventTime := event.Timestamp
	if eventTime.IsZero() {
		eventTime = time.Now()
	}

	switch d := event.Data.(type) {
	case *events.LLMGenerationStartEvent:
		entry := &LLMCallEntry{
			Turn:      d.Turn,
			ModelID:   d.ModelID,
			Status:    "running",
			StartedAt: eventTime,
		}
		state.llmCalls = append(state.llmCalls, entry)
		state.activeLLMCalls = append(state.activeLLMCalls, entry)
	case *events.StreamingChunkEvent:
		if len(state.activeLLMCalls) == 0 || d.Content == "" {
			return
		}
		current := state.activeLLMCalls[0]
		if current.FirstResponseAt.IsZero() {
			current.FirstResponseAt = eventTime
			current.TimeToFirstResponse = eventTime.Sub(current.StartedAt)
		}
		if current.FirstContentAt.IsZero() {
			current.FirstContentAt = eventTime
			current.TimeToFirstContent = eventTime.Sub(current.StartedAt)
		}
	case *events.ToolCallStartEvent:
		if _, exists := state.toolCalls[d.ToolCallID]; !exists {
			state.toolCallOrder = append(state.toolCallOrder, d.ToolCallID)
		}
		state.toolCalls[d.ToolCallID] = &ToolCallEntry{
			ToolCallID: d.ToolCallID,
			ToolName:   d.ToolName,
			Args:       d.ToolParams.Arguments,
			StepID:     stepID,
			Timestamp:  eventTime,
			StartedAt:  eventTime,
		}
		if len(state.activeLLMCalls) > 0 {
			current := state.activeLLMCalls[0]
			if current.FirstResponseAt.IsZero() {
				current.FirstResponseAt = eventTime
				current.TimeToFirstResponse = eventTime.Sub(current.StartedAt)
			}
			if current.FirstToolCallAt.IsZero() {
				current.FirstToolCallAt = eventTime
				current.TimeToFirstToolCall = eventTime.Sub(current.StartedAt)
			}
		}
	case *events.ToolCallEndEvent:
		if tc, ok := state.toolCalls[d.ToolCallID]; ok {
			tc.Result = d.Result
			tc.Duration = d.Duration
			tc.CompletedAt = eventTime
		} else {
			state.toolCallOrder = append(state.toolCallOrder, d.ToolCallID)
			state.toolCalls[d.ToolCallID] = &ToolCallEntry{
				ToolCallID:  d.ToolCallID,
				ToolName:    d.ToolName,
				Result:      d.Result,
				Duration:    d.Duration,
				StepID:      stepID,
				Timestamp:   eventTime,
				StartedAt:   eventTime.Add(-d.Duration),
				CompletedAt: eventTime,
			}
		}
	case *events.ToolCallErrorEvent:
		if tc, ok := state.toolCalls[d.ToolCallID]; ok {
			tc.Error = d.Error
			tc.Duration = d.Duration
			tc.CompletedAt = eventTime
		} else {
			state.toolCallOrder = append(state.toolCallOrder, d.ToolCallID)
			state.toolCalls[d.ToolCallID] = &ToolCallEntry{
				ToolCallID:  d.ToolCallID,
				ToolName:    d.ToolName,
				Error:       d.Error,
				Duration:    d.Duration,
				StepID:      stepID,
				Timestamp:   eventTime,
				StartedAt:   eventTime.Add(-d.Duration),
				CompletedAt: eventTime,
			}
		}
	case *events.LLMGenerationEndEvent:
		entry := consumeActiveLLMCall(state)
		if entry == nil {
			entry = &LLMCallEntry{
				Status:    "success",
				StartedAt: eventTime.Add(-d.Duration),
			}
			state.llmCalls = append(state.llmCalls, entry)
		}
		entry.Status = "success"
		if d.Turn != 0 {
			entry.Turn = d.Turn
		}
		entry.CompletedAt = eventTime
		entry.Duration = d.Duration
		entry.ToolCalls = d.ToolCalls
		entry.PromptTokens = d.UsageMetrics.PromptTokens
		entry.CompletionTokens = d.UsageMetrics.CompletionTokens
		entry.TotalTokens = d.UsageMetrics.TotalTokens
		entry.CacheTokens = d.UsageMetrics.CacheTokens
		entry.ReasoningTokens = d.UsageMetrics.ReasoningTokens
		if entry.ModelID == "" {
			entry.ModelID = extractStringMetadata(d.Metadata, "resolved_model", "model_id", "gemini_model", "claude_code_model")
		}
		entry.ContextUsagePercent = extractFloatMetadata(d.Metadata, "context_usage_percent")
		entry.FixedThresholdPercent = extractFloatMetadata(d.Metadata, "fixed_threshold_percent")
		entry.ModelContextWindow = extractIntMetadata(d.Metadata, "model_context_window")
		c.finalizeLLMEntryTiming(entry)
	case *events.LLMGenerationErrorEvent:
		entry := consumeActiveLLMCall(state)
		if entry == nil {
			entry = &LLMCallEntry{
				StartedAt: eventTime.Add(-d.Duration),
			}
			state.llmCalls = append(state.llmCalls, entry)
		}
		entry.Status = "error"
		if d.Turn != 0 {
			entry.Turn = d.Turn
		}
		if entry.ModelID == "" {
			entry.ModelID = d.ModelID
		}
		entry.Error = d.Error
		entry.CompletedAt = eventTime
		entry.Duration = d.Duration
		c.finalizeLLMEntryTiming(entry)
	case *events.ContextCancelledEvent:
		entry := consumeActiveLLMCall(state)
		if entry == nil {
			return
		}
		entry.Status = "canceled"
		if d.Turn != 0 {
			entry.Turn = d.Turn
		}
		entry.Error = d.Reason
		entry.CompletedAt = eventTime
		entry.Duration = d.Duration
		c.finalizeLLMEntryTiming(entry)
	}
}

func consumeActiveLLMCall(state *timingCaptureState) *LLMCallEntry {
	if state == nil || len(state.activeLLMCalls) == 0 {
		return nil
	}
	entry := state.activeLLMCalls[0]
	state.activeLLMCalls = state.activeLLMCalls[1:]
	return entry
}

func (c *ContextAwareEventBridge) finalizeLLMEntryTiming(entry *LLMCallEntry) {
	if entry == nil {
		return
	}
	if entry.CompletedAt.IsZero() {
		entry.CompletedAt = entry.StartedAt
	}
	if entry.Duration <= 0 && !entry.StartedAt.IsZero() && !entry.CompletedAt.IsZero() {
		entry.Duration = entry.CompletedAt.Sub(entry.StartedAt)
	}
	if entry.TimeToFirstResponse <= 0 && !entry.FirstResponseAt.IsZero() && !entry.StartedAt.IsZero() {
		entry.TimeToFirstResponse = entry.FirstResponseAt.Sub(entry.StartedAt)
	}
	if entry.TimeToFirstContent <= 0 && !entry.FirstContentAt.IsZero() && !entry.StartedAt.IsZero() {
		entry.TimeToFirstContent = entry.FirstContentAt.Sub(entry.StartedAt)
	}
	if entry.TimeToFirstToolCall <= 0 && !entry.FirstToolCallAt.IsZero() && !entry.StartedAt.IsZero() {
		entry.TimeToFirstToolCall = entry.FirstToolCallAt.Sub(entry.StartedAt)
	}
	if entry.FirstResponseAt.IsZero() && !entry.CompletedAt.IsZero() && (entry.ToolCalls > 0 || entry.Duration > 0) {
		entry.FirstResponseAt = entry.CompletedAt
		entry.TimeToFirstResponse = entry.CompletedAt.Sub(entry.StartedAt)
	}
	if entry.FirstContentAt.IsZero() && !entry.CompletedAt.IsZero() && entry.CompletionTokens > 0 {
		entry.FirstContentAt = entry.CompletedAt
		entry.TimeToFirstContent = entry.CompletedAt.Sub(entry.StartedAt)
	}
	if entry.FirstToolCallAt.IsZero() && !entry.CompletedAt.IsZero() && entry.ToolCalls > 0 {
		entry.FirstToolCallAt = entry.CompletedAt
		entry.TimeToFirstToolCall = entry.CompletedAt.Sub(entry.StartedAt)
	}
}

func extractStringMetadata(meta map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := meta[key]; ok {
			if asString, ok := value.(string); ok && strings.TrimSpace(asString) != "" {
				return asString
			}
		}
	}
	return ""
}

func extractFloatMetadata(meta map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := meta[key]; ok {
			switch v := value.(type) {
			case float64:
				return v
			case float32:
				return float64(v)
			case int:
				return float64(v)
			case int64:
				return float64(v)
			}
		}
	}
	return 0
}

func extractIntMetadata(meta map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if value, ok := meta[key]; ok {
			switch v := value.(type) {
			case int:
				return v
			case int64:
				return int(v)
			case float64:
				return int(v)
			case float32:
				return int(v)
			}
		}
	}
	return 0
}
