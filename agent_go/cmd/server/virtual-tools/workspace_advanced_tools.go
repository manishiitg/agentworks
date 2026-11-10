package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
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
// workflow manifest.
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
		// Cursor uses bounded ACP when no persistent owner is requested.
	case llm.ProviderPiCLI:
		// Pi uses bounded RPC when no persistent owner is requested.
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
