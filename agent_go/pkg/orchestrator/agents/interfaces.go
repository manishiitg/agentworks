package agents

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// OrchestratorAgent defines the interface for all orchestrator agents
type OrchestratorAgent interface {
	// Execute executes the agent with the given template variables and returns the result and updated conversation history
	Execute(ctx context.Context, templateVars map[string]string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error)

	// GetType returns the agent type (planning, execution, validation, plan_organizer)
	GetType() string

	// GetConfig returns the agent configuration
	GetConfig() *OrchestratorAgentConfig

	// Initialize initializes the agent with its configuration
	Initialize(ctx context.Context) error

	// Close closes the agent and cleans up resources
	Close() error

	// Event system - now handled by unified events system

	// GetBaseAgent returns the base agent for event listener attachment
	GetBaseAgent() *BaseAgent
}

// OutputFormat represents the output format for an agent
type OutputFormat string

const (
	OutputFormatText       OutputFormat = "text"
	OutputFormatMarkdown   OutputFormat = "markdown"
	OutputFormatStructured OutputFormat = "structured"
)

// LLMModel represents a single LLM configuration
type LLMModel struct {
	ConnectionID string `json:"connection_id,omitempty"`
	Provider     string `json:"provider"` // coding-agent CLI, e.g. "claude-code", "codex-cli"
	ModelID      string `json:"model_id"` // "claude-sonnet-4.5", "gpt-5", etc.

	// Auth per model
	APIKey  *string                `json:"api_key,omitempty"` // For OpenRouter, OpenAI, Anthropic, Vertex
	Region  *string                `json:"region,omitempty"`  // For Bedrock
	Options map[string]interface{} `json:"options,omitempty"` // Provider-specific runtime options
}

// LLMConfig holds the selected LLM configuration
type LLMConfig struct {
	Primary LLMModel `json:"primary"`
}

