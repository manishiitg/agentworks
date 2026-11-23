package mcpagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/agent/convrecord"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ToolExecutor is the implementation of a direct tool.
type ToolExecutor func(context.Context, map[string]interface{}) (string, error)

// AgentDefinition contains the three immutable identity inputs of an agent.
// NewAgentFromDefinition clones this value before creating runtime state.
type AgentDefinition struct {
	Instructions string
	Skills       []*llmtypes.Skill
	Tools        ToolSet
}

// ToolSet contains direct implementations and configured MCP tool sources.
// Tools from both sources share one model-facing namespace.
type ToolSet struct {
	Direct []ToolDefinition
	MCP    []MCPToolSource
}

// ToolDefinition describes one directly executed tool.
//
// DisplayGroup is optional presentation metadata. It is retained by the
// compatibility registry while categories are removed from authorization and
// addressing; callers and models must address the tool only by Name.
type ToolDefinition struct {
	Name         string
	Description  string
	InputSchema  map[string]interface{}
	Execute      ToolExecutor
	Timeout      time.Duration
	DisplayGroup string
}

// MCPToolSource selects a configured MCP server by its stable configuration
// name. Connection details and credentials remain runtime configuration.
type MCPToolSource struct {
	Name string
}

// RuntimeConfig contains infrastructure needed to operate an agent definition.
// Runtime concerns are grouped by purpose so construction has one explicit
// value instead of an order-sensitive list of functional options.
type RuntimeConfig struct {
	Model         llmtypes.Model
	MCPConfigPath string
	ResumeHandle  *AgentSessionHandle
	Generation    GenerationRuntimeConfig
	Tools         ToolRuntimeConfig
	Coding        CodingRuntimeConfig
	MCP           MCPRuntimeConfig
	Workspace     WorkspaceRuntimeConfig
	Observability ObservabilityRuntimeConfig
}

type GenerationRuntimeConfig struct {
	Provider    llm.Provider
	LLM         AgentLLMConfiguration
	Temperature float64
	ToolChoice  string
	MaxTurns    int
	APIKeys     *AgentAPIKeys
}

type ToolRuntimeConfig struct {
	SelectedTools     []string
	SelectedServers   []string
	CodeExecution     bool
	ParallelExecution bool
	Timeout           time.Duration
	DisableCache      bool

	AdditionalBridge []string
}

type CodingRuntimeConfig struct {
	Transport            llm.CodingAgentTransport
	ClaudeCodeTransport  string
	PersistentClaudeCode bool
	PersistentCodex      bool
	PersistentCursor     bool
	PersistentPi         bool
	PersistentMuse       bool
	PersistentAgy        bool
	// UserAnswersNativeQuestions says a person is attending this chat and can
	// answer the coding CLI's native multiple-choice questions (PLAT-354). It
	// is deliberately separate from the Persistent* flags: a scheduler also
	// keeps the native session alive, but nobody is there to answer, so a
	// question must still be auto-answered instead of waiting forever.
	UserAnswersNativeQuestions bool
	CursorBridgeTools          bool
	// AgentToolsMode selects whether coding-provider native tools are available:
	// mcp_only (default) or hybrid.
	AgentToolsMode string
	// ApprovalsMode controls the provider-native approval posture when native
	// tools are enabled: provider_auto (default) or approve_all.
	ApprovalsMode string
	// BridgeToolAdmit filters the hardcoded core bridge tools this session
	// advertises (execute_shell_command, diff_patch_workspace_file,
	// agent_browser). Nil advertises all of them. Pass the same predicate that
	// decides tool registration so the catalog cannot offer a tool the session
	// will refuse.
	BridgeToolAdmit                   func(name string) bool
	CodexSandbox                      string
	CodexNetworkAccess                bool
	CLISecurityPolicy                 *llmtypes.CLISecurityPolicy
	BridgeRoutingInstructionsOverride *string
	// BridgeBinary is the mcpbridge executable to spawn for coding-CLI
	// transports. Explicit beats the MCP_BRIDGE_BINARY environment variable,
	// which beats PATH lookup.
	BridgeBinary string
	// SecretEnvironment is injected only into the native coding-agent child
	// process for the current turn. Admission is decided by
	// llmtypes.IsScopedCodingAgentEnvironmentKey, which is the single owner of
	// that policy: SECRET_* values, VAR_* workflow variables, and — when an
	// application explicitly enables native API transport — its scoped MCP API
	// routes. This layer must not filter by its own list; doing so is what
	// dropped the MCP routes before the child ever saw them.
	SecretEnvironment map[string]string
}

