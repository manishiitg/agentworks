package step_based_workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Pre-parsed templates for execution-only agent - panics at startup if invalid
var executionOnlySystemTemplate = MustRegisterTemplate("executionOnlySystem", guidance.StepSystemPromptTemplate("execution"))
var agentDelegationSystemTemplate = MustRegisterTemplate("agentDelegationSystem", guidance.StepSystemPromptTemplate("agent-delegation"))

var executionOnlyUserTemplate = MustRegisterTemplate("executionOnlyUser", `{{if eq .IsContributionTurn "true"}}{{.BaseDescription}}{{else}}{{if .OrchestratorInstructions}}## Orchestrator Instructions (HIGHEST PRIORITY)
{{.OrchestratorInstructions}}
{{else}}**DESCRIPTION**: {{.BaseDescription}}
{{end}}{{if eq .IsScriptedMode "true"}}**MODE NOTE (scripted)**: Implement the task below as reusable Python code. Use the exact main.py path in the **Code Execution Mode** section: canonical workflow code for version 1, or the run's code/main.py for legacy workflows. Never guess the source path. Treat the resolved **Inputs** list and declared tools as the source of truth. Adapt hardcoded sibling step paths into declared input arguments.
{{else}}**MODE NOTE (agentic)**: This step is running in normal `+"`"+`agentic`+"`"+` mode, not `+"`"+`scripted`+"`"+`. **Tool calls come first.** Call the available tools and APIs directly to inspect state, fetch data, and produce outputs. Do **not** try to write one large reusable Python script for the whole task — that is what `+"`"+`scripted`+"`"+` mode is for, which this step is not in. Use short one-off shell or Python snippets via `+"`"+`execute_shell_command`+"`"+` only when consolidating several tool calls into one materially helps a specific subtask (e.g. batching API calls, parsing JSON with `+"`"+`jq`+"`"+`). A single tool call is a perfectly valid step.
{{end}}**LOCATION**: {{.StepExecutionPath}}/ (Workspace: {{.WorkspacePath}})

{{if .PreviousIterationOutput}}
### Previous Attempt Results
{{.PreviousIterationOutput}}
*Adjust your approach to avoid repeating previous failures.*
{{end}}

{{if .WorkshopHumanInput}}
## Human Input (Highest Priority)
The operator supplied this input with execute_step(..., human_input=...).
You MUST incorporate it into this run. It takes priority over the default step description where they conflict.

{{.WorkshopHumanInput}}
{{end}}

{{/* Only renders on scripted retries. Pure agentic pre-validation failures
     take the continuation path (buildValidationContinuationUserMessage), which
     sends a follow-up user message instead of re-rendering this template. */}}
{{if .ValidationFeedback}}
### Validation Issues
{{.ValidationFeedback}}
*Fix these errors in your next execution.*
{{end}}

### Inputs
{{if .StepContextDependencies}}{{.StepContextDependencies}}{{else}}None{{end}}

### Output
{{if .StepContextOutput}}- **Output File**: {{.StepContextOutput}} (Create in '{{.StepExecutionPath}}/'){{else}}{{if eq .DBAccess "read"}}- **No output file** — read-only DB access; do not persist database changes.{{else if eq .DBDirectAccess "true"}}- **No output file** — persist scripted results through `+"`"+`$DB_PATH`+"`"+`.{{else}}- **No output file** — persist results with `+"`mutate_workflow_db`"+`.{{end}}{{end}}

{{if .ScriptedPriorContext}}{{.ScriptedPriorContext}}
{{end}}### Execution Checklist
1. Review all **Inputs** above. Inlined files are ready to use. For any marked "read via tool", read them first.
{{if .HasSkill}}2. Read **Skill files** as guidance only. The current step description is the main source of truth; use or ignore skill guidance depending on whether it matches this step.
{{else}}2. Treat the current step description as the main source of truth. If you consult learnings files manually, use them only as advisory guidance and ignore stale or conflicting notes.
{{end}}3. Execute the task using tool calls. Do NOT stop mid-task with a text message.
4. **NO FABRICATED DATA**: Every value in the output must come from a real data source (MCP tools, APIs, or input files). Do NOT hardcode or invent output data.
5. Verify the required outputs are fully produced before finishing.
6. Create the output file.{{end}}`)

