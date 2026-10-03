package agents

import (
	"context"
	"fmt"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	internalLLM "github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const orchestratorIDKey contextKey = "orchestrator_id"

func boolPointer(value bool) *bool { return &value }

func platformBridgeToolsFromDirectTools(directTools []mcpagent.ToolDefinition) []string {
	for _, tool := range directTools {
		if tool.Name == "read_image" {
			return []string{tool.Name}
		}
	}
	return nil
}

// AgentMode represents the mode of operation for an agent
type AgentMode string

const (
	SimpleAgent AgentMode = "simple"
)

// AgentType represents the type of agent
type AgentType string

const (
	// Multi-agent TodoPlanner sub-agents (actively used)
	TodoPlannerExecutionQAAgentType         AgentType = "todo_planner_execution_qa"         // Answers questions about execution results (read-only)
	TodoPlannerPlanningAgentType            AgentType = "todo_planner_planning"             // Creates step-wise plan from objective
	TodoPlannerExecutionAgentType           AgentType = "todo_planner_execution"            // Executes first step of plan
	TodoPlannerSuccessLearningAgentType     AgentType = "todo_planner_success_learning"     // Analyzes successful executions to capture best practices
	TodoPlannerLearningDetectionAgentType   AgentType = "todo_planner_learning_detection"   // Detects if new learnings were generated after learning phase
	ConditionalAgentType                    AgentType = "conditional"                       // Conditional decision agent for evaluating step conditions
	OrchestratorAgentType                   AgentType = "todo_task_orchestrator"            // Orchestrator orchestrator agent that manages todo lists and delegates to sub-agents
	GenericExecutionAgentType               AgentType = "generic_execution"                 // Generic execution agent for todo task steps (no learning, no prevalidation)
	TodoPlannerInteractiveWorkshopAgentType AgentType = "todo_planner_interactive_workshop" // Interactive workshop: execute steps, edit plan, update step config in one session
)

// BaseAgentInterface defines the interface for base agent operations
type BaseAgentInterface interface {
	// Core execution
	Execute(ctx context.Context, userMessage string, conversationHistory []llmtypes.MessageContent, systemPrompt string, overwriteSystemPrompt bool) (string, []llmtypes.MessageContent, error)

	// Agent information
	GetType() AgentType
	GetName() string
	GetInstructions() string
	GetMode() AgentMode
	GetServerNames() []string

	// Resource management
	Close() error

	// Event system - now handled by unified events system

	// Workflow support
	GetWorkflowContext() map[string]interface{}
	SetWorkflowContext(context map[string]interface{})
	GetPreviousAgentOutput() string
	SetPreviousAgentOutput(output string)

	// MCP agent access
	Agent() *mcpagent.Agent
}

// BaseAgent provides comprehensive functionality for all orchestrator agents
type BaseAgent struct {
	// Core identity
	agentType AgentType
	name      string

	// Core functionality
	agent        *mcpagent.Agent
	instructions string
	mode         AgentMode
	serverNames  []string
	llm          llmtypes.Model

	// Observability
	tracer  observability.Tracer
	traceID observability.TraceID
	logger  loggerv2.Logger

	// Event system - now handled by unified events system

	// Workflow context
	workflowContext     map[string]interface{}
	previousAgentOutput string

	// Configuration
	configPath   string
	modelID      string
	temperature  float64
	toolChoice   string
	maxTurns     int
	provider     string
	mcpSessionID string
	definition   mcpagent.AgentDefinition
	runtime      mcpagent.RuntimeConfig
	lastHandle   *mcpagent.AgentSessionHandle
	finalized    bool
	session      *mcpagent.Session
}

// NewBaseAgent creates a new BaseAgent instance with comprehensive functionality
func NewBaseAgent(
	ctx context.Context,
	agentType AgentType,
	name string,
	llm llmtypes.Model,
	instructions string,
	serverNames []string,
	directTools []mcpagent.ToolDefinition,
	selectedTools []string, // NEW parameter
	useCodeExecutionMode bool, // NEW parameter
	mode AgentMode,
	tracer observability.Tracer,
	traceID observability.TraceID,
	configPath string,
	modelID string,
	temperature float64,
	toolChoice string,
	maxTurns int,
	provider string,
	logger loggerv2.Logger,
	cacheOnly bool,
	enableContextOffloading *bool, // Context offloading configuration
	largeOutputThreshold int, // Token threshold for context offloading (0 = use default: 10000)
	enableParallelToolExecution bool, // Parallel tool execution configuration
	llmConfig *LLMConfig, // NEW: Full LLM configuration
	apiKeys *AgentAPIKeys, // API keys for providers
	mcpSessionID string, // MCP session ID for connection sharing across agents
	codingAgentWorkingDir string, // CLI coding-agent process working directory
	codingAgentKeepAlive bool, // Keep tmux-backed coding-agent sessions alive after this agent completes
	forceStructuredCodingAgent bool, // Force structured JSON transport for coding-agent CLIs (overrides tmux default)
	isolateCodingAgentWorkspace bool, // Run the coding-CLI session in a private session directory
	codingAgentOutputDir string, // Real artifact directory linked as output/ in an isolated runtime
	cliSecurityPolicy *llmtypes.CLISecurityPolicy, // Server-resolved immutable CLI security policy
	runtimeOverrides mcpclient.RuntimeOverrides, // Runtime config overrides for MCP servers (e.g., output directories)
) (*BaseAgent, error) {
	if common.ScopeAgentMCP != nil {
		names, overrides, aliases, err := common.ScopeAgentMCP(ctx, mcpSessionID, serverNames, runtimeOverrides)
		if err != nil {
			return nil, err
		}
		serverNames = names
		runtimeOverrides = overrides
		selectedTools = common.RemapMCPToolSelection(selectedTools, aliases)
	}
	generation := mcpagent.GenerationRuntimeConfig{
		Provider:    internalLLM.Provider(provider),
		Temperature: temperature,
		ToolChoice:  toolChoice,
		MaxTurns:    maxTurns,
		APIKeys:     apiKeys,
	}
	if llmConfig != nil {
		generation.LLM = mcpagent.AgentLLMConfiguration{
			Primary: mcpagent.LLMModel{
				Provider:     llmConfig.Primary.Provider,
				ModelID:      llmConfig.Primary.ModelID,
				APIKey:       llmConfig.Primary.APIKey,
				Region:       llmConfig.Primary.Region,
				Options:      llmConfig.Primary.Options,
				ConnectionID: llmConfig.Primary.ConnectionID,
			},
		}

	}

	if mcpSessionID != "" {
		logger.Info("🔗 Using MCP session for connection sharing",
			loggerv2.String("session_id", mcpSessionID),
			loggerv2.String("agent_name", name))
	}

	if workingDir := strings.TrimSpace(codingAgentWorkingDir); workingDir != "" {
		logger.Info("🔗 Using coding-agent working directory",
			loggerv2.String("working_dir", workingDir),
			loggerv2.String("agent_name", name))
	}
	if codingAgentKeepAlive {
		logger.Info("🔗 Keeping tmux-backed coding-agent session alive after completion",
			loggerv2.String("agent_name", name))
	}
	if isolateCodingAgentWorkspace {
		logger.Info("🔒 Isolating coding-agent session in a private runtime",
			loggerv2.String("agent_name", name))
	}
	if cliSecurityPolicy != nil {
		logger.Info("🔒 Using server-resolved coding-agent CLI security policy",
			loggerv2.String("mode", string(cliSecurityPolicy.Mode)),
			loggerv2.String("agent_name", name))
	}
	if forceStructuredCodingAgent {
		logger.Info("🔧 Structured JSON transport selected for coding-agent CLIs (step transport=structured)",
			loggerv2.String("agent_name", name))
	}

	if runtimeOverrides != nil {
		logger.Info("🔧 Using runtime overrides for MCP servers",
			loggerv2.String("agent_name", name),
			loggerv2.Int("overrides_count", len(runtimeOverrides)))
	}

	mcpSources := make([]mcpagent.MCPToolSource, 0, len(serverNames))
	for _, serverName := range serverNames {
		if common.ScopeAgentMCP != nil && common.IsBuiltinToolCategory(serverName) {
			continue
		}
		if name := strings.TrimSpace(serverName); name != "" {
			mcpSources = append(mcpSources, mcpagent.MCPToolSource{Name: name})
		}
	}

	if common.ScopeAgentMCP != nil && len(mcpSources) == 0 {
		mcpSources = []mcpagent.MCPToolSource{{Name: mcpclient.NoServers}}
	}

	// Create the agent from one identity value. Runtime options remain on the
	// compatibility path while sessions/events are migrated, but instructions,
	// direct tools, and MCP sources are now fixed before construction.
	definition := mcpagent.AgentDefinition{
		Instructions: instructions,
		Tools: mcpagent.ToolSet{
			Direct: directTools,
			MCP:    mcpSources,
		},
	}
	platformBridgeTools := platformBridgeToolsFromDirectTools(directTools)
	runtime := mcpagent.RuntimeConfig{
		Model:         llm,
		MCPConfigPath: configPath,
		Generation:    generation,
		Tools: mcpagent.ToolRuntimeConfig{
			SelectedTools: selectedTools, SelectedServers: serverNames,
			CodeExecution: useCodeExecutionMode, ParallelExecution: enableParallelToolExecution,
			// read_image is a platform executor. Mount it directly for coding
			// agents (including Muse structured workflow steps) when it exists in
			// this agent's admitted direct-tool registry.
			AdditionalBridge: platformBridgeTools,
		},
		Context: mcpagent.ContextRuntimeConfig{
			Offloading: enableContextOffloading, LargeOutputThreshold: largeOutputThreshold,
		},
		Coding: mcpagent.CodingRuntimeConfig{
			PersistentClaudeCode: codingAgentKeepAlive, PersistentCodex: codingAgentKeepAlive,
			PersistentCursor: codingAgentKeepAlive, PersistentPi: codingAgentKeepAlive,
			PersistentMuse:    codingAgentKeepAlive,
			CLISecurityPolicy: cliSecurityPolicy,
		},
		MCP: mcpagent.MCPRuntimeConfig{SessionID: mcpSessionID, RuntimeOverrides: runtimeOverrides},
		Workspace: mcpagent.WorkspaceRuntimeConfig{
			CodingAgentWorkingDir: codingAgentWorkingDir, IsolatedSession: isolateCodingAgentWorkspace,
			OutputDir: codingAgentOutputDir,
		},
		Observability: mcpagent.ObservabilityRuntimeConfig{
			Logger: logger, Tracers: []observability.Tracer{tracer}, TraceID: traceID,
			// See the matching comment in pkg/agentwrapper/llm_agent.go: false here
			// suppresses clean content-chunk streaming for every workflow agent,
			// leaving structured-transport providers (pi-cli) with no streaming
			// signal at all for the whole turn.
			PromptLogLabel: name, Streaming: true, GenerationStreamingEvents: boolPointer(true),
			// A retained coding-CLI session can accept a later turn directly through
			// tmux without starting another mcpagent Ask stream. The bridge remains
			// the authoritative place where tools execute, so publish its canonical
			// start/end/error receipts as well; otherwise formatted mode shows the
			// agent working but silently omits every tool from those continued turns.
			DirectToolExecutionEvents: true,
		},
	}
	if forceStructuredCodingAgent {
		runtime.Coding.Transport = internalLLM.CodingAgentTransportStructured
	}
	agent, err := mcpagent.NewAgentFromDefinition(ctx, definition, runtime)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	return &BaseAgent{
		agent:        agent,
		name:         name,
		agentType:    agentType,
		logger:       logger,
		tracer:       tracer,
		traceID:      traceID,
		instructions: instructions,
		mode:         mode,
		serverNames:  serverNames,
		llm:          llm,
		configPath:   configPath,
		modelID:      modelID,
		temperature:  temperature,
		toolChoice:   toolChoice,
		maxTurns:     maxTurns,
		provider:     provider,
		mcpSessionID: mcpSessionID,
		definition:   definition,
		runtime:      runtime,
	}, nil
}

// Execute executes the agent with user message and conversation history
func (ba *BaseAgent) Execute(ctx context.Context, userMessage string, conversationHistory []llmtypes.MessageContent, systemPrompt string, overwriteSystemPrompt bool) (string, []llmtypes.MessageContent, error) {
	// Removed verbose logging

	// Set or append system prompt if provided
	if systemPrompt != "" {
		if err := ba.ApplyInstructions(ctx, systemPrompt, overwriteSystemPrompt); err != nil {
			return "", nil, err
		}
	}
	if err := ba.finalizeDefinition(ctx); err != nil {
		return "", nil, err
	}

	startTime := time.Now()

	// Execute the agent with orchestrator context and conversation history
	orchestratorCtx := context.WithValue(ctx, orchestratorIDKey, fmt.Sprintf("%s_%s_%d", ba.agentType, ba.name, time.Now().UnixNano()))
	if ba.mcpSessionID != "" {
		// Continuation turns that call BaseAgent.Execute directly must still preserve
		// the per-agent MCP session so workspace tools resolve the correct session-
		// scoped folder guard instead of falling back to the parent workflow guard.
		orchestratorCtx = context.WithValue(orchestratorCtx, common.ChatSessionIDKey, ba.mcpSessionID)
	}
	result, err := ba.session.Run(orchestratorCtx, mcpagent.Turn{
		Input:   userMessage,
		History: conversationHistory,
	})
	ba.lastHandle = result.Handle

	executionTime := time.Since(startTime)

	if err != nil {
		return "", nil, fmt.Errorf("agent execution failed: %w", err)
	}

	// Removed verbose logging
	_ = executionTime

	return result.Text, result.History, nil
}

// ApplyInstructions creates a new immutable agent identity when a caller needs
// a different prompt. Provider continuation is carried through the opaque
// handle; the existing Agent is never mutated in place.
func (ba *BaseAgent) ApplyInstructions(ctx context.Context, systemPrompt string, overwrite bool) error {
	if strings.TrimSpace(systemPrompt) == "" {
		return nil
	}
	if !ba.finalized {
		if overwrite {
			ba.definition.Instructions = strings.TrimSpace(systemPrompt)
		} else {
			appendDefinitionInstructions(&ba.definition, systemPrompt)
		}
		ba.instructions = ba.definition.Instructions
		return nil
	}

	nextInstructions := systemPrompt
	if !overwrite && strings.TrimSpace(ba.definition.Instructions) != "" {
		nextInstructions = ba.definition.Instructions + "\n\n" + systemPrompt
	}
	if nextInstructions == ba.definition.Instructions {
		return nil
	}

	nextDefinition := ba.definition
	nextDefinition.Instructions = nextInstructions
	return ba.replaceDefinition(ctx, nextDefinition, false)
}

// ApplyIdentity rebuilds the agent with additional construction-time skills
// and prompt supplements. It exists for workflow factories that resolve these
// inputs after their base configuration is loaded but before the first turn.
func (ba *BaseAgent) ApplyIdentity(ctx context.Context, skills []*llmtypes.Skill, supplements ...string) error {
	if !ba.finalized {
		for _, skill := range skills {
			if skill == nil {
				return fmt.Errorf("assemble agent skill: skill cannot be nil")
			}
			ba.definition.Skills = append(ba.definition.Skills, skill)
		}
		for _, supplement := range supplements {
			appendDefinitionInstructions(&ba.definition, supplement)
		}
		ba.instructions = ba.definition.Instructions
		return nil
	}

	nextDefinition := ba.definition
	nextDefinition.Skills = append(append([]*llmtypes.Skill(nil), nextDefinition.Skills...), skills...)
	for _, supplement := range supplements {
		if strings.TrimSpace(supplement) == "" {
			continue
		}
		if strings.TrimSpace(nextDefinition.Instructions) == "" {
			nextDefinition.Instructions = supplement
		} else {
			nextDefinition.Instructions += "\n\n" + supplement
		}
	}
	return ba.replaceDefinition(ctx, nextDefinition, false)
}

// ApplyTool adds a construction-time tool to the immutable definition. The
// structured-output path uses this for its completion contract before the
// first turn; a later contract change rebuilds the definition rather than
// mutating the live Agent registry.
func (ba *BaseAgent) ApplyTool(ctx context.Context, tool mcpagent.ToolDefinition) error {
	for _, existing := range ba.definition.Tools.Direct {
		if existing.Name == tool.Name {
			return nil
		}
	}
	if !ba.finalized {
		ba.definition.Tools.Direct = append(ba.definition.Tools.Direct, tool)
		return nil
	}

	nextDefinition := ba.definition
	nextDefinition.Tools.Direct = append(append([]mcpagent.ToolDefinition(nil), nextDefinition.Tools.Direct...), tool)
	return ba.replaceDefinition(ctx, nextDefinition, true)
}

// RegisterCustomTool appends a direct tool to the construction-time
// AgentDefinition. Tool registration after the first turn is deliberately
// rejected; callers must create a replacement definition instead.
func (ba *BaseAgent) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), displayGroup string) error {
	return ba.RegisterCustomToolWithTimeout(name, description, parameters, execute, 0, displayGroup)
}

