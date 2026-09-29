package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	mcpllm "github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/mcpagent/toolcalllog"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	agentlogger "github.com/manishiitg/coding-agent-loop/agent_go/pkg/logger"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// LLMAgentWrapper wraps the complex MCP Agent to provide a simple LLM-like interface
type LLMAgentWrapper struct {
	agent      *mcpagent.Agent
	session    *mcpagent.Session
	name       string
	mu         sync.RWMutex
	closed     bool
	config     LLMAgentConfig
	metrics    *agentMetricsImpl
	tracer     observability.Tracer
	traceID    observability.TraceID
	logger     loggerv2.Logger
	runtime    mcpagent.RuntimeConfig
	finalized  bool
	definition mcpagent.AgentDefinition
	observers  []mcpagent.AgentEventListener
	// installedSkillResolver belongs to the finalized agent, not the temporary
	// bootstrap agent created by NewLLMAgentWrapper. Keeping it on the wrapper
	// lets FinalizeDefinition carry progressive skill reads across replacement.
	installedSkillResolver mcpagent.InstalledSkillResolver
	// admitTool, when set, decides whether a tool may join the definition at
	// all. Fixed at construction from LLMAgentConfig.AdmitTool.
	admitTool func(string) bool

	// In-memory conversation history for multi-turn state
	history    []llmtypes.MessageContent
	lastResult mcpagent.Result
}

func providerNeedsPlainTextHistory(provider llm.Provider) bool {
	return common.IsCLIProvider(string(provider))
}

func configuredServerNames(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "all" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			result = append(result, name)
		}
	}
	return result
}

func runtimeBool(value bool) *bool { return &value }

func runtimeConfigForLLMAgent(config LLMAgentConfig, model llmtypes.Model, tracer observability.Tracer, traceID observability.TraceID, logger loggerv2.Logger) mcpagent.RuntimeConfig {
	llmConfig := mcpagent.AgentLLMConfiguration{
		Primary: mcpagent.LLMModel{ConnectionID: config.ConnectionID, Provider: string(config.Provider), ModelID: config.ModelID, Options: config.Options},
	}

	runtime := mcpagent.RuntimeConfig{
		Model: model, MCPConfigPath: config.ConfigPath,
		Generation: mcpagent.GenerationRuntimeConfig{
			Provider: config.Provider, LLM: llmConfig, Temperature: config.Temperature,
			ToolChoice: config.ToolChoice, MaxTurns: config.MaxTurns, APIKeys: config.APIKeys,
		},
		Tools: mcpagent.ToolRuntimeConfig{
			SelectedTools: config.SelectedTools, SelectedServers: configuredServerNames(config.ServerName),
			CodeExecution: config.UseCodeExecutionMode, ParallelExecution: config.EnableParallelToolExecution,
			Timeout: config.ToolTimeout, AdditionalBridge: config.AdditionalBridgeTools,
		},
		Context: mcpagent.ContextRuntimeConfig{
			LargeOutputThreshold: config.LargeOutputThreshold,
		},
		Coding: mcpagent.CodingRuntimeConfig{
			ClaudeCodeTransport:               config.ClaudeCodeTransport,
			PersistentClaudeCode:              config.ClaudeCodePersistentInteractiveSession,
			PersistentCodex:                   config.CodexPersistentInteractiveSession,
			PersistentCursor:                  config.CursorPersistentInteractiveSession,
			PersistentPi:                      config.PiPersistentInteractiveSession,
			PersistentMuse:                    config.MusePersistentInteractiveSession,
			PersistentAgy:                     config.AgyPersistentInteractiveSession,
			UserAnswersNativeQuestions:        config.CodingAgentUserAnswersNativeQuestions,
			CursorBridgeTools:                 config.CursorBridgeToolsMode,
			AgentToolsMode:                    config.CodingAgentToolsMode,
			ApprovalsMode:                     config.CodingAgentApprovalsMode,
			BridgeRoutingInstructionsOverride: config.BridgeRoutingInstructionsOverride,
			CLISecurityPolicy:                 config.CLISecurityPolicy,
			SecretEnvironment:                 config.CodingAgentSecretEnvironment,
			CodexNetworkAccess:                config.CodexNetworkAccess,
			// The same predicate that admits tool registration also decides what
			// the coding-agent bridge advertises, so its catalog can never offer
			// a tool this session would refuse to execute.
			BridgeToolAdmit: config.AdmitTool,
		},
		MCP: mcpagent.MCPRuntimeConfig{
			SessionID: config.SessionID, UserID: config.UserID, RuntimeOverrides: config.RuntimeOverrides,
		},
		Workspace: mcpagent.WorkspaceRuntimeConfig{CodingAgentWorkingDir: config.CodingAgentWorkingDir},
		Observability: mcpagent.ObservabilityRuntimeConfig{
			Logger: logger, Tracers: []observability.Tracer{tracer}, TraceID: traceID,
			// GenerationStreamingEvents true is what makes clean per-token content
			// (StreamChunkTypeContent -> StreamingChunkEvent, Source != terminal)
			// actually reach the frontend. With it false, ONLY raw terminal-pane
			// snapshot chunks survive (llm_generation.go exempts those explicitly) --
			// fine for tmux providers, which still have a pane to show, but for a
			// structured-transport provider with no pane (pi-cli) it meant ZERO
			// streaming_chunk events for the whole turn: confirmed live, a 30s
			// pi-cli/gemini turn produced none. The frontend now honors is_delta /
			// source correctly (useChatStore.appendStreamingText), which was the
			// other half of why this was left off.
			PromptLogLabel: config.Name, Streaming: true, GenerationStreamingEvents: runtimeBool(true),
			DirectToolExecutionEvents: true,
		},
	}
	if config.ForceStructuredCodingAgent {
		runtime.Coding.Transport = mcpllm.CodingAgentTransportStructured
	}
	return runtime
}

func sanitizeHistoryForPlainTextProvider(messages []llmtypes.MessageContent) []llmtypes.MessageContent {
	sanitized := make([]llmtypes.MessageContent, 0, len(messages)+2)
	for _, msg := range messages {
		normalized := normalizeMessageForPlainTextProvider(msg)
		if len(sanitized) > 0 &&
			sanitized[len(sanitized)-1].Role == llmtypes.ChatMessageTypeHuman &&
			normalized.Role == llmtypes.ChatMessageTypeHuman {
			sanitized = append(sanitized, llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeAI,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "[Previous request was interrupted before a response was generated.]"}},
			})
		}
		sanitized = append(sanitized, normalized)
	}
	return sanitized
}

