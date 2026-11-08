package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	transcriptevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/workspace/runlog"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type pythonAgentSelectionKey struct{}
type pythonAgentSelection struct {
	Servers []string
	Scripts []mcpagent.ToolDefinition
	Browser bool
}

// The helper omits implicit browser tools; the ordinary factory keeps its own
// baseline independently of this private selection.
func (s *pythonAgentSelection) filterTools(tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	filtered := make([]llmtypes.Tool, 0, len(tools))
	selected := make(map[string]interface{}, len(executors))
	for _, tool := range tools {
		if tool.Function == nil || tool.Function.Name == "agent_browser" && !s.Browser {
			continue
		}
		filtered = append(filtered, tool)
		if execute, ok := executors[tool.Function.Name]; ok {
			selected[tool.Function.Name] = execute
		}
	}
	return filtered, selected
}

// This hook is private to a Python helper invocation. Ordinary step assembly,
// including its owned Place connections, keeps its existing behaviour.
func (s *pythonAgentSelection) apply(config *agents.OrchestratorAgentConfig) {
	config.ServerNames = normalizeServerNames(append([]string{}, s.Servers...))
	// Keep the normal factory's per-tool restrictions. Selecting a connection
	// must not turn a read-only tool selection into server:*.
	previousTools := config.SelectedTools
	config.SelectedTools = nil
	normalizer := mcpagent.NewToolFilter(nil, nil, nil, nil, nil)
	for _, selected := range previousTools {
		server, _, qualified := strings.Cut(selected, ":")
		if !qualified {
			continue
		}
		for _, requested := range s.Servers {
			if normalizer.NormalizeServerName(server) == normalizer.NormalizeServerName(requested) {
				config.SelectedTools = append(config.SelectedTools, selected)
				break
			}
		}
	}
	filtered := mcpclient.RuntimeOverrides{}
	for name, override := range config.RuntimeOverrides {
		if slices.Contains(s.Servers, name) {
			filtered[name] = override
		}
	}
	config.RuntimeOverrides = filtered
	config.DirectTools = append(config.DirectTools, s.Scripts...)
}

type pythonAgentArguments struct {
	Name           string   `json:"name"`
	Messages       []string `json:"messages"`
	Model          string   `json:"model"`
	Tier           string   `json:"tier"`
	Scripts        []string `json:"scripts"`
	MCP            []string `json:"mcp"`
	Skills         []string `json:"skills"`
	Tools          []string `json:"tools"`
	Browser        bool     `json:"browser"`
	CanAskPerson   bool     `json:"can_ask_person"`
	TimeoutSeconds *float64 `json:"timeout_seconds"`
	ConversationID string   `json:"conversation_id"`
}

type pythonAgentConversation struct {
	WorkflowContinuationState
	ParentStepID string               `json:"parent_step_id"`
	Name         string               `json:"name"`
	FirstMessage string               `json:"first_message"`
	Settings     pythonAgentArguments `json:"settings"`
	Config       AgentConfigs         `json:"config"`
	Session      agentSequenceSession `json:"session"`
}

func (hcpo *StepBasedWorkflowOrchestrator) pythonAgentConversationPath(id string) (string, error) {
	if !strings.HasPrefix(id, "python-agent-") {
		return "", fmt.Errorf("Use a conversation_id returned by agent() in this run.")
	}
	if _, err := uuid.Parse(strings.TrimPrefix(id, "python-agent-")); err != nil {
		return "", fmt.Errorf("Use a conversation_id returned by agent() in this run.")
	}
	return filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), "runs", hcpo.selectedRunFolder, "helpers", "conversations", id+".json")), nil
}

