package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GetWorkspaceAdvancedToolCategory returns the category name for workspace advanced tools
func GetWorkspaceAdvancedToolCategory() string {
	return "workspace_advanced"
}

// CreateWorkspaceAdvancedTools returns the shared advanced workspace tools from the workspace package
func CreateWorkspaceAdvancedTools() []llmtypes.Tool {
	return workspace.GetAdvancedToolDefinitions()
}

// CreateWorkspaceAdvancedToolExecutors creates the execution functions for workspace advanced tools
// Uses the shared executors from pkg/workspace
// Includes FolderGuard to restrict LLM writes
// The read_image executor is wrapped with LLM analysis (config read from context at execution time)
func CreateWorkspaceAdvancedToolExecutors() map[string]func(ctx context.Context, args map[string]any) (string, error) {
	wsURL := getWorkspaceAPIURL()
	env := getMCPExtraEnv()
	client := workspace.NewClient(
		wsURL,
		workspace.WithFolderGuard(getDefaultFolderGuard()),
		workspace.WithExtraEnv(env),
	)
	log.Printf("[GLOBAL_CLIENT_DEBUG] Created global workspace client=%p (no session) MCP_API_URL=%s", client, env["MCP_API_URL"])
	executors := workspace.NewAdvancedExecutor(client)
	attachWorkspaceAdvancedLLMExecutors(executors, wsURL)
	return executors
}

// CreateWorkspaceAdvancedToolExecutorsWithUserID creates workspace advanced tool executors
// with an explicit user ID set on the client
// even if the context doesn't carry the user ID.
// The read_image executor is wrapped with LLM analysis (config read from context at execution time)
func CreateWorkspaceAdvancedToolExecutorsWithUserID(userID string) map[string]func(ctx context.Context, args map[string]any) (string, error) {
	wsURL := getWorkspaceAPIURL()
	client := workspace.NewClient(
		wsURL,
		workspace.WithFolderGuard(getDefaultFolderGuard()),
		workspace.WithUserID(userID),
		workspace.WithExtraEnv(getMCPExtraEnv()),
	)
	executors := workspace.NewAdvancedExecutor(client)
	attachWorkspaceAdvancedLLMExecutors(executors, wsURL)
	return executors
}

// CreateReadImageProviderTestExecutor creates a read_image executor for provider
// matrix tests. It reads image bytes through the workspace API, but deliberately
// bypasses workspace-backed image-analysis defaults so the caller's context-injected
// LLM config is the provider being tested.
func CreateReadImageProviderTestExecutor(workspaceURL, userID string) func(ctx context.Context, args map[string]any) (string, error) {
	if strings.TrimSpace(workspaceURL) == "" {
		workspaceURL = getWorkspaceAPIURL()
	}
	clientOptions := []workspace.ClientOption{
		workspace.WithFolderGuard(getDefaultFolderGuard()),
		workspace.WithExtraEnv(getMCPExtraEnv()),
	}
	if strings.TrimSpace(userID) != "" {
		clientOptions = append(clientOptions, workspace.WithUserID(userID))
	}
	client := workspace.NewClient(workspaceURL, clientOptions...)
	executors := workspace.NewAdvancedExecutor(client)
	baseExecutor := executors["read_image"]
	return wrapReadImageWithLLM(baseExecutor, "")
}

// CreateWorkspaceAdvancedToolExecutorsWithSession creates workspace advanced tool executors
// with an explicit user ID and session ID. The session ID is injected as MCP_SESSION_ID
// env var so that code execution mode HTTP tool calls can include it for connection reuse
// (e.g., sharing a stateful MCP connection across calls within a session).
// Returns (executors, envMap) — the envMap is the same map reference used by the workspace
// client, so callers can update MCP_API_URL/MCP_SESSION_ID dynamically when the session changes.
func CreateWorkspaceAdvancedToolExecutorsWithSession(userID, sessionID string) (map[string]func(ctx context.Context, args map[string]any) (string, error), map[string]string) {
	return CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv(userID, sessionID, nil)
}

// CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv creates workspace advanced tool executors
// with session support and additional environment variables (e.g., secrets).
// The extraEnvVars are injected into the shell environment alongside MCP_API_URL, MCP_API_TOKEN, etc.
// Returns (executors, envMap). The client keeps its OWN copy of envMap
// (workspace.WithExtraEnv clones), so in-place writes to the returned map do
// not reach the executors; use SetSessionShellEnv / DeleteSessionShellEnv,
// which update every live client registered for the session.
func CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv(userID, sessionID string, extraEnvVars map[string]string) (map[string]func(ctx context.Context, args map[string]any) (string, error), map[string]string) {
	wsURL := getWorkspaceAPIURL()
	env := getMCPExtraEnv(sessionID)
	// Merge additional env vars (secrets, etc.) — these don't override MCP vars
	for k, v := range extraEnvVars {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}
	client := workspace.NewClient(
		wsURL,
		workspace.WithFolderGuard(getDefaultFolderGuard()),
		workspace.WithUserID(userID),
		workspace.WithExtraEnv(env),
	)
	log.Printf("[SESSION_CLIENT_DEBUG] Created session-aware workspace client=%p sessionID=%s MCP_API_URL=%s", client, sessionID, env["MCP_API_URL"])
	registerSessionShellClient(sessionID, client)
	executors := workspace.NewAdvancedExecutor(client)
	attachWorkspaceAdvancedLLMExecutors(executors, wsURL)
	return executors, env
}