func (ba *BaseAgent) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, displayGroup string) error {
	if ba.finalized {
		return fmt.Errorf("agent definition is already finalized")
	}
	return ba.ApplyTool(context.Background(), mcpagent.ToolDefinition{
		Name:         name,
		Description:  description,
		InputSchema:  parameters,
		Execute:      execute,
		Timeout:      timeout,
		DisplayGroup: displayGroup,
	})
}

func (ba *BaseAgent) AttachSkill(skill *llmtypes.Skill) error {
	return ba.ApplyIdentity(context.Background(), []*llmtypes.Skill{skill})
}

func (ba *BaseAgent) AttachedSkills() []*llmtypes.Skill {
	return append([]*llmtypes.Skill(nil), ba.definition.Skills...)
}

func (ba *BaseAgent) AddObserver(observer mcpagent.AgentEventListener) error {
	if ba.finalized {
		return fmt.Errorf("agent definition is already finalized")
	}
	if observer == nil {
		return fmt.Errorf("observer cannot be nil")
	}
	ba.runtime.Observability.Observers = append(ba.runtime.Observability.Observers, observer)
	return nil
}

// Observers returns every event listener registered on this agent so far
// (PLAT-166: a reflection turn reusing this same agent needs to find its
// already-attached cost observer to toggle its phase, rather than attaching
// a second one).
func (ba *BaseAgent) Observers() []mcpagent.AgentEventListener {
	return append([]mcpagent.AgentEventListener(nil), ba.runtime.Observability.Observers...)
}