// OrchestratorAgentConfig defines the configuration for an orchestrator agent
type OrchestratorAgentConfig struct {
	// Unified LLM configuration (selected model)
	LLMConfig LLMConfig `json:"llm_config"`

	// LLMFactory optionally injects an LLM instance for tests or specialized callers.
	// When set, BaseOrchestratorAgent.Initialize uses this instead of provider-based initialization.
	LLMFactory func() (llmtypes.Model, error) `json:"-"`

	// Temperature is kept separate as it may be overridden per-agent
	Temperature float64 `json:"temperature"`

	// API keys for different providers
	APIKeys *AgentAPIKeys `json:"api_keys,omitempty"`

	// Required Agent behavior
	Mode         AgentMode    `json:"mode" validate:"required"`
	OutputFormat OutputFormat `json:"output_format" validate:"required"`

	// Required MCP configuration
	ServerNames   []string `json:"server_names" validate:"required"`
	SelectedTools []string `json:"selected_tools,omitempty"` // Array of "server:tool" strings
	MCPConfigPath string   `json:"mcp_config_path" validate:"required"`
	ToolChoice    string   `json:"tool_choice" validate:"required"`
	MaxTurns      int      `json:"max_turns" validate:"required"`

	// Required settings
	MaxRetries int `json:"max_retries" validate:"required"`
	Timeout    int `json:"timeout" validate:"required"`    // in seconds
	RateLimit  int `json:"rate_limit" validate:"required"` // requests per minute

	// Optional instructions
	Instructions string `json:"instructions,omitempty"`

	// Optional fields
	Description         string                 `json:"description,omitempty"`
	UseStructuredOutput bool                   `json:"use_structured_output,omitempty"`
	CustomSettings      map[string]interface{} `json:"custom_settings,omitempty"`

	// Agent name (unique identifier for this agent instance)
	AgentName string `json:"agent_name,omitempty"` // e.g., "execution-agent-step-1-title"

	// Structured output configuration
	StructuredOutputSchema string `json:"structured_output_schema,omitempty"`
	StructuredOutputType   string `json:"structured_output_type,omitempty"` // "plan", "steps", "custom"

	// Code execution mode: When enabled, only virtual tools are added to LLM
	// MCP tools are accessed through generated scripts using the on-demand HTTP API specification.
	UseCodeExecutionMode bool `json:"use_code_execution_mode,omitempty"`
	// ClaudeCodeTransport optionally overrides the Claude Code provider
	// transport for this workflow agent. Workflow agents use the interactive
	// tmux transport so chat and step execution share one coding-CLI path.
	ClaudeCodeTransport string `json:"claude_code_transport,omitempty"`
	// CodingAgentWorkingDir sets the process working directory for CLI-backed
	// coding providers. Workflow agents should run from the workflow folder, not
	// from the server/project root.
	CodingAgentWorkingDir string `json:"coding_agent_working_dir,omitempty"`
	// CLISecurityPolicy is an immutable, server-resolved policy snapshot for
	// this invocation. Workflow documents must never author this value.
	CLISecurityPolicy *llmtypes.CLISecurityPolicy `json:"-"`
	// CodingAgentKeepAlive keeps a tmux-backed coding provider session alive after
	// the current agent turn finishes. Workflow steps default to false so each
	// step/sub-agent owns a bounded terminal. Chat agents set provider-specific
	// keepalive outside this orchestrator config.
	CodingAgentKeepAlive bool `json:"coding_agent_keep_alive,omitempty"`
	// ForceStructuredCodingAgent forces the coding-agent CLI providers
	// (claude-code, codex-cli, cursor-cli, pi-cli) to use the
	// structured JSON transport (--print / --exec) for this step,
	// even when tmux would otherwise be used. Wired from the workflow
	// step config's AgentConfigs.Transport == "structured".
	ForceStructuredCodingAgent bool `json:"force_structured_coding_agent,omitempty"`
	// IsolateCodingAgentWorkspace keeps generated instructions, skills and CLI
	// state in a private, session-stable directory instead of the real workflow.
	IsolateCodingAgentWorkspace bool `json:"isolate_coding_agent_workspace,omitempty"`
	// CodingAgentOutputDir is the trusted step artifact directory linked as
	// output/ inside that private runtime. It is never authored in workflow JSON.
	CodingAgentOutputDir string `json:"-"`
	// Context offloading configuration
	EnableContextOffloading *bool `json:"enable_context_offloading,omitempty"` // Enable/disable context offloading (default: true if nil)
	LargeOutputThreshold    int   `json:"large_output_threshold,omitempty"`    // Token threshold for context offloading (0 = use default: 10000)

	// System prompt configuration
	OverwriteSystemPrompt *bool `json:"overwrite_system_prompt,omitempty"` // Overwrite (true) or append (false) system prompt during execution (default: false if nil)

	// Parallel tool execution: When enabled, multiple tool calls in a single LLM response
	// are executed concurrently using a fork-join pattern instead of sequentially
	EnableParallelToolExecution bool `json:"enable_parallel_tool_execution,omitempty"`

	// MCP session ID for connection management
	// When set, MCP connections are shared across agents with the same session ID
	// Connections persist until CloseSession() is called (not when agent closes)
	MCPSessionID string `json:"mcp_session_id,omitempty"`

	// Runtime config overrides for MCP servers
	// Allows workflow-specific modifications like output directories per run
	RuntimeOverrides mcpclient.RuntimeOverrides `json:"runtime_overrides,omitempty"`

	// Per-agent folder guard paths (optional override for shared orchestrator state).
	// When set, these take precedence over BaseOrchestrator.folderGuardReadPaths/WritePaths
	// in registerCustomToolsForAgent, preventing race conditions when parallel sub-agents
	// share the same orchestrator instance.
	FolderGuardReadPaths  []string `json:"-"`
	FolderGuardWritePaths []string `json:"-"`

	// DirectTools is the fully prepared direct-tool identity for this agent.
	// The owning orchestrator resolves categories, folder guards, and executor
	// wrappers before Initialize so mcpagent can register the complete immutable
	// definition during construction.
	DirectTools []mcpagent.ToolDefinition `json:"-"`
}

// AgentAPIKeys is an alias for llm.ProviderAPIKeys (canonical type).
type AgentAPIKeys = llm.ProviderAPIKeys

// BedrockAgentConfig is an alias for llm.BedrockConfig (canonical type).
type BedrockAgentConfig = llm.BedrockConfig

// AzureAgentConfig is an alias for llm.AzureAPIConfig (canonical type).
type AzureAgentConfig = llm.AzureAPIConfig