// WorkflowExecutionOnlyTemplate holds template variables for execution-only agent prompts
type WorkflowExecutionOnlyTemplate struct {
	StepTitle                string
	StepDescription          string
	StepContextDependencies  string
	StepContextOutput        string
	WorkspacePath            string
	IsCodeExecutionMode      string // "true" or "false" - indicates if code execution mode is enabled
	ValidationFeedback       string
	PreviousIterationOutput  string // Previous iteration execution output
	VariableNames            string // Variable names with descriptions ({{VAR_NAME}} - description)
	VariableValues           string // Variable names with actual values ({{VAR_NAME}} = value)
	LearningHistory          string // Formatted learning conversation history (REQUIRED for execution-only mode)
	StepNumber               string // Step identifier (e.g., "step-8" or "step-3-sub-fetch")
	StepExecutionPath        string // Full execution folder path (e.g., "execution/step-8")
	PreviousStepsSummary     string // Summary of previous completed steps (titles, descriptions, outputs)
	WorkshopHumanInput       string // Operator input supplied via execute_step(human_input=...)
	StepSuccessCriteria      string // Success criteria for the step
	BaseDescription          string // Step description without orchestrator instructions
	OrchestratorInstructions string // Orchestrator instructions (split from description)
	HasSkill                 string // "true" if skill files are available
	IsScriptedMode           string // "true" when scripted mode is enabled
	DBAccess                 string // effective "read" or "read-write"
	DBDirectAccess           string // "true" only for saved scripted-code compatibility
	DBGuidance               string // shared managed DB contract for agentic steps
	ScriptedPriorContext     string // Prior script context (failed script + error, or existing script for update)
	IsContributionTurn       string // "true" for a synthetic learnings/KB closing turn — renders JUST the contribution message, no execute-the-task/output-file scaffolding
}

// WorkflowExecutionOnlyAgent executes steps using pre-discovered learning context
// This agent does NOT discover learnings - it receives learning history from readLearningHistory() method
type WorkflowExecutionOnlyAgent struct {
	*agents.BaseOrchestratorAgent
}

// NewWorkflowExecutionOnlyAgent creates a new execution-only agent
func NewWorkflowExecutionOnlyAgent(config *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) *WorkflowExecutionOnlyAgent {
	baseAgent := agents.NewBaseOrchestratorAgentWithEventBridge(
		config,
		logger,
		tracer,
		agents.TodoPlannerExecutionAgentType, // Reuse execution agent type for consistency
		eventBridge,
	)

	return &WorkflowExecutionOnlyAgent{
		BaseOrchestratorAgent: baseAgent,
	}
}

// NewWorkflowDelegatingAgent uses the common message-sequence implementation
// while retaining the orchestration agent type required by child lifecycle and
// progress event plumbing.
func NewWorkflowDelegatingAgent(config *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) *WorkflowExecutionOnlyAgent {
	baseAgent := agents.NewBaseOrchestratorAgentWithEventBridge(
		config,
		logger,
		tracer,
		agents.OrchestratorAgentType,
		eventBridge,
	)

	return &WorkflowExecutionOnlyAgent{BaseOrchestratorAgent: baseAgent}
}

// Execute implements the OrchestratorAgent interface
func (hctpeoa *WorkflowExecutionOnlyAgent) Execute(ctx context.Context, templateVars map[string]string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	// Generate system prompt and user message separately
	systemPrompt := hctpeoa.executionOnlySystemPromptProcessor(templateVars)
	userMessage := hctpeoa.executionOnlyUserMessageProcessor(templateVars)
	if authored, ok := templateVars["AuthoredSystemPrompt"]; ok {
		systemPrompt = authored
		userMessage = templateVars["AuthoredUserMessage"]
	}

	// Create a simple input processor that returns the user message
	inputProcessor := func(map[string]string) string {
		return userMessage
	}

	// Use ExecuteWithTemplateValidation with system prompt (overwrite=true to replace default MCP prompt with agent-specific prompt)
	return hctpeoa.BaseOrchestratorAgent.ExecuteWithTemplateValidation(ctx, templateVars, inputProcessor, conversationHistory, nil, systemPrompt, true)
}

