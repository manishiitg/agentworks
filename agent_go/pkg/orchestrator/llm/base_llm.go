package llm

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	agentlogger "github.com/manishiitg/coding-agent-loop/agent_go/pkg/logger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	baseevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// BaseLLM provides common functionality for all LLM-based operations
type BaseLLM struct {
	llm          llmtypes.Model
	logger       loggerv2.Logger
	tracer       observability.Tracer
	eventEmitter func(context.Context, baseevents.EventData)
}

// NewBaseLLM creates a new BaseLLM instance with mandatory event bridge
func NewBaseLLM(
	llm llmtypes.Model,
	logger loggerv2.Logger,
	tracer observability.Tracer,
	eventBridge mcpagent.AgentEventListener,
	llmType string,
) *BaseLLM {
	if eventBridge == nil {
		logger.Warn(fmt.Sprintf("⚠️ Event bridge is nil for %s LLM - this may limit observability", llmType))
	}

	eventEmitter := CreateEventEmitter(eventBridge, logger, llmType)

	return &BaseLLM{
		llm:          llm,
		logger:       logger,
		tracer:       tracer,
		eventEmitter: eventEmitter,
	}
}

// GetLLM returns the underlying LLM instance
func (b *BaseLLM) GetLLM() llmtypes.Model {
	return b.llm
}

// GetLogger returns the logger
func (b *BaseLLM) GetLogger() loggerv2.Logger {
	return b.logger
}

// GetTracer returns the tracer
func (b *BaseLLM) GetTracer() observability.Tracer {
	return b.tracer
}

// GetEventEmitter returns the event emitter function
func (b *BaseLLM) GetEventEmitter() func(context.Context, baseevents.EventData) {
	return b.eventEmitter
}

// SetEventEmitter sets the event emitter function
func (b *BaseLLM) SetEventEmitter(emitter func(context.Context, baseevents.EventData)) {
	b.eventEmitter = emitter
}

// createLLMLogger creates a separate logger instance for LLM operations
// This logger writes to logs/llm_debug.log to separate LLM logs from server logs
func createLLMLogger() loggerv2.Logger {
	llmLogger, err := agentlogger.CreateLogger("logs/llm_debug.log", "debug", "text", false)
	if err != nil {
		// Fallback to default logger if creation fails
		return loggerv2.NewDefault()
	}
	return llmLogger
}

// CreateLLMInstance creates an LLM instance with standard configuration
// Uses config.LLMConfig as the source of truth for provider/model
func CreateLLMInstance(
	config *agents.OrchestratorAgentConfig,
	logger loggerv2.Logger,
	llmType string,
) (llmtypes.Model, error) {
	logger.Info(fmt.Sprintf("🔧 Creating %s LLM with standard configuration", llmType))

	// Use separate LLM logger for multi-llm-provider-go logs
	llmLogger := createLLMLogger()

	// Generate trace ID for this LLM session
	traceID := observability.TraceID(fmt.Sprintf("%s-llm-%d", llmType, time.Now().UnixNano()))

	// Get primary LLM config from unified LLMConfig
	primaryProvider := config.LLMConfig.Primary.Provider
	primaryModel := config.LLMConfig.Primary.ModelID

	// Clone API keys — same underlying type, so Clone() avoids field-by-field copy.
	// Priority: per-model APIKey > global APIKeys
	llmAPIKeys := config.APIKeys.Clone()

	// Check for per-model API key (takes priority over global if set)
	// This handles cases where API key is specified in LLMConfig.Primary.APIKey
	if config.LLMConfig.Primary.APIKey != nil && *config.LLMConfig.Primary.APIKey != "" {
		if llmAPIKeys == nil {
			llmAPIKeys = &llm.ProviderAPIKeys{}
		}
		llmAPIKeys.SetKeyForProvider(llm.Provider(primaryProvider), config.LLMConfig.Primary.APIKey)
		logger.Info(fmt.Sprintf("🔑 Using per-model API key for %s provider", primaryProvider))
	}

	if err := llmguard.RequireCodingAgentProvider(primaryProvider); err != nil {
		return nil, fmt.Errorf("failed to create %s LLM: %w", llmType, err)
	}

	// Create LLM configuration using unified LLMConfig
	llmConfig := llm.Config{
		ConnectionID: config.LLMConfig.Primary.ConnectionID,
		Provider:     llm.Provider(primaryProvider),
		ModelID:      primaryModel,
		Temperature:  config.Temperature,
		Tracers:      nil, // Tracers will be set later if needed
		TraceID:      traceID,
		MaxRetries:   config.MaxRetries,
		Logger:       llmLogger, // Use separate LLM logger for multi-llm-provider-go logs
		APIKeys:      llmAPIKeys,
	}

	// Initialize LLM using the existing factory
	llmInstance, err := llm.InitializeLLM(llmguard.WithServerAccountAdmission(llmConfig))
	if err != nil {
		logger.Error(fmt.Sprintf("❌ Failed to create %s LLM: %v", llmType, err), err)
		return nil, fmt.Errorf("failed to create %s LLM: %w", llmType, err)
	}

	logger.Info(fmt.Sprintf("✅ %s LLM created successfully - Provider: %s, Model: %s, Temperature: %.1f",
		llmType, primaryProvider, primaryModel, config.Temperature))

	return llmInstance, nil
}

// CreateEventEmitter creates a standard event emitter function for LLM operations
func CreateEventEmitter(
	eventBridge mcpagent.AgentEventListener,
	logger loggerv2.Logger,
	llmType string,
) func(context.Context, baseevents.EventData) {
	return func(ctx context.Context, data baseevents.EventData) {
		if eventBridge == nil {
			logger.Warn(fmt.Sprintf("⚠️ No event bridge available, cannot emit %s LLM event", llmType))
			return
		}

		// Create agent event
		eventType := data.GetEventType()
		if eventType == "" {
			eventType = events.OrchestratorAgentStart // Fallback to current default
		}

		agentEvent := &baseevents.AgentEvent{
			Type:      eventType,
			Timestamp: time.Now(),
			Data:      data,
		}

		// Emit through event bridge
		if err := eventBridge.HandleEvent(ctx, agentEvent); err != nil {
			logger.Warn(fmt.Sprintf("⚠️ Failed to emit %s LLM event: %v", llmType, err), loggerv2.Field{Key: "error", Value: err})
		}
	}
}

// Close cleans up resources
func (b *BaseLLM) Close() error {
	return nil
}