func normalizeMessageForPlainTextProvider(msg llmtypes.MessageContent) llmtypes.MessageContent {
	if msg.Role == llmtypes.ChatMessageTypeTool {
		return llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: plainTextFromParts("[Previous tool result]", msg.Parts)}},
		}
	}

	parts := make([]llmtypes.ContentPart, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch p := part.(type) {
		case llmtypes.TextContent:
			if p.Text != "" {
				parts = append(parts, p)
			}
		case llmtypes.ToolCall:
			parts = append(parts, llmtypes.TextContent{Text: summarizeToolCall(p)})
		case *llmtypes.ToolCall:
			if p != nil {
				parts = append(parts, llmtypes.TextContent{Text: summarizeToolCall(*p)})
			}
		case llmtypes.ToolCallResponse:
			parts = append(parts, llmtypes.TextContent{Text: summarizeToolCallResponse(p)})
		case *llmtypes.ToolCallResponse:
			if p != nil {
				parts = append(parts, llmtypes.TextContent{Text: summarizeToolCallResponse(*p)})
			}
		default:
			parts = append(parts, part)
		}
	}

	if len(parts) == 0 {
		parts = []llmtypes.ContentPart{llmtypes.TextContent{Text: "[Empty prior message omitted by history sanitizer.]"}}
	}

	return llmtypes.MessageContent{
		Role:  msg.Role,
		Parts: parts,
	}
}