// Live shell clients per session, so a secret created mid-turn can be pushed
// into the shell env of the turn that is already running. Each turn creates a
// fresh client; only the most recent few per session are kept, which bounds
// memory without a session-end hook (older clients belong to finished turns).
const sessionShellClientsKept = 8

var sessionShellClients = struct {
	mu sync.Mutex
	m  map[string][]*workspace.Client
}{m: make(map[string][]*workspace.Client)}

var sessionShellSecretNames = struct {
	mu sync.Mutex
	m  map[string]map[string]struct{}
}{m: make(map[string]map[string]struct{})}

func registerSessionShellClient(sessionID string, client *workspace.Client) {
	if sessionID == "" || client == nil {
		return
	}
	sessionShellClients.mu.Lock()
	defer sessionShellClients.mu.Unlock()
	list := append(sessionShellClients.m[sessionID], client)
	if len(list) > sessionShellClientsKept {
		list = list[len(list)-sessionShellClientsKept:]
	}
	sessionShellClients.m[sessionID] = list
}

// SetSessionShellEnv sets one env var on every live shell client of a session
// and reports how many were updated. Used by the secret tools so a
// set_workflow_secret call is visible to execute_shell_command in the same
// turn instead of from the next message on.
func SetSessionShellEnv(sessionID, key, value string) int {
	if sessionID != "" && strings.HasPrefix(key, "SECRET_") {
		sessionShellSecretNames.mu.Lock()
		if sessionShellSecretNames.m[sessionID] == nil {
			sessionShellSecretNames.m[sessionID] = map[string]struct{}{}
		}
		sessionShellSecretNames.m[sessionID][key] = struct{}{}
		sessionShellSecretNames.mu.Unlock()
	}
	sessionShellClients.mu.Lock()
	clients := append([]*workspace.Client(nil), sessionShellClients.m[sessionID]...)
	sessionShellClients.mu.Unlock()
	for _, c := range clients {
		c.SetExtraEnv(key, value)
	}
	return len(clients)
}

// DeleteSessionShellEnv removes one env var from every live shell client of a session.
func DeleteSessionShellEnv(sessionID, key string) int {
	if sessionID != "" && strings.HasPrefix(key, "SECRET_") {
		sessionShellSecretNames.mu.Lock()
		delete(sessionShellSecretNames.m[sessionID], key)
		sessionShellSecretNames.mu.Unlock()
	}
	sessionShellClients.mu.Lock()
	clients := append([]*workspace.Client(nil), sessionShellClients.m[sessionID]...)
	sessionShellClients.mu.Unlock()
	for _, c := range clients {
		c.DeleteExtraEnv(key)
	}
	return len(clients)
}

// ReplaceSessionShellSecrets makes retained clients match the current durable
// attachment set. This removes a secret after it is unchecked in Setup instead
// of leaving the old value in a reused native coding session.
func ReplaceSessionShellSecrets(sessionID string, values map[string]string) int {
	if sessionID == "" {
		return 0
	}
	sessionShellSecretNames.mu.Lock()
	previous := sessionShellSecretNames.m[sessionID]
	next := make(map[string]struct{}, len(values))
	for key := range values {
		if strings.HasPrefix(key, "SECRET_") {
			next[key] = struct{}{}
		}
	}
	sessionShellSecretNames.m[sessionID] = next
	sessionShellSecretNames.mu.Unlock()

	sessionShellClients.mu.Lock()
	clients := append([]*workspace.Client(nil), sessionShellClients.m[sessionID]...)
	sessionShellClients.mu.Unlock()
	for _, c := range clients {
		for key := range previous {
			if _, keep := next[key]; !keep {
				c.DeleteExtraEnv(key)
			}
		}
		for key, value := range values {
			if strings.HasPrefix(key, "SECRET_") {
				c.SetExtraEnv(key, value)
			}
		}
	}
	return len(clients)
}

// CreateWorkspaceAdvancedToolExecutorsWithURL creates workspace advanced tool executors
// pointing to a custom workspace API URL.
func CreateWorkspaceAdvancedToolExecutorsWithURL(wsURL, userID, sessionID string) (map[string]func(ctx context.Context, args map[string]any) (string, error), map[string]string) {
	env := getMCPExtraEnv(sessionID)
	client := workspace.NewClient(
		wsURL,
		workspace.WithFolderGuard(getDefaultFolderGuard()),
		workspace.WithUserID(userID),
		workspace.WithExtraEnv(env),
	)
	executors := workspace.NewAdvancedExecutor(client)
	attachWorkspaceAdvancedLLMExecutors(executors, wsURL)
	return executors, env
}

