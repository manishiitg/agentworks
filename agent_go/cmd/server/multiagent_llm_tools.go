package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

type cliAuthProbeCache struct {
	sync.Mutex
	checkedAt     time.Time
	authenticated bool
	conclusive    bool
	refreshing    bool
}

var (
	claudeCLIAuthProbeCache cliAuthProbeCache
	codexCLIAuthProbeCache  cliAuthProbeCache
	cursorCLIAuthProbeCache cliAuthProbeCache
	agyCLIAuthProbeCache    cliAuthProbeCache
)

var claudeCLIAuthStatusCommand = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "claude", "auth", "status").CombinedOutput()
}

var codexCLIAuthStatusCommand = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "codex", "login", "status").CombinedOutput()
}

var cursorCLIStatusJSON = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "cursor-agent", "status", "--format", "json").Output()
}

// agyCLIModelsCommand lists models without starting a model turn, so it is
// cheap enough to poll from the manifest. It is auth-gated but not
// quota-gated: a quota-exhausted login still lists models.
var agyCLIModelsCommand = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "agy", "models").CombinedOutput()
}

func cursorCLILoginRequiredMessage() string {
	return "Cursor Agent CLI is installed but not logged in. Run `cursor-agent login` in a terminal, or set CURSOR_API_KEY, then try again."
}

// listProviderModelsJSON returns a JSON string of all frontend-visible models for the given
// provider. Dynamic providers use the same live/fallback model source as the frontend picker;
// fixed providers use the shared model metadata catalog.
func listProviderModelsJSON(provider string) string {
	provider = normalizeManagedProvider(provider)
	if !isPublishedLLMProviderAllowed(provider) {
		return prettyJSON(map[string]interface{}{
			"provider": provider,
			"count":    0,
			"models":   []interface{}{},
			"note":     "This provider is not available as a published chat LLM provider.",
		})
	}

	if providerModelSelectionMode(provider) == "dynamic" {
		resp := getDynamicModels(provider, true, false)
		payload := map[string]interface{}{
			"provider":              resp.Provider,
			"model_selection_mode":  resp.ModelSelectionMode,
			"models":                resp.Models,
			"groups":                resp.Groups,
			"supports_custom_model": resp.SupportsCustom,
			"custom_model_hint":     resp.CustomModelHint,
			"source":                resp.Source,
			"cached_at":             resp.CachedAt,
			"cache_ttl_seconds":     resp.CacheTTLSeconds,
		}
		if defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(provider)); ok {
			payload["default_tier_models"] = defaults
		}
		return prettyJSON(payload)
	}

	allModels := allProviderModelMetadata()
	filtered := make([]interface{}, 0, len(allModels))
	for _, m := range allModels {
		if m == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(m.Provider)) != provider {
			continue
		}
		filtered = append(filtered, m)
	}
	payload := map[string]interface{}{
		"provider":             provider,
		"model_selection_mode": "fixed_tier",
		"count":                len(filtered),
		"models":               filtered,
		"source":               "/api/llm-config/models/metadata",
		"default_model":        llm.GetDefaultModel(llm.Provider(provider)),
	}
	if defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(provider)); ok {
		payload["default_tier_models"] = defaults
	}
	return prettyJSON(payload)
}

func prettyJSON(v interface{}) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func normalizeManagedProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func cloneOptionsMap(options map[string]interface{}) map[string]interface{} {
	if len(options) == 0 {
		return nil
	}

	cloned := make(map[string]interface{}, len(options))
	for k, v := range options {
		cloned[k] = v
	}
	return cloned
}

func llmToolOptionsSchema(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": description,
		"properties": map[string]interface{}{
			"reasoning_effort": map[string]interface{}{
				"type":        "string",
				"description": "Reasoning/effort level for providers that support it. For Codex CLI this becomes model_reasoning_effort; for Claude Code this becomes --effort. Prefer values from list_provider_models.reasoning_effort_levels for the selected model.",
				"enum":        []string{"none", "minimal", "low", "medium", "high", "max", "xhigh"},
			},
			"verbosity": map[string]interface{}{
				"type":        "string",
				"description": "Response verbosity for providers that support it.",
				"enum":        []string{"low", "medium", "high"},
			},
			"thinking_level": map[string]interface{}{
				"type":        "string",
				"description": "Thinking level for providers that support a named thinking setting.",
				"enum":        []string{"low", "medium", "high"},
			},
			"thinking_budget": map[string]interface{}{
				"type":        "integer",
				"description": "Thinking budget in tokens for providers that support token-budgeted thinking.",
			},
			"top_p": map[string]interface{}{
				"type":        "number",
				"description": "Optional nucleus sampling value for providers that support it.",
			},
			"top_k": map[string]interface{}{
				"type":        "integer",
				"description": "Optional top-k sampling value for providers that support it.",
			},
			"stop_sequences": map[string]interface{}{
				"type":        "array",
				"description": "Optional stop sequences for providers that support them.",
				"items":       map[string]interface{}{"type": "string"},
			},
		},
		"additionalProperties": true,
	}
}