// buildCodeExecBestPractices returns the Python best practices section — only for
// learn-code mode, where main.py is the mandated output and a canonical call_mcp
// helper is worth embedding. Pure code-exec mode is shell-first (curl/jq/etc.);
// it doesn't need the 35-line Python helper and should avoid pinning agents to
// any one language.
func buildCodeExecBestPractices(isCodeExec bool, templateVars map[string]string, useProjectedReferenceSkills bool) string {
	if !isCodeExec || templateVars["IsScriptedMode"] != "true" || useProjectedReferenceSkills {
		return ""
	}
	var varMappingLines []string
	if raw := templateVars["ScriptedVarMapping"]; raw != "" {
		varMappingLines = strings.Split(raw, "\n")
	}
	hasInputArgs := templateVars["StepContextDependencies"] != ""
	return BuildPythonBestPractices(varMappingLines, hasInputArgs)
}

var hardcodedStepPathCmdRegex = regexp.MustCompile(`(?i)cat\s+'?\{WORKSPACE_PATH\}/step-\d+/[^'\s]+`)

func sanitizeScriptedDescription(desc string) string {
	if desc == "" {
		return desc
	}

	lines := strings.Split(desc, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case hardcodedStepPathCmdRegex.MatchString(trimmed):
			out = append(out, "- Use the resolved dependency file from the Requirements section below. Do NOT hardcode step-numbered paths.")
			continue
		case strings.Contains(trimmed, "Where {WORKSPACE_PATH}"):
			continue
		case strings.Contains(trimmed, "Use ONLY the current run's step-"):
			out = append(out, "- Use only the resolved dependency path from this run. Do NOT explore other iterations or groups.")
			continue
		}
		out = append(out, line)
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}

