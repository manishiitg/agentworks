package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	baseevents "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var workflowExecutionIDCounter atomic.Uint64

// parseWorkflowStepStringMap validates a JSON object whose keys are workflow
// step IDs and whose values are strings. MCP argument decoding normally yields
// map[string]interface{}, while direct/internal callers may supply
// map[string]string; both are valid and neither may be silently discarded.
func parseWorkflowStepStringMap(args map[string]interface{}, name string) (map[string]string, error) {
	raw, exists := args[name]
	if !exists || raw == nil {
		return nil, nil
	}

	values := make(map[string]string)
	add := func(rawKey string, rawValue interface{}) error {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return fmt.Errorf("%s entries must have non-empty step IDs", name)
		}
		value, ok := rawValue.(string)
		if !ok {
			return fmt.Errorf("%s[%q] must be a string", name, rawKey)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s[%q] must be a non-empty string", name, rawKey)
		}
		if _, duplicate := values[key]; duplicate {
			return fmt.Errorf("%s contains duplicate step ID %q after trimming whitespace", name, key)
		}
		values[key] = value
		return nil
	}

	switch decoded := raw.(type) {
	case map[string]interface{}:
		for key, value := range decoded {
			if err := add(key, value); err != nil {
				return nil, err
			}
		}
	case map[string]string:
		for key, value := range decoded {
			if err := add(key, value); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("%s must be an object keyed by step ID", name)
	}
	if len(values) == 0 {
		return nil, nil
	}
	return values, nil
}