func getStoredProviderAPIKey(keys *StoredProviderKeys, provider string) string {
	if keys == nil {
		return ""
	}

	switch normalizeManagedProvider(provider) {
	case "codex-cli":
		return strings.TrimSpace(keys.CodexCLI)
	case "cursor-cli":
		return strings.TrimSpace(keys.CursorCLI)
	case "pi-cli":
		return strings.TrimSpace(keys.PiCLI)
	default:
		return ""
	}
}

func setStoredProviderAPIKey(keys *StoredProviderKeys, provider, apiKey string) bool {
	if keys == nil {
		return false
	}

	value := strings.TrimSpace(apiKey)
	switch normalizeManagedProvider(provider) {
	case "codex-cli":
		keys.CodexCLI = value
	case "cursor-cli":
		keys.CursorCLI = value
	case "pi-cli":
		keys.PiCLI = value
	default:
		return false
	}

	return true
}

type llmCapabilityProvider struct {
	Provider          string                 `json:"provider"`
	Models            []string               `json:"models,omitempty"`
	ModelCount        int                    `json:"model_count,omitempty"`
	DefaultModel      string                 `json:"default_model,omitempty"`
	AuthSource        string                 `json:"auth_source,omitempty"`
	AuthConfigured    bool                   `json:"auth_configured"`
	RuntimeDependency string                 `json:"runtime_dependency,omitempty"`
	RuntimeAvailable  *bool                  `json:"runtime_available,omitempty"`
	Usable            bool                   `json:"usable"`
	Notes             []string               `json:"notes,omitempty"`
	Extra             map[string]interface{} `json:"extra,omitempty"`
}

func runtimeAvailableForProvider(provider string) (string, error) {
	command := providerRuntime(provider)
	if command == "" {
		return "", nil
	}
	if command == "pi" {
		if explicit := strings.TrimSpace(os.Getenv("PI_BIN")); explicit != "" {
			if info, err := os.Stat(explicit); err == nil && !info.IsDir() {
				return explicit, nil
			}
			return explicit, fmt.Errorf("PI_BIN is set to %q but no executable file was found", explicit)
		}
		if path, err := exec.LookPath("pi"); err == nil {
			return path, nil
		}
		if path, err := exec.LookPath("npx"); err == nil {
			return path, nil
		}
		return "pi", fmt.Errorf("Pi CLI not found. Install @earendil-works/pi-coding-agent so pi is available on PATH, or install Node.js/npm so npx can run it.")
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return command, fmt.Errorf("%s not found. Install the CLI so %s is available on the backend PATH.", command, command)
	}
	return path, nil
}

func providerRuntimeAvailable(provider string) *bool {
	command := providerRuntime(provider)
	if command == "" {
		return nil
	}
	_, err := runtimeAvailableForProvider(provider)
	ok := err == nil
	return &ok
}

func providerRuntime(provider string) string {
	contract, ok := llm.GetCodingAgentProviderContract(
		llm.Provider(normalizeManagedProvider(provider)), "")
	if !ok {
		return ""
	}
	return contract.RuntimeBinary
}