func plainTextFromParts(prefix string, parts []llmtypes.ContentPart) string {
	// History can pass through this sanitizer more than once when a retained
	// coding-agent session is resumed. Keep the conversion idempotent: an
	// already serialized tool result is context, not a new result to wrap again.
	if prefix == "[Previous tool result]" && len(parts) == 1 {
		if text, ok := parts[0].(llmtypes.TextContent); ok {
			trimmed := strings.TrimSpace(text.Text)
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "[previous tool result:") || strings.HasPrefix(lower, "[previous tool result]:") {
				return trimmed
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	for _, part := range parts {
		switch p := part.(type) {
		case llmtypes.TextContent:
			if p.Text != "" {
				sb.WriteString(": ")
				sb.WriteString(truncateForHistory(p.Text, 5000))
			}
		case llmtypes.ToolCallResponse:
			sb.WriteString(": ")
			sb.WriteString(summarizeToolCallResponse(p))
		case *llmtypes.ToolCallResponse:
			if p != nil {
				sb.WriteString(": ")
				sb.WriteString(summarizeToolCallResponse(*p))
			}
		}
	}
	return sb.String()
}

func summarizeToolCall(tc llmtypes.ToolCall) string {
	name := ""
	args := ""
	if tc.FunctionCall != nil {
		name = tc.FunctionCall.Name
		args = tc.FunctionCall.Arguments
	}
	if name == "" {
		name = "unknown_tool"
	}
	if args != "" {
		return fmt.Sprintf("[Previous tool call: %s(%s)]", name, truncateForHistory(args, 2000))
	}
	return fmt.Sprintf("[Previous tool call: %s]", name)
}

func summarizeToolCallResponse(resp llmtypes.ToolCallResponse) string {
	name := resp.Name
	if name == "" {
		name = "unknown_tool"
	}
	return fmt.Sprintf("[Previous tool result: %s -> %s]", name, truncateForHistory(resp.Content, 5000))
}

func truncateForHistory(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}

// LLMAgentConfig holds configuration for the LLM agent wrapper
type LLMAgentConfig struct {
	ConnectionID       string
	Name               string
	ServerName         string
	ConfigPath         string
	Provider           llm.Provider // LLM provider (bedrock, openai, anthropic, openrouter)
	ModelID            string
	Options            map[string]interface{}
	Temperature        float64
	ToolChoice         string
	MaxTurns           int
	StreamingChunkSize int
	Timeout            time.Duration
	ToolTimeout        time.Duration      // Tool execution timeout (default: 5 minutes)
	AgentMode          mcpagent.AgentMode // Agent mode (Simple or ReAct)
	SelectedTools      []string           // Selected tools in "server:tool" format
	// AdditionalBridgeTools exposes selected registered platform tools directly
	// through the coding-agent MCP bridge. The coding CLI calls these tools; it
	// does not become their implementation backend.
	AdditionalBridgeTools []string

	// AdmitTool decides which tools may enter this agent's definition. It is a
	// construction input rather than a setter because the decision is only
	// meaningful before the first registration: it defines the agent's identity,
	// which is fixed once assembled.
	//
	// This is not ToolPolicy. ToolPolicy narrows a finished agent per turn and
	// also rewrites the session-wide code-execution registry, so it reaches
	// actors the caller did not intend and can hide a tool the agent has already
	// been told about. Admission decides membership once, before the coding CLI
	// caches its catalog, so a declined tool is never advertised and a retained
	// one can never silently disappear.
	//
	// nil admits everything. Invoked while the wrapper's lock is held, so it
	// must not call back into the wrapper.
	AdmitTool func(name string) bool

	// ToolExecutionContext binds server-owned session identity to every direct
	// tool invocation. Run before the tool's action-specific permission checks.
	// It must preserve cancellation and reject conflicting caller identities.
	ToolExecutionContext func(context.Context, string) (context.Context, error)

	// Code execution mode: When enabled, only virtual tools are added to LLM
	// MCP tools are accessed through generated scripts using the on-demand HTTP API specification.
	UseCodeExecutionMode                   bool
	ClaudeCodePersistentInteractiveSession bool
	CodexPersistentInteractiveSession      bool
	CursorPersistentInteractiveSession     bool
	PiPersistentInteractiveSession         bool
	MusePersistentInteractiveSession       bool
	AgyPersistentInteractiveSession        bool
	// CodingAgentUserAnswersNativeQuestions: a person attends this chat and can
	// answer the coding CLI's native questions; otherwise they are auto-answered.
	CodingAgentUserAnswersNativeQuestions bool
	CursorBridgeToolsMode                 bool
	CodingAgentToolsMode                  string
	CodingAgentApprovalsMode              string
	// BridgeRoutingInstructionsOverride replaces mcpagent's generic
	// bridge-only preamble. Product profiles with native coding tools use an
	// empty override because their own prompt explains the product tools.
	BridgeRoutingInstructionsOverride *string
	ClaudeCodeTransport               string
	// ForceStructuredCodingAgent forces coding-agent CLI providers to use
	// the structured JSON transport (--print/--exec) for this agent's
	// LLM calls, overriding the default tmux behavior. Wired from the
	// workflow step config AgentConfigs.Transport == "structured".
	ForceStructuredCodingAgent   bool
	CodingAgentWorkingDir        string
	CLISecurityPolicy            *llmtypes.CLISecurityPolicy
	CodingAgentSecretEnvironment map[string]string
	// CodexNetworkAccess lets codex's sandboxed native shell reach the network.
	// A native_shell profile's product APIs are session-scoped HTTP endpoints,
	// so without this codex's own shell cannot call any product tool at all —
	// codex's workspace-write sandbox blocks network unless asked otherwise,
	// whereas Claude Code's native Bash has no equivalent restriction.
	CodexNetworkAccess bool
	APIKeys            *llm.ProviderAPIKeys // API keys for providers

	// Context offloading configuration
	LargeOutputThreshold int // Token threshold for context offloading (0 = use default: 10000)

	// Parallel tool execution: When enabled, multiple tool calls in a single LLM response
	// are executed concurrently using a fork-join pattern instead of sequentially
	EnableParallelToolExecution bool

	// MCP session management for connection reuse
	// When set, MCP connections are shared via session registry instead of creating new connections
	// This enables connection reuse in stateful MCP servers.
	SessionID string

	// Optional MCP credential namespace. AgentWorks leaves this empty because
	// MCP credentials are resolved from its shared platform configuration.
	UserID string

	// RuntimeOverrides allows runtime modification of MCP server configuration per-agent.
	// Used to dynamically configure stateful MCP server runtimes.
	RuntimeOverrides mcpclient.RuntimeOverrides
}

// agentMetricsImpl is the concrete implementation of AgentMetrics interface
type agentMetricsImpl struct {
	mu sync.RWMutex

	// Request metrics
	TotalRequests      int64
	SuccessfulRequests int64
	FailedRequests     int64

	// Timing metrics
	TotalLatency   time.Duration
	MinLatency     time.Duration
	MaxLatency     time.Duration
	AverageLatency time.Duration

	// Token metrics
	TotalTokensUsed int64
	InputTokens     int64
	OutputTokens    int64

	// Tool metrics
	ToolCallsExecuted  int64
	ToolCallsSucceeded int64
	ToolCallsFailed    int64

	// Stream metrics
	StreamsStarted   int64
	StreamsCompleted int64
	StreamsFailed    int64

	// Status tracking
	IsHealthy       bool
	LastRequestTime time.Time
	LastSuccessTime time.Time
	LastErrorTime   time.Time
	LastError       error
}

// NewLLMAgentWrapper creates a new LLM agent wrapper.
func NewLLMAgentWrapper(ctx context.Context, config LLMAgentConfig, tracer observability.Tracer, logger loggerv2.Logger) (*LLMAgentWrapper, error) {
	if tracer == nil {
		tracer = observability.GetTracer("noop")
	}
	return NewLLMAgentWrapperWithTrace(ctx, config, tracer, "", logger)
}

// NewLLMAgentWrapperWithTrace creates a wrapper from one immutable definition
// and one grouped runtime configuration.
func NewLLMAgentWrapperWithTrace(ctx context.Context, config LLMAgentConfig, tracer observability.Tracer, mainTraceID observability.TraceID, logger loggerv2.Logger) (*LLMAgentWrapper, error) {
	if logger == nil {
		logger = agentlogger.WithContext(loggerv2.NewDefault(), ctx)
	}
	// Never format the full config here: CodingAgentSecretEnvironment contains
	// plaintext SECRET_* values for the native child process. Logging the struct
	// with %+v would disclose those values to the server debug log.
	secretNames := make([]string, 0, len(config.CodingAgentSecretEnvironment))
	for name := range config.CodingAgentSecretEnvironment {
		secretNames = append(secretNames, name)
	}
	sort.Strings(secretNames)
	logger.Info(fmt.Sprintf("NewLLMAgentWrapper config: name=%s provider=%s model=%s code_execution=%t coding_agent_tools=%s approvals=%s secret_names=%v session=%s", config.Name, config.Provider, config.ModelID, config.UseCodeExecutionMode, config.CodingAgentToolsMode, config.CodingAgentApprovalsMode, secretNames, config.SessionID))
	if config.Name == "" {
		config.Name = "mcp-agent"
	}
	if config.ToolTimeout == 0 {
		config.ToolTimeout = 5 * time.Minute
	}

	traceID := mainTraceID
	if traceID == "" {
		traceID = observability.TraceID(fmt.Sprintf("agent-init-%s-%d", config.Name, time.Now().UnixNano()))
	}
	model, err := initializeLLMWithConfig(ctx, config, logger, traceID)
	if err != nil {
		if tracer != nil && mainTraceID == "" {
			event := events.NewAgentEvent(&events.AgentErrorEvent{
				BaseEventData: events.BaseEventData{TraceID: string(traceID)},
				Error:         "failed to initialize LLM: " + err.Error(), Context: "agent_initialization",
			})
			event.TraceID = string(traceID)
			tracer.EmitEvent(event)
		}
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	definition := mcpagent.AgentDefinition{}
	for _, name := range configuredServerNames(config.ServerName) {
		definition.Tools.MCP = append(definition.Tools.MCP, mcpagent.MCPToolSource{Name: name})
	}
	runtime := runtimeConfigForLLMAgent(config, model, tracer, traceID, logger)
	agent, err := mcpagent.NewAgentFromDefinition(ctx, definition, runtime)
	if err != nil {
		if tracer != nil && mainTraceID == "" {
			event := events.NewAgentEvent(&events.AgentErrorEvent{
				BaseEventData: events.BaseEventData{TraceID: string(traceID)},
				Error:         err.Error(), Context: "agent_creation",
			})
			event.TraceID = string(traceID)
			tracer.EmitEvent(event)
		}
		return nil, fmt.Errorf("failed to create MCP agent: %w", err)
	}

	wrapper := &LLMAgentWrapper{
		agent: agent, name: config.Name, config: config,
		metrics: &agentMetricsImpl{
			MinLatency: time.Duration(^uint64(0) >> 1),
			IsHealthy:  true, LastRequestTime: time.Now(),
		},
		tracer: tracer, traceID: traceID, logger: logger,
		runtime: runtime, definition: definition,
		admitTool: config.AdmitTool,
	}
	if mainTraceID == "" {
		logger.Info(fmt.Sprintf("Created agent trace for conversation: %s", traceID))
	} else if tracer != nil {
		logger.Info(fmt.Sprintf("Using hierarchical tracing, main_trace_id: %s", mainTraceID))
	}
	return wrapper, nil
}

// Invoke implements the LLMAgent interface - simple prompt-in, response-out
func (w *LLMAgentWrapper) Invoke(ctx context.Context, prompt string) (string, error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return "", errors.New("agent is closed")
	}

	// Add user message to wrapper history for tracking
	w.history = append(w.history, llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}},
	})
	w.mu.Unlock()

	// Use InvokeWithHistory to maintain proper conversation state
	return w.InvokeWithHistory(ctx, w.GetHistory())
}