func (hcpo *StepBasedWorkflowOrchestrator) preparePythonAgent(ctx context.Context, parent PlanStepInterface, args pythonAgentArguments) (*pythonAgentConversation, *pythonAgentSelection, error) {
	if strings.TrimSpace(args.Name) == "" || len(args.Messages) == 0 {
		return nil, nil, fmt.Errorf("An agent needs a name and at least one message.")
	}
	for _, message := range args.Messages {
		if strings.TrimSpace(message) == "" {
			return nil, nil, fmt.Errorf("Agent messages must not be empty.")
		}
	}
	if args.Model != "" && args.Tier != "" {
		return nil, nil, fmt.Errorf("Choose model or tier, not both.")
	}
	if args.TimeoutSeconds != nil && (*args.TimeoutSeconds <= 0 || *args.TimeoutSeconds >= float64((1<<63-1)/time.Second)) {
		return nil, nil, fmt.Errorf("timeout_seconds must be a positive representable duration; the parent deadline still applies.")
	}
	var saved pythonAgentConversation
	if args.ConversationID != "" {
		path, err := hcpo.pythonAgentConversationPath(args.ConversationID)
		if err != nil {
			return nil, nil, err
		}
		raw, err := hcpo.ReadWorkspaceFile(ctx, path)
		if err != nil {
			return nil, nil, fmt.Errorf("This conversation is unavailable in this run; start a fresh agent.")
		}
		if err = json.Unmarshal([]byte(raw), &saved); err != nil {
			return nil, nil, fmt.Errorf("This conversation's saved state cannot be read; inspect its run.")
		}
		if saved.WorkspacePath != hcpo.GetWorkspacePath() || saved.RunFolder != hcpo.selectedRunFolder || saved.ParentStepID != parent.GetID() || saved.StepID != args.ConversationID {
			return nil, nil, fmt.Errorf("This conversation does not belong to this script and run.")
		}
		if args.Model != "" && args.Model != saved.Settings.Model || args.Tier != "" && args.Tier != saved.Settings.Tier || len(args.MCP) > 0 && !reflect.DeepEqual(args.MCP, saved.Settings.MCP) || len(args.Skills) > 0 && !reflect.DeepEqual(args.Skills, saved.Settings.Skills) || len(args.Scripts) > 0 && !reflect.DeepEqual(args.Scripts, saved.Settings.Scripts) || len(args.Tools) > 0 && !reflect.DeepEqual(args.Tools, saved.Settings.Tools) || args.Browser && !saved.Settings.Browser || args.CanAskPerson && !saved.Settings.CanAskPerson {
			return nil, nil, fmt.Errorf("A continued conversation keeps its original model, tier and resource selections. Start a fresh agent to change them.")
		}
	} else {
		saved = pythonAgentConversation{WorkflowContinuationState: WorkflowContinuationState{SchemaVersion: 1, WorkspacePath: hcpo.GetWorkspacePath(), RunFolder: hcpo.selectedRunFolder, StepID: "python-agent-" + uuid.NewString(), OwnerKind: "python_agent"}, ParentStepID: parent.GetID(), Name: args.Name, FirstMessage: args.Messages[0], Settings: args}
		if parentConfig := getAgentConfigs(parent); parentConfig != nil {
			raw, _ := json.Marshal(parentConfig)
			if err := json.Unmarshal(raw, &saved.Config); err != nil {
				return nil, nil, err
			}
		}
		saved.Settings.Messages = nil
		saved.Settings.ConversationID = ""
		saved.Settings.TimeoutSeconds = nil
		saved.Config.SelectedServers = normalizeServerNames(args.MCP)
		saved.Config.EnabledSkills = append([]string{}, args.Skills...)
		if args.Model != "" {
			provider, model, valid := strings.Cut(args.Model, "/")
			if !valid || provider == "" || model == "" {
				return nil, nil, fmt.Errorf("model must name a configured provider/model; alternatively choose a tier.")
			}
			provider = strings.ToLower(strings.TrimSpace(provider))
			connection := ""
			if selected := saved.Config.ExecutionLLM; selected != nil && strings.EqualFold(selected.Provider, provider) {
				connection = selected.ConnectionID
			}
			saved.Config.ExecutionLLM = &AgentLLMConfig{Provider: provider, ModelID: model, ConnectionID: connection}
			saved.Config.ExecutionTier = ""
		} else if args.Tier != "" {
			if NormalizeTierOverride(args.Tier) == "" || hcpo.tierResolver == nil {
				return nil, nil, fmt.Errorf("Choose an available high, medium or low tier configured for this workflow.")
			}
			saved.Config.ExecutionLLM = nil
			saved.Config.ExecutionTier = NormalizeTierOverride(args.Tier)
		}
		// Resolve once using the normal account/tier path, then retain that model
		// snapshot instead of silently switching an existing native conversation.
		resolved := hcpo.selectExecutionLLM(ctx, &saved.Config, saved.StepID)
		if resolved == nil {
			return nil, nil, fmt.Errorf("No agent model is configured for this automation; choose its model in the Crew chat.")
		}
		saved.Config.ExecutionLLM = &AgentLLMConfig{Provider: resolved.Primary.Provider, ModelID: resolved.Primary.ModelID, ConnectionID: resolved.Primary.ConnectionID, Options: resolved.Primary.Options}
	}
	selection := &pythonAgentSelection{Servers: append([]string{}, saved.Settings.MCP...), Browser: saved.Settings.Browser}
	saved.Config.EnabledCustomTools = []string{"workspace_advanced:*"}
	if saved.Settings.CanAskPerson {
		saved.Config.EnabledCustomTools = append(saved.Config.EnabledCustomTools, "human_tools:needs_you.ask")
	}
	for _, requested := range saved.Settings.Tools {
		// Files, terminal and browser use the coding agent's native tools.
		// Old step lists may carry this category; it adds no optional app.
		if strings.HasPrefix(requested, "workspace_advanced:") {
			continue
		}
		if err := orchestrator.ValidateCustomToolSelection(requested); err != nil {
			return nil, nil, err
		}
		pool, executors := hcpo.filterOrdinaryStepTools(hcpo.WorkspaceTools, hcpo.WorkspaceToolExecutors)
		functionSelection := strings.HasPrefix(requested, agentworksproduct.ExecutionFunctionTools().Category+":")
		if functionSelection {
			var err error
			pool, executors, err = hcpo.pythonFunctionTools(ctx, saved.StepID)
			if err != nil {
				return nil, nil, err
			}
		}
		matched, enabled := orchestrator.FilterCustomToolsByCategory(pool, executors, []string{requested})
		if len(matched) == 0 {
			return nil, nil, fmt.Errorf("Tool %q is unavailable to this automation; select an available tool in its Crew.", requested)
		}
		for _, tool := range matched {
			name := tool.Function.Name
			if name == "human_feedback" && !saved.Settings.CanAskPerson || name == "agent_browser" && !saved.Settings.Browser {
				return nil, nil, fmt.Errorf("Use can_ask_person or browser to enable that capability explicitly.")
			}
			if enabled[name] == nil {
				return nil, nil, fmt.Errorf("Tool %q is unavailable to this automation.", requested)
			}
			if functionSelection {
				execute, ok := enabled[name].(func(context.Context, map[string]interface{}) (string, error))
				if !ok {
					return nil, nil, fmt.Errorf("Tool %q has no execution binding.", requested)
				}
				raw, err := json.Marshal(tool.Function.Parameters)
				if err != nil {
					return nil, nil, err
				}
				var schema map[string]interface{}
				if err := json.Unmarshal(raw, &schema); err != nil {
					return nil, nil, err
				}
				selection.Scripts = append(selection.Scripts, mcpagent.ToolDefinition{Name: name, Description: tool.Function.Description, InputSchema: schema, DisplayGroup: agentworksproduct.ExecutionFunctionTools().Category, Execute: execute})
			} else {
				saved.Config.EnabledCustomTools = append(saved.Config.EnabledCustomTools, requested)
			}
		}
	}
	for toolName, requested := range map[string]bool{"agent_browser": saved.Settings.Browser, "human_feedback": saved.Settings.CanAskPerson} {
		if !requested {
			continue
		}
		found := false
		for _, tool := range hcpo.WorkspaceTools {
			if tool.Function != nil && tool.Function.Name == toolName && hcpo.WorkspaceToolExecutors[toolName] != nil {
				found = true
				break
			}
		}
		if !found {
			if toolName == "agent_browser" {
				return nil, nil, fmt.Errorf("This automation's browser is not available. In its Crew chat, ask to choose and connect its browser, then retry.")
			}
			return nil, nil, fmt.Errorf("The requested %s tool is unavailable to this automation; connect it in the Crew or turn off that selection.", toolName)
		}
	}
	// A timer starts without a person message; an explicitly authored question
	// still uses the existing bounded human-feedback service, like a Crew turn.
	available := append([]string{}, hcpo.GetSelectedServers()...)
	if PlaceMCPServers != nil {
		names, _ := PlaceMCPServers(ctx, hcpo.GetWorkspacePath())
		available = append(available, names...)
	}
	for _, name := range selection.Servers {
		if name == mcpclient.NoServers || !slices.Contains(available, name) {
			return nil, nil, fmt.Errorf("MCP connection %q is unavailable to this automation. Choose one of its existing connections.", name)
		}
	}
	for _, name := range saved.Config.EnabledSkills {
		if _, err := skills.ValidateSkillName(name); err != nil {
			return nil, nil, fmt.Errorf("Skill %q is unavailable; choose an installed skill.", name)
		}
		if len(skills.LoadAttachableIn(getWorkspaceAPIURL(), hcpo.GetWorkspacePath(), []string{name})) != 1 {
			return nil, nil, fmt.Errorf("Skill %q is unavailable to this automation; install it or remove the selection.", name)
		}
	}
	for _, id := range saved.Settings.Scripts {
		child, _, err := hcpo.preparePythonScriptCall(ctx, parent, map[string]interface{}{"id": id, "inputs": map[string]interface{}{}})
		if err != nil {
			return nil, nil, err
		}
		props := map[string]interface{}{}
		required := []string{}
		for _, input := range child.Definition.Inputs {
			schema := map[string]interface{}{}
			for k, v := range input.Schema {
				schema[k] = v
			}
			kind := input.Type
			if kind == "" {
				kind = "string"
			}
			schema["type"] = kind
			schema["description"] = input.Description
			props[input.Name] = schema
			if input.Required && input.Value == "" {
				required = append(required, input.Name)
			}
		}
		toolName := "script_" + strings.ReplaceAll(child.Definition.ID, "-", "_")
		selection.Scripts = append(selection.Scripts, mcpagent.ToolDefinition{Name: toolName, Description: child.Definition.Name + ": " + child.Definition.Description, InputSchema: map[string]interface{}{"type": "object", "properties": props, "required": required, "additionalProperties": false}, Execute: func(callCtx context.Context, inputs map[string]interface{}) (string, error) {
			// Re-resolve the same owning-Crew resource for every tool invocation.
			arguments := map[string]interface{}{"id": child.Definition.ID, "inputs": inputs}
			current, stack, err := hcpo.preparePythonScriptCall(callCtx, parent, arguments)
			if err != nil {
				data, _ := json.Marshal(pythonHelperReply{Error: err.Error()})
				return string(data), nil
			}
			reply := hcpo.runPythonSavedScript(context.WithValue(callCtx, pythonScriptStackKey{}, stack), parent, current, arguments, "python-"+uuid.NewString())
			data, err := json.Marshal(reply)
			return string(data), err
		}})
	}
	return &saved, selection, nil
}