func providerAuthConfigured(provider string, keys *llm.ProviderAPIKeys) (bool, string) {
	if keys == nil {
		keys = &llm.ProviderAPIKeys{}
	}
	switch normalizeManagedProvider(provider) {
	case string(llm.ProviderClaudeCode):
		if keys.ClaudeCodeOAuthToken != nil && strings.TrimSpace(*keys.ClaudeCodeOAuthToken) != "" {
			return true, "Workflow Claude Code OAuth token"
		}
		configured, _ := claudeCLILocalAuthState()
		return configured, "Claude Code CLI login"
	case string(llm.ProviderCodexCLI):
		if keys.CodexCLI != nil && strings.TrimSpace(*keys.CodexCLI) != "" {
			return true, "CODEX_API_KEY or workspace provider auth"
		}
		configured, _ := codexCLILocalAuthState()
		return configured, "Codex CLI login or CODEX_API_KEY/workspace provider auth"
	case string(llm.ProviderCursorCLI):
		if keys.CursorCLI != nil && strings.TrimSpace(*keys.CursorCLI) != "" {
			return true, "CURSOR_API_KEY or workspace provider auth"
		}
		configured, _ := cursorCLILocalAuthState()
		return configured, "Cursor CLI login or CURSOR_API_KEY/workspace provider auth"
	case string(llm.ProviderPiCLI):
		if piProviderAuthConfigured(keys) {
			return true, "Provider-specific Pi API key or workspace provider auth"
		}
		configured, _ := piCLILocalAuthState()
		return configured, "Pi provider login or workspace provider auth"
	case string(llm.ProviderMuseCLI):
		if keys.MuseCLI != nil && strings.TrimSpace(*keys.MuseCLI) != "" {
			return true, "META_API_KEY or workspace provider auth"
		}
		configured, _ := museCLILocalAuthState()
		return configured, "Muse CLI login or META_API_KEY/workspace provider auth"
	case string(llm.ProviderAgyCLI):
		// No managed-key shortcut: GEMINI_API_KEY alone does not
		// authenticate agy (API-key mode also needs modelProvider in the
		// CLI settings). The models probe reports true for a stored Google
		// login and for a fully configured API-key mode alike.
		configured, _ := agyCLILocalAuthState()
		return configured, "Antigravity CLI login or API-key mode"
	default:
		return false, "unknown provider"
	}
}

func piProviderAuthConfigured(keys *llm.ProviderAPIKeys) bool {
	if keys == nil {
		return false
	}
	if keys.PiCLI != nil && strings.TrimSpace(*keys.PiCLI) != "" {
		return true
	}
	for _, value := range keys.PiProviderKeys {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

var piCLIAuthProbeCache cliAuthProbeCache

func piCLILocalAuthState() (authenticated, conclusive bool) {
	return piCLIAuthProbeCache.serve(func() (bool, bool, error) {
		models, err := listPiCLIModelsFn()
		return err == nil && len(models) > 0, err == nil, err
	}, false)
}

func claudeCLILocalAuthState() (authenticated, conclusive bool) {
	return cachedCLIAuthState("claude", &claudeCLIAuthProbeCache, claudeCLIAuthStatusCommand, claudeCLIAuthStatus)
}

func codexCLILocalAuthState() (authenticated, conclusive bool) {
	return cachedCLIAuthState("codex", &codexCLIAuthProbeCache, codexCLIAuthStatusCommand, codexCLIAuthStatus)
}

func agyCLILocalAuthState() (authenticated, conclusive bool) {
	return cachedCLIAuthState("agy", &agyCLIAuthProbeCache, agyCLIModelsCommand, agyCLIAuthStatus)
}

// agyCLIAuthStatus parses `agy models` output. Exit code is useless here:
// agy exits 0 even for "Please sign in", so the text decides. A header plus
// at least one model row means authenticated (OAuth login or API-key mode);
// the sign-in marker means conclusively logged out.
func agyCLIAuthStatus(out []byte) (authenticated, conclusive bool) {
	lower := strings.ToLower(string(out))
	if strings.Contains(lower, "please sign in") {
		return false, true
	}
	rows := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}
	if rows >= 2 {
		return true, true
	}
	return false, false
}

func invalidateProviderAuthProbe(provider string) {
	var cache *cliAuthProbeCache
	switch normalizeManagedProvider(provider) {
	case string(llm.ProviderClaudeCode):
		cache = &claudeCLIAuthProbeCache
	case string(llm.ProviderCodexCLI):
		cache = &codexCLIAuthProbeCache
	case string(llm.ProviderCursorCLI):
		cache = &cursorCLIAuthProbeCache
	case string(llm.ProviderPiCLI):
		invalidatePiProviderCaches()
		return
	case string(llm.ProviderAgyCLI):
		cache = &agyCLIAuthProbeCache
	default:
		return
	}
	cache.Lock()
	cache.checkedAt = time.Time{}
	cache.authenticated = false
	cache.conclusive = false
	cache.Unlock()
}

func cachedCLIAuthState(runtime string, cache *cliAuthProbeCache, probe func(context.Context) ([]byte, error), parse func([]byte) (bool, bool)) (authenticated, conclusive bool) {
	if _, err := exec.LookPath(runtime); err != nil {
		return false, false
	}
	return cache.serve(func() (bool, bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := probe(ctx)
		a, k := parse(out)
		return a, k, err
	}, true)
}

func claudeCLIAuthStatus(out []byte) (authenticated, conclusive bool) {
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal(out, &status) == nil && status.LoggedIn != nil {
		return *status.LoggedIn, true
	}
	return false, false
}

func codexCLIAuthStatus(out []byte) (authenticated, conclusive bool) {
	lower := strings.ToLower(strings.TrimSpace(string(out)))
	if lower == "" {
		return false, false
	}
	if strings.Contains(lower, "not logged in") || strings.Contains(lower, "logged out") {
		return false, true
	}
	if strings.Contains(lower, "logged in") {
		return true, true
	}
	return false, false
}

func cursorCLILocalAuthState() (authenticated, conclusive bool) {
	if _, err := exec.LookPath("cursor-agent"); err != nil {
		return false, false
	}
	// A timeout or transient status-command failure is not evidence that the
	// user logged out: serve keeps a previously confirmed login.
	return cursorCLIAuthProbeCache.serve(func() (bool, bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := cursorCLIStatusJSON(ctx)
		a, k := cursorCLIAuthStatus(out)
		return a, k, err
	}, true)
}

// museCLILocalAuthState probes stored `muse login` state. `muse auth status`
// was assumed to exist and exit 0 when logged in, but `muse auth`'s only
// subcommand is `set` (verified live 2026-09-11 against `muse auth --help`):
// running `status` always fails with "expected `auth set`" regardless of
// login state, so the previous check reported every muse-cli install as
// logged out — the direct, observed cause of the workflow LLM picker's
// permanent "Needs setup" for Muse despite a real, working stored login.
// `muse login`/`muse auth --help` expose no status subcommand either, so the
// only reliable signal is the credential file `muse login` itself writes:
// $XDG_CONFIG_HOME/muse/auth.json, else ~/.config/muse/auth.json (same
// resolution the muse launcher script and musecli.museSettingsPath use).
// A present, non-empty file is conclusive; anything else is inconclusive,
// not proof of logged-out, since the file could be transiently mid-write.
func museCLILocalAuthState() (authenticated, conclusive bool) {
	if _, err := exec.LookPath("muse"); err != nil {
		return false, false
	}
	path := museAuthJSONPath()
	if path == "" {
		return false, false
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, true
		}
		return false, false
	}
	return info.Size() > 0, true
}

