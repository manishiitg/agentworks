package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaypython"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	workflowtypes "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type pythonRelayModel struct {
	Provider     string                 `json:"provider"`
	ModelID      string                 `json:"model_id"`
	ConnectionID string                 `json:"connection_id"`
	Options      map[string]interface{} `json:"options"`
}

func pythonRelayCallModel(call relaypython.Call, caps WorkflowCapabilities) (pythonRelayModel, error) {
	var model pythonRelayModel
	if caps.LLMConfig != nil {
		builder := caps.LLMConfig.BuilderLLM
		if builder == nil {
			builder, _, _ = workflowtypes.ResolveProviderProfileConfig(caps.LLMConfig)
		}
		b, _ := json.Marshal(builder)
		_ = json.Unmarshal(b, &model)
	}
	if call.Model != nil {
		if text, ok := call.Model.(string); ok {
			provider, id, valid := strings.Cut(text, ":")
			if !valid {
				return model, fmt.Errorf("model must be provider:model_id or an object")
			}
			model = pythonRelayModel{Provider: provider, ModelID: id}
		} else {
			b, err := json.Marshal(call.Model)
			if err != nil {
				return model, err
			}
			if err = json.Unmarshal(b, &model); err != nil {
				return model, err
			}
		}
	}
	if model.Provider == "" {
		return model, fmt.Errorf("Choose a Relay builder model or pass model to call_agent")
	}
	if err := llmguard.RequireCodingAgentProvider(model.Provider); err != nil {
		return model, err
	}
	if model.ModelID == "" {
		model.ModelID = llm.GetDefaultModel(llm.Provider(model.Provider))
	}
	return model, nil
}