// Plain log scope is provider_transcript, not byte-complete OS stdout. Keep
// the existing normalized transcript/redaction contract and record every delta
// actually emitted by the provider alongside it, with original timestamps.
type pythonAgentLogObserver struct{ log *runlog.Log }

func (o *pythonAgentLogObserver) Name() string { return "python-provider-transcript" }
func (o *pythonAgentLogObserver) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	if event == nil {
		return nil
	}
	if chunk, ok := event.Data.(*events.StreamingChunkEvent); ok {
		data, _ := json.Marshal(map[string]interface{}{"source": chunk.Source, "delta": chunk.IsDelta, "text": redaction.Text(chunk.Content), "turn_id": event.TurnID, "index": chunk.ChunkIndex})
		o.log.WriteAt("provider_chunk", append(data, '\n'), event.Timestamp)
	} else if normalized, ok := transcriptevents.NormalizeBackgroundTranscriptEvent(event); ok {
		transcript := transcriptevents.BackgroundAgentTranscript{Events: []transcriptevents.BackgroundAgentTranscriptEvent{normalized}}
		transcript.RedactSensitiveText()
		data, _ := json.Marshal(transcript.Events[0])
		o.log.WriteAt(normalized.Type, append(data, '\n'), normalized.Timestamp)
	}
	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) runPythonAgent(ctx context.Context, parent PlanStepInterface, arguments map[string]interface{}, attempt string) (reply pythonHelperReply) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return pythonHelperReply{Error: err.Error()}
	}
	var args pythonAgentArguments
	if err = json.Unmarshal(raw, &args); err != nil {
		return pythonHelperReply{Error: "Invalid agent arguments: " + err.Error()}
	}
	saved, selection, err := hcpo.preparePythonAgent(ctx, parent, args)
	if err != nil {
		return pythonHelperReply{Error: err.Error()}
	}
	if args.TimeoutSeconds != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*args.TimeoutSeconds*float64(time.Second)))
		defer cancel()
	}
	log, err := hcpo.openPythonHelperLog(attempt + "-provider")
	if err != nil {
		return pythonHelperReply{Error: "Cannot prepare agent transcript.", LogError: err.Error()}
	}
	reply = pythonHelperReply{ConversationID: saved.StepID, LogFile: log.Path(), LogKind: "provider_transcript"}
	log.Note("provider_transcript: normalized messages/tool records and streamed chunks emitted by the provider, with existing redaction; not uncaptured OS stdout")
	defer func() { log.Close(); reply.LogComplete, reply.LogError = log.Error() == "", log.Error() }()
	step := &AgentPlanStep{Type: StepTypeAgent, CommonStepFields: CommonStepFields{ID: saved.StepID, Title: saved.Name, Description: saved.FirstMessage}, AgentConfigs: &saved.Config}
	runRoot := filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), "runs", hcpo.selectedRunFolder))
	read, write := hcpo.setupAgentFolderGuard(saved.StepID, saved.StepID, &saved.Config, AgentWriteAccess{DB: true})
	write = append(write, hcpo.GetWorkspacePath()+"/.sandbox-cache")
	// A native continuation retains its original private cwd and owner session.
	// Derive the runtime identity only from this validated, server-owned
	// conversation, never from a Python-supplied runtime/provider handle. Each
	// call closes its live runtime before returning; restore current guards and
	// resource selection when rebuilding the same conversation's next call.
	toolsSession := "python-runtime-" + saved.StepID
	hcpo.closeAgentRuntime(&agentSequenceSession{runtime: &agentSequenceRuntime{SessionID: toolsSession, Provider: saved.Config.ExecutionLLM.Provider, PreserveWorkspace: true}}, "rebuilding the saved Python agent conversation")
	mcpagent.ClearSessionsStopped([]string{toolsSession})
	agentCtx := context.WithValue(ctx, pythonAgentSelectionKey{}, selection)
	agentCtx = context.WithValue(agentCtx, agentSequenceFolderGuardOverrideKey{}, &agentSequenceFolderGuardOverride{ReadPaths: read, WritePaths: write, OutputPath: runRoot})
	agentCtx = context.WithValue(agentCtx, agentSequenceRuntimeSessionOverrideKey{}, &agentSequenceRuntimeSessionOverride{SessionID: toolsSession, KeepAlive: true})
	agentCtx, finish := hcpo.beginAgentExecution(agentCtx, step)
	var failure error
	defer func() { finish(reply.Text, failure) }()
	var runtime agents.OrchestratorAgent
	// Own cleanup before factory setup can allocate a tool session.
	cleanup := &agentSequenceSession{runtime: &agentSequenceRuntime{SessionID: toolsSession, PreserveWorkspace: true}}
	defer func() { hcpo.closeAgentRuntime(cleanup, "Python agent call finished") }()
	saved.Session.delegation = &agentSequenceDelegation{ReadPaths: read, WritePaths: write, CreateAgent: func(turnCtx context.Context) (agents.OrchestratorAgent, error) {
		made, err := hcpo.createExecutionOnlyAgent(turnCtx, "execution_only", saved.StepID, saved.Name, &saved.Config, step, saved.StepID, "")
		if err != nil {
			return nil, err
		}
		runtime = made
		cleanup.runtime.Agent = made
		cleanup.runtime.Provider = made.GetConfig().LLMConfig.Primary.Provider
		common.SetSessionWorkingDir(toolsSession, workflowAbsPath(runRoot))
		if err = made.GetBaseAgent().AddObserver(&pythonAgentLogObserver{log: log}); err != nil {
			return nil, err
		}
		if handle := saved.AgentSessionHandle; handle != nil && !handle.Empty() {
			if err = made.GetBaseAgent().Resume(turnCtx, handle); err != nil {
				return nil, err
			}
		}
		return made, nil
	}, TurnVars: func(item AgentItem, message string, opening bool) map[string]string {
		vars := hcpo.buildAgentTemplateVars(agentCtx, step, item, 0, saved.StepID, message, read, write, AgentWriteAccess{DB: true})
		vars["WorkspacePath"], vars["StepExecutionPath"] = workflowAbsPath(runRoot), workflowAbsPath(runRoot)
		vars["BaseDescription"] = message
		return vars
	}}
	path, _ := hcpo.pythonAgentConversationPath(saved.StepID)
	persist := func() error {
		saved.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		body, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return hcpo.WriteWorkspaceFile(writeCtx, path, string(body))
	}
	if err = persist(); err != nil {
		failure = err
		reply.Error = "Cannot save this agent conversation: " + err.Error()
		return reply
	}
	var finalAnswer string
	for _, message := range args.Messages {
		item := AgentItem{ID: fmt.Sprintf("turn-%d", saved.Session.ExecutionTurnCount+1), Type: "user_message", Message: message, WriteAccess: AgentWriteAccess{DB: true}}
		result, turnErr := hcpo.executeAgentUserMessage(agentCtx, step, item, 0, saved.StepID, &saved.Session)
		finalAnswer = redaction.Text(result)
		log.WriteAt("answer", []byte(finalAnswer+"\n"), time.Now().UTC())
		reply.Text = scriptOutputPreview(finalAnswer, log.Path())
		saved.AgentSessionHandle = currentAgentSessionHandle(runtime)
		if saveErr := persist(); saveErr != nil {
			failure = saveErr
			reply.Error = "The agent ran, but its continuation state could not be saved: " + saveErr.Error()
			return reply
		}
		if turnErr != nil {
			failure = turnErr
			reply.Error = scriptOutputPreview(redaction.Text(turnErr.Error()), log.Path())
			log.Note("agent call failed: " + redaction.Text(turnErr.Error()))
			return reply
		}
	}
	answerPath := filepath.ToSlash(filepath.Join("runs", hcpo.selectedRunFolder, "helpers", "answers", attempt+".txt"))
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := hcpo.WriteWorkspaceFile(writeCtx, answerPath, finalAnswer); err != nil {
		failure = err
		reply.Error = "The agent answered, but its full final answer could not be saved: " + redaction.Text(err.Error())
		return reply
	}
	reply.AnswerFile = workflowAbsPath(hcpo.GetWorkspacePath(), answerPath)
	reply.OK = true
	return reply
}