func (ba *BaseAgent) finalizeDefinition(ctx context.Context) error {
	if ba.finalized {
		return nil
	}
	runtime := ba.runtime
	runtime.ResumeHandle = ba.lastHandle
	nextAgent, err := mcpagent.NewAgentFromDefinition(ctx, ba.definition, runtime)
	if err != nil {
		return fmt.Errorf("finalize immutable agent definition: %w", err)
	}
	old := ba.agent
	nextSession, err := nextAgent.Start(ctx)
	if err != nil {
		nextAgent.Close()
		return fmt.Errorf("start finalized agent session: %w", err)
	}
	ba.agent = nextAgent
	ba.session = nextSession
	ba.instructions = ba.definition.Instructions
	ba.finalized = true
	mcpagent.RetireReplacedAgent(old)
	return nil
}

func appendDefinitionInstructions(definition *mcpagent.AgentDefinition, instruction string) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return
	}
	if definition.Instructions != "" {
		definition.Instructions += "\n\n"
	}
	definition.Instructions += instruction
}

func (ba *BaseAgent) replaceDefinition(ctx context.Context, nextDefinition mcpagent.AgentDefinition, force bool) error {
	if !force && nextDefinition.Instructions == ba.definition.Instructions && len(nextDefinition.Skills) == len(ba.definition.Skills) {
		return nil
	}
	nextRuntime := ba.runtime
	nextRuntime.ResumeHandle = ba.lastHandle
	nextAgent, err := mcpagent.NewAgentFromDefinition(ctx, nextDefinition, nextRuntime)
	if err != nil {
		return fmt.Errorf("rebuild agent identity: %w", err)
	}
	nextSession, err := nextAgent.Start(ctx)
	if err != nil {
		nextAgent.Close()
		return fmt.Errorf("start rebuilt agent session: %w", err)
	}
	if ba.session != nil {
		_ = ba.session.Close()
	}
	mcpagent.RetireReplacedAgent(ba.agent)
	ba.agent = nextAgent
	ba.session = nextSession
	ba.definition = nextDefinition
	ba.instructions = nextDefinition.Instructions
	ba.finalized = true
	return nil
}