type MCPRuntimeConfig struct {
	// VirtualToolScopeKey identifies a host-owned agent slot within SessionID.
	// Empty keeps a private per-build scope for helpers and child agents.
	VirtualToolScopeKey string
	SessionID           string
	UserID              string
	RuntimeOverrides    mcpclient.RuntimeOverrides
	APIBaseURL          string
	APIToken            string
	// BridgeAPIBaseURL is the host-reachable executor URL the coding-CLI
	// bridge subprocess calls back into, when it differs from APIBaseURL (an
	// in-Docker URL, say). Explicit values take precedence over the
	// MCP_BRIDGE_API_URL / MCP_API_URL environment fallbacks, so several
	// executors can coexist in one process without touching the environment.
	BridgeAPIBaseURL string
}

type WorkspaceRuntimeConfig struct {
	CodingAgentWorkingDir string
	// CodingAgentRuntimeDir separates generated files from a person's project.
	CodingAgentRuntimeDir string
	IsolatedSession       bool
	// OutputDir is an application-admitted, existing artifact directory. With
	// IsolatedSession, it is linked as output/ without copying its contents.
	OutputDir  string
	ReadPaths  []string
	WritePaths []string
}

type ObservabilityRuntimeConfig struct {
	Logger                    loggerv2.Logger
	Tracers                   []observability.Tracer
	TraceID                   observability.TraceID
	PromptLogLabel            string
	ConversationSink          convrecord.Sink
	Streaming                 bool
	GenerationStreamingEvents *bool
	StreamingCallback         func(llmtypes.StreamChunk)
	Observers                 []AgentEventListener
	// DirectToolExecutionEvents emits the canonical tool-call event pair from
	// the bridge handler that actually executes a direct tool. Coding CLIs only
	// report their intent in their transcript, and some omit the result; this
	// opt-in provides the authoritative arguments, result/error and duration.
	DirectToolExecutionEvents bool
}

// NewAgentFromDefinition constructs an Agent from a validated, cloned identity.
// It is the only public constructor. Identity assembly is atomic: the
// definition is cloned and validated before any runtime state exists, so a
// returned Agent is always fully formed. The returned type exposes exactly four
// methods and no fields, so callers cannot mutate identity after construction.
func NewAgentFromDefinition(ctx context.Context, definition AgentDefinition, runtime RuntimeConfig) (*Agent, error) {
	if runtime.Model == nil {
		return nil, fmt.Errorf("runtime model cannot be nil")
	}

	definition, err := cloneAndValidateAgentDefinition(definition)
	if err != nil {
		return nil, err
	}

	// The output/ link instructions talk about native file tools and native shell commands. In
	// mcp_only mode those tools are disabled and the same prompt says so, so the paragraph contradicts
	// the tool list and models read it as "writing is not allowed" (false "read-only filesystem"
	// failures, PLAT-496). Add it only when native tools are on.
	if strings.TrimSpace(runtime.Workspace.OutputDir) != "" &&
		normalizeCodingAgentToolsMode(runtime.Coding.AgentToolsMode) == codingAgentToolsFull {
		definition.Instructions += isolatedOutputInstructions
	}
	options := runtimeAgentOptions(runtime)
	options = append(options, withSystemPrompt(definition.Instructions))
	if len(definition.Tools.MCP) > 0 {
		names := make([]string, 0, len(definition.Tools.MCP))
		for _, source := range definition.Tools.MCP {
			names = append(names, source.Name)
		}
		options = append(options, withServerName(strings.Join(names, ",")))
	}

	agent, err := newAgent(ctx, runtime.Model, runtime.MCPConfigPath, options...)
	if err != nil {
		return nil, err
	}
	finishToolRegistration := agent.DeferToolRegistryPublication()
	defer finishToolRegistration()
	fail := func(cause error) (*Agent, error) {
		// Flush before Close so the deferred finish cannot republish a closed session.
		finishToolRegistration()
		_ = agent.Close()
		return nil, cause
	}
	if err := agent.prepareIsolatedOutputLink(); err != nil {
		return fail(err)
	}
	if runtime.ResumeHandle != nil && !runtime.ResumeHandle.Empty() {
		// A rejected handle leaves the agent without native state; the
		// turn then falls back to history replay instead of resuming.
		agent.applyAgentSessionHandle(runtime.ResumeHandle)
	}
	agent.setFolderGuardPaths(
		append([]string(nil), runtime.Workspace.ReadPaths...),
		append([]string(nil), runtime.Workspace.WritePaths...),
	)

	for _, skill := range definition.Skills {
		if err := agent.attachSkill(skill); err != nil {
			return fail(fmt.Errorf("attach skill %q: %w", skill.Name, err))
		}
	}
	for _, tool := range definition.Tools.Direct {
		group := strings.TrimSpace(tool.DisplayGroup)
		if group == "" {
			group = "custom"
		}
		if err := agent.registerCustomToolWithTimeout(
			tool.Name,
			tool.Description,
			tool.InputSchema,
			tool.Execute,
			tool.Timeout,
			group,
		); err != nil {
			return fail(fmt.Errorf("register direct tool %q: %w", tool.Name, err))
		}
	}
	for _, observer := range runtime.Observability.Observers {
		if observer == nil {
			return fail(fmt.Errorf("runtime observer cannot be nil"))
		}
		agent.addEventListener(observer)
	}
	agent.definition = &definition

	return agent, nil
}

