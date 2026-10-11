package types

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// WorkflowPhaseOption represents an option for a workflow phase
type WorkflowPhaseOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"` // Group this option belongs to (e.g., "run_management", "execution_strategy")
	Default     bool   `json:"default"`
}

// WorkflowPhase represents a workflow phase
type WorkflowPhase struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Options     []WorkflowPhaseOption `json:"options,omitempty"`
}

// WorkflowStatus represents a workflow status
type WorkflowStatus struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// WorkflowConstants contains all workflow-related constants
type WorkflowConstants struct {
	Phases []WorkflowPhase `json:"phases"`
}

// GetWorkflowConstants returns the current workflow constants
func GetWorkflowConstants() WorkflowConstants {
	return WorkflowConstants{
		Phases: []WorkflowPhase{
			{
				ID:          workflowtypes.WorkflowStatusWorkflowBuilder,
				Title:       "Workflow Builder",
				Description: "Execute steps, update the plan, tweak configs, generate learnings, debug, and manage schedules — all in one conversation.",
				Options:     []WorkflowPhaseOption{},
			},
			{
				ID:          workflowtypes.WorkflowStatusPreVerification,
				Title:       "Execution",
				Description: "Execute the approved plan using MCP tools. This phase runs after planning is complete.",
				Options:     []WorkflowPhaseOption{},
			},
		},
	}
}

// GetWorkflowPhaseByID returns a workflow phase by its ID
func GetWorkflowPhaseByID(id string) *WorkflowPhase {
	constants := GetWorkflowConstants()
	for _, phase := range constants.Phases {
		if phase.ID == id {
			return &phase
		}
	}
	return nil
}