// The same mcpagent definition/session and CLI account admission used by the
// workflow agents, with no PlanStep, workflow defaults or continuation handle.
func (api *StreamingAPI) callPythonRelayAgent(ctx context.Context, sctx *ScheduleContext, parent, runPath string, call relaypython.Call, pythonTool relaypython.ToolCaller) (output interface{}, callErr error) {
	if call.Kind == "mcp" {
		return api.pythonRelayMCP(ctx, sctx, call.Server, call.Tool, call.Arguments)
	}
	if len(call.Messages) == 0 || len(call.Messages) > 100 || call.MaxTurns < 1 || call.MaxTurns > 100 {
		return nil, fmt.Errorf("call_agent needs 1-100 messages and max_turns 1-100")
	}
	model, err := pythonRelayCallModel(call, sctx.Capabilities)
	if err != nil {
		return nil, err
	}
	sessionID := "relay-agent-" + uuid.NewString()
	if policy := common.GetSessionShellConfig(parent); policy != nil {
		common.SetSessionWorkflowPath(sessionID, policy.WorkflowPath)
		common.SetSessionWorkingDir(sessionID, policy.WorkingDir)
		common.SetSessionFolderGuardBlockedPaths(sessionID, policy.BlockedPaths)
		common.SetSessionShellEnv(sessionID, policy.Env)
		common.SetSessionSandbox(sessionID, true, false)
	}
	defer common.ClearSessionShellConfig(sessionID)
	common.SetSessionFolderGuard(sessionID, []string{sctx.WorkspacePath}, []string{path.Join(runPath, "execution", call.ID)})
	if api.eventStore != nil {
		api.eventStore.SetSessionOwner(sessionID, sctx.OwnerUserID)
	}
	mcpclient.GetSessionRegistry().RegisterHTTPSession(parent, sessionID)
	defer mcpclient.GetSessionRegistry().CloseSession(sessionID)
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	ctx = context.WithValue(ctx, common.UserIDKey, sctx.OwnerUserID)
	ctx = executor.WithSessionID(ctx, sessionID)
	definition := mcpagent.AgentDefinition{Instructions: call.SystemPrompt, Tools: mcpagent.ToolSet{MCP: []mcpagent.MCPToolSource{{Name: mcpclient.NoServers}}}}
	for _, tool := range call.Tools {
		name := tool.Name
		schema, err := compilePythonRelaySchema(tool.Schema)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", name, err)
		}
		definition.Tools.Direct = append(definition.Tools.Direct, mcpagent.ToolDefinition{Name: name, Description: tool.Description, InputSchema: tool.Schema, Execute: func(toolCtx context.Context, args map[string]interface{}) (string, error) {
			if err := schema.Validate(args); err != nil {
				return "", fmt.Errorf("invalid tool arguments: %w", err)
			}
			return pythonTool(context.WithValue(toolCtx, common.ChatSessionIDKey, parent), name, args)
		}})
	}
	var receipts []map[string]interface{}
	var receiptsMu sync.Mutex
	defer func() {
		output = relaypython.AgentResult{Output: output, Tools: receipts, Provider: model.Provider, Model: model.ModelID}
	}()
	for _, source := range call.MCP {
		resolved, matched, err := api.resolvePlaceAttachedMCP(ctx, sessionID, source.Server)
		if !matched {
			resolved, err = api.resolveGovernedMCP(context.WithValue(ctx, placeScopedKey{}, true), sctx.OwnerUserID, source.Server)
		}
		if err != nil {
			return nil, err
		}
		inventory, err := api.discoverResolvedServerTools(ctx, resolved)
		if err != nil {
			return nil, err
		}
		admitted := map[string]bool{}
		for _, name := range source.Tools {
			admitted[name] = false
		}
		for _, tool := range inventory.Tools {
			if len(admitted) > 0 {
				if _, exists := admitted[tool.Name]; !exists {
					continue
				}
				admitted[tool.Name] = true
			}
			name := tool.Name
			server := source.Server
			definition.Tools.Direct = append(definition.Tools.Direct, mcpagent.ToolDefinition{Name: name, Description: tool.Description, InputSchema: tool.Parameters, Execute: func(toolCtx context.Context, args map[string]interface{}) (string, error) {
				result, err := api.callMCPTool(toolCtx, map[string]interface{}{"server": server, "tool": name, "arguments": args})
				receipt := map[string]interface{}{"name": name, "server": server, "args": args, "result": result}
				if err != nil {
					receipt["error"] = err.Error()
				}
				receiptsMu.Lock()
				receipts = append(receipts, receipt)
				receiptsMu.Unlock()
				return result, err
			}})
		}
		for name, found := range admitted {
			if !found {
				return nil, fmt.Errorf("MCP tool %s is unavailable on %s", name, source.Server)
			}
		}
	}
	definition.Skills = skills.LoadAttachableIn(getWorkspaceAPIURL(), sctx.WorkspacePath, call.Skills)
	if len(definition.Skills) != len(call.Skills) {
		return nil, fmt.Errorf("one or more Relay skills could not be loaded")
	}
	keys, err := api.resolveEffectiveAPIKeys(ctx, sctx.OwnerUserID, sctx.WorkspacePath, nil)
	if err != nil {
		return nil, err
	}
	provider := llm.Provider(model.Provider)
	initialized, err := llm.InitializeLLM(llmguard.WithServerAccountAdmission(llm.Config{Provider: provider, ModelID: model.ModelID, ConnectionID: model.ConnectionID, Logger: api.logger, Context: ctx, APIKeys: keys}))
	if err != nil {
		return nil, err
	}
	outputPath := path.Join(runPath, "execution", call.ID)
	client := workspace.NewClient(getWorkspaceAPIURL())
	client.UserID = sctx.OwnerUserID
	if err := client.CreateFolder(ctx, outputPath); err != nil {
		return nil, err
	}
	ledger := api.costLedger
	if ledger == nil {
		ledger = costledger.DefaultLedger()
	}
	workflowPath := sctx.WorkspacePath
	if policy := common.GetSessionShellConfig(parent); policy != nil {
		workflowPath = policy.WorkflowPath
	}
	observer := newCostObserver(ledger, sessionID, sctx.OwnerUserID, "relay",
		withCostModel(model.Provider, model.ModelID), withCostAccount(costAccountIDFor(model.Provider, model.ConnectionID)),
		withCostAttribution(costobserver.ScopeWorkflowExecution, workflowPath, path.Base(runPath), sessionID))
	runtime := mcpagent.RuntimeConfig{Model: initialized, MCPConfigPath: api.mcpConfigPath,
		Generation:    mcpagent.GenerationRuntimeConfig{Provider: provider, APIKeys: keys, MaxTurns: call.MaxTurns, LLM: mcpagent.AgentLLMConfiguration{Primary: mcpagent.LLMModel{Provider: model.Provider, ModelID: model.ModelID, ConnectionID: model.ConnectionID, Options: model.Options}}},
		Tools:         mcpagent.ToolRuntimeConfig{CodeExecution: true, Discovery: true},
		Coding:        mcpagent.CodingRuntimeConfig{AgentToolsMode: "mcp_only", BridgeToolAdmit: func(string) bool { return false }},
		MCP:           mcpagent.MCPRuntimeConfig{SessionID: sessionID, APIBaseURL: api.GetCodeExecAPIURL(), BridgeAPIBaseURL: api.GetAPIURL(), APIToken: common.BridgeTokenForSession(sessionID)},
		Workspace:     mcpagent.WorkspaceRuntimeConfig{CodingAgentWorkingDir: filepath.Join(fsutil.WorkspaceDocsRoot(), outputPath), IsolatedSession: true, ReadPaths: []string{sctx.WorkspacePath}, WritePaths: []string{outputPath}},
		Observability: mcpagent.ObservabilityRuntimeConfig{Logger: api.logger, Observers: []mcpagent.AgentEventListener{observer}, DirectToolExecutionEvents: true},
	}
	for _, tool := range definition.Tools.Direct {
		runtime.Tools.AdditionalBridge = append(runtime.Tools.AdditionalBridge, tool.Name)
	}
	agent, err := mcpagent.NewAgentFromDefinition(ctx, definition, runtime)
	if err != nil {
		return nil, err
	}
	defer agent.Close()
	session, err := agent.Start(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var response mcpagent.Result
	for _, message := range call.Messages {
		response, err = session.Run(ctx, mcpagent.Turn{Input: message})
		if err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.Text)), &output); err != nil {
		if call.OutputSchema != nil {
			return nil, fmt.Errorf("agent did not return JSON: %w", err)
		}
		return response.Text, nil
	}
	if call.OutputSchema != nil {
		schema, err := compilePythonRelaySchema(call.OutputSchema)
		if err != nil {
			return nil, err
		}
		if err = schema.Validate(output); err != nil {
			return nil, fmt.Errorf("agent output failed schema validation: %w", err)
		}
	}
	return output, nil
}