func museAuthJSONPath() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "muse", "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".config", "muse", "auth.json")
}

func cursorCLIAuthStatus(out []byte) (authenticated, conclusive bool) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return false, false
	}

	var status struct {
		IsAuthenticated *bool  `json:"isAuthenticated"`
		Status          string `json:"status"`
	}
	if json.Unmarshal(out, &status) == nil {
		if status.IsAuthenticated != nil {
			return *status.IsAuthenticated, true
		}
		switch strings.ToLower(strings.TrimSpace(status.Status)) {
		case "authenticated", "logged_in", "logged-in":
			return true, true
		case "unauthenticated", "not_authenticated", "not-authenticated", "logged_out", "logged-out":
			return false, true
		default:
			return false, false
		}
	}

	lower := strings.ToLower(text)
	if strings.Contains(lower, "logged in as") || strings.Contains(lower, "status: authenticated") {
		return true, true
	}
	if strings.Contains(lower, "not logged in") || strings.Contains(lower, "status: unauthenticated") || strings.Contains(lower, "logged out") {
		return false, true
	}
	return false, false
}

func providerUsable(provider string, authConfigured bool) (bool, string, *bool) {
	runtime := providerRuntime(provider)
	runtimeOK := providerRuntimeAvailable(provider)
	usable := authConfigured
	if runtimeOK != nil {
		usable = usable && *runtimeOK
	}
	return usable, runtime, runtimeOK
}