// Resume reconstructs the runtime around persisted continuation state without
// mutating the existing Agent instance.
func (ba *BaseAgent) Resume(ctx context.Context, handle *mcpagent.AgentSessionHandle) error {
	if handle == nil || handle.Empty() {
		return nil
	}
	ba.lastHandle = handle
	return ba.replaceDefinition(ctx, ba.definition, true)
}

// SessionHandle returns the latest opaque continuation state produced by Run.
func (ba *BaseAgent) SessionHandle() *mcpagent.AgentSessionHandle {
	return ba.lastHandle
}

// MCPSessionID returns the platform-assigned session id this agent's MCP
// connections and bridge tool calls are routed under.
//
// This is a different id space from SessionHandle().Provider.NativeSessionID:
// that one is the CLI's own internal session UUID (used for --resume against
// its transcript); this one is what the HTTP bridge handler
// (mcpagent/executor.HandleCustomExecute) receives as req.SessionID and keys
// toolcalllog by. Recovering a call from toolcalllog needs this id, not the
// native one (PLAT-142/143).
func (ba *BaseAgent) MCPSessionID() string {
	if ba == nil {
		return ""
	}
	return ba.mcpSessionID
}

// SetWorkspacePolicy configures filesystem enforcement as runtime policy,
// separate from immutable instructions, skills, and tools.
func (ba *BaseAgent) SetWorkspacePolicy(readPaths, writePaths []string) {
	ba.runtime.Workspace.ReadPaths = append([]string(nil), readPaths...)
	ba.runtime.Workspace.WritePaths = append([]string(nil), writePaths...)
}