// Called by the existing run finalizer, never between helper calls or while
// waiting for capacity. Only these server-generated, run-owned sessions retire.
func (hcpo *StepBasedWorkflowOrchestrator) finishPythonAgentSessions(status string) {
	if status != "completed" && status != "failed" && status != "stopped" && status != "cancelled" {
		return
	}
	if hcpo.selectedRunFolder == "" {
		return
	}
	// Existing protected conversation records persist the server-generated
	// identity before launch. Read that list so terminal cleanup also works
	// after a controller/process restart; never scan unrelated /tmp homes.
	view := workflowfiles.WorkflowFS{Docs: GetPromptDocsRoot(), Root: hcpo.GetWorkspacePath()}
	dir := filepath.ToSlash(filepath.Join("runs", hcpo.selectedRunFolder, "helpers", "conversations"))
	entries, err := view.ReadDir(dir)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("Cannot read Python agent homes for terminal cleanup: %v", err))
		return
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if _, err := hcpo.pythonAgentConversationPath(id); err != nil {
			continue
		}
		raw, err := fs.ReadFile(view, dir+"/"+entry.Name())
		var saved pythonAgentConversation
		if err != nil || json.Unmarshal(raw, &saved) != nil || saved.StepID != id || saved.WorkspacePath != hcpo.GetWorkspacePath() || saved.RunFolder != hcpo.selectedRunFolder || saved.OwnerKind != "python_agent" {
			continue
		}
		mcpagent.CloseSession("python-runtime-" + id)
	}
}