func buildChatLLMCapabilities(keys *llm.ProviderAPIKeys, includeModels bool) []llmCapabilityProvider {
	metadata := allProviderModelMetadata()
	modelsByProvider := map[string][]string{}
	for _, model := range metadata {
		if model == nil {
			continue
		}
		provider := normalizeManagedProvider(model.Provider)
		modelID := strings.TrimSpace(model.ModelID)
		if provider == "" || modelID == "" {
			continue
		}
		modelsByProvider[provider] = append(modelsByProvider[provider], modelID)
	}

	providers := []string{
		string(llm.ProviderCodexCLI),
		string(llm.ProviderCursorCLI),
		string(llm.ProviderPiCLI),
		string(llm.ProviderMuseCLI),
		string(llm.ProviderAgyCLI),
		string(llm.ProviderClaudeCode),
	}
	supportedSet := make(map[string]bool)
	for _, provider := range getSupportedProviders() {
		supportedSet[provider] = true
	}
	result := make([]llmCapabilityProvider, 0, len(providers))
	for _, provider := range providers {
		if !supportedSet[provider] {
			continue
		}
		authConfigured, authSource := providerAuthConfigured(provider, keys)
		usable, runtime, runtimeOK := providerUsable(provider, authConfigured)
		entry := llmCapabilityProvider{
			Provider:          provider,
			ModelCount:        len(modelsByProvider[provider]),
			DefaultModel:      llm.GetDefaultModel(llm.Provider(provider)),
			AuthSource:        authSource,
			AuthConfigured:    authConfigured,
			RuntimeDependency: runtime,
			RuntimeAvailable:  runtimeOK,
			Usable:            usable,
			Notes:             []string{"Use list_provider_models for full chat/text model metadata."},
		}
		if provider == string(llm.ProviderAgyCLI) {
			entry.Notes = append([]string{"Alpha: enable only for local or explicitly approved testing."}, entry.Notes...)
		}
		if includeModels {
			entry.Models = modelsByProvider[provider]
		}
		result = append(result, entry)
	}
	return result
}

func buildFixedCapabilityProviders(keys *llm.ProviderAPIKeys, providerModels map[string][]string, defaults map[string]string, notes map[string][]string) []llmCapabilityProvider {
	result := make([]llmCapabilityProvider, 0, len(providerModels))
	for _, provider := range []string{
		string(llm.ProviderCodexCLI),
		string(llm.ProviderCursorCLI),
		string(llm.ProviderPiCLI),
		string(llm.ProviderClaudeCode),
	} {
		models, ok := providerModels[provider]
		if !ok {
			continue
		}
		authConfigured, authSource := providerAuthConfigured(provider, keys)
		usable, runtime, runtimeOK := providerUsable(provider, authConfigured)
		entry := llmCapabilityProvider{
			Provider:          provider,
			Models:            models,
			ModelCount:        len(models),
			DefaultModel:      defaults[provider],
			AuthSource:        authSource,
			AuthConfigured:    authConfigured,
			RuntimeDependency: runtime,
			RuntimeAvailable:  runtimeOK,
			Usable:            usable,
			Notes:             notes[provider],
		}
		result = append(result, entry)
	}
	return result
}