func attachWorkspaceAdvancedLLMExecutors(executors map[string]func(ctx context.Context, args map[string]any) (string, error), workspaceURL string) {
	wrapReadImageExecutor(executors, workspaceURL)
	// This executor starts unbound. The server replaces it after resolving the
	// current workflow's manifest; generate_text_llm must never choose models
	// from a global workspace default.
	executors["generate_text_llm"] = createGenerateTextLLMExecutor(workspaceURL, nil)
}

// SetGenerateTextWorkflowTierConfig binds generate_text_llm exclusively to
// the current workflow's high/medium/low model configuration. A nil config is
// intentionally fail-closed for chats that are not attached to a workflow.
func SetGenerateTextWorkflowTierConfig(
	executors map[string]func(ctx context.Context, args map[string]any) (string, error),
	workspaceURL string,
	config *WorkflowLLMTierConfig,
	apiKeys ...*llm.ProviderAPIKeys,
) {
	if executors == nil {
		return
	}
	if config != nil && len(apiKeys) > 0 {
		copy := *config
		copy.APIKeys = apiKeys[0].Clone()
		config = &copy
	}
	executors["generate_text_llm"] = createGenerateTextLLMExecutor(workspaceURL, config)
}

// getMCPExtraEnv returns MCP-related env vars to inject into shell commands.
// These are set by server.go at startup for code execution mode.
// An optional sessionID can be passed to inject MCP_SESSION_ID for connection reuse.
func getMCPExtraEnv(sessionID ...string) map[string]string {
	env := make(map[string]string)
	baseURL := os.Getenv("MCP_API_URL")
	sid := ""
	if len(sessionID) > 0 {
		sid = sessionID[0]
	}
	if baseURL != "" {
		if sid != "" {
			// Embed session_id in the URL path: MCP_API_URL becomes {base}/s/{session_id}
			// The server registers session-scoped routes at /s/{session_id}/tools/...
			// so agent code calling {MCP_API_URL}/tools/mcp/{server}/{tool} automatically
			// includes the session_id without the agent needing to add it to the body.
			env["MCP_API_URL"] = baseURL + "/s/" + sid
		} else {
			env["MCP_API_URL"] = baseURL
		}
	}
	// With session tokens on, a shell gets only its own session's token
	// (PopulateMCPBridgeShortEnv below); a shell with no session gets none,
	// never the server's global token.
	if token := os.Getenv("MCP_API_TOKEN"); token != "" && !common.BridgeTokensEnabled() {
		env["MCP_API_TOKEN"] = token
	}
	if sid != "" {
		env["MCP_SESSION_ID"] = sid
	}
	common.PopulateMCPBridgeShortEnv(env)
	log.Printf("[MCP_ENV_DEBUG] getMCPExtraEnv: baseURL=%s sessionID=%s final_MCP_API_URL=%s", baseURL, sid, env["MCP_API_URL"])
	return env
}

type generateTextLLMResult struct {
	Tier     string `json:"tier"`
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
	Response string `json:"response"`
}

// WorkflowLLMTierConfig is the resolved high/medium/low model set from one
// workflow manifest. It is intentionally distinct from DelegationTierConfig,
// which belongs to the general multi-agent delegation feature.
type WorkflowLLMTierConfig struct {
	APIKeys *llm.ProviderAPIKeys
	High    *TierModel
	Medium  *TierModel
	Low     *TierModel
}

func createGenerateTextLLMExecutor(workspaceURL string, workflowTiers *WorkflowLLMTierConfig) func(ctx context.Context, args map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		userMessage := strings.TrimSpace(fmt.Sprintf("%v", args["user_message"]))
		if userMessage == "" {
			return "", fmt.Errorf("user_message is required")
		}

		tier := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", args["tier"])))
		if tier != "high" && tier != "medium" && tier != "low" {
			return "", fmt.Errorf("tier must be one of: high, medium, low")
		}

		tierModel, err := loadWorkflowTierModel(workflowTiers, tier)
		if err != nil {
			return "", err
		}

		llmModel, err := createLLMFromTierModel(ctx, tierModel, generateTextAPIKeys(ctx, workspaceURL, workflowTiers))
		if err != nil {
			return "", fmt.Errorf("failed to initialize LLM for tier %q: %w", tier, err)
		}

		resp, err := llmModel.GenerateContent(ctx, []llmtypes.MessageContent{
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: userMessage},
				},
			},
		}, structuredOneShotCallOptions(llm.Provider(tierModel.Provider))...)
		if err != nil {
			return "", fmt.Errorf("generate_text_llm failed for tier %q: %w", tier, err)
		}

		responseText := ""
		if len(resp.Choices) > 0 {
			responseText = strings.TrimSpace(resp.Choices[0].Content)
		}
		if responseText == "" {
			responseText = "(No response generated)"
		}

		payload, err := json.Marshal(generateTextLLMResult{
			Tier:     tier,
			Provider: tierModel.Provider,
			ModelID:  tierModel.ModelID,
			Response: responseText,
		})
		if err != nil {
			return "", fmt.Errorf("failed to marshal generate_text_llm result: %w", err)
		}

		return string(payload), nil
	}
}