// InvokeWithHistory allows multi-turn conversation by passing a full message history.
func (w *LLMAgentWrapper) InvokeWithHistory(ctx context.Context, messages []llmtypes.MessageContent) (string, error) {
	if err := w.FinalizeDefinition(ctx); err != nil {
		return "", err
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return "", errors.New("agent is closed")
	}
	runtimeSession := w.session
	// Use the passed messages directly, don't overwrite internal history
	w.mu.Unlock()

	// Create timeout context
	timeoutCtx := ctx
	if w.config.Timeout > 0 {
		var cancel context.CancelFunc
		timeoutCtx, cancel = context.WithTimeout(ctx, w.config.Timeout)
		defer cancel()
	}

	// Start tracking metrics
	startTime := time.Now()
	w.updateRequestMetrics()
	turnID := newPlatformTurnID()

	// Emit server selection event
	if w.agent != nil {
		// Get the list of connected servers
		serverNames := configuredServerNames(w.config.ServerName)
		totalServers := len(serverNames)

		// Determine source based on configuration
		source := "manual"
		if w.config.ServerName == "all" || len(serverNames) == 0 {
			source = "all"
		}

		// Debug logging removed - excessive verbosity

		// Create server selection event
		serverSelectionEvent := events.NewMCPServerSelectionEvent(
			1, // turn 1 for initial query
			serverNames,
			totalServers,
			source,
			"", // query will be extracted from messages if needed
		)
		attachTurnID(serverSelectionEvent, turnID)

		// Emit the event
		w.emitEvent(serverSelectionEvent)
	}

	// Check for context cancellation before executing the request
	if ctx.Err() != nil {
		w.logger.Info(fmt.Sprintf("Context canceled before agent execution: %s", ctx.Err().Error()))
		return "", fmt.Errorf("agent execution canceled: %w", ctx.Err())
	}

	// Execute the request with message history
	if providerNeedsPlainTextHistory(w.config.Provider) {
		messages = sanitizeHistoryForPlainTextProvider(messages)
	}
	if runtimeSession == nil {
		return "", errors.New("agent session is not initialized")
	}
	result, err := runtimeSession.Run(timeoutCtx, mcpagent.Turn{ID: turnID, History: messages})
	response := result.Text
	updatedMessages := result.History
	duration := time.Since(startTime)

	// End the trace after conversation completion
	if w.traceID != "" && w.tracer != nil {
		w.logger.Info(fmt.Sprintf("Ending agent trace - trace_id: %s, response_length: %d, duration_ms: %d",
			w.traceID, len(response), duration.Milliseconds()))

		// Agent end event removed - no longer needed
	} else {
		w.logger.Info(fmt.Sprintf("Not ending trace - trace_id: %s, tracer: %v", w.traceID, w.tracer != nil))
	}

	// Update metrics based on result
	if err != nil {
		w.updateFailureMetrics(duration, err)
		return response, fmt.Errorf("agent request failed: %w", err)
	}

	w.updateSuccessMetrics(duration, response)

	// Add assistant message to history
	w.mu.Lock()
	w.history = updatedMessages
	w.lastResult = result
	w.mu.Unlock()

	return response, nil
}

// GetUnderlyingAgent returns the underlying MCP agent for direct access
func (w *LLMAgentWrapper) GetUnderlyingAgent() *mcpagent.Agent {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.agent
}

// SetInstalledSkillResolver configures read_skill fallback resolution for the
// immutable agent that this wrapper will finalize. Setting it only on
// GetUnderlyingAgent() is incorrect: FinalizeDefinition replaces that draft.
func (w *LLMAgentWrapper) SetInstalledSkillResolver(resolver mcpagent.InstalledSkillResolver) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureDefinitionMutable(); err != nil {
		return err
	}
	w.installedSkillResolver = resolver
	return nil
}

func (w *LLMAgentWrapper) AddInstructions(instructions ...string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureDefinitionMutable(); err != nil {
		return err
	}
	for _, instruction := range instructions {
		instruction = strings.TrimSpace(instruction)
		if instruction == "" {
			continue
		}
		if w.definition.Instructions != "" {
			w.definition.Instructions += "\n\n"
		}
		w.definition.Instructions += instruction
	}
	return nil
}

func (w *LLMAgentWrapper) ResetInstructions(base string, supplements ...string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureDefinitionMutable(); err != nil {
		return err
	}
	w.definition.Instructions = strings.TrimSpace(base)
	for _, supplement := range supplements {
		supplement = strings.TrimSpace(supplement)
		if supplement == "" {
			continue
		}
		if w.definition.Instructions != "" {
			w.definition.Instructions += "\n\n"
		}
		w.definition.Instructions += supplement
	}
	return nil
}

func (w *LLMAgentWrapper) AttachSkill(skill *llmtypes.Skill) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureDefinitionMutable(); err != nil {
		return err
	}
	if skill == nil {
		return errors.New("skill cannot be nil")
	}
	// Incremental assembly can discover the same skill through more than one
	// configuration source. For example, Workflow Builder receives installed
	// playbooks through both capabilities.selected_skills and
	// installed_playbooks. The legacy registry was keyed by name; preserve that
	// idempotent, last-write-wins behavior while assembling the immutable slice.
	// mcpagent deliberately remains strict and rejects duplicates that reach its
	// final definition boundary.
	name := strings.TrimSpace(skill.Name)
	if name != "" {
		for i, existing := range w.definition.Skills {
			if existing != nil && strings.TrimSpace(existing.Name) == name {
				if w.logger != nil {
					w.logger.Debug(fmt.Sprintf("Re-attached skill %q; replacing the earlier definition", name))
				}
				w.definition.Skills[i] = skill
				return nil
			}
		}
	}
	w.definition.Skills = append(w.definition.Skills, skill)
	return nil
}