func unknownWorkflowStepInputIDs(steps []PlanStepInterface, inputs map[string]string) []string {
	if len(inputs) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(steps))
	collectKnownWorkflowStepIDs(steps, known)
	unknown := make([]string, 0)
	for stepID := range inputs {
		if _, ok := known[stepID]; !ok {
			unknown = append(unknown, stepID)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// collectKnownWorkflowStepIDs descends into todo_task predefined_routes the
// same way collectStepIDsRecursive (planning_management.go) does for step-ID
// uniqueness, so a human_inputs key can target either a top-level step or a
// route inside an orchestrator by the exact ID shown in the plan JSON.
//
// Without this, run_full_workflow(human_inputs={"a-route-id": "..."}) was
// rejected outright with "human_inputs contains unknown step ID(s)" — a route
// ID is real and visible in the plan, but this check never looked past the
// top-level steps array, so the rejection was wrong, not the input.
func collectKnownWorkflowStepIDs(steps []PlanStepInterface, known map[string]struct{}) {
	for _, step := range steps {
		if step == nil {
			continue
		}
		if id := strings.TrimSpace(step.GetID()); id != "" {
			known[id] = struct{}{}
		}
		if todo, ok := step.(*AgentPlanStep); ok {
			for _, route := range todo.PredefinedRoutes {
				if route.SubAgentStep == nil {
					continue
				}
				collectKnownWorkflowStepIDs([]PlanStepInterface{route.SubAgentStep}, known)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Chat-mode system prompt templates for debugger phases
// Key difference from orchestrator versions: no human_feedback requirement,
// conversational style, agent reads files on demand via workspace tools.
// ---------------------------------------------------------------------------

var executionDebuggerChatTemplate = MustRegisterTemplate("executionDebuggerChatSystem", `# Execution Debugger (Chat Mode)

## 🤖 ROLE
You are a **read-only** execution analysis assistant. Help the user understand what happened during workflow execution.

## ⚠️ RULES
1. **Read-Only**: You MUST NOT modify any files. You have no write access or plan modification tools.
2. **Answer Directly**: For general questions, answer from the plan context below.
3. **Read Files Only When Needed**: Only read execution logs if user asks about specific failures or "why did X happen".
4. **Conversational**: Ask follow-up questions if the user's query is ambiguous.

## 📋 CONTEXT
- **Workspace**: {{.WorkspacePath}}
- **Run folder**: Check 'runs/' directory for available iterations. Ask the user which run to analyze if unclear.

### Current Plan
{{if .ExistingPlanJSON}}`+"`"+`json
{{.ExistingPlanJSON}}
`+"`"+`{{else}}No plan provided. Read it from 'planning/plan.json'.{{end}}

## 📁 FILE LOCATIONS
- **Plan file**: '{{.WorkspacePath}}/planning/plan.json'
- **Runs**: '{{.WorkspacePath}}/runs/' — list to find available iterations
- **Execution outputs**: '{{.WorkspacePath}}/runs/{iteration}/{group}/execution/{step-id}/'
- **Validation logs**: '{{.WorkspacePath}}/runs/{iteration}/{group}/logs/{step-id}/validation-{N}.json'
- **Execution logs**: '{{.WorkspacePath}}/runs/{iteration}/{group}/logs/{step-id}/execution/'
- **Routing evaluations**: '{{.WorkspacePath}}/runs/{iteration}/{group}/logs/{step-id}/routing-evaluation.json'
- **Orchestration routing**: '{{.WorkspacePath}}/runs/{iteration}/{group}/logs/{step-id}/orchestration-execution.json' (JSONL)
- **Todo task progress**: '{{.WorkspacePath}}/runs/{iteration}/{group}/execution/{step-id}/tasks.md'

## 📖 STEP FOLDER NAMING
- Scripted steps: '{step-id}/' using the declared ID from planning/plan.json
- Conversational specialist calls: '{parent-step}/agents/{route-id}/calls/{call-id}/'
- Generic agent calls: '{parent-step}/agents/generic/calls/{call-id}/'
- Scripted Agent items: '{parent-step}/scripts/items/{item-id}/calls/{call-id}/'
- Scripted routes: '{parent-step}/scripts/routes/{route-id}/calls/{call-id}/'
- Specialist session logs: '{parent-step}/agents/{route-id}/session.json'

All nested runtime artifacts belong to the parent Agent subtree. Do not look for
retired flattened sub-agent or agents folders.

{{if .IsCodeExecutionMode}}{{"{{TOOL_STRUCTURE}}"}}{{end}}`)

// PhaseChatSystemPrompt generates the system prompt for any chat-compatible phase.
// Dispatches to the correct template based on phaseId.
func PhaseChatSystemPrompt(phaseId string, templateVars map[string]string) string {
	now := time.Now()
	templateData := map[string]interface{}{
		"WorkspacePath":               templateVars["WorkspacePath"],
		"ExistingPlanJSON":            templateVars["ExistingPlanJSON"],
		"VariableNames":               templateVars["VariableNames"],
		"IsCodeExecutionMode":         templateVars["IsCodeExecutionMode"],
		"UseProjectedReferenceSkills": templateVars["UseProjectedReferenceSkills"],
		"CurrentDate":                 promptDate(now),
		"CurrentTime":                 promptTime(now),
	}

	var tmpl = interactiveWorkshopSystemTemplate // default: workflow-builder template
	switch phaseId {
	case "execution-qa":
		tmpl = executionDebuggerChatTemplate
	case "workflow-builder":
		// Use the full workshop system template (same as orchestrator mode)
		// so the chat agent gets all plan design guidance, optimization tips, etc.
		// PlanJSON is intentionally NOT injected here — the agent reads plan.json
		// via shell command on demand, avoiding a large static injection on every request.
		templateData["RunFolder"] = templateVars["RunFolder"]
		templateData["StepConfigSummary"] = templateVars["StepConfigSummary"]
		templateData["ProgressSummary"] = templateVars["ProgressSummary"]
		templateData["GroupInfo"] = templateVars["GroupInfo"]
		templateData["UseKnowledgebase"] = templateVars["UseKnowledgebase"]
		kbShape := templateVars["KBShape"]
		if kbShape == "" {
			kbShape = workflowtypes.KBShapeGraphNotes
		}
		templateData["KBShape"] = kbShape
		templateData["CustomInstructions"] = templateVars["CustomInstructions"]
		templateData["StepSummary"] = templateVars["StepSummary"]
		templateData["WorkshopMode"] = templateVars["WorkshopMode"]
		templateData["ExecutionMode"] = templateVars["ExecutionMode"]
		templateData["AvailableGroups"] = templateVars["AvailableGroups"]
		// Both native API and CLI sessions attach the same mode-filtered reference skill.
		templateData["SpecialWorkspaceToolsInstructions"] = instructions.GetSpecialWorkspaceToolsPointer()
		wsPath := templateVars["WorkspacePath"]
		templateData["AbsWorkspacePath"] = GetPromptDocsRoot() + "/" + wsPath
		templateData["AbsDocsRoot"] = GetPromptDocsRoot()
		templateData["PlanJSON"] = ""    // Intentionally empty — agent reads plan.json on demand via shell command
		templateData["UserRequest"] = "" // Not applicable in chat mode — user messages come via conversation
		tmpl = interactiveWorkshopSystemTemplate
		if templateVars["WorkshopMode"] != "workshop" {
			tmpl = interactiveRunSystemTemplate
		}
	}

	var result strings.Builder
	if err := tmpl.Execute(&result, templateData); err != nil {
		panic(fmt.Sprintf("[FATAL] Phase chat system prompt template failed for phase=%q: %v — this means the LLM will receive no system prompt. Fix the template or templateData.", phaseId, err))
	}
	rendered := result.String()
	if phaseId == "workflow-builder" {
		rendered += "\n\n" + workflowFolderAccessBuilderPrompt(templateVars["WorkspacePath"])
	}
	// Guard against empty or suspiciously short prompts — the workshop template should be 10K+ chars
	if len(rendered) < 1000 {
		panic(fmt.Sprintf("[FATAL] Phase chat system prompt for phase=%q is only %d chars (expected 10000+). Template likely has missing variables or rendering issues.", phaseId, len(rendered)))
	}
	// Conditional blocks in the template each carry their own surrounding blank
	// line, so a block that renders empty (an {{if}} that's false, or one whose
	// body is itself blank in this mode) leaves runs of 3+ newlines behind.
	// Harmless to the model, but it reads like the prompt has holes in it.
	return collapseBlankLines(rendered)
}

// collapseBlankLines squashes 3+ consecutive newlines down to a single blank
// line (2 newlines), the same normalization a Markdown renderer would apply.
func collapseBlankLines(s string) string {
	return blankLineRunPattern.ReplaceAllString(s, "\n\n")
}

var blankLineRunPattern = regexp.MustCompile(`\n{3,}`)

// SchedulerCallbacks provides schedule CRUD operations via callbacks from server.go.
// This avoids importing database/scheduler packages in the workshop package.
type SchedulerCallbacks struct {
	ListSchedules          func(ctx context.Context, workspacePath string) (string, error)
	CreateSchedule         func(ctx context.Context, workspacePath, name, cronExpr, timezone string, groupNames []string, routeSelections map[string]string, mode string, messages []string, directMessagesReason string, workshopMode string, resumePrevious *bool, policy ScheduleRuntimePolicy) (string, error)
	CreateCalendarSchedule func(ctx context.Context, workspacePath, name, timezone string, groupNames []string, calendarItemsJSON string, mode string, messages []string, directMessagesReason string, workshopMode string, policy ScheduleRuntimePolicy) (string, error)
	UpdateSchedule         func(ctx context.Context, jobID, name, cronExpr, timezone string, groupNames []string, setGroupNames bool, routeSelections map[string]string, setRouteSelections bool, enabled *bool, mode string, messages []string, setMessages bool, directMessagesReason *string, workshopMode string, resumePrevious *bool, policy *ScheduleRuntimePolicy) (string, error)
	DeleteSchedule         func(ctx context.Context, jobID string) error
	TriggerSchedule        func(ctx context.Context, jobID string) (string, error)
	GetScheduleRuns        func(ctx context.Context, jobID string, limit int) (string, error)

	// GetContractUpgrades reports the workflow's pending contract migrations.
	GetContractUpgrades func(ctx context.Context, workspacePath string) (string, error)

	// NextContractUpgrade returns the next migration owed by the workflow.
	NextContractUpgrade func(ctx context.Context, workspacePath string) (target string, label string, err error)
}

// ScheduleRuntimePolicy carries typed scheduling behavior that must be
// enforced by the runtime rather than inferred from a natural-language
// message or a conveniently spaced cron expression.
type ScheduleRuntimePolicy struct {
	InputPreset              *string
	Inputs                   map[string]string
	ExecutionMode            string
	CollisionPolicy          string
	ConcurrencyMode          string
	ParallelRiskAcknowledged bool
	MaxStartDelayMinutes     int
	AfterScheduleID          string
	AfterScheduleIDs         []string
	AfterTerminalStatus      string
	AfterDelayMinutes        int
	DependencyDeadline       string
	// RunDestination and ChatID choose where a schedule with messages runs,
	// as the Schedules screen does (new, chat with chat_id, isolated).
	// Empty and nil keep the current choice.
	RunDestination string
	ChatID         *string
	// Set* is used only by update_schedule. It distinguishes an omitted field
	// from an explicit empty/zero value, so changing one policy does not erase
	// the other three.
	SetExecutionMode bool

	SetCollisionPolicy          bool
	SetConcurrencyMode          bool
	SetParallelRiskAcknowledged bool
	SetMaxStartDelayMinutes     bool
	SetAfterScheduleID          bool
	SetAfterScheduleIDs         bool
	SetAfterTerminalStatus      bool
	SetAfterDelayMinutes        bool
	SetDependencyDeadline       bool
}

// SkillCallbacks provides skill management operations via callbacks from server.go.
type SkillCallbacks struct {
	ListSkills   func(ctx context.Context) (string, error)
	ImportSkill  func(ctx context.Context, githubURL, token string) (string, error)
	DeleteSkill  func(ctx context.Context, folderName string) error
	SearchSkills func(ctx context.Context, query string) (string, error)  // Search skills registry via CLI
	InstallSkill func(ctx context.Context, source string) (string, error) // Install via npx skills add (owner/repo@skill)
}

// LLMToolsCallbacks provides LLM management operations via callbacks from server.go.
// This avoids importing the server package in the workshop package.
type LLMToolsCallbacks struct {
	ListPublishedLLMs  func(ctx context.Context) (string, error)
	ListProviderModels func(ctx context.Context, provider string) (string, error)
	ValidateLLM        func(ctx context.Context, args map[string]interface{}) (string, error)
}

// WorkshopChatSession holds the per-session controller and step registry for interactive
// workshop in chat mode. Create with NewWorkshopChatSession; clean up with Close().
type WorkshopChatSession struct {
	toolCallMu             sync.Mutex
	controller             *StepBasedWorkflowOrchestrator
	StepRegistry           *WorkshopStepRegistry
	sessionCtx             context.Context
	cancelFunc             context.CancelFunc
	toolCallQueryFunc      ToolCallQueryFunc
	tmuxLookupFunc         TmuxLookupFunc
	mainSessionID          string
	config                 *WorkshopConfig // Original config for creating fresh controllers
	schedulerWorkspacePath string
	schedulerFuncs         *SchedulerCallbacks
	skillFuncs             *SkillCallbacks
	llmToolsFuncs          *LLMToolsCallbacks
	listAvailableSecrets   func(ctx context.Context) ([]string, error)
	resolveSecretValues    func(ctx context.Context, names []string) map[string]string
	secretsAttached        func(set map[string]string, removed []string)
	// workshopNotifier is the base notifier wired to StepRegistry (set at creation time).
	// SetExtraSubAgentNotifier chains a server-side notifier on top of this.
	workshopNotifier      SubAgentNotifier
	extraSubAgentNotifier SubAgentNotifier
	executionNotifier     WorkshopExecutionNotifier // optional: notifies server when executions start/complete
	hasPendingCompletions func() bool               // optional: server-level check for queued completions
	hasRunningAgents      func() bool               // optional: server-level check for running background agents
	cancelAllServerAgents func()                    // optional: cancel all running agents in server registry
	listServerAgents      func() []ServerAgentInfo  // optional: list all agents from server registry
	workshopModeOverride  string                    // frontend-selected workshop mode; PLAT-262 forces this to "run" for a read-only identity, see workflow_phase_tools.go
	recoveryOnce          sync.Once                 // starts durable continuation replay once server notifiers are wired
	onStepCorrelationDone func(string)
}

// GetConfig returns the workshop config (for accessing session-aware executors, etc.)
func (s *WorkshopChatSession) GetConfig() *WorkshopConfig {
	return s.config
}

// SetOnStepCorrelationDone registers a callback invoked when a workshop step finishes
// its full-workflow run. The argument is the step's ForceCorrelationID
// ("workshop-step-<stepID>-<ts>"), allowing server-side maps keyed on that ID to be
// cleaned up when they're no longer needed.
func (s *WorkshopChatSession) SetOnStepCorrelationDone(fn func(string)) {
	s.onStepCorrelationDone = fn
}

func workflowRunValidationVariableValues(ctx context.Context, session *WorkshopChatSession, groupName string) map[string]string {
	if session == nil || session.controller == nil {
		return nil
	}
	manifest, err := readVariablesFromFile(ctx, session.controller.GetWorkspacePath(), func(ctx context.Context, path string) (string, error) {
		return session.controller.ReadWorkspaceFile(ctx, path)
	})
	if err != nil || manifest == nil {
		manifest = session.controller.variablesManifest
	}
	if manifest == nil {
		return nil
	}
	for _, group := range manifest.Groups {
		if group.Name == groupName || session.controller.sanitizeDisplayNameForFolder(group.Name) == groupName {
			return MergeGroupWithDefaults(manifest, group.Values)
		}
	}
	return MergeGroupWithDefaults(manifest, nil)
}

func routeScopedValidationSteps(steps []PlanStep, variableValues map[string]string, humanInputs map[string]string, routeSelections map[string]string) []PlanStep {
	for i, step := range steps {
		routingStep, ok := step.(routeSwitchStep)
		if !ok || len(routingStep.GetRoutes()) == 0 {
			continue
		}
		route := inferValidationRoute(routingStep, variableValues, humanInputs, routeSelections)
		if route == nil || strings.EqualFold(route.NextStepID, "end") {
			continue
		}
		start := planStepIndexByID(steps, route.NextStepID)
		if start < 0 || start <= i {
			continue
		}
		end := routeSegmentEndIndex(steps, start)
		if end < start {
			end = len(steps) - 1
		}
		scoped := make([]PlanStep, 0, i+1+end-start+1)
		scoped = append(scoped, steps[:i+1]...)
		scoped = append(scoped, steps[start:end+1]...)
		return scoped
	}
	return steps
}

func inferValidationRoute(step routeSwitchStep, variableValues map[string]string, humanInputs map[string]string, routeSelections map[string]string) *RoutingRoute {
	if step == nil {
		return nil
	}
	routes := step.GetRoutes()
	if routeSelections != nil {
		if raw := strings.TrimSpace(routeSelections[step.GetID()]); raw != "" {
			if routeID, err := resolveRouteSelectionValue(routes, raw); err == nil {
				for i := range routes {
					route := &routes[i]
					if route.RouteID == routeID {
						return route
					}
				}
			}
		}
	}
	if humanInputs != nil {
		if raw := strings.TrimSpace(humanInputs[step.GetID()]); raw != "" {
			normalized := strings.ToLower(raw)
			for i := range routes {
				route := &routes[i]
				if strings.ToLower(route.RouteID) == normalized || strings.ToLower(route.RouteName) == normalized {
					return route
				}
			}
		}
	}
	if defaultRouteID := strings.TrimSpace(step.GetDefaultRouteID()); defaultRouteID != "" {
		if routeID, err := resolveRouteSelectionValue(routes, defaultRouteID); err == nil {
			for i := range routes {
				route := &routes[i]
				if route.RouteID == routeID {
					return route
				}
			}
		}
	}
	for i := range routes {
		route := &routes[i]
		condition := strings.ToLower(route.Condition)
		for name, value := range variableValues {
			if value == "" {
				continue
			}
			varToken := "$var_" + strings.ToLower(name)
			if strings.Contains(condition, varToken) &&
				strings.Contains(condition, "equals") &&
				strings.Contains(condition, `"`+strings.ToLower(value)+`"`) {
				return route
			}
		}
	}
	return nil
}

func planStepIndexByID(steps []PlanStep, stepID string) int {
	for i, step := range steps {
		if step.GetID() == stepID {
			return i
		}
	}
	return -1
}

func routeSegmentEndIndex(steps []PlanStep, start int) int {
	for i := start; i < len(steps); i++ {
		routingStep, ok := steps[i].(routeSwitchStep)
		if !ok || len(routingStep.GetRoutes()) == 0 {
			continue
		}
		allEnd := true
		for _, route := range routingStep.GetRoutes() {
			if !strings.EqualFold(strings.TrimSpace(route.NextStepID), "end") {
				allEnd = false
				break
			}
		}
		if allEnd {
			return i
		}
	}
	return len(steps) - 1
}

// MainSessionID returns the owning chat session ID for this workshop session.
func (s *WorkshopChatSession) MainSessionID() string {
	return s.mainSessionID
}

// StopExecution uses the same step registry and termination notification as
// the run-mode stop_step tool. External callers can invoke it after checking
// the session and execution ownership without cancelling sibling work.
func (s *WorkshopChatSession) StopExecution(executionID string) (WorkshopStepSnapshot, error) {
	if s == nil {
		return WorkshopStepSnapshot{}, ErrWorkshopExecutionNotFound
	}
	return stopWorkshopExecution(s.StepRegistry, s.executionNotifier, executionID)
}

func (s *WorkshopChatSession) combinedSubAgentNotifier() SubAgentNotifier {
	if s == nil {
		return nil
	}
	if s.workshopNotifier != nil && s.extraSubAgentNotifier != nil {
		return ChainSubAgentNotifiers(s.workshopNotifier, s.extraSubAgentNotifier)
	}
	if s.workshopNotifier != nil {
		return s.workshopNotifier
	}
	return s.extraSubAgentNotifier
}

// SetExtraSubAgentNotifier chains a server-supplied notifier (e.g. bgAgentRegistry)
// with the workshop's own notifier. Safe to call on every request — always rebuilds
// the chain so there are no duplicates.
func (s *WorkshopChatSession) SetExtraSubAgentNotifier(n SubAgentNotifier) {
	s.extraSubAgentNotifier = n
	if s.controller != nil {
		s.controller.SetSubAgentNotifier(s.combinedSubAgentNotifier())
	}
}

// SetWorkshopExecutionNotifier sets the notifier that the server layer uses to track
// workshop step/background executions in bgAgentRegistry (keeps frontend polling alive).
func (s *WorkshopChatSession) SetWorkshopExecutionNotifier(n WorkshopExecutionNotifier) {
	s.executionNotifier = n
	s.controller.SetWorkshopExecutionNotifier(n)
	if n != nil {
		s.recoveryOnce.Do(func() {
			go s.RecoverPendingContinuations(context.Background())
		})
	}
}

// SetExecutionStateChecks sets server-level callbacks for querying and controlling background execution state.
func (s *WorkshopChatSession) SetExecutionStateChecks(hasPending, hasRunning func() bool, cancelAll func(), listAgents func() []ServerAgentInfo) {
	s.hasPendingCompletions = hasPending
	s.hasRunningAgents = hasRunning
	s.cancelAllServerAgents = cancelAll
	s.listServerAgents = listAgents
}

// SetWorkshopModeOverride sets the frontend-selected workshop mode.
// This takes priority over auto-detection when building AUTO-NOTIFICATION action hints.
func (s *WorkshopChatSession) SetWorkshopModeOverride(mode string) {
	s.workshopModeOverride = mode
}

// WorkshopConfig bundles all settings for a workshop session to replicate the
// exact same tool/LLM/browser/image-gen setup as normal workflow execution.
// Built by server.go using the same preset-loading logic as the normal workflow path.
type WorkshopConfig struct {
	// Crew step runs keep their current selection on the existing chat record,
	// so evicting a runtime never silently creates a different run for step 2.
	CurrentStepRun func(context.Context) (string, error)
	SelectStepRun  func(context.Context, string) error
	// Root Builder only. Never copied into the step tool pool.
	KnowledgebaseProjectTool func(context.Context, string, map[string]interface{}) (string, error)
	// UserID and SourcePlatform are server-resolved launch identity, never tool
	// arguments. Detached workshop children retain it for accounting (PLAT-377).
	UserID                 string
	SourcePlatform         string
	WebhookInvocation      *WebhookInvocation  // Set internally by API trigger dispatch, never from tool arguments.
	ScheduleInvocation     *ScheduleInvocation // Set internally by saved-schedule dispatch, never from tool arguments.
	CrewResources          *CrewResources      // Saved configuration and scripts supplied by the server.
	AgentFunctionTools     AgentFunctionToolFactory
	ScheduleCollisionCheck ScheduleCollisionCheck
	WorkspacePath          string
	RunFolder              string
	MCPConfigPath          string
	SelectedServers        []string
	SelectedTools          []string
	UseCodeExecutionMode   bool
	CustomTools            []llmtypes.Tool
	CustomToolExecutors    map[string]interface{}
	ToolCategories         map[string]string
	// BrowserRuntime stores configured intent (auto/cdp/headless + candidate
	// ports). The executor resolves live CDP reachability at tool-call time.
	BrowserRuntime      *browser.BrowserRuntimeConfig
	LLMConfig           *orchestrator.LLMConfig
	PresetPhaseLLM      *AgentLLMConfig
	PresetBackgroundLLM *AgentLLMConfig
	UseKnowledgebase    bool
	LLMAllocationMode   string
	TieredConfig        *TieredLLMConfig
	Logger              loggerv2.Logger
	EventBridge         mcpagent.AgentEventListener
	// Session tracking — needed for MCP connection sharing and session cleanup
	SessionID string
	// Secrets for step execution (merged global + user secrets)
	Secrets []orchestrator.SecretEntry
	// Skills loaded from preset for skill-based step execution
	SelectedSkills []string
	// WorkspaceEnvRef holds the env map reference for session-aware workspace executors.
	// When set, code execution mode uses this to get MCP_API_URL with session scoping.
	WorkspaceEnvRef map[string]string
	// EnabledGroupNames holds the group names selected from the workspace toolbar.
	// When set, the session auto-resolves variable values and run folder for these groups.
	EnabledGroupNames []string
	InputPreset       string
	Inputs            map[string]string
	// ToolCallQueryFunc provides live tool call query capability for query_step_tools.
	// Set by server.go which has access to the EventStore.
	ToolCallQueryFunc ToolCallQueryFunc
	// TmuxLookupFunc resolves the live tmux session name for a tmux-backed (coding-CLI)
	// step so query_step can surface it. Set by server.go (has the terminal store).
	TmuxLookupFunc TmuxLookupFunc
	// SchedulerWorkspacePath is the workspace folder path (needed for schedule management)
	SchedulerWorkspacePath string
	// SchedulerFuncs provides callbacks for schedule CRUD operations.
	// Set by server.go which has access to the database and scheduler service.
	SchedulerFuncs *SchedulerCallbacks
	// SkillFuncs provides callbacks for skill import/delete operations.
	// Set by server.go which has access to the workspace API.
	SkillFuncs *SkillCallbacks
	// LLMToolsFuncs provides callbacks for LLM management operations.
	// Set by server.go which has access to provider keys and model metadata.
	LLMToolsFuncs *LLMToolsCallbacks
	// ListAvailableSecrets returns names of all available secrets (global + workflow-stored).
	// Used by get_workflow_config to show which secrets can be added.
	ListAvailableSecrets func(ctx context.Context) ([]string, error)
	// ResolveSecretValues returns plaintext values for the given selected secret names,
	// keyed by their $SECRET_ name (a Vault secret by the name its owner gave it).
	// Missing names are simply absent from the returned map - never an error. Used by
	// update_workflow_config to refresh the workshop shell's SECRET_* env vars mid-session.
	ResolveSecretValues func(ctx context.Context, names []string) map[string]string
	// SecretsAttached pushes attached secret values (and removals) into the
	// builder chat's live shell, whose environment was captured at turn start.
	// Without it an attached secret stays unset until the next turn.
	SecretsAttached func(set map[string]string, removed []string)
}

// NewWorkshopChatSession creates a WorkshopChatSession using the full tool/LLM config
// from server.go — matching the exact same setup as a normal workflow execution.
func NewWorkshopChatSession(ctx context.Context, cfg *WorkshopConfig) (*WorkshopChatSession, error) {
	logger := cfg.Logger
	logger.Info(fmt.Sprintf("[WORKSHOP] NewWorkshopChatSession: workspace=%s, runFolder=%s, servers=%v",
		cfg.WorkspacePath, cfg.RunFolder, cfg.SelectedServers))
	logger.Info(fmt.Sprintf("[WORKSHOP] Config: tools=%d, executors=%d, categories=%d, codeExec=%v, knowledgebase=%v, llmMode=%s",
		len(cfg.CustomTools), len(cfg.CustomToolExecutors), len(cfg.ToolCategories),
		cfg.UseCodeExecutionMode, cfg.UseKnowledgebase, cfg.LLMAllocationMode))
	if cfg.PresetPhaseLLM != nil {
		logger.Info(fmt.Sprintf("[WORKSHOP] presetPhaseLLM=%s/%s", cfg.PresetPhaseLLM.Provider, cfg.PresetPhaseLLM.ModelID))
	}
	if cfg.PresetBackgroundLLM != nil {
		logger.Info(fmt.Sprintf("[WORKSHOP] presetBackgroundLLM=%s/%s", cfg.PresetBackgroundLLM.Provider, cfg.PresetBackgroundLLM.ModelID))
	}
	if cfg.TieredConfig != nil {
		logger.Info(fmt.Sprintf("[WORKSHOP] tiered: T1=%s T2=%s T3=%s",
			formatTierAgentLLM(cfg.TieredConfig.Tier1),
			formatTierAgentLLM(cfg.TieredConfig.Tier2),
			formatTierAgentLLM(cfg.TieredConfig.Tier3)))
	}
	// Log tool names for debugging
	toolNames := make([]string, 0, len(cfg.CustomTools))
	for _, t := range cfg.CustomTools {
		if t.Function != nil {
			toolNames = append(toolNames, t.Function.Name)
		}
	}
	logger.Info(fmt.Sprintf("[WORKSHOP] Tool definitions: %v", toolNames))

	sessionCtx, cancelFunc := newWorkshopSessionContext(ctx, cfg)

	controller, err := NewStepBasedWorkflowOrchestrator(
		ctx,
		"",       // provider (unused — LLM comes from preset/step config)
		"",       // model (unused)
		0.7,      // temperature
		"simple", // agentMode
		cfg.SelectedServers,
		cfg.SelectedTools,
		cfg.UseCodeExecutionMode,
		cfg.MCPConfigPath,
		cfg.LLMConfig,
		100, // maxTurns
		logger,
		nil, // tracer
		cfg.EventBridge,
		cfg.CustomTools,
		cfg.CustomToolExecutors,
		cfg.ToolCategories,
		cfg.PresetPhaseLLM,
		cfg.PresetBackgroundLLM,
		cfg.UseKnowledgebase,
		cfg.TieredConfig,
	)
	if err != nil {
		cancelFunc()
		return nil, fmt.Errorf("failed to create workshop controller: %w", err)
	}

	controller.SetWorkspacePath(cfg.WorkspacePath)
	bindCrewResources(controller, cfg.CrewResources)
	// Product policy must be loaded before the Builder registers execution tools.
	if err := controller.loadCodeLayout(ctx); err != nil {
		cancelFunc()
		return nil, fmt.Errorf("load workshop product: %w", err)
	}
	if cfg.BrowserRuntime != nil {
		mode, ports := cfg.BrowserRuntime.Snapshot()
		controller.SetBrowserMode(mode)
		controller.SetCdpPorts(ports)
	}

	// Propagate the HTTP session ID for chat history, but keep the controller's
	// independently generated MCP session ID for stateful connection isolation.
	if cfg.SessionID != "" {
		controller.SetHTTPSessionID(cfg.SessionID)
		logger.Debug(fmt.Sprintf("[WORKSHOP] Session ID propagation: HTTP=%s, MCP=%s (kept separate for stateful connection isolation)",
			cfg.SessionID, controller.GetMCPSessionID()))
	}

	// Propagate secrets for step execution
	if len(cfg.Secrets) > 0 {
		controller.SetSecrets(cfg.Secrets)
		logger.Debug(fmt.Sprintf("[WORKSHOP] Set %d secrets", len(cfg.Secrets)))
	}

	// Propagate selected skills
	if len(cfg.SelectedSkills) > 0 {
		controller.SetSelectedSkills(cfg.SelectedSkills)
		logger.Debug(fmt.Sprintf("[WORKSHOP] Set %d skills: %v", len(cfg.SelectedSkills), cfg.SelectedSkills))
	}

	// Propagate workspace env ref for code execution mode
	if cfg.WorkspaceEnvRef != nil {
		controller.SetWorkspaceEnvRef(cfg.WorkspaceEnvRef)
		logger.Debug(fmt.Sprintf("[WORKSHOP] Set workspace env ref (MCP_API_URL=%s)", cfg.WorkspaceEnvRef["MCP_API_URL"]))
	}

	// Set run folder if provided. With per-call group support, the run folder
	// can also be set on each execute_step call, so it's OK if empty here.
	if cfg.RunFolder != "" {
		controller.SetSelectedRunFolder(cfg.RunFolder)
		logger.Debug(fmt.Sprintf("[WORKSHOP] Run folder set from session init: %s", cfg.RunFolder))
	}
	if cfg.ScheduleInvocation != nil {
		controller.SetExecutionOptions(&ExecutionOptions{
			CrewResources:     cfg.CrewResources,
			SelectedRunFolder: cfg.ScheduleInvocation.BoundRunFolder(),
			RunKind:           cfg.ScheduleInvocation.RunKind(), ScheduleRunID: cfg.ScheduleInvocation.BoundRunID(),
			ScheduleID: cfg.ScheduleInvocation.ScheduleID, TriggerSource: cfg.ScheduleInvocation.TriggerSource,
			ScheduledFor: cfg.ScheduleInvocation.ScheduledFor,
		})
	}
	// Saved Crew resources resolve their service from the controller's options, and
	// execute_step builds fresh strategy options without touching them —
	// so the session runner must live on the controller from init.
	bindAgentFunctionTools(controller, cfg.AgentFunctionTools)

	controller.enabledGroupNames = nil
	var existingManifest *VariablesManifest
	if controller.directCrewScript != nil {
		existingManifest = controller.directCrewScript.InputDefinitions()
	} else {
		variablesPath := fmt.Sprintf("%s/variables/variables.json", cfg.WorkspacePath)
		_, existing, varErr := controller.variableManager.checkExistingVariables(ctx, variablesPath)
		if varErr != nil {
			return nil, fmt.Errorf("read workflow inputs: %w", varErr)
		}
		existingManifest = existing
	}
	controller.variablesManifest = existingManifest
	if controller.isRelayExecution() {
		values, err := controller.relayVariableValues()
		if err != nil {
			logger.Warn(fmt.Sprintf("[WORKSHOP] Flow configuration needs migration: %v", err))
		} else {
			controller.variableValues = values
			SyncExactVariablesToWorkspaceEnv(controller.BaseOrchestrator, values)
		}
	} else {
		values, err := ResolveWorkflowInputs(existingManifest, cfg.InputPreset, cfg.Inputs)
		if err != nil {
			return nil, err
		}
		controller.variableValues = values
		SyncExactVariablesToWorkspaceEnv(controller.BaseOrchestrator, values)
		if cfg.InputPreset != "" {
			controller.enabledGroupNames = []string{cfg.InputPreset}
		}
	}

	registry := NewWorkshopStepRegistry()
	wsn := &workshopSubAgentNotifier{registry: registry}
	controller.SetSubAgentNotifier(wsn)
	controller.SetWorkshopExecutionContext(sessionCtx, registry)

	return &WorkshopChatSession{
		controller:             controller,
		StepRegistry:           registry,
		sessionCtx:             sessionCtx,
		cancelFunc:             cancelFunc,
		toolCallQueryFunc:      cfg.ToolCallQueryFunc,
		tmuxLookupFunc:         cfg.TmuxLookupFunc,
		mainSessionID:          cfg.SessionID,
		config:                 cfg,
		schedulerWorkspacePath: cfg.SchedulerWorkspacePath,
		schedulerFuncs:         cfg.SchedulerFuncs,
		skillFuncs:             cfg.SkillFuncs,
		llmToolsFuncs:          cfg.LLMToolsFuncs,
		listAvailableSecrets:   cfg.ListAvailableSecrets,
		resolveSecretValues:    cfg.ResolveSecretValues,
		secretsAttached:        cfg.SecretsAttached,
		workshopNotifier:       wsn,
	}, nil
}

// Workshop work survives the request and ends when the session closes. Carry
// its authorized identity explicitly; a bare Background silently files every
// child LLM call under an empty user, even when the parent chat is attributed.
func newWorkshopSessionContext(ctx context.Context, cfg *WorkshopConfig) (context.Context, context.CancelFunc) {
	base := context.Background()
	userID, platform := strings.TrimSpace(cfg.UserID), strings.TrimSpace(cfg.SourcePlatform)
	if userID == "" {
		userID = common.SessionUserIDFromContext(ctx)
	}
	if platform == "" {
		platform = costobserver.SourcePlatformFromContext(ctx)
	}
	if userID != "" {
		base = context.WithValue(base, common.UserIDKey, userID)
	}
	base = costobserver.ContextWithSourcePlatform(base, platform)
	base = costobserver.ContextWithAttribution(base, costobserver.AttributionFromContext(ctx))
	base = costobserver.ContextWithSource(base, costobserver.SourceFromContext(ctx))
	return context.WithCancel(base)
}

func formatTierAgentLLM(cfg *AgentLLMConfig) string {
	if cfg == nil {
		return "<nil>"
	}
	if cfg.Provider == "" && cfg.ModelID == "" {
		return "<empty>"
	}
	return fmt.Sprintf("%s/%s", cfg.Provider, cfg.ModelID)
}

// UpdatePresetLLMConfigs refreshes the controller's preset LLM configs.
// Called when reusing a cached workshop session to pick up any LLM config changes
// the user made in the workflow editor since the session was first created.
func (s *WorkshopChatSession) UpdatePresetLLMConfigs(phaseLLM *AgentLLMConfig, pulseLLM *AgentLLMConfig) {
	s.controller.presetPhaseLLM = phaseLLM
	s.controller.presetBackgroundLLM = pulseLLM
	if s.config != nil {
		s.config.PresetPhaseLLM = phaseLLM
		s.config.PresetBackgroundLLM = pulseLLM
	}
}

// UpdateTieredConfig refreshes the controller's tiered LLM allocation config.
// Called when reusing a cached workshop session to pick up any tiered config changes
// the user made in the workflow editor since the session was first created.
// Also updates session.config.TieredConfig so run_full_workflow picks up the new config
// when it creates a fresh controller (e.g. after initial manifest read failed due to
// context cancellation).
func (s *WorkshopChatSession) UpdateTieredConfig(tieredConfig *TieredLLMConfig) {
	if tieredConfig != nil {
		orchestratorLLMConfig := s.controller.GetLLMConfig()
		var apiKeys *orchestrator.APIKeys
		if orchestratorLLMConfig != nil {
			apiKeys = orchestratorLLMConfig.APIKeys
		}
		s.controller.tierResolver = NewTierResolver(tieredConfig, apiKeys)
		// Also persist into session config so run_full_workflow (which reads cfg.TieredConfig)
		// uses the refreshed value rather than the stale one from session creation.
		if s.config != nil {
			s.config.TieredConfig = tieredConfig
			s.config.LLMAllocationMode = "tiered"
		}
	} else {
		s.controller.tierResolver = nil
		if s.config != nil {
			s.config.TieredConfig = nil
		}
	}
}

// UpdateAPIKeys refreshes the orchestrator's API keys.
// Called on session reuse to ensure workspace-stored keys are always current.
func (s *WorkshopChatSession) UpdateAPIKeys(apiKeys *orchestrator.APIKeys) {
	llmCfg := s.controller.GetLLMConfig()
	if llmCfg != nil {
		llmCfg.APIKeys = apiKeys
	}
	// Also refresh tier resolver's API keys if active
	if s.controller.tierResolver != nil && s.config != nil && s.config.TieredConfig != nil {
		s.controller.tierResolver = NewTierResolver(s.config.TieredConfig, apiKeys)
	}
}

// UpdatePresetSettings refreshes non-LLM controller settings from the preset.
// Called when reusing a cached workshop session to pick up any config changes
// the user made in the workflow editor (MCP servers, tools, knowledgebase, etc.).
// The *Parsed flags indicate whether the JSON field was successfully parsed; if false,
// the existing value is kept to avoid clearing settings on parse failure.
func (s *WorkshopChatSession) UpdatePresetSettings(
	selectedServers []string,
	selectedTools []string, toolsParsed bool,
	useCodeExecutionMode bool,
	useKnowledgebase bool,
	selectedSkills []string, skillsParsed bool,
	secrets []orchestrator.SecretEntry,
) {
	s.controller.SetSelectedServers(selectedServers)
	if toolsParsed {
		s.controller.SetSelectedTools(selectedTools)
	}
	s.controller.SetUseCodeExecutionMode(useCodeExecutionMode)
	s.controller.useKnowledgebase = useKnowledgebase
	if skillsParsed {
		s.controller.SetSelectedSkills(selectedSkills)
	}
	previousSecrets := s.controller.GetSecrets()
	s.controller.SetSecrets(secrets)
	// SetSecrets refreshes the controller env; the active Builder shell also
	// needs the existing callback, including names that were just deselected.
	selected := make(map[string]bool, len(secrets))
	for _, secret := range secrets {
		selected[secret.Name] = true
	}
	removed := []string{}
	for _, secret := range previousSecrets {
		if !selected[secret.Name] {
			removed = append(removed, secret.Name)
		}
	}
	notifySecretsAttached(s.secretsAttached, secrets, removed)

	// Sync back to session.config so run_full_workflow (which
	// creates fresh controllers from cfg) picks up the latest values.
	if s.config != nil {
		s.config.SelectedServers = selectedServers
		if toolsParsed {
			s.config.SelectedTools = selectedTools
		}
		s.config.UseCodeExecutionMode = useCodeExecutionMode
		s.config.UseKnowledgebase = useKnowledgebase
		s.config.Secrets = append([]orchestrator.SecretEntry(nil), secrets...)
	}
}

// UpdateBrowserRuntime refreshes configured browser intent on a reused workshop
// session. It does not resolve or cache CDP availability; the shared
// agent_browser executor queries that live for status and every auto-mode action.
func (s *WorkshopChatSession) UpdateBrowserRuntime(mode string, cdpPorts []int) {
	if s == nil || s.controller == nil {
		return
	}
	if s.config != nil {
		if s.config.BrowserRuntime == nil {
			s.config.BrowserRuntime = browser.NewBrowserRuntimeConfig(mode, cdpPorts)
		} else {
			s.config.BrowserRuntime.Update(mode, cdpPorts)
		}
	}
	s.controller.SetBrowserMode(mode)
	s.controller.SetCdpPorts(cdpPorts)
}

// UpdateInvocationInputs replaces the next invocation's optional values. An
// omitted preset means defaults; cached chats never retain a previous choice.
func (s *WorkshopChatSession) UpdateInvocationInputs(ctx context.Context, preset string, inputs map[string]string) error {
	_, manifest, err := s.controller.variableManager.checkExistingVariables(ctx, s.controller.GetWorkspacePath()+"/variables/variables.json")
	if err != nil {
		return err
	}
	values, err := ResolveWorkflowInputs(manifest, preset, inputs)
	if err != nil {
		return err
	}
	s.controller.variablesManifest = manifest
	if s.config != nil {
		s.config.InputPreset, s.config.Inputs = preset, cloneWorkflowStringMap(inputs)
	}
	s.controller.variableValues = values
	SyncExactVariablesToWorkspaceEnv(s.controller.BaseOrchestrator, values)
	return nil
}

// RegisterWorkshopChatTools registers the complete workshop-only tool surface on
// the given agent using the session's controller.
func RegisterWorkshopChatTools(
	mcpAgent DefinitionRegistrar,
	session *WorkshopChatSession,
	logger loggerv2.Logger,
) {
	if session.config != nil {
		mcpAgent = guardScheduleRegistrar(mcpAgent, session.config.ScheduleCollisionCheck)
	}
	iwm := &InteractiveWorkshopManager{
		controller:             session.controller,
		workshopConfig:         session.config,
		sessionID:              session.mainSessionID,
		stepRegistry:           session.StepRegistry,
		sessionCtx:             session.sessionCtx,
		toolCallQueryFunc:      session.toolCallQueryFunc,
		tmuxLookupFunc:         session.tmuxLookupFunc,
		mainSessionID:          session.mainSessionID,
		schedulerWorkspacePath: session.schedulerWorkspacePath,
		schedulerFuncs:         session.schedulerFuncs,
		llmToolsFuncs:          session.llmToolsFuncs,
		skillFuncs:             session.skillFuncs,
		listAvailableSecrets:   session.listAvailableSecrets,
		resolveSecretValues:    session.resolveSecretValues,
		secretsAttached:        session.secretsAttached,
		executionNotifier:      session.executionNotifier,
		hasPendingCompletions:  session.hasPendingCompletions,
		hasRunningAgents:       session.hasRunningAgents,
		cancelAllServerAgents:  session.cancelAllServerAgents,
		listServerAgents:       session.listServerAgents,
		workshopModeOverride:   session.workshopModeOverride,
	}
	if session.controller.isRelayExecution() {
		mcpAgent = relayDefinitionRegistrar{DefinitionRegistrar: mcpAgent}
	}
	registerWorkshopAgentTools(iwm, mcpAgent, session.config.WorkspacePath, logger)
}

// Close cancels all background goroutines for this workshop session.
func (s *WorkshopChatSession) Close() {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	if s.controller != nil {
		s.controller.CloseWorkshopGroupSessions()
	}
}

// AttachSecretToWorkflow adds (or updates the value of) a secret in the workshop's
// in-memory state and live workshop shell env so a freshly saved secret is usable
// as $SECRET_<NAME> in the SAME session. The server has already saved the value in
// the owner's Vault and selected it on the workflow (its Vault name); the Workshop
// never writes secret selections itself, since its in-memory names are the
// $SECRET_ names, not the Vault names a selection holds.
func (s *WorkshopChatSession) AttachSecretToWorkflow(ctx context.Context, name, value string) error {
	if s == nil || s.controller == nil || name == "" {
		return nil
	}

	current := s.controller.GetSecrets()
	updated := make([]orchestrator.SecretEntry, 0, len(current)+1)
	found := false
	for _, entry := range current {
		if entry.Name == name {
			entry.Value = value
			found = true
		}
		updated = append(updated, entry)
	}
	if !found {
		updated = append(updated, orchestrator.SecretEntry{Name: name, Value: value})
	}

	s.controller.SetSecrets(updated)
	if s.config != nil {
		cloned := make([]orchestrator.SecretEntry, len(updated))
		copy(cloned, updated)
		s.config.Secrets = cloned
	}

	// Inject into the live shell env map (the same reference execute_shell_command
	// reads at execution time), so the very next shell command in this session
	// sees $SECRET_<NAME> without a session rebuild.
	if envRef := s.controller.GetWorkspaceEnvRef(); envRef != nil {
		s.controller.LockWorkspaceEnv()
		envRef["SECRET_"+name] = value
		s.controller.UnlockWorkspaceEnv()
	}
	return nil
}

// DetachSecretFromWorkflow removes a secret from the workshop's in-memory state
// and shell env after the server unselected it. Safe to call even if the name
// was never attached.
func (s *WorkshopChatSession) DetachSecretFromWorkflow(ctx context.Context, name string) error {
	if s == nil || s.controller == nil || name == "" {
		return nil
	}

	current := s.controller.GetSecrets()
	filtered := make([]orchestrator.SecretEntry, 0, len(current))
	for _, entry := range current {
		if entry.Name != name {
			filtered = append(filtered, entry)
		}
	}
	s.controller.SetSecrets(filtered)
	if s.config != nil {
		cloned := make([]orchestrator.SecretEntry, len(filtered))
		copy(cloned, filtered)
		s.config.Secrets = cloned
	}
	if envRef := s.controller.GetWorkspaceEnvRef(); envRef != nil {
		s.controller.LockWorkspaceEnv()
		delete(envRef, "SECRET_"+name)
		s.controller.UnlockWorkspaceEnv()
	}
	return nil
}

// workflowProgressBridge wraps an existing event bridge and intercepts step completion
// events to send progress notifications to the main workshop agent via bgAgentRegistry.
type workflowProgressBridge struct {
	inner          mcpagent.AgentEventListener
	session        *WorkshopChatSession
	logger         loggerv2.Logger
	parentID       string // parent execution ID for correlation
	iteration      string
	groupName      string
	progressMu     sync.Mutex
	progressID     map[string]string
	progressEnded  map[string]bool
	ownedSubAgents *workshopSubAgentCleanup
}

func (b *workflowProgressBridge) HandleEvent(ctx context.Context, event *baseevents.AgentEvent) error {
	// Forward all events to the inner bridge first
	if b.inner != nil {
		if err := b.inner.HandleEvent(ctx, event); err != nil {
			return err
		}
	}

	// A script dispatch already has one registered step lifecycle. Its optional
	// model repair turns must not create a second completion/notification owner.
	// Nested child owners remain independent and still pass through tracking.
	if owner, ok := ctx.Value(scriptInspectionOwnerKey{}).(string); ok && owner != "" && owner == currentWorkshopParentExecutionID(ctx) {
		return nil
	}

	// Intercept step start/end events so the server-side bgAgentRegistry can
	// notify the main workflow-builder agent while a full workflow runs.
	switch event.Type {
	case orchestrator_events.OrchestratorAgentStart:
		if startEvent, ok := event.Data.(*orchestrator_events.OrchestratorAgentStartEvent); ok && workflowProgressTracksAgent(startEvent.AgentType, startEvent.AgentName) {
			stepID := workflowProgressStepID(startEvent)
			execID := b.workflowProgressExecIDForStart(stepID, startEvent.AgentType, startEvent.AgentName, startEvent.StepIndex)
			if stepID == "" {
				stepID = startEvent.AgentName
			}
			// Register a running snapshot so query_step can find this step while it's active.
			// AgentSessionID is intentionally empty: the prefix-scan in collectQueryToolCallSummaries
			// uses the resolved stepID to find tool calls under sub-<kind>-<stepID>-* sessions.
			if b.session != nil && b.session.StepRegistry != nil {
				b.session.StepRegistry.Register(&WorkshopStepExecution{
					RunFolder: b.iteration,
					ID:        execID,
					StepID:    stepID,
					Status:    WorkshopStepRunning,
					CreatedAt: time.Now(),
				})
			}
			if b.session != nil && b.session.executionNotifier != nil {
				b.session.executionNotifier.OnExecutionStart(WorkshopExecutionStart{
					ID:                execID,
					ParentExecutionID: b.parentID,
					Name:              workflowProgressDisplayName(startEvent.AgentName),
					Kind:              string(workflowProgressExecutionKind(startEvent.AgentType)),
					Metadata:          map[string]string{"step_id": stepID},
				})
			}
		}
	case orchestrator_events.OrchestratorAgentEnd:
		if endEvent, ok := event.Data.(*orchestrator_events.OrchestratorAgentEndEvent); ok {
			agentType := endEvent.AgentType
			// A todo_task orchestrator can end many successful LLM turns while the
			// step is still running. In particular, an asynchronous call_sub_agent
			// launch deliberately ends the current turn so the runtime can wait for
			// the child outside the CLI process and deliver its completion back into
			// the same conversation. Treating that turn boundary as step completion
			// sends a false AUTO-NOTIFICATION to the workshop chat and closes the
			// parent execution while its child is still live. The controller emits a
			// AgentStepCompleted event only after all owned children have settled
			// and their results have been reconciled; that is the success boundary.
			if agentType == "delegating_agent" && endEvent.Success {
				break
			}
			if workflowProgressTracksAgent(agentType, endEvent.AgentName) {
				stepName := endEvent.AgentName
				// Use the plan-level step ID stamped by context_aware_bridge, falling back to
				// the full agent name so query_step(step_id=<plan-id>) works out of the box.
				stepID := workflowProgressStepID(endEvent)
				progressID, alreadyStarted := b.workflowProgressExecIDForEnd(stepID, agentType, stepName, endEvent.StepIndex)
				if stepID == "" {
					stepID = stepName
				}
				status := "completed"
				result := endEvent.Result
				var execErr error
				if !endEvent.Success {
					status = "failed"
					if endEvent.Error != "" {
						result = endEvent.Error
					}
					execErr = fmt.Errorf("%s", result)
				}

				progressExec := &WorkshopStepExecution{
					RunFolder: b.iteration,
					ID:        progressID,
					StepID:    stepID,
					Status:    WorkshopStepDone,
					Result:    fmt.Sprintf("[Step %d: %s] %s — %s", endEvent.StepIndex, stepName, status, truncateResult(result, 500)),
				}
				if !endEvent.Success {
					progressExec.Status = WorkshopStepFailed
					progressExec.Err = execErr
				}
				b.session.StepRegistry.Register(progressExec)

				if b.session.onStepCorrelationDone != nil && strings.HasPrefix(endEvent.CorrelationID, "workshop-step-") {
					b.session.onStepCorrelationDone(endEvent.CorrelationID)
				}

				if b.session != nil && b.session.executionNotifier != nil {
					meta := map[string]string{
						"execution_type": "workflow-step",
						"step_name":      stepName,
						"step_id":        stepID,
						"agent_type":     agentType,
						"step_index":     fmt.Sprintf("%d", endEvent.StepIndex),
					}
					if b.iteration != "" {
						meta["iteration"] = b.iteration
					}
					if b.groupName != "" {
						meta["group_name"] = b.groupName
					}
					if !alreadyStarted {
						b.session.executionNotifier.OnExecutionStart(WorkshopExecutionStart{
							ID:                progressID,
							ParentExecutionID: b.parentID,
							Name:              workflowProgressDisplayName(stepName),
							Kind:              string(workflowProgressExecutionKind(agentType)),
							Metadata:          map[string]string{"step_id": stepID},
						})
					}
					b.session.executionNotifier.OnExecutionComplete(progressID, workflowProgressDisplayName(stepName), result, meta, execErr)
				}

				if b.logger != nil {
					b.logger.Info(fmt.Sprintf("📊 [WORKFLOW_PROGRESS] Step %d '%s' %s", endEvent.StepIndex, stepName, status))
				}
			}
		}
	case orchestrator_events.AgentStepCompleted:
		if completedEvent, ok := event.Data.(*AgentStepCompletedEvent); ok {
			stepName := strings.TrimSpace(completedEvent.StepTitle)
			if stepName == "" {
				stepName = strings.TrimSpace(completedEvent.StepID)
			}
			if stepName == "" {
				stepName = fmt.Sprintf("Step %d", completedEvent.StepIndex)
			}
			result := strings.TrimSpace(completedEvent.CompletionReason)
			if result == "" {
				result = "Todo task step completed"
			}

			progressID, alreadyStarted := b.workflowProgressExecIDForEnd(completedEvent.StepID, "delegating_agent", stepName, completedEvent.StepIndex)
			if b.session != nil && b.session.StepRegistry != nil {
				b.session.StepRegistry.Register(&WorkshopStepExecution{
					RunFolder: b.iteration,
					ID:        progressID,
					StepID:    completedEvent.StepID,
					Status:    WorkshopStepDone,
					Result:    fmt.Sprintf("[Step %d: %s] completed — %s", completedEvent.StepIndex, stepName, truncateResult(result, 500)),
				})
			}

			if b.session != nil && b.session.executionNotifier != nil {
				meta := map[string]string{
					"execution_type": "workflow-step",
					"step_name":      stepName,
					"step_id":        completedEvent.StepID,
					"agent_type":     "delegating_agent",
					"step_index":     fmt.Sprintf("%d", completedEvent.StepIndex),
				}
				if b.iteration != "" {
					meta["iteration"] = b.iteration
				}
				if b.groupName != "" {
					meta["group_name"] = b.groupName
				}
				if !alreadyStarted {
					b.session.executionNotifier.OnExecutionStart(WorkshopExecutionStart{
						ID:                progressID,
						ParentExecutionID: b.parentID,
						Name:              workflowProgressDisplayName(stepName),
						Kind:              string(orchestrator_events.ExecutionKindAgent),
					})
				}
				b.session.executionNotifier.OnExecutionComplete(progressID, workflowProgressDisplayName(stepName), result, meta, nil)
			}

			if b.logger != nil {
				b.logger.Info(fmt.Sprintf("📊 [WORKFLOW_PROGRESS] Todo task step %d '%s' completed", completedEvent.StepIndex, stepName))
			}
		}
	}

	return nil
}

func workflowProgressTracksAgentType(agentType string) bool {
	switch agentType {
	case "todo_planner_execution", "delegating_agent", "generic_execution":
		return true
	default:
		return false
	}
}

func workflowProgressTracksAgent(agentType string, agentName string) bool {
	if !workflowProgressTracksAgentType(agentType) {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(agentName), "agent-")
}

// workflowProgressExecutionKind maps a todo_task agent's internal agentType onto
// the declared ExecutionKind, so the terminal store and rail badge no longer have
// to re-derive it from the "Step -> " name convention below.
func workflowProgressExecutionKind(agentType string) orchestrator_events.ExecutionKind {
	if agentType == "delegating_agent" {
		return orchestrator_events.ExecutionKindAgent
	}
	return orchestrator_events.ExecutionKindSubAgent
}

// workflowProgressStepID extracts the actual workflow step ID from an orchestrator event's
// metadata. The context_aware_bridge stamps current_step_id on every event it processes,
// so this is the reliable way to map an execution-agent name back to its plan step ID.
// Falls back to an empty string when no step metadata is present.
func workflowProgressStepID(eventData interface{}) string {
	type baseGetter interface {
		GetBaseEventData() *baseevents.BaseEventData
	}
	if bg, ok := eventData.(baseGetter); ok {
		if bd := bg.GetBaseEventData(); bd != nil {
			if stepID, ok := bd.Metadata["current_step_id"].(string); ok {
				return strings.TrimSpace(stepID)
			}
		}
	}
	return ""
}

// workflowProgressDisplayName used to prefix the name with "Step -> " so the
// legacy isWorkflowStepTrackingExecution name-sniffer in cmd/server/delegation.go
// would classify it correctly. That prefix is no longer needed now that the
// OnExecutionStart calls below declare their ExecutionKind explicitly, and it
// actively broke the frontend title: getBackgroundExecutionDisplayName only
// recognized "Workflow step -> ", so "Step -> Foo Bar" fell through to raw
// title-casing, which turns the "-" in "->" into a space — producing "Step >
// Foo Bar" — which splitExecutionDisplayPath then misread as a "Foo Bar" title
// nested "inside Step".
func workflowProgressDisplayName(agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return "Step"
	}
	return agentName
}

func workflowProgressKey(stepID, agentType, agentName string, stepIndex int) string {
	// The base-agent lifecycle omits the positional StepIndex (zero), while
	// AgentStepCompleted carries the controller's real index. Both carry the
	// plan step ID. A display name/index mismatch must not create a second
	// progress mirror and leave the first to fail during parent cleanup.
	// This map belongs to one parent run; legacy events without a stable ID
	// retain their exact old identity rather than guessing by name alone.
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		return "step:" + stepID
	}
	return fmt.Sprintf("legacy:%s:%d:%s", strings.TrimSpace(agentType), stepIndex, strings.TrimSpace(agentName))
}

func (b *workflowProgressBridge) workflowProgressExecIDForStart(stepID, agentType, agentName string, stepIndex int) string {
	if b == nil {
		return fmt.Sprintf("workflow-step-%d-%s", stepIndex, workflowExecutionIDToken())
	}
	key := workflowProgressKey(stepID, agentType, agentName, stepIndex)
	b.progressMu.Lock()
	defer b.progressMu.Unlock()
	if b.progressID == nil {
		b.progressID = make(map[string]string)
	}
	if id := b.progressID[key]; id != "" && !b.progressEnded[key] {
		return id
	}
	id := fmt.Sprintf("%s-step-%d-%s", b.parentID, stepIndex, workflowExecutionIDToken())
	b.progressID[key] = id
	delete(b.progressEnded, key)
	return id
}

func (b *workflowProgressBridge) workflowProgressExecIDForEnd(stepID, agentType, agentName string, stepIndex int) (string, bool) {
	if b == nil {
		return fmt.Sprintf("workflow-step-%d-%s", stepIndex, workflowExecutionIDToken()), false
	}
	key := workflowProgressKey(stepID, agentType, agentName, stepIndex)
	b.progressMu.Lock()
	defer b.progressMu.Unlock()
	if b.progressID == nil {
		b.progressID = make(map[string]string)
	}
	if b.progressEnded == nil {
		b.progressEnded = make(map[string]bool)
	}
	b.progressEnded[key] = true
	if id := b.progressID[key]; id != "" {
		return id, true
	}
	id := fmt.Sprintf("%s-step-%d-%s", b.parentID, stepIndex, workflowExecutionIDToken())
	b.progressID[key] = id
	return id, false
}

func workflowExecutionIDToken() string {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 36)
	seq := strconv.FormatUint(workflowExecutionIDCounter.Add(1)%1296, 36)
	if len(seq) == 1 {
		seq = "0" + seq
	}
	return ts + seq
}

func (b *workflowProgressBridge) Name() string {
	if b.inner != nil {
		return "workflow-progress-" + b.inner.Name()
	}
	return "workflow-progress"
}

// truncateResult truncates a string for progress notifications
func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// RegisterRunFullWorkflowTool registers a run_full_workflow tool that executes the complete
// workflow pipeline once with the resolved inputs in the background. The LLM is notified
// when execution completes. This is the workshop-builder equivalent of the orchestrator-mode
// full execution, but triggered as a tool call.
func RegisterRunFullWorkflowTool(
	mcpAgent DefinitionToolRegistrar,
	session *WorkshopChatSession,
	logger loggerv2.Logger,
) {
	if session.config != nil {
		mcpAgent = GuardScheduleTools(mcpAgent, session.config.ScheduleCollisionCheck)
	}
	if session.controller.isRelayExecution() {
		mcpAgent = relayToolRegistrar{DefinitionToolRegistrar: mcpAgent}
	}
	if err := mcpAgent.RegisterCustomTool(
		"run_full_workflow",
		"Execute the complete workflow for real with its normal permissions, live database and actual tool actions. A person's request to run or test authorizes this execution; do not ask again for the same request. Resolve defaults, optional saved inputs, and explicit inputs once, then run all steps in a unique run folder. A saved schedule or webhook retains its server-bound run identity. Starts from the beginning and runs in background - you will be notified when complete. Use send_step_message with the returned execution_id to steer whichever workflow child-agent turn is currently active. Use human_inputs for run-specific instructions or responses, keyed by the exact target step ID; each value is visible only to that step. If the plan contains deterministic routing steps and the user's request already selected a branch, pass route_selections keyed by routing step ID.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"input_preset": map[string]interface{}{
					"type":        "string",
					"description": "Optional exact saved-input preset name. Omit to use workflow defaults. This only fills input values; route selections are separate.",
				},
				"human_inputs": map[string]interface{}{
					"type":        "object",
					"description": "Per-step run instructions keyed by exact step ID. Each value is delivered only to that step, including delegated route agents. Example: {\"search-jobs\": \"Use only Best Matches and My Feed\"}. Do not use this for routing choices; use route_selections instead.",
					"additionalProperties": map[string]interface{}{
						"type": "string",
					},
				},
				"route_selections": map[string]interface{}{
					"type":        "object",
					"description": "Deterministic route choices, keyed by routing step ID. Values may be a route_id or a unique next_step_id for that routing step. Use this when the user has already told the builder which fixed branch to run. Example: {\"pick-job\": \"lead-verification\"}.",
					"additionalProperties": map[string]interface{}{
						"type": "string",
					},
				},
				"inputs": map[string]interface{}{
					"type":        "object",
					"description": "Per-run values for this run only, keyed by declared variable name (see variables/variables.json), overriding optional saved input values. Use for per-run data such as a PR number, URL or ID the user gave; never rely on a saved value for it. Unknown names are refused. Not allowed for a run started by a webhook or function, whose values are fixed by the trigger. Example: {\"PR_NUMBER\": \"149\"}.",
					"additionalProperties": map[string]interface{}{
						"type": "string",
					},
				},
			},
			"required": []string{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {

			cfg := session.config
			if cfg != nil {
				snapshot := *cfg
				cfg = &snapshot
			}
			if cfg == nil {
				return "session config not available — cannot create workflow controller", nil
			}

			iteration := "run-" + workflowExecutionIDToken()
			if cfg.WebhookInvocation != nil {
				iteration = cfg.WebhookInvocation.RunFolder
			} else if cfg.ScheduleInvocation != nil {
				iteration = cfg.ScheduleInvocation.BoundRunFolder()
			}
			strategy := "start_from_beginning_no_human"

			for _, retired := range []string{"group_name", "enabled_group_names", "variables"} {
				if _, supplied := args[retired]; supplied {
					return "", fmt.Errorf("%s is retired; use input_preset and inputs", retired)
				}
			}
			if cfg.WebhookInvocation != nil {
				if _, present := args["input_preset"]; present {
					return "", fmt.Errorf("input_preset is fixed by this function or webhook invocation")
				}
			}
			groupName, _ := args["input_preset"].(string)
			if value, supplied := args["input_preset"]; supplied {
				if _, ok := value.(string); !ok {
					return "input_preset must be a string", nil
				}
			}
			var enabledGroupNames []string
			if !session.controller.isRelayExecution() {
				var inputErr error
				groupName, inputErr = session.controller.resolveWorkshopInputGroup(ctx, groupName, func() []string {
					if cfg.InputPreset != "" {
						return []string{cfg.InputPreset}
					}
					return nil
				}())
				if inputErr != nil {
					return "", inputErr
				}
				if groupName != "" {
					enabledGroupNames = []string{groupName}
				}
			}

			// Parse human_inputs strictly. The old assertion silently discarded
			// alternate decoded map shapes and non-string values, allowing a run
			// to continue without the operator's safety constraint.
			humanInputs, parseErr := parseWorkflowStepStringMap(args, "human_inputs")
			if parseErr != nil {
				return parseErr.Error(), nil
			}
			// Parse route_selections (optional map of routing_step_id -> route_id or next_step_id)
			var routeSelections map[string]string
			if rs, ok := args["route_selections"]; ok && rs != nil {
				rsMap, ok := rs.(map[string]interface{})
				if !ok {
					return "route_selections must be an object keyed by routing step ID.", nil
				}
				routeSelections = make(map[string]string, len(rsMap))
				for k, v := range rsMap {
					stepID := strings.TrimSpace(k)
					value, ok := v.(string)
					if !ok {
						return fmt.Sprintf("route_selections[%q] must be a string route_id or next_step_id.", k), nil
					}
					value = strings.TrimSpace(value)
					if stepID == "" || value == "" {
						return "route_selections entries must have non-empty step IDs and route values.", nil
					}
					routeSelections[stepID] = value
				}
			}

			runVariables, parseErr := parseWorkflowStepStringMap(args, "inputs")
			if parseErr != nil {
				return parseErr.Error(), nil
			}
			if len(runVariables) > 0 {
				if cfg.WebhookInvocation != nil {
					return "variables cannot be set here: this run was started by a webhook or function, whose values are fixed by the trigger.", nil
				}
				if problem := undeclaredRunVariables(session.controller.variablesManifest, runVariables); problem != "" {
					return problem, nil
				}
			}

			invocationInputs := cloneWorkflowStringMap(cfg.Inputs)
			if invocationInputs == nil {
				invocationInputs = map[string]string{}
			}
			if cfg.WebhookInvocation != nil {
				for name, value := range cfg.WebhookInvocation.Variables {
					invocationInputs[name] = value
				}
			}
			for name, value := range runVariables {
				invocationInputs[name] = value
			}
			resolvedInputs, inputErr := ResolveWorkflowInputs(session.controller.variablesManifest, groupName, invocationInputs)
			if inputErr == nil {
				inputErr = ValidateWorkflowLaunchInputs(session.controller.variablesManifest, resolvedInputs)
			}
			if inputErr != nil {
				return "", inputErr
			}

			if cfg.WebhookInvocation != nil {
				var bindingErr error
				if session.controller.isRelayExecution() {
					routeSelections = cfg.WebhookInvocation.RouteSelections
				} else {
					routeSelections, bindingErr = cfg.WebhookInvocation.RoutesForGroup(groupName)
				}
				if bindingErr != nil {
					return "", bindingErr
				}
			}

			// Validate exact input target IDs and selected routes.
			// Route-scoped validation matters for workflows like Upwork where one plan contains
			// bid/search/profile branches; a search run must not be forced to answer bid approval.
			plan, err := session.controller.ReadCurrentPlan(ctx)
			if err != nil {
				return fmt.Sprintf("Failed to load plan: %v", err), nil
			}
			if cfg.WebhookInvocation != nil && plan != nil {
				humanInputs = attachWebhookStepInputs(plan.Steps, humanInputs, cfg.WebhookInvocation.InputFile)
			}

			// Preflight: refuse to launch when the workflow declares MCP
			// servers that the host config doesn't actually provide.
			// Without this the run silently fails later — locked scripts hit
			// "SCOPE DENIED" and the cursor fallback returns 0 tokens after
			// minutes of waiting. Fail-fast with one clear punch list instead.
			if missing, err := validateWorkflowDependencies(ctx, session, cfg.MCPConfigPath, logger); err != nil {
				logger.Warn(fmt.Sprintf("preflight: dependency check skipped (%v) — proceeding without it", err))
			} else if len(missing) > 0 {
				workflowLabel := ""
				if cfg.WorkspacePath != "" {
					workflowLabel = cfg.WorkspacePath
				}
				return formatMissingDependencies(workflowLabel, missing, cfg.MCPConfigPath), nil
			}
			if plan != nil {
				if unknown := unknownWorkflowStepInputIDs(plan.Steps, humanInputs); len(unknown) > 0 {
					return fmt.Sprintf("human_inputs contains unknown step ID(s): %s. Read the current plan and key each value by an exact step ID.", strings.Join(unknown, ", ")), nil
				}
				var legacyRoutingSteps []string
				validationSteps := plan.Steps
				variableValues := resolvedInputs
				if err := validateRequiredLaunchVariables(plan.Steps, variableValues, routeSelections, 0, false); err != nil {
					return err.Error(), nil
				}
				if len(variableValues) > 0 {
					validationSteps = routeScopedValidationSteps(plan.Steps, variableValues, humanInputs, routeSelections)
				} else if len(routeSelections) > 0 {
					validationSteps = routeScopedValidationSteps(plan.Steps, nil, humanInputs, routeSelections)
				}
				for _, step := range validationSteps {
					if step.StepType() == StepTypeRouting || step.StepType() == StepTypeBranch {
						if routingStep, ok := step.(routeSwitchStep); ok && routingStep.GetDescription() != "" {
							legacyRoutingSteps = append(legacyRoutingSteps, fmt.Sprintf("  - %s (id: %s)", step.GetTitle(), step.GetID()))
						}
					}
				}
				if len(legacyRoutingSteps) > 0 {
					return fmt.Sprintf("❌ Plan has routing/branch steps with legacy descriptions. Both are deterministic-only and never execute agents:\n%s\n\nMove each probe/judgment into a prior agent step that writes route_selection.json, then clear the description and point the step at that file via route_source_file or context_dependencies.", strings.Join(legacyRoutingSteps, "\n")), nil
				}
			}

			if cfg.WebhookInvocation != nil {
				if session.controller.isRelayExecution() {
					if err := validateRelayWorkshopRunFolder(iteration); err != nil {
						return "", err
					}
				} else if !IsPermanentRunFolder(iteration) {
					return "webhook run folder unavailable", nil
				}
				if !session.controller.isRelayExecution() {
					if err := cfg.WebhookInvocation.ClaimGroup(groupName); err != nil {
						return err.Error(), nil
					}
				}
			}

			execToken := workflowExecutionIDToken()
			if session.controller.isRelayExecution() && cfg.WebhookInvocation == nil {
				iteration = "relay-" + execToken
			}
			execID := fmt.Sprintf("workflow-full-%s", execToken)
			if cfg.ScheduleInvocation != nil && cfg.ScheduleInvocation.Next != nil {
				next, err := cfg.ScheduleInvocation.Next(ctx, execID)
				if err != nil {
					return "", err
				}
				snapshot := *cfg
				snapshot.ScheduleInvocation = next
				cfg = &snapshot
				iteration = next.RunFolder
			}

			execCtx, cancel := context.WithCancel(session.sessionCtx)
			execCtx = withWorkshopExecutionParent(execCtx, ctx)

			agentSessionID := fmt.Sprintf("workshop-workflow-full-%s", execToken)
			execCtx = context.WithValue(execCtx, orchestrator_events.AgentSessionIDKey, agentSessionID)
			execCtx = context.WithValue(execCtx, orchestrator_events.ForceCorrelationIDKey, agentSessionID)
			execCtx = context.WithValue(execCtx, orchestrator_events.IsSubAgentContextKey, true)

			exec := &WorkshopStepExecution{
				RunFolder:      iteration,
				ID:             execID,
				StepID:         "full-workflow",
				AgentSessionID: agentSessionID,
				Status:         WorkshopStepRunning,
				cancel:         cancel,
			}
			session.StepRegistry.Register(exec)

			// Register two parent-chat mappings:
			// 1) The background workflow agent session for SpawnListener mirroring.
			// 2) The workflow MCP session so human_input / human_feedback lookups
			//    resolve against the same session ID step agents actually use.
			workflowGroup := ""
			if len(enabledGroupNames) > 0 {
				workflowGroup = ""
			}
			workflowSessionID := session.controller.GetMCPSessionID()
			if workflowSessionID != "" {
				virtualtools.RegisterParentChat(workflowSessionID, &virtualtools.ParentChatContext{
					SessionID:    session.mainSessionID,
					WorkflowPath: cfg.WorkspacePath,
					GroupName:    workflowGroup,
					AgentID:      execID,
				})
			}
			virtualtools.RegisterParentChat(agentSessionID, &virtualtools.ParentChatContext{
				SessionID:    session.mainSessionID,
				WorkflowPath: cfg.WorkspacePath,
				GroupName:    workflowGroup,
				AgentID:      execID,
			})

			// Notify workshop execution notifier so frontend keeps polling
			// Include group and iteration in display name so notifications are unambiguous
			workflowDisplayName := "full-run"
			if len(enabledGroupNames) > 0 && iteration != "" {
				workflowDisplayName = fmt.Sprintf("full-run [%s / %s]", enabledGroupNames[0], iteration)
			} else if len(enabledGroupNames) > 0 {
				workflowDisplayName = fmt.Sprintf("full-run [%s]", enabledGroupNames[0])
			}
			execMeta := map[string]string{
				"workshop_mode":  "runner",
				"execution_type": "full-workflow",
			}
			if cfg.WebhookInvocation != nil {
				execMeta["trigger_source"] = "webhook"
			}
			if iteration != "" {
				execMeta["iteration"] = iteration
			}
			if len(enabledGroupNames) > 0 {
				execMeta["input_preset"] = enabledGroupNames[0]
			}
			if iteration != "" {
				runFolder := iteration
				if len(enabledGroupNames) > 0 {
					runFolder = iteration
				}
				execMeta["run_folder"] = runFolder
			}
			if session.executionNotifier != nil {
				session.executionNotifier.OnExecutionStart(WorkshopExecutionStart{
					ID:                execID,
					ParentExecutionID: currentWorkshopParentExecutionID(execCtx),
					Name:              workflowDisplayName,
					// A full run is a CONTAINER, not an agent: it has no
					// conversation of its own, only the steps beneath it. It is
					// still registered so cancellation and HasRunningAgents()
					// work, but declaring the kind keeps it out of the terminal
					// rail instead of sitting there beside real agents.
					Kind:     string(orchestrator_events.ExecutionKindFullRun),
					Metadata: execMeta,
					Cancel:   cancel,
				})
			}
			execCtx = virtualtools.WithBackgroundAgentID(execCtx, execID)
			execCtx = context.WithValue(execCtx, orchestrator_events.ParentExecutionIDKey, execID)

			go func() {
				// Tear down the parent-chat mapping when the background workflow
				// exits. The SpawnListener sees this and stops mirroring the
				// child's events into the parent chat thread.
				if workflowSessionID != "" {
					defer virtualtools.UnregisterParentChat(workflowSessionID)
				}
				defer virtualtools.UnregisterParentChat(agentSessionID)

				var result string
				var execErr error
				var progressBridge *workflowProgressBridge
				defer func() {
					skipNotify := finalizeExecStatus(exec, execCtx, &result, &execErr)
					if progressBridge != nil {
						progressBridge.finishProgressAfterChildrenStop()
					}
					if !skipNotify && session.executionNotifier != nil {
						session.executionNotifier.OnExecutionComplete(execID, "Full Workflow Execution", result, execMeta, execErr)
					}
				}()

				// Wrap event bridge with progress listener to send per-step notifications
				progressBridge = &workflowProgressBridge{
					inner:          cfg.EventBridge,
					session:        session,
					logger:         logger,
					parentID:       execID,
					iteration:      iteration,
					ownedSubAgents: &workshopSubAgentCleanup{},
				}
				if len(enabledGroupNames) > 0 {
					progressBridge.groupName = ""
				}

				workflowController, err := NewStepBasedWorkflowOrchestrator(
					execCtx,
					"", "", 0.7, "simple",
					cfg.SelectedServers,
					cfg.SelectedTools,
					cfg.UseCodeExecutionMode,
					cfg.MCPConfigPath,
					cfg.LLMConfig,
					100,
					logger,
					nil,
					progressBridge, // wrapped bridge with per-step notifications
					cfg.CustomTools,
					cfg.CustomToolExecutors,
					cfg.ToolCategories,
					cfg.PresetPhaseLLM,
					cfg.PresetBackgroundLLM,
					cfg.UseKnowledgebase,
					cfg.TieredConfig,
				)
				if err != nil {
					execErr = fmt.Errorf("failed to create workflow controller: %w", err)
					return
				}
				defer workflowController.CloseWorkshopGroupSessions()
				workflowController.workshopSubAgentCleanup = progressBridge.ownedSubAgents

				// Wire sub-agent tracking so generic/predefined sub-agents spawned by the
				// runner controller appear in the session's stepRegistry and are visible
				// via list_executions/query_step. Without this, hcpo.subAgentNotifier is
				// nil inside controller_todo_task.go and sub-agent tracking is silently skipped.
				workflowController.SetSubAgentNotifier(session.combinedSubAgentNotifier())
				workflowController.SetWorkshopExecutionContext(execCtx, session.StepRegistry)
				// Wire the workshop execution notifier so step types that emit their
				// own lifecycle notifications directly (agent items,
				// kb-update, continuation recovery) actually reach the main agent
				// during a full-workflow run. Without this the notifier is nil and
				// those notifications silently no-op — e.g. an agent step
				// (login/discovery/retrieval phases) ran for many minutes with the
				// main agent never told it started, progressed, or finished.
				workflowController.SetWorkshopExecutionNotifier(session.executionNotifier)

				// Propagate session context
				if cfg.SessionID != "" {
					workflowController.SetHTTPSessionID(cfg.SessionID)
				}
				if len(cfg.Secrets) > 0 {
					workflowController.SetSecrets(cfg.Secrets)
				}
				env := session.controller.snapshotWorkspaceEnv()
				if env == nil {
					env = map[string]string{}
				}
				workflowController.SetWorkspaceEnvRef(env)

				if skills := session.controller.GetSelectedSkills(); len(skills) > 0 {
					workflowController.SetSelectedSkills(skills)
				}
				if ports := session.controller.GetCdpPorts(); len(ports) > 0 {
					workflowController.SetCdpPorts(ports)
				} else if session.controller.GetCdpPort() > 0 {
					workflowController.SetCdpPort(session.controller.GetCdpPort())
				}
				workflowController.SetBrowserMode(session.controller.GetBrowserMode())

				// Set execution options
				execOpts := &ExecutionOptions{
					SelectedRunFolder:  iteration,
					RunFolderAllocated: true,
					ExecutionStrategy:  strategy,
					InputPreset:        groupName,
					Inputs:             invocationInputs,
					ResolvedInputs:     resolvedInputs,
					HumanInputs:        humanInputs,
					RouteSelections:    routeSelections,
					// Fresh controllers start with no options: without the
					// resource service, saved agent/script references cannot be resolved here.
					CrewResources:      cfg.CrewResources,
					AgentFunctionTools: cfg.AgentFunctionTools,
				}
				if cfg.ScheduleInvocation != nil {
					execOpts.RunKind = cfg.ScheduleInvocation.RunKind()
					execOpts.ScheduleRunID = cfg.ScheduleInvocation.BoundRunID()
					execOpts.ScheduleID = cfg.ScheduleInvocation.ScheduleID
					execOpts.TriggerSource = cfg.ScheduleInvocation.TriggerSource
					execOpts.ScheduledFor = cfg.ScheduleInvocation.ScheduledFor
					execOpts.ExecutionID = execID
				}
				if cfg.WebhookInvocation != nil {
					execOpts.WebhookInputFile = cfg.WebhookInvocation.InputFile
					execOpts.WebhookVariables = cfg.WebhookInvocation.Variables
				} else if len(runVariables) > 0 {
					execOpts.WebhookVariables = runVariables
				}
				workflowController.SetExecutionOptions(execOpts)
				if len(routeSelections) > 0 {
					// PLAT-066: pairs with the seed-time log in seedRouteSelectionsForRun.
					// If that log never shows this run's ID/route pair, the value was
					// lost somewhere inside CreateTodoList's call chain, not here.
					logger.Info(fmt.Sprintf("🔀 run_full_workflow: SetExecutionOptions carries route_selections=%v before CreateTodoList", routeSelections))
				}

				result, execErr = workflowController.CreateTodoList(
					execCtx,
					session.controller.GetObjective(),
					cfg.WorkspacePath,
				)
				result = firstNonEmpty(strings.TrimSpace(result), "Workflow execution completed successfully.")
			}()

			groupInfo := ""
			if len(enabledGroupNames) > 0 {
				groupInfo = fmt.Sprintf("\nSaved inputs: %s", enabledGroupNames[0])
			}
			iterInfo := "\nIteration: new (auto-created)"
			if session.controller.isRelayExecution() {
				iterInfo = fmt.Sprintf("\nrun_folder: %q", iteration)
			} else if iteration != "" {
				iterInfo = fmt.Sprintf("\nrun_folder: %q", iteration)
			}
			return fmt.Sprintf("Full workflow execution started.\nexecution_id: %q\nStrategy: %s%s%s\nAll steps will be executed end-to-end. Use send_step_message(execution_id=%q, message=...) for a live correction while a child-agent turn is active.\nYou will be automatically notified when it completes.", execID, strategy, groupInfo, iterInfo, execID), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register run_full_workflow tool: %v", err))
	}
}

// RegisterHTMLReportTools registers the HTML-only report contract for a
// workflow-builder session. Reports are HTML documents under db/reports/;
// index.html remains the default and views.json may carry display metadata.
func RegisterHTMLReportTools(
	mcpAgent DefinitionToolRegistrar,
	workspacePath string,
	logger loggerv2.Logger,
	readFile func(context.Context, string) (string, error),
	hooks ReportHTMLValidationHooks,
) error {
	return registerHTMLReportTools(mcpAgent, workspacePath, logger, readFile, hooks)
}

// ReadPlanFromWorkspace reads plan.json from the workspace and returns it as JSON string.
// Returns empty string if plan doesn't exist.
func ReadPlanFromWorkspace(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) string {
	planPath := "planning/plan.json"
	if workspacePath != "" {
		planPath = workspacePath + "/planning/plan.json"
	}
	content, err := readFile(ctx, planPath)
	if err != nil {
		return ""
	}
	// Validate it's valid JSON
	var plan interface{}
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return ""
	}
	return content
}

// ReadVariablesFromWorkspace reads variables.json and returns formatted variable names.
// Returns empty string if variables don't exist.
func ReadVariablesFromWorkspace(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) string {
	varPath := "planning/variables.json"
	if workspacePath != "" {
		varPath = workspacePath + "/planning/variables.json"
	}
	content, err := readFile(ctx, varPath)
	if err != nil {
		return ""
	}

	// Parse the variables manifest
	var manifest VariablesManifest
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return ""
	}
	return FormatVariableNames(&manifest)
}

// undeclaredRunVariables refuses per-run variables the workflow does not
// declare, listing the declared names.
func undeclaredRunVariables(manifest *VariablesManifest, values map[string]string) string {
	declared := map[string]bool{}
	var names []string
	if manifest != nil {
		for _, variable := range manifest.Variables {
			if variable.Name != "" && !declared[variable.Name] {
				declared[variable.Name] = true
				names = append(names, variable.Name)
			}
		}
	}
	var unknown []string
	for name := range values {
		if !declared[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	sort.Strings(names)
	return fmt.Sprintf("Unknown variable(s) %s. Declared variables: %s. Put other per-run instructions in human_inputs for the step that needs them.", strings.Join(unknown, ", "), strings.Join(names, ", "))
}

// scheduleDestinationArguments reads run_destination and chat_id from a
// schedule tool call; a missing chat_id is nil.
func scheduleDestinationArguments(args map[string]interface{}) (string, *string) {
	destination, _ := args["run_destination"].(string)
	var chatID *string
	if value, ok := args["chat_id"].(string); ok {
		chatID = &value
	}
	return destination, chatID
}