func buildLLMCapabilities(ctx context.Context, capability string, includeModels bool) map[string]interface{} {
	keys := MergedProviderAPIKeys(ctx)
	capability = normalizeManagedProvider(capability)
	if capability == "" {
		capability = "all"
	}

	all := map[string]interface{}{
		"schema_version": 1,
		"notes": []string{
			"usable means required workspace/env auth is configured and any required CLI runtime is installed.",
			"Provider auth is managed by set_provider_auth and related provider-auth APIs; do not inspect or hand-edit config files.",
		},
	}
	if capability == "all" || capability == "chat" || capability == "text" {
		all["chat"] = map[string]interface{}{
			"description": "Providers usable for normal chat/text LLM calls.",
			"providers":   buildChatLLMCapabilities(keys, includeModels),
		}
	}

	if capability == "all" || capability == "search" || capability == "search_web" {
		all["search_web"] = map[string]interface{}{
			"description": "Providers usable by search_web_llm. Routing comes from the workspace published LLM set managed by list_published_llms/save_published_llm.",
			"providers": buildFixedCapabilityProviders(
				keys,
				map[string][]string{
					string(llm.ProviderClaudeCode): claudeCodeCapabilityModels(),
					string(llm.ProviderCodexCLI):   {"codex-cli", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.3-codex-spark"},
					string(llm.ProviderCursorCLI):  {"cursor-cli", "composer-2.5", "gpt-5", "sonnet-4-thinking", "sonnet-4"},
					string(llm.ProviderPiCLI):      {"google/gemini-3.5-flash", "google/gemini-2.5-flash"},
				},
				map[string]string{},
				map[string][]string{
					string(llm.ProviderCursorCLI): {"Uses Cursor Agent CLI through tmux; model availability follows the signed-in Cursor account."},
					string(llm.ProviderPiCLI):     {"Uses Pi CLI through tmux marker transport; use provider/model ids such as google/gemini-3.5-flash."},
				},
			),
			"routing_fields": map[string]interface{}{
				"search_role":     []string{"primary", "fallback"},
				"search_priority": "lower number wins within the same role",
			},
		}
	}

	return all
}

func buildLLMCapabilityPromptSection(ctx context.Context) string {
	capabilities := buildLLMCapabilities(ctx, "all", false)
	orderedCapabilities := []string{
		"chat",
		"search_web",
	}

	var lines []string
	for _, capability := range orderedCapabilities {
		entry, _ := capabilities[capability].(map[string]interface{})
		if entry == nil {
			continue
		}
		providers, _ := entry["providers"].([]llmCapabilityProvider)
		if len(providers) == 0 {
			continue
		}

		var providerSummaries []string
		for _, provider := range providers {
			if strings.TrimSpace(provider.Provider) == "" {
				continue
			}
			summary := provider.Provider
			if provider.DefaultModel != "" {
				summary += " (" + provider.DefaultModel + ")"
			}
			if provider.Usable {
				summary += " usable"
			} else if provider.AuthConfigured {
				summary += " auth configured"
			} else {
				summary += " auth missing"
			}
			providerSummaries = append(providerSummaries, summary)
		}
		if len(providerSummaries) == 0 {
			continue
		}
		lines = append(lines, "- `"+capability+"`: "+strings.Join(providerSummaries, ", "))
	}

	if len(lines) == 0 {
		return ""
	}

	return `## Workspace LLM Capability Snapshot

Published LLM entries are chat/text routing entries. Use ` + "`list_llm_capabilities`" + ` for authoritative chat and web-search provider, model, auth, and runtime status. The only active shared provider tools are ` + "`generate_text_llm`" + ` and ` + "`search_web_llm`" + `.

` + strings.Join(lines, "\n")
}

// registerMultiAgentLLMTools registers the shared AgentWorks model-library
// tools. Product profiles may opt out of individual tools through
// profile.tool_policy.disabled; otherwise every tool-backed chat receives the
// generic administration surface by default.
func (api *StreamingAPI) registerMultiAgentLLMTools(underlyingAgent definitionToolRegistrar, disabled func(string) bool) error {
	if underlyingAgent == nil {
		return fmt.Errorf("underlying agent is nil")
	}

	registerTool := func(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error)) error {
		if disabled != nil && disabled(name) {
			return nil
		}
		return underlyingAgent.RegisterCustomTool(name, description, params, exec, "llm_config_tools")
	}

	if err := registerLLMCapabilityTools(registerTool); err != nil {
		return err
	}

	return nil
}

func registerLLMCapabilityDiscoveryTools(registerTool func(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error)) error) error {
	if err := registerTool(
		"list_llm_capabilities",
		"List supported and currently usable LLM providers/models by capability: chat and search_web. Use include_models=true before choosing an explicit provider/model_id pair. Includes workspace defaults, auth requirements, and CLI runtime availability.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"capability": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter. Supported values: all, chat, search_web.",
				},
				"include_models": map[string]interface{}{
					"type":        "boolean",
					"description": "When true, include full model id lists where available. Use true before passing an explicit model_id to an LLM-backed tool so provider and model_id come from the same capability entry. Defaults to false because chat catalogs can be large.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			capability, _ := args["capability"].(string)
			includeModels, _ := args["include_models"].(bool)
			return prettyJSON(buildLLMCapabilities(ctx, capability, includeModels)), nil
		},
	); err != nil {
		return err
	}

	return nil
}

func (api *StreamingAPI) registerWorkflowLLMDiscoveryTools(underlyingAgent definitionToolRegistrar) error {
	if underlyingAgent == nil {
		return fmt.Errorf("underlying agent is nil")
	}
	registerTool := func(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error)) error {
		return underlyingAgent.RegisterCustomTool(name, description, params, exec, "llm_config_tools")
	}
	return registerLLMCapabilityDiscoveryTools(registerTool)
}