// GenerateTextOneShot is an exported helper that mirrors generate_text_llm
// but is callable directly from Go code (not via the LLM tool surface).
//
// Pass tier "low", "medium", or "high"; system + user are the two messages.
// Returns the model's text response, trimmed.
func GenerateTextOneShot(ctx context.Context, workflowTiers *WorkflowLLMTierConfig, tier, systemMessage, userMessage string) (string, error) {
	if strings.TrimSpace(userMessage) == "" {
		return "", fmt.Errorf("user_message is required")
	}
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier != "high" && tier != "medium" && tier != "low" {
		return "", fmt.Errorf("tier must be one of: high, medium, low")
	}

	workspaceURL := getWorkspaceAPIURL()

	tierModel, err := loadWorkflowTierModel(workflowTiers, tier)
	if err != nil {
		return "", err
	}

	llmModel, err := createLLMFromTierModel(ctx, tierModel, generateTextAPIKeys(ctx, workspaceURL, workflowTiers))
	if err != nil {
		return "", fmt.Errorf("failed to initialize LLM for tier %q: %w", tier, err)
	}

	messages := make([]llmtypes.MessageContent, 0, 2)
	if strings.TrimSpace(systemMessage) != "" {
		messages = append(messages, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemMessage}},
		})
	}
	messages = append(messages, llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: userMessage}},
	})

	resp, err := llmModel.GenerateContent(ctx, messages, structuredOneShotCallOptions(llm.Provider(tierModel.Provider))...)
	if err != nil {
		return "", fmt.Errorf("GenerateTextOneShot failed for tier %q: %w", tier, err)
	}

	if len(resp.Choices) > 0 {
		return strings.TrimSpace(resp.Choices[0].Content), nil
	}
	return "", nil
}

func loadWorkflowTierModel(cfg *WorkflowLLMTierConfig, tier string) (*TierModel, error) {
	if cfg == nil {
		return nil, fmt.Errorf("generate_text_llm requires a current workflow with capabilities.llm_config")
	}
	var model *TierModel
	switch tier {
	case "high":
		model = cfg.High
	case "medium":
		model = cfg.Medium
	case "low":
		model = cfg.Low
	}

	if model == nil || strings.TrimSpace(model.Provider) == "" || strings.TrimSpace(model.ModelID) == "" {
		return nil, fmt.Errorf("tier %q is not configured in the current workflow's capabilities.llm_config", tier)
	}

	return sanitizeTierModelLocal(model), nil
}

func sanitizeTierModelLocal(model *TierModel) *TierModel {
	if model == nil {
		return nil
	}

	provider := strings.TrimSpace(model.Provider)
	modelID := strings.TrimSpace(model.ModelID)
	if provider == "" || modelID == "" {
		return nil
	}

	sanitized := &TierModel{
		Provider:     provider,
		ModelID:      modelID,
		Options:      model.Options,
		ConnectionID: model.ConnectionID,
	}

	return sanitized
}

func loadWorkspaceProviderAPIKeys(ctx context.Context, workspaceURL string) *llm.ProviderAPIKeys {
	if workspaceURL == "" {
		return nil
	}

	rawKeys, exists, err := services.LoadProviderKeys(ctx, workspaceURL)
	if err != nil {
		log.Printf("[GENERATE_TEXT_LLM] Failed to load provider keys from workspace: %v", err)
		return nil
	}
	if !exists || len(rawKeys) == 0 {
		return nil
	}

	// Top-level upstream keys (openai, anthropic, vertex, ...) are Pi CLI's
	// provider credentials; nothing calls those APIs directly.
	keys := &llm.ProviderAPIKeys{}
	if value, ok := rawKeys["openai"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.OpenAI = &v
	}
	if value, ok := rawKeys["anthropic"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.Anthropic = &v
	}
	if value, ok := rawKeys["z-ai"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.ZAI = &v
	}
	if value, ok := rawKeys["kimi"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.Kimi = &v
	}
	if value, ok := rawKeys["vertex"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.Vertex = &v
	}
	if value, ok := rawKeys["codex_cli"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.CodexCLI = &v
	}
	if value, ok := rawKeys["cursor_cli"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.CursorCLI = &v
	}
	if value, ok := rawKeys["pi_cli"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.PiCLI = &v
	}
	if value, ok := rawKeys["minimax"].(string); ok && strings.TrimSpace(value) != "" {
		v := value
		keys.MiniMax = &v
	}
	if value, ok := rawKeys["pi_provider_keys"].(map[string]string); ok && len(value) > 0 {
		keys.PiProviderKeys = map[string]string{}
		for provider, key := range value {
			provider = strings.ToLower(strings.TrimSpace(provider))
			key = strings.TrimSpace(key)
			if provider != "" && key != "" {
				keys.PiProviderKeys[provider] = key
			}
		}
	} else if value, ok := rawKeys["pi_provider_keys"].(map[string]interface{}); ok && len(value) > 0 {
		keys.PiProviderKeys = map[string]string{}
		for provider, rawKey := range value {
			key, ok := rawKey.(string)
			if !ok {
				continue
			}
			provider = strings.ToLower(strings.TrimSpace(provider))
			key = strings.TrimSpace(key)
			if provider != "" && key != "" {
				keys.PiProviderKeys[provider] = key
			}
		}
		if len(keys.PiProviderKeys) == 0 {
			keys.PiProviderKeys = nil
		}
	}
	return keys
}