// Send routes input into the active runtime session without exposing Agent
// transport internals to workflow callers.
func (ba *BaseAgent) Send(ctx context.Context, input string) (mcpagent.DeliveryResult, error) {
	if ba.session == nil {
		return mcpagent.DeliveryResult{}, fmt.Errorf("agent session is not running")
	}
	return ba.session.Send(ctx, input)
}

// Agent returns the underlying MCP agent
func (ba *BaseAgent) Agent() *mcpagent.Agent {
	return ba.agent
}

// GetName returns the agent name
func (ba *BaseAgent) GetName() string {
	return ba.name
}

// GetType returns the agent type
func (ba *BaseAgent) GetType() AgentType {
	return ba.agentType
}

// GetInstructions returns the agent instructions
func (ba *BaseAgent) GetInstructions() string {
	return ba.instructions
}

// GetMode returns the agent mode
func (ba *BaseAgent) GetMode() AgentMode {
	return ba.mode
}

// GetServerNames returns the server names
func (ba *BaseAgent) GetServerNames() []string {
	return ba.serverNames
}

// Close closes the agent
func (ba *BaseAgent) Close() error {
	if ba.session != nil {
		_ = ba.session.Close()
	}
	if ba.agent != nil {
		ba.agent.Close()
	}
	return nil
}

// GetWorkflowContext returns the workflow context
func (ba *BaseAgent) GetWorkflowContext() map[string]interface{} {
	return ba.workflowContext
}

// SetWorkflowContext sets the workflow context
func (ba *BaseAgent) SetWorkflowContext(context map[string]interface{}) {
	ba.workflowContext = context
}

// GetPreviousAgentOutput returns the previous agent output
func (ba *BaseAgent) GetPreviousAgentOutput() string {
	return ba.previousAgentOutput
}

// SetPreviousAgentOutput sets the previous agent output
func (ba *BaseAgent) SetPreviousAgentOutput(output string) {
	ba.previousAgentOutput = output
}
