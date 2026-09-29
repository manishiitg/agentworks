package agents

import (
	"context"
	"fmt"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"reflect"
	"regexp"
	"strings"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	baseevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"

	agentlogger "github.com/manishiitg/coding-agent-loop/agent_go/pkg/logger"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// OrchestratorContext holds context information for event emission
// Removed: OrchestratorContext and related context-specific fields are now handled by the context-aware bridge.

// BaseOrchestratorAgent provides common functionality for all orchestrator agents
type BaseOrchestratorAgent struct {
	config               *OrchestratorAgentConfig
	logger               loggerv2.Logger
	baseAgent            *BaseAgent // set during init
	tracer               observability.Tracer
	agentType            AgentType
	systemPrompt         string
	eventBridge          mcpagent.AgentEventListener    // Event bridge for auto events
	userMessageProcessor func(map[string]string) string // Optional processor for user messages (replaces inputProcessor)
	agentSessionID       string                         // Agent session ID for correlating orchestrator_agent_start and orchestrator_agent_end events
}

// NewBaseOrchestratorAgentWithEventBridge creates a new base orchestrator agent with event bridge
func NewBaseOrchestratorAgentWithEventBridge(
	config *OrchestratorAgentConfig,
	logger loggerv2.Logger,
	tracer observability.Tracer,
	agentType AgentType,
	eventBridge mcpagent.AgentEventListener,
) *BaseOrchestratorAgent {
	return &BaseOrchestratorAgent{
		config:       config,
		logger:       logger,
		tracer:       tracer,
		agentType:    agentType,
		systemPrompt: "", // Not used for base orchestrator
		eventBridge:  eventBridge,
	}
}

// Initialize initializes the base orchestrator agent
func (boa *BaseOrchestratorAgent) Initialize(ctx context.Context) error {
	agentName := string(boa.agentType)
	if boa.config.AgentName != "" {
		agentName = boa.config.AgentName
	}

	// Create LLM instance
	llmInstance, err := boa.createLLM()
	if err != nil {
		return fmt.Errorf("failed to create LLM: %w", err)
	}

	// Create traceID using LLMConfig.Primary
	traceID := observability.TraceID(fmt.Sprintf("%s-agent-%s-%d",
		boa.agentType,
		boa.config.LLMConfig.Primary.ModelID,
		time.Now().UnixNano()))

	// Determine agent name: use unique AgentName from config if available, otherwise fall back to agent type
	if boa.config.AgentName != "" {
		agentName = boa.config.AgentName
	}

	// Create base agent using LLMConfig as source of truth
	baseAgent, err := NewBaseAgent(
		ctx,
		boa.agentType,
		agentName, // Use unique agent name if available, otherwise agent type
		llmInstance,
		boa.systemPrompt,
		boa.config.ServerNames,
		boa.config.DirectTools,
		boa.config.SelectedTools,
		boa.config.UseCodeExecutionMode,
		boa.config.Mode,
		boa.tracer,
		traceID,
		boa.config.MCPConfigPath,
		boa.config.LLMConfig.Primary.ModelID,
		boa.config.Temperature,
		boa.config.ToolChoice,
		boa.config.MaxTurns,
		boa.config.LLMConfig.Primary.Provider,
		boa.logger,
		false, // cacheOnly - not used in orchestrator agents
		boa.config.EnableContextOffloading,
		boa.config.LargeOutputThreshold,        // Token threshold for context offloading
		boa.config.EnableParallelToolExecution, // Parallel tool execution
		&boa.config.LLMConfig,                  // Pass LLMConfig
		boa.config.APIKeys,                     // Pass API keys
		boa.config.MCPSessionID,                // MCP session ID for connection sharing
		boa.config.CodingAgentWorkingDir,       // CLI coding-agent working directory
		boa.config.CodingAgentKeepAlive,        // Keep tmux-backed coding sessions alive after completion
		boa.config.ForceStructuredCodingAgent,  // Force structured JSON transport (step-level)
		boa.config.IsolateCodingAgentWorkspace, // Run coding-CLI in a fresh tmp dir (workflow steps only)
		boa.config.CLISecurityPolicy,           // Server-resolved immutable CLI security policy
		boa.config.RuntimeOverrides,            // Runtime config overrides for MCP servers
	)
	if err != nil {
		return fmt.Errorf("failed to create base agent: %w", err)
	}

	boa.baseAgent = baseAgent

	return nil
}

// ExecuteWithInputProcessor executes the agent with a custom input processor
// This is a convenience method that delegates to ExecuteWithTemplateValidation with nil templateData
func (boa *BaseOrchestratorAgent) ExecuteWithInputProcessor(ctx context.Context, templateVars map[string]string, inputProcessor func(map[string]string) string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	// Delegate to ExecuteWithTemplateValidation with nil templateData to skip validation
	return boa.ExecuteWithTemplateValidation(ctx, templateVars, inputProcessor, conversationHistory, nil, "", false)
}

// ExecuteWithTemplateValidation executes the agent with template validation
func (boa *BaseOrchestratorAgent) ExecuteWithTemplateValidation(ctx context.Context, templateVars map[string]string, inputProcessor func(map[string]string) string, conversationHistory []llmtypes.MessageContent, templateData interface{}, systemPrompt string, overwriteSystemPrompt bool) (string, []llmtypes.MessageContent, error) {
	startTime := time.Now()

	// Use userMessageProcessor if set, otherwise use provided inputProcessor
	var userMessage string
	if boa.userMessageProcessor != nil {
		userMessage = boa.userMessageProcessor(templateVars)
	} else {
		userMessage = inputProcessor(templateVars)
	}
	if err := boa.applyInstructions(ctx, systemPrompt, overwriteSystemPrompt); err != nil {
		return "", nil, err
	}

	// Auto-emit agent start event (after computing user message so it can be included)
	boa.emitAgentStartEvent(ctx, templateVars, systemPrompt, userMessage)

	// Always mark as sub-agent so ContextAwareEventBridge tags tool events
	// with this agent's correlation ID (BaseOrchestratorAgent is only used
	// for orchestrator step agents, never the main chat agent).
	agentCtx := context.WithValue(ctx, events.AgentSessionIDKey, boa.agentSessionID)
	agentCtx = context.WithValue(agentCtx, events.IsSubAgentContextKey, true)
	if sid := boa.config.MCPSessionID; sid != "" {
		agentCtx = context.WithValue(agentCtx, common.ChatSessionIDKey, sid)
	}

	// Validate template fields at compile time (skip validation if templateData is nil)
	if templateData != nil {
		if err := boa.validateTemplateFields(userMessage, templateData); err != nil {
			boa.logger.Error(fmt.Sprintf("❌ Template validation failed for agent %s: %v", boa.agentType, err), err)
			return "", nil, fmt.Errorf("template validation failed: %w", err)
		}
	}

	// Delegate to template's Execute method which enforces event patterns
	result, updatedConversationHistory, err := boa.baseAgent.Execute(agentCtx, userMessage, conversationHistory, "", false)

	duration := time.Since(startTime)

	// Auto-emit agent end event
	boa.emitAgentEndEvent(ctx, templateVars, result, err, duration)

	if err != nil {
		boa.logger.Error(fmt.Sprintf("❌ Base Orchestrator Agent (%s) execution failed: %v", boa.agentType, err), err)
		return "", nil, fmt.Errorf("base orchestrator execution failed: %w", err)
	}

	// Orchestrator agent execution completed
	return result, updatedConversationHistory, nil
}

// GetType returns the agent type
func (boa *BaseOrchestratorAgent) GetType() string {
	return string(boa.agentType)
}

// GetConfig returns the agent configuration
func (boa *BaseOrchestratorAgent) GetConfig() *OrchestratorAgentConfig {
	return boa.config
}

// Close closes the base orchestrator agent
func (boa *BaseOrchestratorAgent) Close() error {
	if boa.baseAgent != nil && boa.baseAgent.agent != nil {
		boa.baseAgent.agent.Close()
	}
	return nil
}

// BaseAgent returns the base agent
func (boa *BaseOrchestratorAgent) BaseAgent() *BaseAgent {
	return boa.baseAgent
}

// GetBaseAgent returns the base agent (implements OrchestratorAgent interface)
func (boa *BaseOrchestratorAgent) GetBaseAgent() *BaseAgent {
	return boa.baseAgent
}

// SetEventBridge sets the event bridge for the agent
func (boa *BaseOrchestratorAgent) SetEventBridge(bridge mcpagent.AgentEventListener) {
	boa.eventBridge = bridge
}

// GetTracer returns the tracer
func (boa *BaseOrchestratorAgent) GetTracer() observability.Tracer {
	return boa.tracer
}

// GetEventBridge returns the event bridge
func (boa *BaseOrchestratorAgent) GetEventBridge() mcpagent.AgentEventListener {
	return boa.eventBridge
}

// SetUserMessageProcessor sets the user message processor function
func (boa *BaseOrchestratorAgent) SetUserMessageProcessor(processor func(map[string]string) string) {
	boa.userMessageProcessor = processor
}

// GetUserMessageProcessor returns the user message processor if set, otherwise returns nil
func (boa *BaseOrchestratorAgent) GetUserMessageProcessor() func(map[string]string) string {
	return boa.userMessageProcessor
}

// UserMessageProcessorSetter is an interface for setting user message processor
type UserMessageProcessorSetter interface {
	SetUserMessageProcessor(func(map[string]string) string)
}

// emitEvent emits an event through the event bridge
func (boa *BaseOrchestratorAgent) emitEvent(ctx context.Context, eventType baseevents.EventType, data baseevents.EventData) {
	// Check if event bridge is available
	if boa.eventBridge == nil {
		boa.logger.Debug(fmt.Sprintf("⚠️ Event bridge is nil, skipping event emission: %s", eventType))
		return
	}

	// Create agent event
	// Set CorrelationID to this agent's own session ID so the bridge doesn't
	// override it with a parent agent's ID from context (prevents sub-agent events
	// from being incorrectly parented under the parent orchestrator agent).
	agentEvent := &baseevents.AgentEvent{
		Type:          eventType,
		Timestamp:     time.Now(),
		Data:          data,
		CorrelationID: boa.agentSessionID,
	}

	// Emit through event bridge
	if err := boa.eventBridge.HandleEvent(ctx, agentEvent); err != nil {
		boa.logger.Warn(fmt.Sprintf("⚠️ Failed to emit event %s: %v", eventType, err), loggerv2.Field{Key: "error", Value: err})
	} else {
		boa.logger.Debug(fmt.Sprintf("✅ Successfully emitted event %s", eventType))
	}
}

// emitAgentStartEvent emits an agent start event automatically
func (boa *BaseOrchestratorAgent) emitAgentStartEvent(ctx context.Context, templateVars map[string]string, systemPrompt string, userMessage string) {
	// Removed verbose logging

	// Generate unique agent session ID for correlating start/end events
	boa.agentSessionID = baseevents.GenerateEventID()

	agentName := string(boa.agentType)
	if boa.baseAgent != nil {
		agentName = boa.baseAgent.name
	}

	var fullSystemPrompt string
	if boa.baseAgent != nil {
		if mcpAg := boa.baseAgent.Agent(); mcpAg != nil {
			fullSystemPrompt = mcpAg.Definition().Instructions
		}
	}

	// Suppress verbose prompts for internal agents (learning, organizer) — they're noisy in the UI
	emitSystemPrompt := fullSystemPrompt
	emitUserMessage := userMessage
	if boa.agentType == TodoPlannerSuccessLearningAgentType {
		emitSystemPrompt = ""
		emitUserMessage = ""
	}

	eventData := &events.OrchestratorAgentStartEvent{
		BaseEventData: baseevents.BaseEventData{
			Timestamp:     time.Now(),
			CorrelationID: boa.agentSessionID, // Use shared session ID for correlation
		},
		AgentType:            string(boa.agentType),
		AgentName:            agentName,
		InputData:            templateVars,
		ModelID:              boa.config.LLMConfig.Primary.ModelID,
		Provider:             boa.config.LLMConfig.Primary.Provider,
		ServersCount:         len(boa.config.ServerNames),
		MaxTurns:             boa.config.MaxTurns,
		UseCodeExecutionMode: boa.config.UseCodeExecutionMode,
		UseScriptedMode:      strings.EqualFold(strings.TrimSpace(templateVars["IsScriptedMode"]), "true"),
		SystemPrompt:         emitSystemPrompt,
		UserMessage:          emitUserMessage,
	}

	boa.emitEvent(ctx, events.OrchestratorAgentStart, eventData)
}

func (boa *BaseOrchestratorAgent) applyInstructions(ctx context.Context, systemPrompt string, overwrite bool) error {
	if systemPrompt == "" || boa.baseAgent == nil || boa.baseAgent.agent == nil {
		return nil
	}
	return boa.baseAgent.ApplyInstructions(ctx, systemPrompt, overwrite)
}

// emitAgentEndEvent emits an agent end event automatically.
func (boa *BaseOrchestratorAgent) emitAgentEndEvent(ctx context.Context, templateVars map[string]string, result string, err error, duration time.Duration) {
	agentName := string(boa.agentType)
	if boa.baseAgent != nil {
		agentName = boa.baseAgent.name
	}

	// Get token usage from agent if available
	var promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount int
	if boa.baseAgent != nil && boa.baseAgent.agent != nil {
		usage := mcpagent.ReadAgentDiagnostics(boa.baseAgent.agent).Usage
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
		totalTokens = usage.TotalTokens
		cacheTokens = usage.CacheTokens
		reasoningTokens = usage.ReasoningTokens
		llmCallCount = usage.LLMCalls
		cacheEnabledCallCount = usage.CacheEnabledLLMCalls
	}

	eventData := &events.OrchestratorAgentEndEvent{
		BaseEventData: baseevents.BaseEventData{
			Timestamp:     time.Now(),
			CorrelationID: boa.agentSessionID, // Use shared session ID for correlation
		},
		AgentType: string(boa.agentType),
		AgentName: agentName,
		InputData: templateVars,
		Result:    result,
		Success:   err == nil,
		Error: func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}(),
		Duration:              duration,
		ModelID:               boa.config.LLMConfig.Primary.ModelID,
		Provider:              boa.config.LLMConfig.Primary.Provider,
		ServersCount:          len(boa.config.ServerNames),
		MaxTurns:              boa.config.MaxTurns,
		PromptTokens:          promptTokens,
		CompletionTokens:      completionTokens,
		TotalTokens:           totalTokens,
		CacheTokens:           cacheTokens,
		ReasoningTokens:       reasoningTokens,
		LLMCallCount:          llmCallCount,
		CacheEnabledCallCount: cacheEnabledCallCount,
	}

	boa.emitEvent(ctx, events.OrchestratorAgentEnd, eventData)
}