func createLLMFromTierModel(ctx context.Context, model *TierModel, apiKeys *llm.ProviderAPIKeys) (llmtypes.Model, error) {
	if err := llmguard.RequireCodingAgentProvider(model.Provider); err != nil {
		return nil, err
	}
	apiKeys = keysWithTurnResolver(ctx, apiKeys)
	llmCfg := llm.Config{
		Provider:     llm.Provider(model.Provider),
		ConnectionID: model.ConnectionID,
		ModelID:      model.ModelID,
		Context:      ctx,
		APIKeys:      apiKeys,
		MaxRetries:   3,
	}

	return llm.InitializeLLM(llmguard.WithServerAccountAdmission(llmCfg))
}

// structuredOneShotCallOptions keeps generate_text_llm out of every coding
// CLI's interactive/tmux path. The tool is a bounded one-shot operation, so a
// provider-native structured stream is the only valid CLI transport.
func structuredOneShotCallOptions(provider llm.Provider) []llmtypes.CallOption {
	options := make([]llmtypes.CallOption, 0, 2)
	switch provider {
	case llm.ProviderClaudeCode:
		options = append(options, llmproviders.WithClaudeStructuredTransport(true))
	case llm.ProviderCodexCLI:
		options = append(options, llmproviders.WithCodexStructuredTransport(true))
	case llm.ProviderCursorCLI:
		options = append(options, llmproviders.WithCursorStructuredTransport(true))
	case llm.ProviderPiCLI:
		options = append(options, llmproviders.WithPiStructuredTransport(true))
	default:
		return nil
	}

	// Use the stable workspace root rather than the release directory as the
	// CLI cwd. No interactive session ID, persistence, or resume option is added.
	if workingDir := llmproviders.CodingAgentWorkingDirOption(llmproviders.Provider(provider), fsutil.WorkspaceDocsRoot()); workingDir != nil {
		options = append(options, workingDir)
	}
	return options
}

// wrapReadImageExecutor wraps the read_image executor in the map with LLM analysis.
// The LLM config is read from context at execution time (injected by conversation.go).
func wrapReadImageExecutor(executors map[string]func(ctx context.Context, args map[string]any) (string, error), workspaceURL string) {
	if baseExecutor, exists := executors["read_image"]; exists {
		executors["read_image"] = wrapReadImageWithLLM(baseExecutor, workspaceURL)
		log.Printf("[READ_IMAGE_DEBUG] read_image executor wrapped with workspace-configurable LLM analysis")
	}
}

// SetReadImageLLMConfig re-wraps the read_image executor so that when the
// context doesn't carry ToolExecutionLLMConfigKey (e.g. HTTP calls from claude CLI),
// the provided selected config is injected before the inner executor runs.
// Call this after both CreateWorkspaceAdvancedToolExecutors* AND the agent have been
// created, so the real LLM config is known.
func SetReadImageLLMConfig(
	executors map[string]func(ctx context.Context, args map[string]any) (string, error),
	selected mcpagent.LLMModel,
	keys *llm.ProviderAPIKeys,
) {
	if existing, ok := executors["read_image"]; ok {
		executors["read_image"] = injectSelectedLLMConfig(existing, selected, keys)
		log.Printf("[READ_IMAGE_DEBUG] read_image executor wrapped with LLM selected (provider=%s, model=%s)",
			selected.Provider, selected.ModelID)
	}
}

func stringFromMap(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

// injectSelectedLLMConfig wraps an executor: if the context has no ToolExecutionLLMConfigKey,
// the selected config is injected before calling the inner executor. keys are the turn's provider accounts:
// a call that arrives over the CLI's tool bridge is a plain HTTP request and carries none, and without them the
// analysis starts the CLI under the server's own account (no login, "Select login method") instead of the
// account the session runs on. They are attached when the context has none.
func injectSelectedLLMConfig(
	inner func(ctx context.Context, args map[string]any) (string, error),
	selected mcpagent.LLMModel,
	keys *llm.ProviderAPIKeys,
) func(ctx context.Context, args map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if ctx.Value(mcpagent.ToolExecutionLLMConfigKey) == nil {
			log.Printf("[READ_IMAGE_DEBUG] No LLM config in context, injecting selected (provider=%s, model=%s)",
				selected.Provider, selected.ModelID)
			ctx = context.WithValue(ctx, mcpagent.ToolExecutionLLMConfigKey, selected)
		}
		if keys != nil && ProviderAccountKeysFromContext(ctx) == nil {
			ctx = WithProviderAccountKeys(ctx, keys)
		}
		return inner(ctx, args)
	}
}