func runtimeAgentOptions(runtime RuntimeConfig) []agentOption {
	options := make([]agentOption, 0, 32)
	generation := runtime.Generation
	if generation.Provider != "" {
		options = append(options, withProvider(generation.Provider))
	}
	if generation.LLM.Primary.Provider != "" || generation.LLM.Primary.ModelID != "" {
		options = append(options, withLLMConfig(generation.LLM))
	}
	if generation.Temperature != 0 {
		options = append(options, withTemperature(generation.Temperature))
	}
	if generation.ToolChoice != "" {
		options = append(options, withToolChoice(generation.ToolChoice))
	}
	if generation.MaxTurns != 0 {
		options = append(options, withMaxTurns(generation.MaxTurns))
	}
	if generation.APIKeys != nil {
		options = append(options, withAPIKeys(generation.APIKeys))
	}

	tools := runtime.Tools
	if len(tools.SelectedTools) > 0 {
		options = append(options, withSelectedTools(tools.SelectedTools))
	}
	if len(tools.SelectedServers) > 0 {
		options = append(options, withSelectedServers(tools.SelectedServers))
	}
	if tools.CodeExecution {
		options = append(options, withCodeExecutionMode(true))
	}
	if tools.ParallelExecution {
		options = append(options, withParallelToolExecution(true))
	}
	if tools.Timeout != 0 {
		options = append(options, withToolTimeout(tools.Timeout))
	}
	if tools.DisableCache {
		options = append(options, withDisableCache(true))
	}

	if len(tools.AdditionalBridge) > 0 {
		options = append(options, withAdditionalBridgeTools(tools.AdditionalBridge...))
	}

	coding := runtime.Coding
	if coding.Transport != "" {
		options = append(options, withCodingAgentTransport(coding.Transport))
	}
	if coding.ClaudeCodeTransport != "" {
		options = append(options, withClaudeCodeTransport(coding.ClaudeCodeTransport))
	}
	if coding.PersistentClaudeCode {
		options = append(options, withClaudeCodePersistentInteractiveSession(true))
	}
	if coding.PersistentCodex {
		options = append(options, withCodexPersistentInteractiveSession(true))
	}
	if coding.PersistentCursor {
		options = append(options, withCursorPersistentInteractiveSession(true))
	}
	if coding.PersistentPi {
		options = append(options, withPiPersistentInteractiveSession(true))
	}
	if coding.PersistentMuse {
		options = append(options, withMusePersistentInteractiveSession(true))
	}
	if coding.PersistentAgy {
		options = append(options, withAgyPersistentInteractiveSession(true))
	}
	if coding.UserAnswersNativeQuestions {
		options = append(options, withUserAnswersNativeQuestions(true))
	}
	if coding.CursorBridgeTools {
		options = append(options, withCursorBridgeToolsMode(true))
	}
	if coding.AgentToolsMode != "" {
		options = append(options, withCodingAgentToolsMode(coding.AgentToolsMode))
	}
	if coding.ApprovalsMode != "" {
		options = append(options, withCodingAgentApprovalsMode(coding.ApprovalsMode))
	}
	if coding.BridgeToolAdmit != nil {
		options = append(options, withBridgeToolAdmit(coding.BridgeToolAdmit))
	}
	if coding.CodexSandbox != "" {
		options = append(options, withCodexSandbox(coding.CodexSandbox))
	}
	if coding.CodexNetworkAccess {
		options = append(options, withCodexNetworkAccess(true))
	}
	if coding.CLISecurityPolicy != nil {
		options = append(options, withCLISecurityPolicy(*coding.CLISecurityPolicy))
	}
	if coding.BridgeRoutingInstructionsOverride != nil {
		options = append(options, withBridgeRoutingInstructions(*coding.BridgeRoutingInstructionsOverride))
	}
	if coding.BridgeBinary != "" {
		options = append(options, withBridgeBinary(coding.BridgeBinary))
	}
	if len(coding.SecretEnvironment) > 0 {
		options = append(options, withCodingAgentSecretEnvironment(coding.SecretEnvironment))
	}

	mcpConfig := runtime.MCP
	if mcpConfig.VirtualToolScopeKey != "" {
		options = append(options, func(a *Agent) { a.bridgeVirtualScopeID = mcpConfig.VirtualToolScopeKey })
	}
	if mcpConfig.SessionID != "" {
		options = append(options, withSessionID(mcpConfig.SessionID))
	}
	if mcpConfig.UserID != "" {
		options = append(options, withUserID(mcpConfig.UserID))
	}
	if len(mcpConfig.RuntimeOverrides) > 0 {
		options = append(options, withRuntimeOverrides(mcpConfig.RuntimeOverrides))
	}
	if mcpConfig.APIBaseURL != "" || mcpConfig.APIToken != "" {
		options = append(options, withAPIConfig(mcpConfig.APIBaseURL, mcpConfig.APIToken))
	}
	if mcpConfig.BridgeAPIBaseURL != "" {
		options = append(options, withBridgeAPIBaseURL(mcpConfig.BridgeAPIBaseURL))
	}

	workspace := runtime.Workspace
	if workspace.CodingAgentWorkingDir != "" {
		options = append(options, withCodingAgentWorkingDir(workspace.CodingAgentWorkingDir))
	}
	if workspace.CodingAgentRuntimeDir != "" {
		options = append(options, func(a *Agent) { a.codingAgentRuntimeDir = strings.TrimSpace(workspace.CodingAgentRuntimeDir) })
	}
	if workspace.OutputDir != "" {
		options = append(options, withCodingAgentOutputDir(workspace.OutputDir))
	}
	if workspace.IsolatedSession {
		options = append(options, withIsolatedSessionWorkspace(true))
	}

	obs := runtime.Observability
	if obs.Logger != nil {
		options = append(options, withLogger(obs.Logger))
	}
	for _, tracer := range obs.Tracers {
		if tracer != nil {
			options = append(options, withTracer(tracer))
		}
	}
	if obs.TraceID != "" {
		options = append(options, withTraceID(obs.TraceID))
	}
	if obs.PromptLogLabel != "" {
		options = append(options, withPromptLogLabel(obs.PromptLogLabel))
	}
	if obs.ConversationSink != nil {
		options = append(options, withConversationSink(obs.ConversationSink))
	}
	if obs.Streaming {
		options = append(options, withStreaming(true))
	}
	if obs.GenerationStreamingEvents != nil {
		options = append(options, withGenerationStreamingEvents(*obs.GenerationStreamingEvents))
	}
	if obs.StreamingCallback != nil {
		options = append(options, withStreamingCallback(obs.StreamingCallback))
	}
	if obs.DirectToolExecutionEvents {
		options = append(options, withDirectToolExecutionEvents(true))
	}
	return options
}