// createLLM creates an LLM instance based on the agent configuration
// Uses the unified LLMConfig (selected model) as the source of truth
func (boa *BaseOrchestratorAgent) createLLM() (llmtypes.Model, error) {
	if boa.config != nil && boa.config.LLMFactory != nil {
		return boa.config.LLMFactory()
	}

	// Generate trace ID for this agent session
	traceID := observability.TraceID(fmt.Sprintf("%s-agent-%d", boa.agentType, time.Now().UnixNano()))

	if boa.config == nil {
		return nil, fmt.Errorf("selected LLM configuration is required")
	}

	// Get primary LLM config
	primaryProvider := boa.config.LLMConfig.Primary.Provider
	primaryModel := boa.config.LLMConfig.Primary.ModelID

	if strings.TrimSpace(primaryProvider) == "" || strings.TrimSpace(primaryModel) == "" {
		return nil, fmt.Errorf("selected LLM provider and model are required")
	}

	// Direct assignment — AgentAPIKeys is an alias for llm.ProviderAPIKeys,
	// so no conversion needed.
	var llmAPIKeys *llm.ProviderAPIKeys
	if boa.config.APIKeys != nil {
		llmAPIKeys = boa.config.APIKeys
	}

	// Create a separate LLM logger that writes to llm_debug.log
	// This separates LLM logs (including [GEMINI] logs from multi-llm-provider-go) from server logs
	var llmLogger loggerv2.Logger
	llmLoggerInstance, err := agentlogger.CreateLogger("logs/llm_debug.log", "debug", "text", false)
	if err != nil {
		// Fallback to the provided logger if LLM logger creation fails
		if boa.logger != nil {
			llmLogger = boa.logger
		} else {
			llmLogger = loggerv2.NewDefault()
		}
	} else {
		llmLogger = llmLoggerInstance
	}

	// Create LLM configuration using unified LLMConfig
	config := llm.Config{
		ConnectionID:        boa.config.LLMConfig.Primary.ConnectionID,
		Provider:            llm.Provider(primaryProvider),
		ModelID:             primaryModel,
		Temperature:         boa.config.Temperature,
		Tracers:             nil, // Tracers will be set later if needed
		TraceID:             traceID,
		MaxRetries:          boa.config.MaxRetries,
		Logger:              llmLogger, // Use separate LLM logger for multi-llm-provider-go logs
		APIKeys:             llmAPIKeys,
		ClaudeCodeTransport: boa.config.ClaudeCodeTransport,
	}

	if err := llmguard.RequireCodingAgentProvider(primaryProvider); err != nil {
		return nil, err
	}
	llmInstance, err := llm.InitializeLLM(llmguard.WithServerAccountAdmission(config))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	return llmInstance, nil
}