func (w *LLMAgentWrapper) AttachedSkills() []*llmtypes.Skill {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return append([]*llmtypes.Skill(nil), w.definition.Skills...)
}

func (w *LLMAgentWrapper) AddObserver(observer mcpagent.AgentEventListener) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureDefinitionMutable(); err != nil {
		return err
	}
	if observer == nil {
		return errors.New("observer cannot be nil")
	}
	w.observers = append(w.observers, observer)
	return nil
}

// SetCLISecurityPolicy replaces the coding CLI's launch policy before the
// immutable Agent is finalized (the chat's folder guard is known only after
// the wrapper is built).
func (w *LLMAgentWrapper) SetCLISecurityPolicy(policy *llmtypes.CLISecurityPolicy) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finalized {
		return errors.New("agent definition is already finalized")
	}
	if policy == nil {
		w.runtime.Coding.CLISecurityPolicy = nil
		return nil
	}
	copyPolicy := policy.Clone()
	w.runtime.Coding.CLISecurityPolicy = &copyPolicy
	return nil
}

// SetCodingAgentWorkingDir updates construction-time runtime state before the
// immutable Agent is finalized. It deliberately does not mutate a live Agent.
func (w *LLMAgentWrapper) SetCodingAgentWorkingDir(dir string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finalized {
		return errors.New("agent definition is already finalized")
	}
	w.runtime.Workspace.CodingAgentWorkingDir = strings.TrimSpace(dir)
	return nil
}

func (w *LLMAgentWrapper) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), category string) error {
	return w.RegisterCustomToolWithTimeout(name, description, parameters, execute, 0, category)
}

// RegisterCustomToolWithTimeout records a tool on the pre-finalize definition.
//
// Re-registering a name replaces the earlier entry rather than adding a second
// one. This wrapper exists to convert legacy incremental assembly into one
// immutable definition, and that assembly registered into a map keyed by tool
// name — re-registration was idempotent, last-write-wins. Accumulating a slice
// instead silently made it fatal: mcpagent's finalizeDefinition rejects a
// duplicate name, so the whole agent fails to construct.
//
// This matters whenever a retained chat definition is assembled again and
// delegation tools are registered onto a wrapper that already carries them.
// Without replacement semantics, the next turn dies before step 1 with
// `duplicate direct tool name "delegate"`.
//
// A replacement is logged: last-write-wins is right for re-assembly, but two
// genuinely different tools claiming one name is a bug worth seeing.
func (w *LLMAgentWrapper) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureDefinitionMutable(); err != nil {
		return err
	}
	// Registration admission. Declining is not an error: the caller registered
	// a tool this agent's profile does not include, which is a policy outcome,
	// not a failure. Callers treat a returned error as fatal to the session.
	if w.admitTool != nil && !w.admitTool(name) {
		return nil
	}
	if resolve := w.config.ToolExecutionContext; resolve != nil && execute != nil {
		original := execute
		execute = func(ctx context.Context, args map[string]interface{}) (string, error) {
			boundCtx, err := resolve(ctx, name)
			if err != nil {
				return "", err
			}
			return original(boundCtx, args)
		}
	}
	tool := mcpagent.ToolDefinition{
		Name: name, Description: description, InputSchema: parameters,
		Execute: execute, Timeout: timeout, DisplayGroup: category,
	}
	for i, existing := range w.definition.Tools.Direct {
		if existing.Name == name {
			if w.logger != nil {
				w.logger.Debug(fmt.Sprintf("Re-registered custom tool %q; replacing the earlier definition", name))
			}
			w.definition.Tools.Direct[i] = tool
			return nil
		}
	}
	w.definition.Tools.Direct = append(w.definition.Tools.Direct, tool)
	return nil
}

func (w *LLMAgentWrapper) AssemblyInstructions() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.definition.Instructions
}

func (w *LLMAgentWrapper) ensureDefinitionMutable() error {
	if w.closed {
		return errors.New("agent is closed")
	}
	if w.finalized {
		return errors.New("agent definition is already finalized")
	}
	return nil
}

// FinalizeDefinition converts the legacy incremental chat/server assembly into
// one immutable definition before the first turn. It is idempotent. Callers may
// continue reading the underlying runtime afterward, but identity mutations
// must happen before this boundary.
func (w *LLMAgentWrapper) FinalizeDefinition(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("agent is closed")
	}
	if w.finalized {
		return nil
	}
	if w.agent == nil {
		return errors.New("underlying agent is nil")
	}

	runtime := w.runtime
	runtime.ResumeHandle = w.lastResult.Handle
	runtime.Observability.Observers = append([]mcpagent.AgentEventListener(nil), w.observers...)
	next, err := mcpagent.NewAgentFromDefinition(ctx, w.definition, runtime)
	if err != nil {
		return fmt.Errorf("finalize immutable agent definition: %w", err)
	}
	next.SetInstalledSkillResolver(w.installedSkillResolver)
	nextSession, err := next.Start(ctx)
	if err != nil {
		_ = next.Close()
		return fmt.Errorf("start finalized agent session: %w", err)
	}
	old := w.agent
	oldSession := w.session
	w.agent = next
	w.session = nextSession
	w.finalized = true
	if oldSession != nil {
		_ = oldSession.Close()
	}
	mcpagent.RetireReplacedAgent(old)
	return nil
}

// Close releases the wrapper-owned durable session and its immutable Agent.
// The wrapper is the conversation owner; individual Run calls do not close it.
func (w *LLMAgentWrapper) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	session := w.session
	agent := w.agent
	w.session = nil
	w.agent = nil
	w.mu.Unlock()

	if session != nil {
		_ = session.Close()
	}
	if agent != nil {
		return agent.Close()
	}
	return nil
}

// AgentMetricsSnapshot is a read-only snapshot of agent metrics
type AgentMetricsSnapshot struct {
	InputTokens       int64
	OutputTokens      int64
	ToolCallsExecuted int64
	TotalCostUSD      float64
}