func registerLLMCapabilityTools(registerTool func(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error)) error) error {
	if err := registerTool(
		"list_llm_capabilities",
		"List supported and currently usable LLM providers/models by capability: chat and search_web. Use include_models=true before choosing an explicit provider/model_id pair. Includes workspace defaults, auth requirements, and CLI runtime availability.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"capability": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter. Supported values: all, chat, search_web.",
				},
				"include_models": map[string]interface{}{
					"type":        "boolean",
					"description": "When true, include full model id lists where available. Use true before passing an explicit model_id to an LLM-backed tool so provider and model_id come from the same capability entry. Defaults to false because chat catalogs can be large.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			capability, _ := args["capability"].(string)
			includeModels, _ := args["include_models"].(bool)
			return prettyJSON(buildLLMCapabilities(ctx, capability, includeModels)), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"list_published_llms",
		"List workspace-backed published chat/text LLM entries. Use this to answer which LLMs are published or available for workflow selection. Do not inspect config files directly.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, _ map[string]interface{}) (string, error) {
			llms, err := LoadPublishedLLMsWithAuto(ctx)
			if err != nil {
				return "", fmt.Errorf("failed to load published LLMs: %w", err)
			}
			if llms == nil {
				llms = []StoredPublishedLLM{}
			}
			return prettyJSON(map[string]interface{}{
				"count": len(llms),
				"llms":  llms,
				"note":  "These are the published chat/text models available for selection.",
			}), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"list_provider_models",
		"List the frontend-visible models for a provider. Fixed providers use /api/llm-config/models/metadata; dynamic providers use /api/llm-config/providers/{provider}/models.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{
					"type":        "string",
					"description": "Coding-agent CLI provider id: claude-code, codex-cli, cursor-cli, pi-cli, or muse-cli.",
				},
			},
			"required": []string{"provider"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			provider := normalizeManagedProvider(fmt.Sprintf("%v", args["provider"]))
			if provider == "" {
				return "provider is required.", nil
			}
			return listProviderModelsJSON(provider), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"test_llm",
		"Validate an LLM provider/model configuration before publishing. Uses workspace-backed provider auth by default, but temporary overrides can be provided in the call.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{
					"type":        "string",
					"description": "Coding-agent CLI provider id: claude-code, codex-cli, cursor-cli, pi-cli, or muse-cli.",
				},
				"model_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional model id to validate.",
				},
				"api_key": map[string]interface{}{
					"type":        "string",
					"description": "Optional temporary API key override. If omitted, the tool uses workspace-backed provider auth when available.",
				},
				"options": llmToolOptionsSchema("Optional model-specific options object. Use reasoning_effort for Codex CLI and Claude Code effort control, and use list_provider_models to discover supported levels."),
			},
			"required": []string{"provider"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			provider := normalizeManagedProvider(fmt.Sprintf("%v", args["provider"]))
			modelID, _ := args["model_id"].(string)
			apiKey, _ := args["api_key"].(string)
			options, _ := args["options"].(map[string]interface{})

			if provider == "" {
				return "provider is required.", nil
			}
			if !isPublishedLLMProviderAllowed(provider) {
				return unsupportedCodingAgentProviderMessage("chat LLM", provider), nil
			}

			explicitAPIKeyProvided := strings.TrimSpace(apiKey) != ""
			validationOptions := cloneOptionsMap(options)

			keys, err := LoadProviderKeys(ctx)
			if err != nil {
				return "", err
			}

			usedWorkspaceAuth := false
			if !explicitAPIKeyProvided && keys != nil {
				if provider == "pi-cli" {
					if value := selectStoredPiAPIKeyForModel(keys, modelID); value != "" {
						apiKey = value
						usedWorkspaceAuth = true
					}
				} else if value := getStoredProviderAPIKey(keys, provider); value != "" {
					apiKey = value
					usedWorkspaceAuth = true
				}
			}

			response := validateProviderConfig(llm.APIKeyValidationRequest{
				Provider: provider,
				APIKey:   strings.TrimSpace(apiKey),
				ModelID:  strings.TrimSpace(modelID),
				Options:  validationOptions,
			})

			return prettyJSON(map[string]interface{}{
				"provider":            provider,
				"model_id":            strings.TrimSpace(modelID),
				"valid":               response.Valid,
				"message":             response.Message,
				"error":               response.Error,
				"corrected_options":   response.CorrectedOptions,
				"used_workspace_auth": usedWorkspaceAuth,
			}), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"save_published_llm",
		"Create or update a workspace-backed published LLM entry. Store only the minimal routable fields: name, provider, model_id, and optional options such as reasoning_effort. Authentication is managed separately through provider-auth tools.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "Optional existing published LLM id to update. If omitted, the tool upserts by provider/model_id/name.",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Display name for the published LLM.",
				},
				"provider": map[string]interface{}{
					"type":        "string",
					"description": "Coding-agent CLI provider id: claude-code, codex-cli, cursor-cli, pi-cli, or muse-cli.",
				},
				"model_id": map[string]interface{}{
					"type":        "string",
					"description": "Model id for the published LLM.",
				},
				"options": llmToolOptionsSchema("Optional model-specific options to persist with this published LLM. Include reasoning_effort for Codex CLI or Claude Code when you want the published entry to run at a specific effort level."),
			},
			"required": []string{"name", "provider", "model_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["name"].(string)
			provider, _ := args["provider"].(string)
			modelID, _ := args["model_id"].(string)
			id, _ := args["id"].(string)
			options, _ := args["options"].(map[string]interface{})

			name = strings.TrimSpace(name)
			provider = strings.TrimSpace(provider)
			modelID = strings.TrimSpace(modelID)
			if name == "" || provider == "" || modelID == "" {
				return "name, provider, and model_id are required.", nil
			}
			if !isPublishedLLMProviderAllowed(provider) {
				return unsupportedCodingAgentProviderMessage("published LLM", provider), nil
			}

			llms, err := LoadPublishedLLMs(ctx)
			if err != nil {
				return "", err
			}
			if llms == nil {
				llms = []StoredPublishedLLM{}
			}

			entry := StoredPublishedLLM{
				ID:        strings.TrimSpace(id),
				Name:      name,
				Provider:  provider,
				ModelID:   modelID,
				Options:   options,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}

			matchIndex := -1
			for i, existing := range llms {
				if entry.ID != "" && existing.ID == entry.ID {
					matchIndex = i
					entry.CreatedAt = existing.CreatedAt
					break
				}
				if entry.ID == "" && existing.Provider == entry.Provider && existing.ModelID == entry.ModelID && existing.Name == entry.Name {
					matchIndex = i
					entry.ID = existing.ID
					entry.CreatedAt = existing.CreatedAt
					break
				}
			}

			if matchIndex >= 0 {
				llms[matchIndex] = entry
			} else {
				llms = append(llms, entry)
			}

			if err := SavePublishedLLMs(ctx, llms); err != nil {
				return "", err
			}

			return fmt.Sprintf("Saved published LLM.\n%s", prettyJSON(entry)), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"set_provider_auth",
		"Create or update workspace-backed coding-agent CLI authentication (API keys for codex-cli, cursor-cli, and pi-cli sub-providers).",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{
					"type":        "string",
					"description": "Provider id: pi-cli, codex-cli, or cursor-cli. Use pi_provider for a Pi sub-provider such as MiniMax.",
				},
				"api_key": map[string]interface{}{
					"type":        "string",
					"description": "API key for the provider.",
				},
				"model_id": map[string]interface{}{
					"type":        "string",
					"description": "For provider=pi-cli, infer the Pi sub-provider from this model id, for example openrouter/minimax/minimax-m3-20260531.",
				},
				"pi_provider": map[string]interface{}{
					"type":        "string",
					"description": "Optional explicit Pi sub-provider for provider=pi-cli, such as google, openrouter, deepseek, zai, kimi-coding, moonshotai, or minimax.",
				},
			},
			"required": []string{"provider"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			provider := normalizeManagedProvider(fmt.Sprintf("%v", args["provider"]))
			apiKey, _ := args["api_key"].(string)
			modelID, _ := args["model_id"].(string)
			piProvider, _ := args["pi_provider"].(string)

			keys, err := LoadProviderKeys(ctx)
			if err != nil {
				return "", err
			}
			if keys == nil {
				keys = &StoredProviderKeys{}
			}

			switch provider {
			case "pi-cli":
				if strings.TrimSpace(apiKey) == "" {
					return "api_key is required for pi-cli.", nil
				}
				storedProvider, ok := setStoredPiProviderAPIKey(keys, piProvider, modelID, apiKey)
				if !ok {
					return "api_key is required for pi-cli.", nil
				}
				if err := SaveProviderKeys(ctx, keys); err != nil {
					return "", err
				}
				return fmt.Sprintf("Updated Pi provider auth for %s.", storedProvider), nil
			default:
				if strings.TrimSpace(apiKey) == "" {
					return fmt.Sprintf("api_key is required for %s.", provider), nil
				}
				if !setStoredProviderAPIKey(keys, provider, apiKey) {
					return fmt.Sprintf("Unsupported managed provider %q.", provider), nil
				}
			}

			if err := SaveProviderKeys(ctx, keys); err != nil {
				return "", err
			}

			return fmt.Sprintf("Updated provider auth for %s.", provider), nil
		},
	); err != nil {
		return err
	}

	return nil
}

func unsupportedCodingAgentProviderMessage(kind, provider string) string {
	return fmt.Sprintf("unsupported %s provider %q. Only coding-agent CLIs are supported: %s.", kind, provider, strings.Join(llmguard.CodingAgentProviders(), ", "))
}
