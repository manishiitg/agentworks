package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
)

type stepRuntimeSkillPreview struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Attachment  string   `json:"attachment"`
	Description string   `json:"description"`
	References  []string `json:"references,omitempty"`
}

// This is a construction preview, not a saved run or an execution admission.
// Live inputs and provider/MCP discovery remain explicitly unresolved. Never
// initialize an agent, allocate a session or repair saved files in this path.
type stepRuntimePreview struct {
	StepID             string                    `json:"step_id"`
	ItemID             string                    `json:"item_id,omitempty"`
	SystemPrompt       string                    `json:"system_prompt,omitempty"`
	UserMessage        string                    `json:"user_message,omitempty"`
	Skills             []stepRuntimeSkillPreview `json:"skills"`
	Tools              []string                  `json:"workspace_tools"`
	InvocationTools    []string                  `json:"conditional_invocation_tools,omitempty"`
	MCPServers         []string                  `json:"configured_mcp_servers"`
	MCPToolSelection   []string                  `json:"configured_mcp_tool_selection"`
	ReadPaths          []string                  `json:"read_paths"`
	WritePaths         []string                  `json:"write_paths"`
	ParallelToolCalls  bool                      `json:"parallel_tool_calls"`
	Unresolved         []string                  `json:"unresolved"`
	ConfigurationNotes []string                  `json:"configuration_notes,omitempty"`
}