// GetMetricsSnapshot returns a snapshot of the agent's current metrics
func (w *LLMAgentWrapper) GetMetricsSnapshot() AgentMetricsSnapshot {
	w.metrics.mu.RLock()
	defer w.metrics.mu.RUnlock()
	snapshot := AgentMetricsSnapshot{
		InputTokens:       w.metrics.InputTokens,
		OutputTokens:      w.metrics.OutputTokens,
		ToolCallsExecuted: w.metrics.ToolCallsExecuted,
	}
	// Get total cost from the underlying agent (includes provider-reported costs)
	w.mu.RLock()
	snapshot.TotalCostUSD = w.lastResult.Usage.TotalCostUSD
	w.mu.RUnlock()
	return snapshot
}

// GetName implements the AgentCapabilities interface
func (w *LLMAgentWrapper) GetName() string {
	return w.name
}

// GetHistory returns a copy of the current conversation history
func (w *LLMAgentWrapper) GetHistory() []llmtypes.MessageContent {
	w.mu.RLock()
	defer w.mu.RUnlock()
	h := make([]llmtypes.MessageContent, len(w.history))
	copy(h, w.history)
	return h
}

// AppendUserMessage adds a user message to the agent's history
func (w *LLMAgentWrapper) AppendUserMessage(text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	// Let the agent handle everything - just add user message to wrapper history for tracking
	w.history = append(w.history, llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
	})
}

// AppendMessage adds a message to the conversation history
func (w *LLMAgentWrapper) AppendMessage(msg llmtypes.MessageContent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.history = append(w.history, msg)
}

// Helper methods for metrics tracking

func (w *LLMAgentWrapper) updateRequestMetrics() {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()

	w.metrics.TotalRequests++
	w.metrics.LastRequestTime = time.Now()
}

func (w *LLMAgentWrapper) updateSuccessMetrics(duration time.Duration, response string) {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()

	w.metrics.SuccessfulRequests++
	w.metrics.LastSuccessTime = time.Now()
	w.metrics.IsHealthy = true

	// Update latency metrics
	w.metrics.TotalLatency += duration
	if duration < w.metrics.MinLatency {
		w.metrics.MinLatency = duration
	}
	if duration > w.metrics.MaxLatency {
		w.metrics.MaxLatency = duration
	}
	if w.metrics.TotalRequests > 0 {
		w.metrics.AverageLatency = w.metrics.TotalLatency / time.Duration(w.metrics.TotalRequests)
	}

	// Estimate token usage (simplified)
	w.metrics.OutputTokens += int64(len(response) / 4) // Rough estimation
}

func (w *LLMAgentWrapper) updateFailureMetrics(duration time.Duration, err error) {
	w.metrics.mu.Lock()
	defer w.metrics.mu.Unlock()

	w.metrics.FailedRequests++
	w.metrics.LastErrorTime = time.Now()
	w.metrics.LastError = err

	// Update latency metrics even for failures
	w.metrics.TotalLatency += duration
	if duration < w.metrics.MinLatency {
		w.metrics.MinLatency = duration
	}
	if duration > w.metrics.MaxLatency {
		w.metrics.MaxLatency = duration
	}
	if w.metrics.TotalRequests > 0 {
		w.metrics.AverageLatency = w.metrics.TotalLatency / time.Duration(w.metrics.TotalRequests)
	}
}

// initializeLLMWithConfig initializes an LLM using detailed configuration from frontend
func initializeLLMWithConfig(ctx context.Context, config LLMAgentConfig, logger loggerv2.Logger, traceID observability.TraceID) (llmtypes.Model, error) {
	// Validate and convert provider string to llm.Provider type
	llmProvider, err := llm.ValidateProvider(string(config.Provider))
	if err != nil {
		return nil, fmt.Errorf("invalid LLM provider '%s': %w", config.Provider, err)
	}
	if err := llmguard.RequireCodingAgentProvider(string(llmProvider)); err != nil {
		return nil, err
	}

	// Create a separate LLM logger that writes to llm_debug.log
	// This separates LLM logs (including [GEMINI] logs from multi-llm-provider-go) from server logs
	var v2LoggerForLLM loggerv2.Logger
	llmLogger, err := agentlogger.CreateLoggerWithContext("logs/llm_debug.log", "info", "text", false, ctx)
	if err != nil {
		// Fallback to the provided logger if LLM logger creation fails
		if logger != nil {
			v2LoggerForLLM = logger
		} else {
			v2LoggerForLLM = agentlogger.WithContext(loggerv2.NewDefault(), ctx)
		}
	} else {
		v2LoggerForLLM = llmLogger
	}

	// Use the existing LLM provider system with the selected model
	llmConfig := llm.Config{
		ConnectionID: config.ConnectionID,
		Provider:     llmProvider,
		ModelID:      config.ModelID,
		Temperature:  config.Temperature,
		TraceID:      traceID, // Pass the trace ID for proper span hierarchy

		MaxRetries:          3,
		Logger:              v2LoggerForLLM,
		APIKeys:             config.APIKeys, // Use API keys directly from config
		ClaudeCodeTransport: config.ClaudeCodeTransport,
	}

	// Initialize the LLM using the factory with the selected model
	return llm.InitializeLLM(llmguard.WithServerAccountAdmission(llmConfig))
}

// EmitTypedEvent emits a typed event through the agent's event dispatcher
func (w *LLMAgentWrapper) EmitTypedEvent(ctx context.Context, eventData events.EventData) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed || w.agent == nil {
		return
	}
	w.emitEvent(eventData)
}

func (w *LLMAgentWrapper) emitEvent(eventData events.EventData) {
	if eventData == nil || w.tracer == nil {
		return
	}
	event := events.NewAgentEvent(eventData)
	event.TraceID = string(w.traceID)
	w.tracer.EmitEvent(event)
}

func attachTurnID(eventData events.EventData, turnID string) {
	if eventData == nil || turnID == "" {
		return
	}
	if base, ok := eventData.(interface{ GetBaseEventData() *events.BaseEventData }); ok {
		data := base.GetBaseEventData()
		if data.Metadata == nil {
			data.Metadata = make(map[string]interface{})
		}
		data.Metadata["turn_id"] = turnID
	}
}

func newPlatformTurnID() string {
	return "turn_" + events.GenerateEventID()
}