func cloneAndValidateAgentDefinition(input AgentDefinition) (AgentDefinition, error) {
	result := AgentDefinition{Instructions: input.Instructions}

	result.Skills = make([]*llmtypes.Skill, 0, len(input.Skills))
	seenSkills := make(map[string]struct{}, len(input.Skills))
	for i, skill := range input.Skills {
		if skill == nil {
			return AgentDefinition{}, fmt.Errorf("skill %d is nil", i)
		}
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			return AgentDefinition{}, fmt.Errorf("skill %d has an empty name", i)
		}
		if _, exists := seenSkills[name]; exists {
			return AgentDefinition{}, fmt.Errorf("duplicate skill name %q", name)
		}
		seenSkills[name] = struct{}{}
		result.Skills = append(result.Skills, cloneSkill(skill))
	}

	result.Tools.MCP = make([]MCPToolSource, 0, len(input.Tools.MCP))
	seenSources := make(map[string]struct{}, len(input.Tools.MCP))
	for i, source := range input.Tools.MCP {
		name := strings.TrimSpace(source.Name)
		if name == "" {
			return AgentDefinition{}, fmt.Errorf("MCP tool source %d has an empty name", i)
		}
		if name != source.Name {
			return AgentDefinition{}, fmt.Errorf("MCP tool source %q has surrounding whitespace", source.Name)
		}
		if _, exists := seenSources[name]; exists {
			return AgentDefinition{}, fmt.Errorf("duplicate MCP tool source %q", name)
		}
		seenSources[name] = struct{}{}
		result.Tools.MCP = append(result.Tools.MCP, MCPToolSource{Name: name})
	}

	result.Tools.Direct = make([]ToolDefinition, 0, len(input.Tools.Direct))
	seenTools := make(map[string]struct{}, len(input.Tools.Direct))
	for i, tool := range input.Tools.Direct {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return AgentDefinition{}, fmt.Errorf("direct tool %d has an empty name", i)
		}
		if name != tool.Name {
			return AgentDefinition{}, fmt.Errorf("direct tool %q has surrounding whitespace", tool.Name)
		}
		if tool.Execute == nil {
			return AgentDefinition{}, fmt.Errorf("direct tool %q has no executor", name)
		}
		if name == readSkillToolName {
			return AgentDefinition{}, fmt.Errorf("direct tool name %q is reserved for attached skill access", name)
		}
		if _, exists := seenTools[name]; exists {
			return AgentDefinition{}, fmt.Errorf("duplicate direct tool name %q", name)
		}
		seenTools[name] = struct{}{}
		result.Tools.Direct = append(result.Tools.Direct, ToolDefinition{
			Name:         name,
			Description:  tool.Description,
			InputSchema:  cloneStringAnyMap(tool.InputSchema),
			Execute:      tool.Execute,
			Timeout:      tool.Timeout,
			DisplayGroup: tool.DisplayGroup,
		})
	}

	return result, nil
}

func cloneSkill(input *llmtypes.Skill) *llmtypes.Skill {
	result := *input
	result.Paths = append([]string(nil), input.Paths...)
	if input.Metadata != nil {
		result.Metadata = make(map[string]string, len(input.Metadata))
		for key, value := range input.Metadata {
			result.Metadata[key] = value
		}
	}
	result.SupportingFiles = make([]llmtypes.SkillFile, len(input.SupportingFiles))
	for i, file := range input.SupportingFiles {
		result.SupportingFiles[i] = file
		result.SupportingFiles[i].Content = append([]byte(nil), file.Content...)
	}
	return &result
}

func cloneStringAnyMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	result := make(map[string]interface{}, len(input))
	for key, value := range input {
		result[key] = cloneJSONLikeValue(value)
	}
	return result
}

func cloneJSONLikeValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneStringAnyMap(typed)
	case []interface{}:
		result := make([]interface{}, len(typed))
		for i, item := range typed {
			result[i] = cloneJSONLikeValue(item)
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