// validateTemplateFields validates that all template field references exist in the struct
func (boa *BaseOrchestratorAgent) validateTemplateFields(templateStr string, templateData interface{}) error {
	// Extract all template field references using regex
	re := regexp.MustCompile(`\{\{\.([A-Za-z][A-Za-z0-9_]*)\}\}`)
	matches := re.FindAllStringSubmatch(templateStr, -1)

	// Get struct field names using reflection
	structFields := boa.getStructFieldNames(templateData)

	// Check if all template references exist in struct
	for _, match := range matches {
		fieldName := match[1]
		if !boa.contains(structFields, fieldName) {
			return fmt.Errorf("template references non-existent field: %s", fieldName)
		}
	}

	return nil
}

// getStructFieldNames extracts field names from a struct using reflection
func (boa *BaseOrchestratorAgent) getStructFieldNames(v interface{}) []string {
	if v == nil {
		return []string{}
	}

	val := reflect.ValueOf(v)
	typ := reflect.TypeOf(v)

	// Handle pointers
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return []string{}
		}
		val = val.Elem()
		typ = typ.Elem()
	}

	// Only handle structs
	if val.Kind() != reflect.Struct {
		return []string{}
	}

	var fieldNames []string
	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		// Only include exported fields (uppercase)
		if field.PkgPath == "" {
			fieldNames = append(fieldNames, field.Name)
		}
	}

	return fieldNames
}

// contains checks if a slice contains a string
func (boa *BaseOrchestratorAgent) contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