func (hcpo *StepBasedWorkflowOrchestrator) previewStepRuntime(ctx context.Context, stepID, itemID string) (*stepRuntimePreview, error) {
	if strings.TrimSpace(stepID) == "" || strings.ContainsAny(stepID, `/\`) {
		return nil, fmt.Errorf("step_id must be a current plan ID, not a file path")
	}
	plan, err := hcpo.ReadCurrentPlan(ctx)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveWorkshopStepID(plan, stepID)
	if err != nil {
		return nil, err
	}
	info := findWorkshopStepByID(plan.Steps, resolved)
	if info == nil {
		return nil, fmt.Errorf("step %q not found", resolved)
	}
	step := info.Step
	// Read current config without ReadStepConfigs' legacy repair/write path.
	content, readErr := hcpo.ReadWorkspaceFile(ctx, "planning/step_config.json")
	if readErr == nil && strings.TrimSpace(content) != "" {
		configs, parseErr := ParseStepConfigContent(content)
		if parseErr != nil {
			return nil, parseErr
		}
		if matched := MatchStepConfigByID(step.GetID(), configs); matched != nil {
			if getAgentConfigs(step) == nil {
				setPreviewStepConfig(step, matched)
			} else {
				MergeAgentConfigFields(getAgentConfigs(step), matched, step.GetID(), hcpo.GetLogger())
			}
		}
		for _, entry := range configs {
			if entry.ID == step.GetID() && entry.ValidationSchema != nil {
				updateValidationSchemaOnStep(step, entry.ValidationSchema)
			}
		}
	} else if readErr != nil && !strings.Contains(strings.ToLower(readErr.Error()), "not exist") && !strings.Contains(strings.ToLower(readErr.Error()), "not found") {
		return nil, fmt.Errorf("read current step config: %w", readErr)
	}
	if overrides, _ := hcpo.ReadStepOverrides(ctx); overrides != nil {
		if getAgentConfigs(step) == nil {
			setPreviewStepConfig(step, overrides)
		} else {
			MergeAgentConfigFields(getAgentConfigs(step), overrides, step.GetID(), hcpo.GetLogger())
		}
	}
	stepConfig := getAgentConfigs(step)
	result := &stepRuntimePreview{StepID: step.GetID(), ParallelToolCalls: true, Unresolved: []string{
		"Preview of current construction, not execution or a historical run. Runtime may select another run/group.",
		"Variable values are placeholders; dependency file contents, live human input, foreach rows and retry failures resolve during execution.",
		"Provider-added instructions, intrinsic tools, live MCP tool discovery, browser connectivity and specialist invocation scope resolve during execution.",
		"Skill bodies are read on demand; this result lists attached identities and reference paths separately from the system instructions.",
	}}
	if stepConfig != nil && stepConfig.DisableParallelToolExecution != nil {
		result.ParallelToolCalls = !*stepConfig.DisableParallelToolExecution
	}
	config := &agents.OrchestratorAgentConfig{ServerNames: normalizeServerNames(hcpo.GetSelectedServers()), SelectedTools: hcpo.GetSelectedTools()}
	if stepConfig != nil && len(stepConfig.SelectedServers) > 0 {
		config.ServerNames = filterServersByWorkflow(stepConfig.SelectedServers, hcpo.GetSelectedServers())
	}
	if stepConfig != nil && len(stepConfig.SelectedTools) > 0 {
		config.SelectedTools = filterToolsByWorkflow(stepConfig.SelectedTools, hcpo.GetSelectedServers())
	}
	result.MCPServers, result.MCPToolSelection = config.ServerNames, config.SelectedTools
	scripted := isScriptedStep(step, stepConfig)
	if llmConfig := hcpo.selectExecutionLLM(ctx, stepConfig, step.GetID()); llmConfig != nil {
		config.LLMConfig.Primary.Provider = llmConfig.Primary.Provider
	} else {
		result.Unresolved = append(result.Unresolved, "Execution model selection is unavailable; transport-specific instructions may differ.")
	}
	config.UseCodeExecutionMode = scripted || common.IsCLIProvider(config.LLMConfig.Primary.Provider)
	if !config.UseCodeExecutionMode && stepConfig != nil && stepConfig.UseCodeExecutionMode != nil {
		config.UseCodeExecutionMode = *stepConfig.UseCodeExecutionMode
	}
	tools, _ := hcpo.prepareCustomTools(stepConfigWithDescribedHumanTools(stepConfig, step))
	result.Tools = registeredToolNames(tools)
	sort.Strings(result.Tools)
	kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())
	learningsAccess := hcpo.resolveExecutionLearningsAccess(stepConfig, step)
	dbAccess := hcpo.resolveDBAccess(stepConfig)
	readPaths, writePaths := hcpo.setupExecutionFolderGuard(step.GetID(), step.GetID(), kbAccess, learningsAccess, dbAccess, stepConfig)
	values := map[string]string{}
	for name := range hcpo.variableValues {
		values[name] = "<VARIABLE:" + name + ">"
	}
	if hcpo.variablesManifest != nil {
		for _, name := range hcpo.variablesManifest.GetVariableNames() {
			values[name] = "<VARIABLE:" + name + ">"
		}
	}
	root := hcpo.agentSequenceAbsPath("")
	executionPath := filepath.Join(root, "execution")
	if hcpo.selectedRunFolder != "" {
		executionPath = filepath.Join(root, "runs", hcpo.selectedRunFolder, "execution")
	} else {
		result.Unresolved = append(result.Unresolved, "No run folder selected: the displayed scope is a construction baseline; actual run paths will resolve when admitted.")
	}
	stepPath := filepath.Join(executionPath, step.GetID())
	vars := map[string]string{
		"StepTitle": ResolveVariables(step.GetTitle(), values), "StepDescription": ResolveVariables(step.GetDescription(), values),
		"StepContextDependencies": strings.Join(step.GetContextDependencies(), "\n"), "StepContextOutput": ResolveVariables(step.GetContextOutput().String(), values),
		"WorkspacePath": executionPath, "WorkflowRoot": root, "StepExecutionPath": stepPath,
		"DBPath": filepath.Join(root, DBFolderName), "DBAccess": dbAccess, "DBDirectAccess": fmt.Sprint(scripted),
		"KnowledgebasePath": filepath.Join(root, KnowledgebaseFolderName), "KbAccess": kbAccess, "KbAccessLabel": kbAccessLabel(kbAccess),
		"KBGuidanceBlock": sharedStepKBGuidance(hcpo.GetWorkspacePath(), kbAccess, kbContributionForPrompt(stepConfig), filepath.Join(root, KnowledgebaseFolderName, KBNotesFolderName)),
		"IsScriptedMode":  fmt.Sprint(scripted), "ScriptedWorkingDir": hcpo.scriptedWorkingDir(step.GetID(), stepPath),
		"ValidationSchema": agentSequenceValidationSchemaJSON(step.GetValidationSchema()),
	}
	if scripted && hcpo.usesCodeTree() {
		vars["ScriptedWorkingDir"] = hcpo.agentSequenceAbsPath(hcpo.scriptedSourceDir(step.GetID()))
		vars["DirectCodeSource"] = "true"
	}
	if scripted && stepConfig != nil && stepConfig.LockCode != nil {
		vars["IsScriptedLocked"] = fmt.Sprint(*stepConfig.LockCode)
	}
	if agent, ok := step.(*AgentPlanStep); ok {
		hadHelpers := len(agent.PredefinedRoutes) > 0
		runtimeAgent := *agent
		runtimeAgent.PredefinedRoutes = goalScriptedRoutes(agent.PredefinedRoutes)
		agent = &runtimeAgent
		step = agent
		if agent.AuthoredPrompt {
			return nil, fmt.Errorf("authored-prompt steps use their own renderer; inspect their authored contract and get_step_prompts for a saved run")
		}
		var item *AgentItem
		if agentDescriptionFirst(agent) && (itemID == "" || itemID == AgentDescriptionTurnID) {
			opener := agentDescriptionItem(agent)
			item = &opener
		}
		for i := range agent.Items {
			if item != nil {
				break
			}
			candidate := &agent.Items[i]
			if (itemID == "" && candidate.Type == "user_message") || candidate.ID == itemID {
				item = candidate
				break
			}
		}
		if item == nil || item.Type != "user_message" {
			return nil, fmt.Errorf("select a user_message item to preview an agent turn; foreach/scripted/prevalidation inputs resolve at runtime")
		}
		access := hcpo.resolveAgentItemWriteAccess(stepConfig, *item)
		if item.ID == AgentDescriptionTurnID && agent.InitialWriteAccess != nil {
			access = hcpo.constrainAgentWriteAccess(stepConfig, *agent.InitialWriteAccess)
		}
		readPaths, writePaths = hcpo.setupAgentFolderGuard(step.GetID(), step.GetID(), stepConfig, access, true)
		vars = hcpo.buildAgentTemplateVars(ctx, agent, *item, max(info.TopIndex-1, 0), step.GetID(), ResolveVariables(item.Message, values), readPaths, writePaths, access, values)
		result.ItemID = item.ID
		if !agentDescriptionFirst(agent) && (itemID == "" || (len(agent.Items) > 0 && item.ID == agent.Items[0].ID)) {
			vars["InitialTaskDescription"] = ResolveVariables(agentTaskDescription(agent), values)
		}
		if hadHelpers {
			readPaths, writePaths = hcpo.setupOrchestratorFolderGuard(step, step.GetID())
			vars = hcpo.buildOrchestratorTemplateVars(ctx, agent, max(info.TopIndex-1, 0), step.GetID(), nil, nil, plan.Steps, "", nil, values)
			vars["StepContextDependencies"] = strings.Join(step.GetContextDependencies(), "\n")
			vars["FollowUpMessage"] = ResolveVariables(item.Message, values)
			applyAgentPromptContract(vars, agent, values)
			if !agentDescriptionFirst(agent) && (itemID == "" || (len(agent.Items) > 0 && item.ID == agent.Items[0].ID)) {
				vars["InitialTaskDescription"] = ResolveVariables(agentTaskDescription(agent), values)
			}
			for _, tool := range virtualtools.CreateSubAgentTools() {
				if !agent.AuthoredPrompt && goalScriptHelperTool(tool.Function.Name) {
					result.InvocationTools = append(result.InvocationTools, tool.Function.Name)
				}
			}
			result.Unresolved = append(result.Unresolved, "Scripted helper calls require an owning invocation; typed parameters and output paths resolve at runtime. LLM child agents are retired.")
		}
	} else if _, ok := step.(*ScriptedPlanStep); !ok {
		return nil, fmt.Errorf("step type %q does not create an execution agent", step.StepType())
	} else if itemID != "" {
		return nil, fmt.Errorf("item_id is only valid for an agent step")
	}
	readPaths = common.DeduplicateStrings(appendDescriptionReferenceReadPaths(readPaths, hcpo.GetWorkspacePath(), step.GetDescription()))
	selected := hcpo.effectiveStepSkills(stepConfig)
	skillRead, _ := BuildSkillFolderGuardPaths(executionSelectedSkills(selected))
	readPaths = common.DeduplicateStrings(append(readPaths, skillRead...))
	if scripted {
		writePaths = common.DeduplicateStrings(append(writePaths, stepPath+"/code"))
	}
	result.ReadPaths, result.WritePaths = readPaths, writePaths
	vars["FolderGuardReadPaths"] = folderGuardPromptText(hcpo.GetWorkspacePath(), readPaths, false)
	vars["FolderGuardWritePaths"] = folderGuardPromptText(hcpo.GetWorkspacePath(), writePaths, true)
	if _, agentStep := step.(*AgentPlanStep); !agentStep {
		vars["IsCodeExecutionMode"] = fmt.Sprint(config.UseCodeExecutionMode)
	}
	placementCtx := ctx
	placementIndex := info.TopIndex - 1
	if info.ParentID != "" {
		if parent := findWorkshopStepByID(plan.Steps, info.ParentID); parent != nil {
			placementCtx = withStepParentPlacement(ctx, parent.Step.GetTitle(), "Caller instructions resolve when this specialist is invoked.")
			placementIndex = parent.TopIndex - 1
		}
		result.Unresolved = append(result.Unresolved, "Specialist placement shows the saved parent; actual delegated task resolves at invocation.")
	}
	vars["PlanPosition"] = hcpo.buildStepPlacement(placementCtx, plan.Steps, placementIndex)
	vars["UseProjectedReferenceSkills"] = "true"
	vars["ReferencedGuides"] = hcpo.referencedGuidesForStep(ctx, step.GetID(), step.GetDescription())
	vars["ScriptedParameterSchema"] = formatScriptParameterContract(scriptedParameterDefinitions(step))
	vars["ScriptedVarMapping"] = buildScriptedVarMappingForPrompt(config.UseCodeExecutionMode || scripted, hcpo.variablesManifest)
	vars["VariableNames"] = FormatVariableNames(hcpo.variablesManifest)
	registeredTools := append(append([]string(nil), result.Tools...), result.InvocationTools...)
	identity, supplements := hcpo.assembleStepRuntimeIdentity(config, selected, learningsAccess != LearningsAccessNone, registeredTools, scripted, kbAccessAllowsRead(kbAccess), filepath.Join(executionPath, "Downloads"))
	seen := map[string]bool{}
	for _, skill := range identity {
		if seen[strings.TrimSpace(skill.Name)] {
			return nil, fmt.Errorf("duplicate skill name %q in step runtime", skill.Name)
		}
		seen[strings.TrimSpace(skill.Name)] = true
		entry := stepRuntimeSkillPreview{Name: skill.Name, Source: skill.Source.Origin, Attachment: "platform", Description: skill.Description}
		for _, selectedName := range executionSelectedSkills(selected) {
			if selectedName == skill.Name {
				entry.Attachment = "step_selection"
			}
		}
		for _, file := range skill.SupportingFiles {
			entry.References = append(entry.References, file.RelPath)
		}
		sort.Strings(entry.References)
		result.Skills = append(result.Skills, entry)
	}
	for _, name := range selected {
		if strings.TrimSpace(name) == "builder-reference" {
			result.ConfigurationNotes = append(result.ConfigurationNotes, "Explicit builder-reference uses the platform-owned, capability-filtered runtime bundle; no second copy is loaded.")
		} else if !seen[name] {
			result.ConfigurationNotes = append(result.ConfigurationNotes, fmt.Sprintf("Selected skill %q was not attached; check its installation and resolved name.", name))
		}
	}
	renderer := &WorkflowExecutionOnlyAgent{}
	result.SystemPrompt = composeStepRuntimeSystemPrompt(renderer.executionOnlySystemPromptProcessor(vars), supplements)
	result.UserMessage = renderer.executionOnlyUserMessageProcessor(vars)
	// Mask known secret values even if authored text accidentally contains one.
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var document interface{}
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	secretValues := []string{}
	for _, secret := range hcpo.GetSecrets() {
		if secret.Value != "" {
			secretValues = append(secretValues, secret.Value)
		}
	}
	sort.Slice(secretValues, func(i, j int) bool { return len(secretValues[i]) > len(secretValues[j]) })
	redacted, err := json.Marshal(redactStepPreview(document, secretValues))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(redacted, result); err != nil {
		return nil, err
	}
	return result, nil
}

func setPreviewStepConfig(step PlanStepInterface, config *AgentConfigs) {
	switch step := step.(type) {
	case *AgentPlanStep:
		step.AgentConfigs = config
	case *ScriptedPlanStep:
		step.AgentConfigs = config
	}
}

func redactStepPreview(value interface{}, secrets []string) interface{} {
	switch value := value.(type) {
	case string:
		for _, secret := range secrets {
			value = strings.ReplaceAll(value, secret, "<REDACTED>")
		}
		return value
	case []interface{}:
		for i := range value {
			value[i] = redactStepPreview(value[i], secrets)
		}
	case map[string]interface{}:
		for key, entry := range value {
			value[key] = redactStepPreview(entry, secrets)
		}
	}
	return value
}