func compilePythonRelaySchema(value map[string]interface{}) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	// Disallow external schema references: user schemas cannot read host files or
	// fetch arbitrary URLs through validation. Local #/$defs references work.
	if hasExternalPythonRelayRef(value) {
		return nil, fmt.Errorf("only local schema references are supported")
	}
	if err := compiler.AddResource("https://relay.invalid/schema", value); err != nil {
		return nil, err
	}
	return compiler.Compile("https://relay.invalid/schema")
}
func hasExternalPythonRelayRef(value interface{}) bool {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, item := range v {
			if key == "$ref" || key == "$dynamicRef" || key == "$recursiveRef" {
				text, _ := item.(string)
				if !strings.HasPrefix(text, "#") {
					return true
				}
			}
			if hasExternalPythonRelayRef(item) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if hasExternalPythonRelayRef(item) {
				return true
			}
		}
	}
	return false
}
func (api *StreamingAPI) pythonRelayMCP(ctx context.Context, sctx *ScheduleContext, server, tool string, args map[string]interface{}) (interface{}, error) {
	result, err := api.callMCPTool(ctx, map[string]interface{}{"server": server, "tool": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	var value interface{}
	if json.Unmarshal([]byte(result), &value) == nil {
		return value, nil
	}
	return result, nil
}