// executionOnlySystemPromptProcessor generates the system prompt for execution-only agent
func (hctpeoa *WorkflowExecutionOnlyAgent) executionOnlySystemPromptProcessor(templateVars map[string]string) string {
	workspacePath := templateVars["WorkspacePath"]
	stepContextOutput := templateVars["StepContextOutput"]
	isCodeExecutionMode := templateVars["IsCodeExecutionMode"] == "true"
	learningHistory := templateVars["LearningHistory"]
	stepNumber := templateVars["StepNumber"]               // e.g., "step-8" or "step-3-sub-fetch"
	stepExecutionPath := templateVars["StepExecutionPath"] // e.g., "execution/step-8"
	previousStepsSummary := templateVars["PreviousStepsSummary"]
	knowledgebasePath := templateVars["KnowledgebasePath"] // Knowledgebase folder path (persistent files across runs)
	dbPath := templateVars["DBPath"]                       // DB folder path (structured JSON, always enabled)
	dbAccess := strings.TrimSpace(templateVars["DBAccess"])
	if dbAccess == "" {
		dbAccess = DBAccessReadWrite
	}
	dbDirectAccess := templateVars["DBDirectAccess"]
	if dbDirectAccess == "" {
		dbDirectAccess = fmt.Sprintf("%t", templateVars["IsScriptedMode"] == "true")
	}
	useProjectedReferenceSkills := hctpeoa.useProjectedReferenceSkills(templateVars)
	if useProjectedReferenceSkills {
		// Every transport receives builder-reference and workflow-learnings as
		// attached identity. Keep the prompt focused on this run's dynamic
		// contract instead of repeating static reference text or a recursive
		// legacy file inventory.
		learningHistory = ""
	}

	// Get current date and time
	now := time.Now()
	currentDate := now.Format("2006-01-02")
	currentTime := now.Format("15:04:05")

	// Build code execution section using common builder
	useCodeStyleRules := isCodeExecutionMode
	codeExecutionSection := BuildCodeExecutionSection(isCodeExecutionMode, workspacePath)

	// Learn code mode: append instructions to write main.py (added on top of code execution section)
	isScriptedMode := templateVars["IsScriptedMode"] == "true"
	if isScriptedMode {
		isRelearnMode := templateVars["IsRelearnMode"] == "true"
		priorScript := templateVars["ScriptedPriorScript"]
		priorError := templateVars["ScriptedPriorError"]
		codeDirAbsPath := filepath.Join(stepExecutionPath, "code")
		if supplied := templateVars["ScriptedWorkingDir"]; supplied != "" {
			codeDirAbsPath = supplied
		}

		// Parse input arg paths from templateVars (newline-separated)
		var inputArgPaths []string
		if raw := templateVars["ScriptedInputArgs"]; raw != "" {
			inputArgPaths = strings.Split(raw, "\n")
		}

		// Parse env var names from templateVars (newline-separated)
		var envVarNames []string
		if raw := templateVars["ScriptedEnvVarNames"]; raw != "" {
			envVarNames = strings.Split(raw, "\n")
		}

		// Parse variable→env mapping lines (newline-separated)
		var varMappingLines []string
		if raw := templateVars["ScriptedVarMapping"]; raw != "" {
			varMappingLines = strings.Split(raw, "\n")
		}

		validationSchemaJSON := templateVars["ValidationSchema"]
		hasBrowser := templateVars["HasBrowserAccess"] == "true"
		isCodeLocked := templateVars["IsScriptedLocked"] == "true"
		codeExecutionSection += GetScriptedModeInstructions(codeDirAbsPath, stepExecutionPath, isRelearnMode, priorScript, priorError, inputArgPaths, envVarNames, varMappingLines, validationSchemaJSON, hasBrowser, isCodeLocked, useProjectedReferenceSkills, templateVars["DirectCodeSource"] == "true")
		if contract := strings.TrimSpace(templateVars["ScriptedParameterSchema"]); contract != "" {
			codeExecutionSection += "\n**Script parameter contract:** This reusable script must read `STEP_PARAMS_JSON`, parse it as a JSON object, and support exactly this declared contract:\n```json\n" + contract + "\n```\nDo not hardcode current parameter values. `STEP_DELEGATION_ROUTE_ID` and `STEP_DELEGATION_TODO_ID` identify an orchestrated call. Positional arguments remain reserved for declared context dependencies.\n"
			if values := strings.TrimSpace(templateVars["ScriptedParameterValues"]); values != "" {
				codeExecutionSection += "Current validated call values (also present in `STEP_PARAMS_JSON`): `" + values + "`\n"
			}
		}
		if templateVars["DirectCodeSource"] == "true" && !isCodeLocked {
			codeExecutionSection += "\nThis workflow uses canonical code/ source. Edit the supplied working directory and shared helpers in WORKFLOW_CODE_ROOT directly. The controller runs this code after each authoring/repair turn; do not execute main.py separately or fabricate outputs to satisfy validation. No copy-back is performed. Keep outputs at STEP_OUTPUT_DIR or db/assets.\n"
		}
	}

	// Get variable names and values for system prompt
	variableNames := templateVars["VariableNames"]
	variableValues := templateVars["VariableValues"]
	validationSchema := templateVars["ValidationSchema"] // Validation schema JSON string
	folderGuardReadPaths := templateVars["FolderGuardReadPaths"]
	folderGuardWritePaths := templateVars["FolderGuardWritePaths"]
	systemStepDescription := templateVars["StepDescription"]
	if isScriptedMode {
		systemStepDescription = sanitizeScriptedDescription(systemStepDescription)
	}

	// Execute the pre-parsed template
	var result strings.Builder
	err := executionOnlySystemTemplate.Execute(&result, map[string]interface{}{
		"StepTitle":                 templateVars["StepTitle"],
		"StepDescription":           systemStepDescription,
		"StepContextDependencies":   templateVars["StepContextDependencies"],
		"WorkspacePath":             workspacePath,
		"IsCodeExecutionMode":       isCodeExecutionMode,
		"CodeExecutionSection":      codeExecutionSection,
		"StepContextOutput":         stepContextOutput,
		"CurrentDate":               currentDate,
		"CurrentTime":               currentTime,
		"LearningHistory":           learningHistory,
		"HasLearnings":              fmt.Sprintf("%t", learningHistory != ""),
		"VariableNames":             variableNames,
		"VariableValues":            variableValues,
		"VarMapping":                templateVars["ScriptedVarMapping"], // {{VAR}} → SECRET_VAR mapping (for code exec guidance)
		"UseCodeStyleRules":         useCodeStyleRules,
		"PythonBestPractices":       buildCodeExecBestPractices(isCodeExecutionMode, templateVars, useProjectedReferenceSkills),
		"StepNumber":                stepNumber,
		"StepExecutionPath":         stepExecutionPath,
		"PreviousStepsSummary":      previousStepsSummary,
		"PlanPosition":              templateVars["PlanPosition"],            // Where this step sits in the plan — steps cannot read planning/plan.json
		"ValidationSchema":          validationSchema,                        // Validation schema JSON string
		"PriorValidationFailures":   templateVars["PriorValidationFailures"], // Unresolved prevalidation concerns from earlier runs of this step
		"KnowledgebasePath":         knowledgebasePath,                       // Knowledgebase folder path
		"DBPath":                    dbPath,                                  // DB folder path (always enabled)
		"DBAccess":                  dbAccess,
		"DBDirectAccess":            dbDirectAccess,
		"DBGuidance":                BuildManagedWorkflowDBGuidance(dbAccess),
		"KbAccess":                  templateVars["KbAccess"],                  // "read" | "write" | "read-write" | "none"
		"KbAccessLabel":             templateVars["KbAccessLabel"],             // Human-readable label (e.g., "READ/WRITE")
		"KnowledgebaseContribution": templateVars["KnowledgebaseContribution"], // Author-authored instruction for the step's KB contribution (direct mode only)
		"KBGuidanceBlock":           templateVars["KBGuidanceBlock"],           // Pre-built KB guidance block — non-empty only when the step has KB write access
		"FolderGuardReadPaths":      folderGuardReadPaths,                      // Folder guard read paths for agent guidance
		"FolderGuardWritePaths":     folderGuardWritePaths,                     // Folder guard write paths for agent guidance
		"MessageSequenceAccessNote": templateVars["MessageSequenceAccessNote"], // Effective inherited/narrowed access for message_sequence turns
		"IsScriptedMode":            templateVars["IsScriptedMode"],            // Learn code mode flag (validation schema shown in scripted section instead)
		"WorkflowRoot":              templateVars["WorkflowRoot"],              // Workflow root path for absolute cwd display
		"DocsRoot":                  GetPromptDocsRoot(),                       // Workspace docs base path — differs between macOS dev (/Users/.../workspace-docs) and Docker (/app/workspace-docs); do NOT hardcode.
		// Browser authoring rules (refs-are-ephemeral + durable-selector priority
		// + canonical DOM probe) apply to every browser step — code-exec throwaway
		// scripts AND learn-code saved main.py. Only the final-artifact permanence
		// differs between modes; the discovery/selector discipline is identical.
		"BrowserAuthoringRules": browserAuthoringRulesForExecution(templateVars, useProjectedReferenceSkills),
		"DelegationGuidance":    renderAgentDelegationGuidance(templateVars),
	})
	if err != nil {
		panic(fmt.Sprintf("execution-only system prompt template execution failed (missing variable?): %v", err))
	}

	return result.String()
}