// currentTurnID resolves the turn ID that owns the session's canonical
// completion RIGHT NOW, rather than trusting a turn ID captured once when a
// tool-call hook was registered. A retained (tmux-delivered) delivery never
// calls AskWithHistory again -- mcpagent's Session mints its own turn
// lifecycle for it via Session.Send -- so a hook that only ever knew about
// the turn active at registration time tags every later retained turn's
// tool-call events with the wrong, stale ID (PLAT-180). Falls back to
// fallback (the turn ID from registration time) if the session is
// unavailable or reports no turn active, which preserves prior behavior for
// the turn that registered the hook.
func (w *LLMAgentWrapper) currentTurnID(fallback string) string {
	w.mu.RLock()
	session := w.session
	w.mu.RUnlock()
	if session != nil {
		if id := session.ActiveTurnID(); id != "" {
			return id
		}
	}
	return fallback
}

// TurnInProgress reports whether the durable session is currently owned by a
// normal Run or a retained coding-CLI turn. The latter can outlive the HTTP
// request and therefore is not reliably represented by the server's display
// busy flag, cancel map, or input-lane bookkeeping.
func (w *LLMAgentWrapper) TurnInProgress() bool {
	w.mu.RLock()
	session := w.session
	w.mu.RUnlock()
	return session != nil && session.ActiveTurnID() != ""
}

func buildSessionTurn(prompt string, history []llmtypes.MessageContent, policy mcpagent.ToolPolicy, streamingCallback func(llmtypes.StreamChunk)) mcpagent.Turn {
	return mcpagent.Turn{
		ID:                newPlatformTurnID(),
		Input:             prompt,
		History:           history,
		ToolPolicy:        policy,
		StreamingCallback: streamingCallback,
	}
}

// StreamWithEvents streams text chunks from the agent during execution
// Events are handled separately via the EventObserver and polling API
func (w *LLMAgentWrapper) StreamWithEvents(ctx context.Context, prompt string) (<-chan string, error) {
	textChan, _, err := w.StreamWithEventsAndOutcome(ctx, prompt)
	return textChan, err
}