// wrapReadImageWithLLM wraps the base read_image executor (which returns base64 data)
// with a dedicated LLM call that analyzes the image and returns a text response.
// The LLM config (provider, model, API key) is read from context at execution time.
func wrapReadImageWithLLM(
	baseExecutor func(ctx context.Context, args map[string]any) (string, error),
	workspaceURL string,
) func(ctx context.Context, args map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		log.Printf("[READ_IMAGE_DEBUG] Wrapped read_image executor called")

		// Step 1: Call the base executor to get base64 image data from workspace
		rawResult, err := baseExecutor(ctx, args)
		if err != nil {
			log.Printf("[READ_IMAGE_DEBUG] Base executor failed: %v", err)
			return "", err
		}

		// Step 2: Parse the structured result from workspace
		var imageData workspace.ReadImageResult
		if err := json.Unmarshal([]byte(rawResult), &imageData); err != nil {
			log.Printf("[READ_IMAGE_DEBUG] Failed to parse base result as ReadImageResult: %v", err)
			return "", fmt.Errorf("failed to parse image data: %w", err)
		}

		log.Printf("[READ_IMAGE_DEBUG] Image data received: filepath=%s, mimeType=%s, base64Length=%d",
			imageData.Filepath, imageData.MimeType, len(imageData.Data))

		// Step 3: Resolve the analysis LLM from explicit provider/model args,
		// workspace config, or finally the current agent model.
		requestedProvider := strings.TrimSpace(imageData.Provider)
		if requestedProvider == "" {
			requestedProvider = stringFromMap(args, "provider")
		}
		requestedModelID := strings.TrimSpace(imageData.ModelID)
		if requestedModelID == "" {
			requestedModelID = stringFromMap(args, "model_id")
		}
		llmModel, provider, modelID, err := createImageAnalysisLLM(ctx, workspaceURL, requestedProvider, requestedModelID)
		if err != nil {
			log.Printf("[READ_IMAGE_DEBUG] Failed to create LLM client: %v", err)
			return "", fmt.Errorf("failed to initialize LLM for image analysis: %w", err)
		}

		log.Printf("[READ_IMAGE_DEBUG] LLM client created (provider=%s, model=%s), making GenerateContent call",
			provider, modelID)

		// Step 4: Make the LLM call with the image + query.
		// CLI providers can inspect local files when given the workspace path
		// directly. Their adapters do not consume base64 ImageContent through
		// these transports today, so keep CLI image analysis path-based.
		parts := []llmtypes.ContentPart{
			llmtypes.TextContent{Text: imageData.Query},
			llmtypes.ImageContent{
				SourceType: "base64",
				MediaType:  imageData.MimeType,
				Data:       imageData.Data,
			},
		}
		if pathBasedImageAnalysisProvider(provider) {
			absoluteImagePath := workspaceAbsolutePath(normalizeWorkspaceDocumentPath(imageData.Filepath))
			if _, statErr := os.Stat(absoluteImagePath); statErr != nil {
				return "", fmt.Errorf("%s image analysis requires a readable local workspace file at %q: %w", provider, absoluteImagePath, statErr)
			}
			parts = []llmtypes.ContentPart{
				llmtypes.TextContent{Text: fmt.Sprintf("Inspect the local image file at this workspace path:\n%s\n\nQuestion: %s", absoluteImagePath, imageData.Query)},
			}
		}

		messages := []llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: parts,
			},
		}

		// A coding-agent provider starts a fresh CLI/tmux runtime for this
		// analysis. Give it the stable workspace-documents root explicitly rather
		// than inheriting the server's release directory, which changes at every
		// deploy and can trigger Claude Code's trust/onboarding prompt. The adapter
		// pre-trusts this server-owned directory before launch; the prompt handler
		// remains a fallback for future Claude UI changes.
		callOptions := []llmtypes.CallOption{}
		if workingDirOption := llmproviders.CodingAgentWorkingDirOption(llmproviders.Provider(provider), fsutil.WorkspaceDocsRoot()); workingDirOption != nil {
			callOptions = append(callOptions, workingDirOption)
		}
		if strings.EqualFold(provider, string(llmproviders.ProviderClaudeCode)) {
			// The analysis prompt is passed directly, so do not transiently project
			// it as CLAUDE.md into the shared docs root.
			callOptions = append(callOptions, llmproviders.WithClaudeCodeWriteProjectInstructionFile(false))
		}

		resp, err := llmModel.GenerateContent(ctx, messages, callOptions...)
		if err != nil {
			log.Printf("[READ_IMAGE_DEBUG] LLM GenerateContent failed: %v", err)
			return "", fmt.Errorf("LLM image analysis failed: %w", err)
		}

		// Step 5: Extract and return the text response
		var responseText string
		if len(resp.Choices) > 0 {
			responseText = resp.Choices[0].Content
		}
		if responseText == "" {
			responseText = "(No response from image analysis)"
		}

		log.Printf("[READ_IMAGE_DEBUG] LLM response received: %d chars", len(responseText))

		// Cap response size
		const maxResponseSize = 100 * 1024
		if len(responseText) > maxResponseSize {
			responseText = responseText[:maxResponseSize] + "\n... (response truncated)"
			log.Printf("[READ_IMAGE_DEBUG] Response truncated to %d chars", maxResponseSize)
		}

		// Return final JSON result
		response := map[string]any{
			"filepath": imageData.Filepath,
			"query":    imageData.Query,
			"provider": provider,
			"model":    modelID,
			"response": responseText,
		}

		responseJSON, err := json.Marshal(response)
		if err != nil {
			return "", fmt.Errorf("failed to marshal response: %w", err)
		}

		log.Printf("[READ_IMAGE_DEBUG] read_image complete, returning LLM analysis result")
		return string(responseJSON), nil
	}
}