func renderAgentDelegationGuidance(templateVars map[string]string) string {
	routes := strings.TrimSpace(templateVars["PredefinedRoutes"])
	if routes == "" {
		return ""
	}
	var result strings.Builder
	if err := agentDelegationSystemTemplate.Execute(&result, map[string]interface{}{
		"PredefinedRoutes":    routes,
		"IsCodeExecutionMode": templateVars["IsCodeExecutionMode"] == "true",
	}); err != nil {
		panic(fmt.Sprintf("agent delegation system prompt template execution failed: %v", err))
	}
	return result.String()
}

// useProjectedReferenceSkills retains the legacy template switch used by
// archived/replayed prompts. Production attaches the reference corpus on every
// transport; coding CLIs additionally project it to disk.
func (hctpeoa *WorkflowExecutionOnlyAgent) useProjectedReferenceSkills(templateVars map[string]string) bool {
	if hctpeoa == nil || hctpeoa.BaseOrchestratorAgent == nil {
		return usesProjectedReferenceSkills(nil, templateVars)
	}
	return usesProjectedReferenceSkills(hctpeoa.BaseOrchestratorAgent.GetConfig(), templateVars)
}

func browserAuthoringRulesForExecution(templateVars map[string]string, useProjectedReferenceSkills bool) string {
	if useProjectedReferenceSkills {
		return ""
	}
	return BrowserAuthoringRulesFromTemplateVars(templateVars)
}