// StreamWithEventsAndOutcome is StreamWithEvents plus a one-shot terminal
// outcome channel. StreamWithEvents historically reports only setup failures;
// errors returned later by Session.Run are rendered as a user-visible chunk and
// otherwise disappear behind a normally closed text channel. Callers that must
// commit durable delivery state (notably background auto-notifications) need the
// actual terminal outcome before marking the message delivered.
func (w *LLMAgentWrapper) StreamWithEventsAndOutcome(ctx context.Context, prompt string) (<-chan string, <-chan error, error) {
	if err := w.FinalizeDefinition(ctx); err != nil {
		return nil, nil, err
	}
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return nil, nil, errors.New("agent is closed")
	}
	w.mu.RUnlock()

	// Create channel for text chunks only
	textChan := make(chan string, 50)
	outcomeChan := make(chan error, 1)

	// Start streaming in a goroutine
	go func() {
		var turnErr error
		var chanClosed atomic.Bool
		defer func() {
			chanClosed.Store(true)
			close(textChan)
			outcomeChan <- turnErr
			close(outcomeChan)
		}()

		// Set up real-time streaming callback to forward content chunks as they arrive.
		// This is critical for CLI providers such as Claude Code where the entire
		// agentic loop runs inside the CLI process — without this, the user sees nothing
		// until the full response is ready.
		var streamedAny atomic.Bool
		var streamedChunks atomic.Int64

		streamingCallback := func(chunk llmtypes.StreamChunk) {
			if chunk.Type == llmtypes.StreamChunkTypeContent && chunk.Content != "" && !chanClosed.Load() {
				if !streamedAny.Load() {
					w.logger.Info(fmt.Sprintf("[STREAMING] First real-time chunk received (len=%d), streaming callback active", len(chunk.Content)))
				}
				streamedAny.Store(true)
				streamedChunks.Add(1)
				select {
				case <-ctx.Done():
				case textChan <- chunk.Content:
				}
			}
		}
		w.logger.Info("[STREAMING] Real-time streaming callback installed")

		// Clear any stale tool call records from a previous canceled run for this session
		// so we only capture calls from the current run.
		if w.config.SessionID != "" {
			toolcalllog.Clear(w.config.SessionID)
		}

		// Report the per-turn callback outcome on exit.
		defer func() {
			if streamedAny.Load() {
				w.logger.Info(fmt.Sprintf("[STREAMING] Streamed %d chunks in real-time", streamedChunks.Load()))
			} else {
				w.logger.Info("[STREAMING] No real-time chunks received, will send full response")
			}
		}()

		// Snapshot prior wrapper history separately from this turn. Session owns the
		// durable continuation history after its first Run, so every later turn
		// must carry the new prompt in Turn.Input. Passing only Turn.History makes
		// Session ignore the new message once its internal history is non-empty,
		// which previously replayed the first user message for every synthetic
		// background-agent notification.
		messages := w.GetHistory()
		if providerNeedsPlainTextHistory(w.config.Provider) {
			before := len(messages)
			messages = sanitizeHistoryForPlainTextProvider(messages)
			w.logger.Info(fmt.Sprintf("[CANCEL_DEBUG] Sanitized CLI history before AskWithHistory | provider=%s msgs_before=%d msgs_after=%d",
				w.config.Provider, before, len(messages)))
		}
		turn := buildSessionTurn(prompt, messages, mcpagent.ToolPolicy{}, streamingCallback)

		w.logger.Info(fmt.Sprintf("[CANCEL_DEBUG] AskWithHistory starting | history_msgs=%d", len(messages)))

		var unregisterHTTPToolHook func()
		if w.config.SessionID != "" {
			unregisterHTTPToolHook = toolcalllog.RegisterHook(w.config.SessionID, toolcalllog.Hook{
				OnStart: func(tc toolcalllog.StartedCall) {
					// The HTTP bridge owns the authoritative arguments for coding
					// agents. Provider stream events can have empty ToolArgs even
					// though the command is about to run. Emitting through the tracer
					// does not require the in-memory Agent pointer, which may be
					// swapped during native resume; do not discard this detail.
					ev := events.NewToolCallStartEventWithCorrelation(
						1,
						tc.Name,
						events.ToolParams{Arguments: tc.ArgsJSON},
						"http-bridge",
						string(w.traceID),
						string(w.traceID),
					)
					ev.ToolCallID = tc.ID
					attachTurnID(ev, w.currentTurnID(turn.ID))
					w.emitEvent(ev)
				},
				OnEnd: func(tc toolcalllog.CompletedCall) {
					duration := time.Duration(0)
					if !tc.StartedAt.IsZero() {
						duration = tc.CompletedAt.Sub(tc.StartedAt)
					}
					ev := events.NewToolCallEndEventWithTokenUsageAndModel(
						1,
						tc.Name,
						tc.Result,
						"http-bridge",
						duration,
						"",
						0, 0, 0,
						w.config.ModelID,
					)
					ev.ToolCallID = tc.ID
					attachTurnID(ev, w.currentTurnID(turn.ID))
					w.emitEvent(ev)
				},
			})
			defer unregisterHTTPToolHook()
		}

		// Execute through the single turn API. It selects fresh versus provider-
		// native continuation internally and returns history, handle, and usage
		// as one result.
		w.mu.RLock()
		runtimeSession := w.session
		w.mu.RUnlock()
		if runtimeSession == nil {
			turnErr = errors.New("agent session is not initialized")
			select {
			case <-ctx.Done():
			case textChan <- "agent session is not initialized":
			}
			return
		}
		// Turn.Input is required, not optional: Session owns the durable history
		// after its first Run, so a later turn that passes only History is
		// ignored and the first user message gets replayed — which broke every
		// synthetic background-agent notification. ToolPolicy is empty here
		// because this branch derives the tool surface at registration instead
		// (6c4a8908), so there is no per-turn policy to thread.
		result, err := runtimeSession.Run(ctx, turn)
		turnErr = err
		response := result.Text
		updatedMessages := result.History

		// Fetch completed tool calls recorded at the HTTP execution level.
		// These are written by executor/handlers.go when a tool finishes — independent of
		// whether the CLI subprocess was still alive to receive the result.
		var httpCompletedCalls []toolcalllog.CompletedCall
		if w.config.SessionID != "" {
			httpCompletedCalls = toolcalllog.GetAndClear(w.config.SessionID)
		}

		w.logger.Info(fmt.Sprintf("[CANCEL_DEBUG] AskWithHistory returned | err=%v updated_msgs=%d input_msgs=%d http_tools_captured=%d",
			err, len(updatedMessages), len(messages), len(httpCompletedCalls)))

		// Always save updated messages, even on cancellation.
		// For API providers (Anthropic, Bedrock, etc.), conversation.go appends completed
		// tool call results to messages before returning on cancel — so updatedMessages may
		// contain valuable tool results that ran before the cancellation. Discarding them
		// would cause the model to re-run those tools on the next request.
		// For CLI providers (claude-code, codex-cli), updatedMessages equals the input
		// messages (the CLI manages its own tool loop internally) — we reconstruct the
		// tool history from cliCompletedToolCalls below instead.
		if len(updatedMessages) > 0 {
			w.mu.Lock()
			historyBefore := len(w.history)
			w.history = updatedMessages
			w.lastResult = result

			// For CLI providers: rebuild history from tool calls recorded at the HTTP level.
			// These are captured by executor/handlers.go when a tool finishes — even if the
			// CLI subprocess was killed before it received the result back.
			if len(httpCompletedCalls) > 0 {
				// Use plain-text summary instead of structured ToolCall/ToolCallResponse
				// ContentParts. CLI providers (claude-code, codex-cli) serialize ToolCall and
				// ChatMessageTypeTool to null in their input stream, causing "choice.Content is
				// empty" errors. A text summary in a normal AI message is universally serializable.
				var sb strings.Builder
				sb.WriteString("[Canceled run context — tools executed before cancellation:\n")
				for _, tc := range httpCompletedCalls {
					w.logger.Info(fmt.Sprintf("[CANCEL_DEBUG] HTTP tool reconstructed: id=%s name=%s args_len=%d result_len=%d",
						tc.ID, tc.Name, len(tc.ArgsJSON), len(tc.Result)))
					result := tc.Result
					if len(result) > 5000 {
						result = result[:5000] + "...[truncated]"
					}
					sb.WriteString(fmt.Sprintf("- %s(%s) → %s\n", tc.Name, tc.ArgsJSON, result))
				}
				sb.WriteString("]")
				w.history = append(w.history, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: sb.String()}},
				})
				w.logger.Info(fmt.Sprintf("[CANCEL_DEBUG] Reconstructed %d HTTP-level tool calls into history (text summary) | msgs_before=%d msgs_after=%d",
					len(httpCompletedCalls), historyBefore, len(w.history)))
			}

			// Fix dangling user message: if the conversation was canceled before the model
			// produced any response, history ends with a user message and no assistant reply.
			// Appending the next user message would create two consecutive user messages,
			// which is invalid for Anthropic-style APIs and confuses CLI providers.
			// Add a synthetic assistant acknowledgement so the history stays valid.
			last := w.history[len(w.history)-1]
			if err != nil && last.Role == llmtypes.ChatMessageTypeHuman {
				w.logger.Info("[CANCEL_DEBUG] History ends with dangling user message — appending synthetic assistant reply")
				w.history = append(w.history, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "[Request canceled — no response generated]"}},
				})
			}
			w.logger.Info(fmt.Sprintf("[CANCEL_DEBUG] History saved | final_msgs=%d err=%v", len(w.history), err))
			w.mu.Unlock()
		} else {
			w.logger.Info("[CANCEL_DEBUG] updatedMessages is empty — history NOT updated")
		}

		if err != nil {
			w.logger.Error("AskWithHistory failed", err)
			// Surface a user-visible error message so the frontend doesn't just silently hang.
			errMsg := "⚠️ An error occurred while generating a response. Please try again."
			errStr := err.Error()
			if strings.Contains(errStr, "high demand") || strings.Contains(errStr, "signal: killed") {
				errMsg = "⚠️ The provider is currently overloaded — no response received. Please try again in a moment."
			} else if strings.Contains(errStr, "context deadline exceeded") || strings.Contains(errStr, "context canceled") {
				errMsg = "⚠️ Request timed out. Please try again."
			}
			select {
			case <-ctx.Done():
			case textChan <- errMsg:
			}
			return
		}

		// Update the agent's history with the updated messages from the conversation.
		// Also handles the case where messages were summarized (fewer) or expanded (more).
		// Note: history may already be set above (cancellation path), but we overwrite here
		// on success to ensure the definitive post-run state is captured.
		w.mu.Lock()
		w.history = updatedMessages
		w.mu.Unlock()

		// Only send the full response if we didn't already stream it via callback.
		// For non-streaming providers (standard API), no callback fires and we send the full text.
		// For CLI providers with streaming, chunks were already sent incrementally.
		if !streamedAny.Load() && response != "" {
			select {
			case <-ctx.Done():
				return
			case textChan <- response:
			}
		}
	}()

	return textChan, outcomeChan, nil
}