func createImageAnalysisLLM(ctx context.Context, workspaceURL, requestedProvider, requestedModelID string) (llmtypes.Model, string, string, error) {
	apiKeys := loadWorkspaceProviderAPIKeys(ctx, workspaceURL)
	// Configured image models run on the server account: admit it for this
	// turn's scope first.
	admitServer := func(provider string) error { return admitServerAccountFromContext(ctx, provider) }
	requestedProvider = strings.TrimSpace(requestedProvider)
	requestedModelID = strings.TrimSpace(requestedModelID)
	if requestedProvider == "<nil>" {
		requestedProvider = ""
	}
	if requestedModelID == "<nil>" {
		requestedModelID = ""
	}

	if requestedProvider != "" || requestedModelID != "" {
		provider, modelID, err := normalizeImageAnalysisProviderAndModel(requestedProvider, requestedModelID)
		if err != nil {
			return nil, "", "", err
		}
		apiKeysWithEnv := imageAnalysisAPIKeysWithEnv(apiKeys)
		if !hasWorkspaceDefaultImageAnalysisAuth(provider, apiKeysWithEnv) {
			return nil, "", "", fmt.Errorf("read_image requires auth/runtime for requested provider/model %s/%s. Use list_llm_capabilities(capability=\"read_image\", include_models=true) to choose a usable provider/model pair", provider, modelID)
		}
		if err := admitServer(provider); err != nil {
			return nil, "", "", err
		}
		model, err := llm.InitializeLLM(llm.Config{
			Provider: llm.Provider(provider),
			ModelID:  modelID,
			Context:  ctx,
			APIKeys:  apiKeysWithEnv,
		})
		if err != nil {
			return nil, "", "", err
		}
		return model, provider, modelID, nil
	}

	if workspaceURL != "" {
		imageCfg, exists, err := services.LoadImageAnalysisConfig(ctx, workspaceURL)
		if err != nil {
			log.Printf("[READ_IMAGE_DEBUG] Failed to load image analysis config: %v", err)
		} else if exists && imageCfg != nil {
			var candidates []services.ImageGenerationModelConfig
			if imageCfg.Primary != nil {
				candidates = append(candidates, *imageCfg.Primary)
			}

			for _, candidate := range candidates {
				provider, modelID, err := normalizeImageAnalysisProviderAndModel(candidate.Provider, candidate.ModelID)
				if err != nil {
					continue
				}
				if !hasImageAnalysisProviderAuth(provider, apiKeys) {
					continue
				}
				if err := admitServer(provider); err != nil {
					log.Printf("[READ_IMAGE_DEBUG] %s server account not admitted: %v", provider, err)
					continue
				}
				model, err := llm.InitializeLLM(llm.Config{
					Provider: llm.Provider(provider),
					ModelID:  modelID,
					Context:  ctx,
					APIKeys:  apiKeys,
				})
				if err == nil {
					return model, provider, modelID, nil
				}
				log.Printf("[READ_IMAGE_DEBUG] Failed to initialize configured image analysis model %s/%s: %v", provider, modelID, err)
			}
			return nil, "", "", fmt.Errorf("image analysis config requires a valid configured provider/model with matching auth")
		}

	}

	llmConfigRaw := ctx.Value(mcpagent.ToolExecutionLLMConfigKey)
	if llmConfigRaw == nil {
		log.Printf("[READ_IMAGE_DEBUG] No LLM config in context — cannot perform image analysis fallback")
		return nil, "", "", fmt.Errorf("LLM configuration not available in context for image analysis")
	}
	llmConfig, ok := llmConfigRaw.(mcpagent.LLMModel)
	if !ok {
		log.Printf("[READ_IMAGE_DEBUG] LLM config in context has unexpected type: %T", llmConfigRaw)
		return nil, "", "", fmt.Errorf("LLM configuration in context has unexpected type")
	}

	model, err := createLLMFromConfig(ctx, llmConfig)
	if err != nil {
		return nil, "", "", err
	}
	return model, llmConfig.Provider, llmConfig.ModelID, nil
}

func imageAnalysisAPIKeysWithEnv(apiKeys *llm.ProviderAPIKeys) *llm.ProviderAPIKeys {
	merged := &llm.ProviderAPIKeys{}
	if apiKeys != nil {
		*merged = *apiKeys
	}

	// Claude Code's subscription credential is intentionally distinct from an
	// Anthropic API key. The rootless deployment supplies it as a service-scoped
	// environment variable; pass it explicitly to a child CLI runtime rather
	// than relying on its ambient saved-login state.
	if merged.ClaudeCodeOAuthToken == nil {
		if value := firstNonEmptyEnv("CLAUDE_CODE_OAUTH_TOKEN"); value != "" {
			merged.ClaudeCodeOAuthToken = &value
		}
	}
	return merged
}