// executionOnlyUserMessageProcessor generates the user message for execution-only agent
func (hctpeoa *WorkflowExecutionOnlyAgent) executionOnlyUserMessageProcessor(templateVars map[string]string) string {
	// Message-sequence turns after the opener are already fully authored user
	// messages. Preserve them verbatim instead of re-rendering the opening task
	// envelope on every turn.
	if followUp := strings.TrimSpace(templateVars["FollowUpMessage"]); followUp != "" {
		return followUp
	}

	// Split description into base description and orchestrator instructions
	fullDescription := templateVars["StepDescription"]
	isScriptedMode := templateVars["IsScriptedMode"] == "true"
	dbAccess := strings.TrimSpace(templateVars["DBAccess"])
	if dbAccess == "" {
		dbAccess = DBAccessReadWrite
	}
	dbDirectAccess := templateVars["DBDirectAccess"]
	if dbDirectAccess == "" {
		dbDirectAccess = fmt.Sprintf("%t", isScriptedMode)
	}
	if isScriptedMode {
		fullDescription = sanitizeScriptedDescription(fullDescription)
	}
	baseDescription := fullDescription
	orchestratorInstructions := ""
	if idx := strings.Index(fullDescription, "\n\n## Orchestrator Instructions\n\n"); idx >= 0 {
		baseDescription = strings.TrimSpace(fullDescription[:idx])
		orchestratorInstructions = strings.TrimSpace(fullDescription[idx+len("\n\n## Orchestrator Instructions\n\n"):])
	}

	// Create template data
	templateData := WorkflowExecutionOnlyTemplate{
		StepTitle:                templateVars["StepTitle"],
		StepDescription:          fullDescription,
		BaseDescription:          baseDescription,
		OrchestratorInstructions: orchestratorInstructions,
		StepContextDependencies:  templateVars["StepContextDependencies"],
		StepContextOutput:        templateVars["StepContextOutput"],
		WorkspacePath:            templateVars["WorkspacePath"],
		IsCodeExecutionMode:      templateVars["IsCodeExecutionMode"],
		ValidationFeedback:       templateVars["ValidationFeedback"],
		PreviousIterationOutput:  templateVars["PreviousIterationOutput"],
		VariableNames:            templateVars["VariableNames"],
		VariableValues:           templateVars["VariableValues"],
		LearningHistory:          templateVars["LearningHistory"],
		StepNumber:               templateVars["StepNumber"],
		StepExecutionPath:        templateVars["StepExecutionPath"],
		PreviousStepsSummary:     templateVars["PreviousStepsSummary"],
		WorkshopHumanInput:       templateVars["WorkshopHumanInput"],
		StepSuccessCriteria:      templateVars["StepSuccessCriteria"],
		HasSkill:                 fmt.Sprintf("%t", templateVars["LearningHistory"] != ""),
		IsScriptedMode:           fmt.Sprintf("%t", isScriptedMode),
		DBAccess:                 dbAccess,
		DBDirectAccess:           dbDirectAccess,
		ScriptedPriorContext:     BuildScriptedPriorContext(templateVars["ScriptedPriorScript"], templateVars["ScriptedPriorError"], templateVars["ScriptedMetadataPath"], templateVars["IsScriptedLocked"] == "true"),
		IsContributionTurn:       templateVars["IsContributionTurn"],
	}

	// Execute the pre-parsed template
	var result strings.Builder
	if err := executionOnlyUserTemplate.Execute(&result, templateData); err != nil {
		panic(fmt.Sprintf("execution-only user message template execution failed (missing variable?): %v", err))
	}

	return result.String()
}