// HandleWorkflowConstants returns the current workflow constants via HTTP
func HandleWorkflowConstants(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get workflow constants
	workflowConstants := GetWorkflowConstants()

	// Create response
	response := map[string]interface{}{
		"success":   true,
		"constants": workflowConstants,
		"message":   "Workflow constants retrieved successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// WorkflowOrchestrator handles todo-list-based workflow execution
type WorkflowOrchestrator struct {
	// Base orchestrator for common functionality
	*orchestrator.BaseOrchestrator

	// Preset-level agent defaults
	presetBuilderLLM *step_based_workflow.AgentLLMConfig // Default for workflow-builder agents.
	presetPulseLLM   *step_based_workflow.AgentLLMConfig // Default for review, maintenance, and Pulse agents.

	// Preset-level feature toggles
	useKnowledgebase bool   // Whether to create and reference knowledgebase folder (default: true)
	kbShape          string // "graph+notes" (default) | "notes-only"; controls which KB artifacts exist.

	// Tiered LLM allocation mode
	tieredConfig *step_based_workflow.TieredLLMConfig

	// Frontend-provided execution options (when provided, skips interactive prompts)
	executionOptions *step_based_workflow.ExecutionOptions

	// Synthetic plan for "Task Agent" mode
	virtualPlan *step_based_workflow.PlanningResponse

	// Session ID for MCP connection management
	// Generated once when workflow starts, used by all agents to share MCP connections
	sessionID string

	// HTTP session ID (from the frontend/API layer) for scoped MCP cleanup
	httpSessionID string

	// Explicit CDP ports for browser mode detection and specialized
	// multi-profile/login testing. cdpPort remains the primary compatibility value.
	cdpPort  int
	cdpPorts []int

	// Browser mode for prompt instructions ("headless", "cdp", or "none").
	browserMode string

	// toolCallQueryFunc provides live tool call query capability for workshop sessions.
	// Set by the server layer which has access to the EventStore.
	toolCallQueryFunc step_based_workflow.ToolCallQueryFunc

	// extraSubAgentNotifier is an additional notifier set by the server layer (e.g. for bgAgentRegistry).
	// It is reserved for execution paths that need extra sub-agent notifications.
	extraSubAgentNotifier step_based_workflow.SubAgentNotifier
}

// SetToolCallQueryFunc sets the function for querying live tool calls from the event store.
// Called by server.go after creating the orchestrator to inject EventStore access.
func (wo *WorkflowOrchestrator) SetToolCallQueryFunc(fn step_based_workflow.ToolCallQueryFunc) {
	wo.toolCallQueryFunc = fn
}

// SetExtraSubAgentNotifier sets an additional notifier chained with the workshop notifier.
// Used by server.go to hook bgAgentRegistry for synthetic turn notifications.
func (wo *WorkflowOrchestrator) SetExtraSubAgentNotifier(n step_based_workflow.SubAgentNotifier) {
	wo.extraSubAgentNotifier = n
}

// SetVirtualPlan sets a synthetic plan for the workflow (used by Task Agent mode)
func (wo *WorkflowOrchestrator) SetVirtualPlan(plan *step_based_workflow.PlanningResponse) {
	wo.virtualPlan = plan
}

// SetExecutionOptions sets the execution details for the current workflow run.
func (wo *WorkflowOrchestrator) SetExecutionOptions(options *step_based_workflow.ExecutionOptions) {
	wo.executionOptions = options
	if options != nil {
		wo.ApplyWorkflowLogContext(wo.GetWorkspacePath(), orchestrator.SingleSelectedGroupName(options.EnabledGroupNames))
	}
	if options != nil {
		wo.GetLogger().Info(fmt.Sprintf("📋 WorkflowOrchestrator: Execution options set: strategy=%s, run_folder=%s",
			options.ExecutionStrategy, options.SelectedRunFolder))
	}
}

// GetExecutionOptions returns the current execution options
func (wo *WorkflowOrchestrator) GetExecutionOptions() *step_based_workflow.ExecutionOptions {
	return wo.executionOptions
}

// UseKnowledgebase returns whether the knowledgebase feature is enabled
func (wo *WorkflowOrchestrator) UseKnowledgebase() bool {
	return wo.useKnowledgebase
}

// KBShape returns the preset-declared KB shape (raw; empty = default "graph+notes").
func (wo *WorkflowOrchestrator) KBShape() string {
	return wo.kbShape
}

// SetKBShape overrides the KB shape; accepts empty to fall back to the default.
func (wo *WorkflowOrchestrator) SetKBShape(v string) {
	wo.kbShape = v
}

// Human verification types
type HumanVerificationRequest struct {
	Objective        string    `json:"objective"`
	TodoListMarkdown string    `json:"todo_list_markdown"`
	GeneratedAt      time.Time `json:"generated_at"`
	VerificationID   string    `json:"verification_id"`
	Status           string    `json:"status"` // "pending", "approved", "modified", "rejected"
}

// LLM verification check
type LLMVerificationCheck struct {
	VerificationFile string    `json:"verification_file"`
	CheckedAt        time.Time `json:"checked_at"`
	IsVerified       bool      `json:"is_verified"`
	Reasoning        string    `json:"reasoning"`
}

// Todo verification response
type TodoVerificationResponse struct {
	VerificationID           string    `json:"verification_id"`
	IsApproved               bool      `json:"is_approved"`
	Reasoning                string    `json:"reasoning"`
	VerifiedAt               time.Time `json:"verified_at"`
	SuggestedModifications   []string  `json:"suggested_modifications,omitempty"`
	ModifiedTodoListMarkdown string    `json:"modified_todo_list_markdown,omitempty"`
}

// convertDBAgentLLMConfig converts a workflowtypes.AgentLLMConfig to step_based_workflow.AgentLLMConfig,
// preserving the selected model.
func convertDBAgentLLMConfig(dbConfig *workflowtypes.AgentLLMConfig) *step_based_workflow.AgentLLMConfig {
	if dbConfig == nil {
		return nil
	}
	cfg := &step_based_workflow.AgentLLMConfig{
		PublishedLLMID: dbConfig.PublishedLLMID,
		Provider:       dbConfig.Provider,
		ModelID:        dbConfig.ModelID,
		Options:        dbConfig.Options,
		ConnectionID:   dbConfig.ConnectionID,
	}

	return cfg
}

// NewWorkflowOrchestrator creates a new workflow orchestrator
// Note: provider and model parameters removed - LLM selection uses temp override → step config → preset LLM priority
func NewWorkflowOrchestrator(
	mcpConfigPath string,
	temperature float64,
	agentMode string,
	logger loggerv2.Logger,
	eventBridge mcpagent.AgentEventListener,
	tracer observability.Tracer,
	selectedServers []string,
	selectedTools []string, // NEW parameter
	useCodeExecutionMode bool, // NEW parameter
	customTools []llmtypes.Tool,
	customToolExecutors map[string]interface{},
	llmConfig *orchestrator.LLMConfig,
	maxTurns int,
	toolCategories map[string]string, // NEW: tool category map
	presetLLMConfig *workflowtypes.PresetLLMConfig, // Optional preset LLM config for agent defaults
) (*WorkflowOrchestrator, error) {

	// Create base orchestrator
	// Note: provider and model parameters removed - not used for workflow orchestrator (LLM comes from temp override/step config/preset)
	baseOrchestrator, err := orchestrator.NewBaseOrchestrator(
		logger,
		eventBridge,
		orchestrator.OrchestratorTypeWorkflow,
		mcpConfigPath,
		temperature,
		agentMode,
		selectedServers,
		selectedTools,        // NEW: Pass through
		useCodeExecutionMode, // NEW: Pass through
		llmConfig,            // LLM configuration
		maxTurns,
		customTools,
		customToolExecutors,
		toolCategories, // NEW: Pass category map
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create base orchestrator: %w", err)
	}

	// Resolve the workflow Builder and Pulse models.
	var presetBuilderLLM *step_based_workflow.AgentLLMConfig
	var presetPulseLLM *step_based_workflow.AgentLLMConfig
	var resolvedCodingAgentTiered *workflowtypes.TieredLLMConfig
	if presetLLMConfig != nil {
		if builder, tiered, ok := workflowtypes.ResolveProviderProfileConfig(presetLLMConfig); ok {
			presetBuilderLLM = convertDBAgentLLMConfig(builder)
			resolvedCodingAgentTiered = tiered
			if pulse, ok := workflowtypes.ResolveProviderProfilePulseConfig(presetLLMConfig); ok {
				presetPulseLLM = convertDBAgentLLMConfig(pulse)
			}
			log.Printf("[CODING_AGENT_LLM] Resolved provider profile %s dynamically", presetLLMConfig.Provider)
		} else {
			if presetLLMConfig.PulseLLM != nil && presetLLMConfig.PulseLLM.Provider != "" && presetLLMConfig.PulseLLM.ModelID != "" {
				presetPulseLLM = convertDBAgentLLMConfig(presetLLMConfig.PulseLLM)
			}
			if presetLLMConfig.BuilderLLM != nil && presetLLMConfig.BuilderLLM.Provider != "" && presetLLMConfig.BuilderLLM.ModelID != "" {
				presetBuilderLLM = convertDBAgentLLMConfig(presetLLMConfig.BuilderLLM)
			}
		}
	}
	if presetPulseLLM == nil {
		presetPulseLLM = presetBuilderLLM
	}

	// Extract tiered LLM allocation config
	var tieredConfig *step_based_workflow.TieredLLMConfig
	if resolvedCodingAgentTiered != nil || (presetLLMConfig != nil && presetLLMConfig.TieredConfig != nil) {
		sourceTiered := resolvedCodingAgentTiered
		if sourceTiered == nil {
			sourceTiered = presetLLMConfig.TieredConfig
		}
		tier1 := convertDBAgentLLMConfig(sourceTiered.Tier1)
		tier2 := convertDBAgentLLMConfig(sourceTiered.Tier2)
		tier3 := convertDBAgentLLMConfig(sourceTiered.Tier3)
		tieredConfig = &step_based_workflow.TieredLLMConfig{
			Tier1: tier1,
			Tier2: tier2,
			Tier3: tier3,
		}
		// The Builder model is independent of the execution tiers.
		if presetBuilderLLM != nil {
			log.Printf("[TIERED_LLM] Builder model: %s/%s", presetBuilderLLM.Provider, presetBuilderLLM.ModelID)
		} else {
			log.Printf("[TIERED_LLM] WARNING: No Builder model configured - builder agents will fail")
		}
		log.Printf("[TIERED_LLM] Tiered mode enabled - Tier1: %s/%s, Tier2: %s/%s, Tier3: %s/%s",
			tieredConfig.Tier1.Provider, tieredConfig.Tier1.ModelID,
			tieredConfig.Tier2.Provider, tieredConfig.Tier2.ModelID,
			tieredConfig.Tier3.Provider, tieredConfig.Tier3.ModelID)
	}

	// Extract feature toggles from preset config
	useKnowledgebase := true // Default to enabled
	if presetLLMConfig != nil && presetLLMConfig.UseKnowledgebase != nil {
		useKnowledgebase = *presetLLMConfig.UseKnowledgebase
	}
	kbShape := ""
	if presetLLMConfig != nil {
		kbShape = presetLLMConfig.KBShape
	}

	// Create workflow orchestrator instance
	wo := &WorkflowOrchestrator{
		BaseOrchestrator: baseOrchestrator,
		presetBuilderLLM: presetBuilderLLM,
		presetPulseLLM:   presetPulseLLM,
		useKnowledgebase: useKnowledgebase,
		kbShape:          kbShape,
		tieredConfig:     tieredConfig,
	}

	return wo, nil
}

// executeFlow executes a workflow with the given parameters
func (wo *WorkflowOrchestrator) executeFlow(
	ctx context.Context,
	objective string,
	workspacePath string,
	workflowStatus string,
	selectedOptions *workflowtypes.WorkflowSelectedOptions,
	stepID string, // Optional step ID for step-specific phase execution
) (string, error) {
	// Initialize MCP session ID early so all agents share connections
	// This generates the session ID and propagates it to BaseOrchestrator
	sessionID := wo.getSessionID()
	wo.GetLogger().Info(fmt.Sprintf("🔗 Workflow using MCP session: %s", sessionID))

	// Close all session connections when workflow ends
	// This releases browser profiles and other resources held by MCP servers
	defer func() {
		wo.GetLogger().Info(fmt.Sprintf("🔗 Closing MCP session: %s", sessionID))
		mcpagent.CloseSession(sessionID)
	}()

	// Set workspace path from parameter
	wo.SetWorkspacePath(workspacePath)
	wo.ApplyWorkflowLogContext(workspacePath, orchestrator.SingleSelectedGroupName(func() []string {
		if wo.executionOptions == nil {
			return nil
		}
		return wo.executionOptions.EnabledGroupNames
	}()))
	if wo.GetWorkspacePath() == "" {
		return "", fmt.Errorf("workspace path is required")
	}

	// Route to appropriate phase based on workflow status
	// IMPORTANT: Each phase is isolated and should NOT trigger other phases
	// Note: Variable extraction is now handled by planning agent tools, no separate phase needed

	if workflowStatus == workflowtypes.WorkflowStatusWorkflowBuilder {
		return "", fmt.Errorf("%s is a chat-only phase — use phase chat mode instead of orchestrator execution", workflowStatus)
	}

	// All other workflow statuses (execution) go through execution phase
	// Execution requires both variables.json and plan.json to exist
	return wo.runPlanning(ctx, objective, selectedOptions)
}

// runPlanning runs the execution phase (requires both variables.json and plan.json to exist)
// This is called for execution status and executes the approved plan
func (wo *WorkflowOrchestrator) runPlanning(ctx context.Context, objective string, selectedOptions *workflowtypes.WorkflowSelectedOptions) (string, error) {
	wo.GetLogger().Info(fmt.Sprintf("🚀 Starting Execution Phase"))

	return wo.runHumanControlledPlanning(ctx, objective)
}

// runHumanControlledPlanning runs the execution phase (CreateTodoList)
// This requires both variables.json and plan.json to exist
func (wo *WorkflowOrchestrator) runHumanControlledPlanning(ctx context.Context, objective string) (string, error) {
	wo.GetLogger().Info(fmt.Sprintf("🚀 Running Execution for objective: %s", objective))

	// Create human controlled planner orchestrator directly
	llmConfig := wo.GetLLMConfig()
	todoPlannerAgent, err := step_based_workflow.NewStepBasedWorkflowOrchestrator(
		ctx,
		"", // provider (not used - LLM comes from temp override/step config/preset)
		"", // model (not used - LLM comes from temp override/step config/preset)
		wo.GetTemperature(),
		wo.GetAgentMode(),
		wo.GetSelectedServers(),
		wo.GetSelectedTools(),        // NEW: Pass selected tools
		wo.GetUseCodeExecutionMode(), // NEW: Pass code execution mode
		wo.GetMCPConfigPath(),
		llmConfig,
		wo.GetMaxTurns(),
		wo.GetLogger(),
		wo.GetTracer(),
		wo.GetContextAwareBridge(),
		wo.WorkspaceTools,
		wo.WorkspaceToolExecutors,
		wo.ToolCategories,
		wo.presetBuilderLLM,
		wo.presetPulseLLM,
		wo.useKnowledgebase,
		wo.tieredConfig,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create human controlled planner orchestrator: %w", err)
	}

	// Propagate workspace env ref BEFORE session ID so SetMCPSessionID can update it
	if envRef := wo.GetWorkspaceEnvRef(); envRef != nil {
		todoPlannerAgent.SetWorkspaceEnvRef(envRef)
	}

	// Propagate MCP session ID to child orchestrator for connection sharing
	todoPlannerAgent.SetMCPSessionID(wo.getSessionID())
	// Propagate HTTP session ID for MCP cleanup scoping
	if wo.httpSessionID != "" {
		todoPlannerAgent.SetHTTPSessionID(wo.httpSessionID)
	}

	// Propagate selected skills to child orchestrator
	if skills := wo.GetSelectedSkills(); len(skills) > 0 {
		todoPlannerAgent.SetSelectedSkills(skills)
	}

	// Propagate secrets to child orchestrator
	if secrets := wo.GetSecrets(); len(secrets) > 0 {
		todoPlannerAgent.SetSecrets(secrets)
	}

	// Propagate CDP port for browser mode detection
	if len(wo.cdpPorts) > 0 {
		todoPlannerAgent.SetCdpPorts(wo.cdpPorts)
	} else if wo.cdpPort > 0 {
		todoPlannerAgent.SetCdpPort(wo.cdpPort)
	}
	if wo.browserMode != "" {
		todoPlannerAgent.SetBrowserMode(wo.browserMode)
	}

	// Propagate the declared KB shape.
	todoPlannerAgent.SetKBShape(wo.kbShape)

	// Pass execution options from WorkflowOrchestrator to the todo planner if set
	if wo.executionOptions != nil {
		todoPlannerAgent.SetExecutionOptions(wo.executionOptions)
		wo.GetLogger().Info(fmt.Sprintf("📋 Passed execution options to todo planner: strategy=%s",
			wo.executionOptions.ExecutionStrategy))
	}

	// Generate todo list using Execute method
	planningResult, err := todoPlannerAgent.Execute(ctx, objective, wo.GetWorkspacePath(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create/update todo list: %w", err)
	}

	// Emit the workflow completion event. Do not also emit unified_completion here:
	// the workflow step/agent end events already carry the result text, and rendering
	// both events causes duplicate final output in the UI.
	wo.EmitOrchestratorEnd(ctx, objective, planningResult, "completed", "", "workflow_execution")

	return planningResult, nil
}

// Helper methods for workflow operations
// getSessionID returns the session ID for this workflow
// The session ID is generated once and reused for all agents in the workflow
// This allows MCP connections to be shared across agents in the same workflow
func (wo *WorkflowOrchestrator) getSessionID() string {
	if wo.sessionID == "" {
		// Generate session ID once when first requested
		wo.sessionID = fmt.Sprintf("workflow-session-%d", time.Now().UnixNano())
		wo.GetLogger().Info(fmt.Sprintf("🔗 Generated MCP session ID: %s", wo.sessionID))
		// Propagate to BaseOrchestrator so all agents inherit the session ID via config
		wo.SetMCPSessionID(wo.sessionID)
		// Register with HTTP session tracker so CloseHTTPSession cleans it up on stop
		if wo.httpSessionID != "" {
			mcpagent.RegisterHTTPSession(wo.httpSessionID, wo.sessionID)
		}
	}
	return wo.sessionID
}

// GetMCPSessionID returns the MCP session ID for external use (e.g., agent config)
func (wo *WorkflowOrchestrator) GetMCPSessionID() string {
	return wo.getSessionID()
}

// SetHTTPSessionID sets the HTTP session ID so that MCP sessions can be tracked
// and closed via mcpagent.CloseHTTPSession when the workflow stops.
func (wo *WorkflowOrchestrator) SetHTTPSessionID(httpSessionID string) {
	wo.httpSessionID = httpSessionID
}

// SetCdpPort sets the CDP port for browser mode detection.
// 0 = headless mode, >0 = CDP mode (connected to user's Chrome).
func (wo *WorkflowOrchestrator) SetCdpPort(port int) {
	wo.SetCdpPorts([]int{port})
}

// SetCdpPorts authorizes independent Chrome profiles for this workflow.
func (wo *WorkflowOrchestrator) SetCdpPorts(ports []int) {
	wo.cdpPorts = append([]int(nil), ports...)
	wo.cdpPort = 0
	if len(wo.cdpPorts) > 0 {
		wo.cdpPort = wo.cdpPorts[0]
	}
}

// SetBrowserMode sets the browser mode for prompt instructions.
func (wo *WorkflowOrchestrator) SetBrowserMode(mode string) {
	wo.browserMode = mode
}

// Execute implements the Orchestrator interface
func (wo *WorkflowOrchestrator) Execute(ctx context.Context, objective string, workspacePath string, options map[string]interface{}) (string, error) {
	logger := wo.GetLogger()
	if logger == nil {
		return "", fmt.Errorf("logger is nil in Execute method")
	}

	// Validate options if provided
	if options != nil {
		// Validate workflowStatus if provided
		if workflowStatusVal, exists := options["workflowStatus"]; exists {
			if workflowStatus, ok := workflowStatusVal.(string); !ok {
				return "", fmt.Errorf("invalid workflowStatus: expected string, got %T", workflowStatusVal)
			} else if workflowStatus == "" {
				return "", fmt.Errorf("invalid workflowStatus: cannot be empty string")
			} else {
				// Validate it's a known workflow status
				validStatuses := []string{
					workflowtypes.WorkflowStatusPreVerification,
					workflowtypes.WorkflowStatusWorkflowBuilder,
				}
				valid := false
				for _, status := range validStatuses {
					if workflowStatus == status {
						valid = true
						break
					}
				}
				if !valid {
					return "", fmt.Errorf("invalid workflowStatus: %s, valid statuses: %v", workflowStatus, validStatuses)
				}
			}
		}

		// Validate selectedOptions if provided
		if selectedOptsVal, exists := options["selectedOptions"]; exists {
			if selectedOptsVal != nil {
				if _, ok := selectedOptsVal.(*workflowtypes.WorkflowSelectedOptions); !ok {
					return "", fmt.Errorf("invalid selectedOptions: expected *workflowtypes.WorkflowSelectedOptions, got %T", selectedOptsVal)
				}
			}
		}
	}

	// Extract options from the map with defaults
	var workflowStatus string
	if ws, ok := options["workflowStatus"].(string); ok && ws != "" {
		workflowStatus = ws
	} else {
		workflowStatus = workflowtypes.WorkflowStatusPreVerification // Default to planning phase
	}

	var selectedOptions *workflowtypes.WorkflowSelectedOptions
	if opts, ok := options["selectedOptions"]; ok && opts != nil {
		if so, ok := opts.(*workflowtypes.WorkflowSelectedOptions); ok {
			selectedOptions = so
		}
	}

	// Validate workspace path is provided
	if workspacePath == "" {
		return "", fmt.Errorf("workspace path is required")
	}

	// Extract stepId from options if provided
	var stepID string
	if stepIDVal, exists := options["stepId"]; exists {
		if stepIDStr, ok := stepIDVal.(string); ok && stepIDStr != "" {
			stepID = stepIDStr
		}
	}

	// Call the existing executeFlow method with the extracted parameters
	result, err := wo.executeFlow(ctx, objective, workspacePath, workflowStatus, selectedOptions, stepID)
	if err != nil {
		wo.GetLogger().Error(fmt.Sprintf("❌ Workflow execution failed: %v", err), err)
		return "", err
	}

	return result, nil
}