// NewOrchestratorAgentConfig creates a new agent configuration with minimal defaults
func NewOrchestratorAgentConfig(name string) *OrchestratorAgentConfig {
	return &OrchestratorAgentConfig{
		// LLMConfig.Primary must be set by caller
		LLMConfig:      LLMConfig{},
		Temperature:    0.0,
		Mode:           "", // Must be set by caller
		OutputFormat:   OutputFormatText,
		ServerNames:    []string{},
		MaxRetries:     0,
		Timeout:        0,
		RateLimit:      0,
		MCPConfigPath:  "", // Must be set by caller
		ToolChoice:     "", // Must be set by caller
		MaxTurns:       0,
		CustomSettings: make(map[string]interface{}),
	}
}

// LoadOrchestratorAgentConfigFromEnv creates a new agent configuration with values from environment variables
func LoadOrchestratorAgentConfigFromEnv(name string) *OrchestratorAgentConfig {
	config := NewOrchestratorAgentConfig(name)

	// Load LLM configuration from environment variables
	if provider := os.Getenv("ORCHESTRATOR_PROVIDER"); provider != "" {
		config.LLMConfig.Primary.Provider = provider
	}
	if model := os.Getenv("ORCHESTRATOR_MODEL"); model != "" {
		config.LLMConfig.Primary.ModelID = model
	}
	if tempStr := os.Getenv("ORCHESTRATOR_TEMPERATURE"); tempStr != "" {
		if temp, err := strconv.ParseFloat(tempStr, 64); err == nil {
			config.Temperature = temp
		}
	}

	if mode := os.Getenv("ORCHESTRATOR_MODE"); mode != "" {
		config.Mode = AgentMode(mode)
	}
	if maxRetriesStr := os.Getenv("ORCHESTRATOR_MAX_RETRIES"); maxRetriesStr != "" {
		if maxRetries, err := strconv.Atoi(maxRetriesStr); err == nil {
			config.MaxRetries = maxRetries
		}
	}
	if timeoutStr := os.Getenv("ORCHESTRATOR_TIMEOUT"); timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil {
			config.Timeout = timeout
		}
	}
	if rateLimitStr := os.Getenv("ORCHESTRATOR_RATE_LIMIT"); rateLimitStr != "" {
		if rateLimit, err := strconv.Atoi(rateLimitStr); err == nil {
			config.RateLimit = rateLimit
		}
	}
	if mcpConfigPath := os.Getenv("ORCHESTRATOR_MCP_CONFIG_PATH"); mcpConfigPath != "" {
		config.MCPConfigPath = mcpConfigPath
	}
	if toolChoice := os.Getenv("ORCHESTRATOR_TOOL_CHOICE"); toolChoice != "" {
		config.ToolChoice = toolChoice
	}
	if maxTurnsStr := os.Getenv("ORCHESTRATOR_MAX_TURNS"); maxTurnsStr != "" {
		if maxTurns, err := strconv.Atoi(maxTurnsStr); err == nil {
			config.MaxTurns = maxTurns
		}
	}

	return config
}

// ValidateOrchestratorAgentConfig validates that all required fields are provided
func ValidateOrchestratorAgentConfig(config *OrchestratorAgentConfig) error {
	var errors []string

	// Check required LLM configuration (using unified LLMConfig)
	if config.LLMConfig.Primary.Provider == "" {
		errors = append(errors, "LLMConfig.Primary.Provider is required")
	}
	if config.LLMConfig.Primary.ModelID == "" {
		errors = append(errors, "LLMConfig.Primary.ModelID is required")
	}

	// Check required agent behavior
	if config.Mode == "" {
		errors = append(errors, "Mode is required")
	}
	if config.OutputFormat == "" {
		errors = append(errors, "OutputFormat is required")
	}

	// Check required MCP configuration
	if len(config.ServerNames) == 0 {
		errors = append(errors, "ServerNames is required")
	}
	if config.MCPConfigPath == "" {
		errors = append(errors, "MCPConfigPath is required")
	}
	if config.ToolChoice == "" {
		errors = append(errors, "ToolChoice is required")
	}
	if config.MaxTurns == 0 {
		errors = append(errors, "MaxTurns is required")
	}

	// Check required settings
	if config.MaxRetries == 0 {
		errors = append(errors, "MaxRetries is required")
	}
	if config.Timeout == 0 {
		errors = append(errors, "Timeout is required")
	}
	if config.RateLimit == 0 {
		errors = append(errors, "RateLimit is required")
	}

	if len(errors) > 0 {
		return fmt.Errorf("OrchestratorAgentConfig validation failed: %s", strings.Join(errors, ", "))
	}

	return nil
}