func hasWorkspaceDefaultImageAnalysisAuth(provider string, apiKeys *llm.ProviderAPIKeys) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case string(llm.ProviderClaudeCode):
		if apiKeys == nil || apiKeys.ClaudeCodeOAuthToken == nil || strings.TrimSpace(*apiKeys.ClaudeCodeOAuthToken) == "" {
			return false
		}
		_, err := exec.LookPath("claude")
		return err == nil
	case string(llm.ProviderCodexCLI):
		_, err := exec.LookPath("codex")
		return err == nil
	case string(llm.ProviderCursorCLI):
		_, err := exec.LookPath("cursor-agent")
		return err == nil
	}
	return false
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

// createLLMFromConfig creates an LLM model instance using multi-llm-provider-go
// from the agent's LLMModel config (extracted from context).
func createLLMFromConfig(ctx context.Context, config mcpagent.LLMModel) (llmtypes.Model, error) {
	if err := llmguard.RequireCodingAgentProvider(config.Provider); err != nil {
		return nil, err
	}
	var apiKeys *llm.ProviderAPIKeys
	if config.APIKey != nil {
		apiKeys = &llm.ProviderAPIKeys{}
		switch llm.Provider(config.Provider) {
		case llm.ProviderCodexCLI:
			apiKeys.CodexCLI = config.APIKey
		case llm.ProviderCursorCLI:
			apiKeys.CursorCLI = config.APIKey
		case llm.ProviderPiCLI:
			apiKeys.PiCLI = config.APIKey
		}
	}

	// The agent's own model: same account, same scope as its turn.
	connectionID := ""
	if turnKeys := ProviderAccountKeysFromContext(ctx); turnKeys != nil {
		keys := turnKeys.Clone()
		if apiKeys != nil {
			keys.CodexCLI, keys.CursorCLI, keys.PiCLI = firstKey(apiKeys.CodexCLI, keys.CodexCLI), firstKey(apiKeys.CursorCLI, keys.CursorCLI), firstKey(apiKeys.PiCLI, keys.PiCLI)
		}
		apiKeys = keys
		connectionID = config.ConnectionID
	}
	llmCfg := llm.Config{
		Provider:     llm.Provider(config.Provider),
		ConnectionID: connectionID,
		ModelID:      config.ModelID,
		Context:      ctx,
		APIKeys:      apiKeys,
	}

	return llm.InitializeLLM(llmguard.WithServerAccountAdmission(llmCfg))
}

func generateTextAPIKeys(ctx context.Context, workspaceURL string, tiers *WorkflowLLMTierConfig) *llm.ProviderAPIKeys {
	if tiers != nil && tiers.APIKeys != nil {
		return tiers.APIKeys.Clone()
	}
	return loadWorkspaceProviderAPIKeys(ctx, workspaceURL)
}

type providerAccountKeysContextKey struct{}

// WithProviderAccountKeys carries a turn's provider keys, including the
// server's account resolver for that turn's scope, to the tools the turn
// runs.
func WithProviderAccountKeys(ctx context.Context, keys *llm.ProviderAPIKeys) context.Context {
	if keys == nil {
		return ctx
	}
	return context.WithValue(ctx, providerAccountKeysContextKey{}, keys)
}

// ProviderAccountKeysFromContext returns the turn's keys, if any.
func ProviderAccountKeysFromContext(ctx context.Context) *llm.ProviderAPIKeys {
	if ctx == nil {
		return nil
	}
	keys, _ := ctx.Value(providerAccountKeysContextKey{}).(*llm.ProviderAPIKeys)
	return keys
}

// keysWithTurnResolver makes sure keys that name accounts can be resolved:
// keys without a resolver take the turn's.
func keysWithTurnResolver(ctx context.Context, keys *llm.ProviderAPIKeys) *llm.ProviderAPIKeys {
	if keys != nil && keys.ResolveConnection != nil {
		return keys
	}
	turnKeys := ProviderAccountKeysFromContext(ctx)
	if turnKeys == nil || turnKeys.ResolveConnection == nil {
		return keys
	}
	if keys == nil {
		return turnKeys.Clone()
	}
	withResolver := keys.Clone()
	withResolver.ResolveConnection = turnKeys.ResolveConnection
	return withResolver
}

// admitServerAccountFromContext runs the server-account admission of the
// turn's scope for provider, without changing which keys are used.
func admitServerAccountFromContext(ctx context.Context, provider string) error {
	turnKeys := ProviderAccountKeysFromContext(ctx)
	if turnKeys == nil || turnKeys.ResolveConnection == nil {
		return nil
	}
	_, err := turnKeys.ResolveConnection(ctx, llm.Provider(provider), llmguard.ServerDefaultConnectionPrefix+provider)
	return err
}

func firstKey(values ...*string) *string {
	for _, value := range values {
		if value != nil && *value != "" {
			return value
		}
	}
	return nil
}
