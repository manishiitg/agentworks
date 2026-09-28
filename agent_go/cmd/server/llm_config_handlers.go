package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// supportedLLMProviders is the product offering: coding-agent CLIs only.
// Direct API transports (openai, anthropic, vertex, bedrock, azure, ...) were
// removed; SUPPORTED_LLM_PROVIDERS can only narrow this list.
var supportedLLMProviders = []string{
	"claude-code",
	"codex-cli",
	"cursor-cli",
	"pi-cli",
	"muse-cli",
	"agy-cli",
}

func isPublishedLLMProviderAllowed(provider string) bool {
	if !llmguard.IsCodingAgentProvider(provider) {
		return false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, offered := range getSupportedProviders() {
		if provider == offered {
			return true
		}
	}
	return false
}

func agyAlphaEnabled() bool {
	return strings.TrimSpace(os.Getenv("AGY_ALPHA")) == "1" && !IsMultiUserMode()
}

func defaultPublishedLLMProviderAndModel() (string, string) {
	for _, provider := range []string{"codex-cli", "cursor-cli", "pi-cli", "claude-code"} {
		modelID := strings.TrimSpace(llm.GetDefaultModel(llm.Provider(provider)))
		if modelID != "" {
			return provider, modelID
		}
	}
	return "codex-cli", "codex-cli"
}

// getSupportedProviders returns the list of supported LLM providers based on environment configuration
func getSupportedProviders() []string {
	offered := make([]string, 0, len(supportedLLMProviders))
	for _, provider := range supportedLLMProviders {
		if provider != "agy-cli" || agyAlphaEnabled() {
			offered = append(offered, provider)
		}
	}
	envValue := os.Getenv("SUPPORTED_LLM_PROVIDERS")
	if envValue == "" {
		return offered
	}

	// Parse comma-separated list
	parts := strings.Split(envValue, ",")
	validProviders := make(map[string]bool)
	for _, p := range offered {
		validProviders[p] = true
	}

	var supported []string
	for _, part := range parts {
		provider := strings.ToLower(strings.TrimSpace(part))
		if provider == "" {
			continue
		}
		if validProviders[provider] {
			supported = append(supported, provider)
		} else {
			log.Printf("Warning: ignoring invalid provider '%s' in SUPPORTED_LLM_PROVIDERS", part)
		}
	}

	// If no valid providers found, return all
	if len(supported) == 0 {
		log.Printf("Warning: no valid providers found in SUPPORTED_LLM_PROVIDERS, enabling all providers")
		return offered
	}

	return supported
}

// isGlobalLLMConfigLocked returns true if all LLM configuration is locked
func isGlobalLLMConfigLocked() bool {
	val := os.Getenv("LLM_CONFIG_LOCKED")
	return val == "true" || val == "1"
}

// getLockedProviders returns a list of locked providers from the environment variable
func getLockedProviders() []string {
	val := os.Getenv("LLM_CONFIG_LOCKED")
	if val == "true" || val == "1" {
		return []string{"all"}
	}
	if val == "" || val == "false" || val == "0" {
		return []string{}
	}
	// Split by comma
	parts := strings.Split(val, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(strings.ToLower(parts[i]))
	}
	return parts
}

// isProviderLocked returns true if the specific provider is locked
func isProviderLocked(provider string) bool {
	if provider == "" {
		return false
	}
	locked := getLockedProviders()
	for _, p := range locked {
		if p == "all" || p == strings.ToLower(provider) {
			return true
		}
	}
	return false
}

// isAllowedDefaultLLM returns true when (provider, modelID) is in the default published LLMs list (for locked mode).
func isAllowedDefaultLLM(provider, modelID string) bool {
	if provider == "" || modelID == "" {
		return false
	}
	defaults := llm.GetLLMDefaults()
	// Only restrict to defaults if the *specific* provider is locked
	if !isProviderLocked(provider) {
		return true
	}

	// An explicitly published list is the whole menu: when the operator set
	// DEFAULT_PUBLISHED_LLMS (or its _PATH), a locked provider may only run
	// what that list names. Without one, the historical behaviour stands and
	// any model the platform knows for a locked provider is accepted -- which
	// let a workflow's saved config keep a provider the UI no longer offered
	// (found on RTS 2026-09-03 while fixing AgentWorks to one Cursor model).
	if publishedLLMListConfigured() {
		if publishedLLMListContains(provider, modelID, defaults.PrimaryConfig) {
			return true
		}
		// A published coding-agent provider brings its role profile with it
		// (see lockedPresetLLMConfig), so those models are published too.
		if publishedLLMProviderListed(provider, defaults.PrimaryConfig) {
			if tiers, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(provider)); ok {
				for _, ref := range []llmproviders.CodingAgentTierModelRef{tiers.Builder, tiers.High, tiers.Medium, tiers.Low, tiers.Pulse} {
					if strings.EqualFold(strings.TrimSpace(ref.ModelID), modelID) {
						return true
					}
				}
			}
		}
		return false
	}

	// Allow any model listed in AvailableModels for this provider. Dynamic CLI
	// providers can come from the curated discovery options when the provider
	// library does not expose them through static defaults.
	models := defaults.AvailableModels[provider]
	if len(models) == 0 {
		models = discoveryModelOptions(provider)
	}
	for _, m := range models {
		if m == modelID {
			return true
		}
	}

	return publishedLLMListContains(provider, modelID, defaults.PrimaryConfig)
}

// publishedLLMListConfigured reports whether the operator explicitly published
// an LLM list (as opposed to the auto-generated one built from known models).
func publishedLLMListConfigured() bool {
	return strings.TrimSpace(os.Getenv("DEFAULT_PUBLISHED_LLMS")) != "" || strings.TrimSpace(os.Getenv("DEFAULT_PUBLISHED_LLMS_PATH")) != ""
}

func publishedLLMProviderListed(provider string, primaryConfig interface{}) bool {
	for _, entry := range getDefaultPublishedLLMs(true, primaryConfig) {
		if p, _ := entry["provider"].(string); strings.EqualFold(strings.TrimSpace(p), provider) {
			return true
		}
	}
	return false
}

func publishedLLMListContains(provider, modelID string, primaryConfig interface{}) bool {
	for _, entry := range getDefaultPublishedLLMs(true, primaryConfig) {
		p, _ := entry["provider"].(string)
		m, _ := entry["model_id"].(string)
		if p == provider && m == modelID {
			return true
		}
	}
	return false
}

// resolveLockedLLM is the provider/model a request runs with while
// LLM_CONFIG_LOCKED is on. A binding owned by a product profile (Video
// Studio pins claude-code in its product.yaml) is operator configuration
// too, so it always wins; anything else must be on the published list, or
// the server default applies.
func resolveLockedLLM(cfg *orchestrator.LLMConfig, source string) (string, string) {
	if cfg != nil {
		p, m := strings.TrimSpace(cfg.Primary.Provider), strings.TrimSpace(cfg.Primary.ModelID)
		if p != "" && m != "" {
			if source == llmConfigSourceAgentProfile || isAllowedDefaultLLM(p, m) {
				return p, m
			}
		}
	}
	return getPrimaryProviderAndModelFromDefaults()
}

func buildProviderCapabilities(ctx context.Context) map[string][]string {
	raw := buildLLMCapabilities(ctx, "all", false)
	grouped := make(map[string]map[string]bool)

	for capabilityName, value := range raw {
		section, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		providers, ok := section["providers"].([]llmCapabilityProvider)
		if !ok {
			continue
		}
		for _, provider := range providers {
			if strings.TrimSpace(provider.Provider) == "" {
				continue
			}
			if grouped[provider.Provider] == nil {
				grouped[provider.Provider] = make(map[string]bool)
			}
			grouped[provider.Provider][capabilityName] = true
		}
	}

	result := make(map[string][]string, len(grouped))
	for provider, capabilitySet := range grouped {
		capabilities := make([]string, 0, len(capabilitySet))
		for capabilityName := range capabilitySet {
			capabilities = append(capabilities, capabilityName)
		}
		sort.Strings(capabilities)
		result[provider] = capabilities
	}
	return result
}

// getPrimaryProviderAndModelFromDefaults extracts provider and model_id from llm.GetLLMDefaults().PrimaryConfig.
func getPrimaryProviderAndModelFromDefaults() (provider, modelID string) {
	defaults := llm.GetLLMDefaults()
	defaultProvider, defaultModelID := defaultPublishedLLMProviderAndModel()
	bytes, err := json.Marshal(defaults.PrimaryConfig)
	if err != nil {
		return defaultProvider, defaultModelID
	}
	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		return defaultProvider, defaultModelID
	}
	if p, _ := m["provider"].(string); p != "" {
		provider = p
	} else {
		provider = defaultProvider
	}
	if mid, _ := m["model_id"].(string); mid != "" {
		modelID = mid
	} else {
		modelID = llm.GetDefaultModel(llm.Provider(provider))
	}
	return provider, modelID
}

// buildProviderAPIKeysFromEnv builds llm.ProviderAPIKeys from environment variables (for locked mode).
// Top-level upstream keys (OpenAI, Anthropic, Vertex, ...) are Pi CLI credentials.
func buildProviderAPIKeysFromEnv() *llm.ProviderAPIKeys {
	keys := &llm.ProviderAPIKeys{}
	setProviderKeyFromEnv := func(provider llm.Provider, envNames ...string) {
		for _, envName := range envNames {
			if s := strings.TrimSpace(os.Getenv(envName)); s != "" {
				keys.SetKeyForProvider(provider, &s)
				return
			}
		}
	}

	setProviderKeyFromEnv(llm.ProviderOpenAI, "OPENAI_API_KEY")
	setProviderKeyFromEnv(llm.ProviderOpenRouter, "OPENROUTER_API_KEY", "OPEN_ROUTER_API_KEY")
	setProviderKeyFromEnv(llm.ProviderAnthropic, "ANTHROPIC_API_KEY")
	// A Claude Code setup token is an OAuth credential for the Claude CLI, not
	// an Anthropic API key. Keep it on its dedicated field so the adapter can
	// inject it into the per-project CLI process without exposing it to chat.
	if s := strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")); s != "" {
		keys.ClaudeCodeOAuthToken = &s
	}
	setProviderKeyFromEnv(llm.ProviderZAI, "ZAI_API_KEY")
	setProviderKeyFromEnv(llm.ProviderKimi, "KIMI_API_KEY")
	if s := os.Getenv("VERTEX_API_KEY"); s != "" {
		keys.Vertex = &s
	} else if s := os.Getenv("GOOGLE_API_KEY"); s != "" {
		keys.Vertex = &s
	} else if s := os.Getenv("GEMINI_API_KEY"); s != "" {
		keys.Vertex = &s
	} else if s := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); s != "" {
		keys.Vertex = &s
	}
	// Codex CLI: only use explicit CODEX_API_KEY (not OPENAI_API_KEY).
	// Codex CLI has its own stored auth via `codex login`.
	if s := os.Getenv("CODEX_API_KEY"); s != "" {
		keys.CodexCLI = &s
	}
	if s := os.Getenv("CURSOR_API_KEY"); s != "" {
		keys.CursorCLI = &s
	}
	if s := os.Getenv("PI_API_KEY"); s != "" {
		keys.PiCLI = &s
	} else if s := os.Getenv("GEMINI_API_KEY"); s != "" {
		keys.PiCLI = &s
	} else if s := os.Getenv("GOOGLE_API_KEY"); s != "" {
		keys.PiCLI = &s
	}
	if s := os.Getenv("MINIMAX_API_KEY"); s != "" {
		keys.MiniMax = &s
	}
	// Muse CLI: explicit META_API_KEY only (never a third-party key).
	// Empty means stored `muse login`, mirroring the adapter's precedence.
	if s := os.Getenv("META_API_KEY"); s != "" {
		keys.MuseCLI = &s
	}
	keys.PiProviderKeys = buildPiProviderKeysFromEnv()
	return keys
}

func buildPiProviderKeysFromEnv() map[string]string {
	envByProvider := map[string][]string{
		"google":            {"PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"},
		"google-vertex":     {"PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"},
		"openai":            {"OPENAI_API_KEY"},
		"anthropic":         {"ANTHROPIC_API_KEY"},
		"openrouter":        {"OPENROUTER_API_KEY"},
		"deepseek":          {"DEEPSEEK_API_KEY"},
		"nvidia":            {"NVIDIA_API_KEY"},
		"mistral":           {"MISTRAL_API_KEY"},
		"groq":              {"GROQ_API_KEY"},
		"cerebras":          {"CEREBRAS_API_KEY"},
		"xai":               {"XAI_API_KEY"},
		"zai":               {"ZAI_API_KEY"},
		"zai-coding-cn":     {"ZAI_CODING_CN_API_KEY"},
		"opencode":          {"OPENCODE_API_KEY"},
		"opencode-go":       {"OPENCODE_API_KEY"},
		"fireworks":         {"FIREWORKS_API_KEY"},
		"together":          {"TOGETHER_API_KEY"},
		"kimi-coding":       {"KIMI_API_KEY"},
		"moonshotai":        {"KIMI_API_KEY"},
		"moonshotai-cn":     {"KIMI_API_KEY"},
		"minimax":           {"MINIMAX_API_KEY"},
		"minimax-cn":        {"MINIMAX_CN_API_KEY"},
		"vercel-ai-gateway": {"AI_GATEWAY_API_KEY"},
	}
	result := map[string]string{}
	for provider, envNames := range envByProvider {
		for _, envName := range envNames {
			if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
				result[provider] = value
				break
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

type llmDiscoveryCandidate struct {
	ID               string   `json:"id"`
	Provider         string   `json:"provider"`
	ModelID          string   `json:"model_id"`
	ModelName        string   `json:"model_name,omitempty"`
	Label            string   `json:"label"`
	Kind             string   `json:"kind"`
	DetectionSource  string   `json:"detection_source"`
	AuthSource       string   `json:"auth_source,omitempty"`
	AuthConfigured   bool     `json:"auth_configured"`
	RuntimeCommand   string   `json:"runtime_command,omitempty"`
	RuntimeAvailable *bool    `json:"runtime_available,omitempty"`
	Usable           bool     `json:"usable"`
	Recommended      bool     `json:"recommended"`
	Reason           string   `json:"reason"`
	SetupHint        string   `json:"setup_hint,omitempty"`
	Options          []string `json:"options,omitempty"`
}

type llmDiscoveryResponse struct {
	Candidates []llmDiscoveryCandidate `json:"candidates"`
	Notes      []string                `json:"notes"`
}

func providerDisplayLabel(provider string) string {
	switch provider {
	case "codex-cli":
		return "OpenAI Codex CLI"
	case "cursor-cli":
		return "Cursor CLI"
	case "pi-cli":
		return "Pi CLI"
	case "muse-cli":
		return "Muse"
	case "claude-code":
		return "Claude Code"
	default:
		return provider
	}
}

func modelNameForProviderModel(provider, modelID string) string {
	for _, metadata := range allProviderModelMetadata() {
		if metadata == nil {
			continue
		}
		if strings.EqualFold(metadata.Provider, provider) && metadata.ModelID == modelID {
			if metadata.ModelName != "" {
				return metadata.ModelName
			}
			return metadata.ModelID
		}
	}
	return modelID
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func discoveryCandidateKind(provider string) string {
	return providerKind(provider)
}

func discoveryModelOptions(provider string) []string {
	switch provider {
	case "codex-cli":
		return []string{"codex-cli", "high", "medium", "low"}
	case "cursor-cli":
		return []string{"cursor-cli", "composer-2.5", "gpt-5", "sonnet-4-thinking", "sonnet-4"}
	case "pi-cli":
		return piCuratedModelIDs()
	case "muse-cli":
		return []string{"muse-spark-1.3-contributor"}
	case "agy-cli":
		return []string{"gemini-3.8-flash-high"}
	case "claude-code":
		options := append([]string{}, claudeCodeCapabilityModels()...)
		for _, alias := range []string{"high", "medium", "low"} {
			if !stringSliceContains(options, alias) {
				options = append(options, alias)
			}
		}
		return options
	default:
		return nil
	}
}

func discoverySetupHint(provider string, runtimeMissing bool) string {
	if runtimeMissing {
		switch provider {
		case "codex-cli":
			return "Install Codex CLI so the codex command is available on the backend PATH."
		case "cursor-cli":
			return "Install Cursor CLI so the cursor-agent command is available on the backend PATH."
		case "pi-cli":
			return "Install Pi CLI with npm install -g @earendil-works/pi-coding-agent, or ensure npx is available on the backend PATH."
		case "muse-cli":
			return "Install Muse CLI so the muse command is available on the backend PATH."
		case "agy-cli":
			return "Install Antigravity CLI so the agy command is available on the backend PATH."
		case "claude-code":
			return "Install Claude Code so the claude command is available on the backend PATH."
		default:
			return "Install the provider CLI so its command is available on the backend PATH."
		}
	}

	switch provider {
	case "codex-cli":
		return "Run codex login or set CODEX_API_KEY, then test again."
	case "cursor-cli":
		return "Run cursor-agent login or set CURSOR_API_KEY, then test again."
	case "pi-cli":
		return "Set PI_API_KEY, GEMINI_API_KEY, or GOOGLE_API_KEY, then test again."
	case "muse-cli":
		return "Run muse login or set META_API_KEY, then test again."
	case "agy-cli":
		return "Launch agy and complete Google sign-in, then test again."
	case "claude-code":
		return "Run claude to finish Claude Code authentication, then test again."
	default:
		return "Provider auth was not detected in the server environment or workspace provider keys."
	}
}

func buildLLMDiscovery(ctx context.Context) llmDiscoveryResponse {
	keys := MergedProviderAPIKeys(ctx)
	supported := getSupportedProviders()
	supportedSet := make(map[string]bool, len(supported))
	for _, provider := range supported {
		supportedSet[provider] = true
	}

	providerOrder := []string{
		"claude-code",
		"codex-cli",
		"cursor-cli",
		"pi-cli",
		"muse-cli",
	}

	candidates := make([]llmDiscoveryCandidate, 0, len(providerOrder))
	for _, provider := range providerOrder {
		if !supportedSet[provider] {
			continue
		}

		authConfigured, authSource := providerAuthConfigured(provider, keys)
		usable, runtimeCommand, runtimeOK := providerUsable(provider, authConfigured)
		modelID := llm.GetDefaultModel(llm.Provider(provider))
		if strings.TrimSpace(modelID) == "" {
			if options := discoveryModelOptions(provider); len(options) > 0 {
				modelID = options[0]
			}
		}
		if modelID == "" {
			continue
		}

		kind := discoveryCandidateKind(provider)
		discovered := authConfigured || usable
		if runtimeOK != nil && *runtimeOK {
			discovered = true
		}
		if !discovered && kind != "local_cli" {
			continue
		}

		source := "Environment or workspace auth"
		if runtimeOK != nil && *runtimeOK {
			source = "Local CLI"
		} else if runtimeOK != nil && !*runtimeOK {
			source = "CLI not found"
		}

		reason := "Ready to enable."
		setupHint := ""
		if runtimeOK != nil && !*runtimeOK {
			reason = "CLI runtime was not detected."
			setupHint = discoverySetupHint(provider, true)
		} else if !usable {
			reason = "Detected, but setup may be incomplete."
			setupHint = discoverySetupHint(provider, false)
		}

		candidates = append(candidates, llmDiscoveryCandidate{
			ID:               provider + ":" + modelID,
			Provider:         provider,
			ModelID:          modelID,
			ModelName:        modelNameForProviderModel(provider, modelID),
			Label:            providerDisplayLabel(provider),
			Kind:             kind,
			DetectionSource:  source,
			AuthSource:       authSource,
			AuthConfigured:   authConfigured,
			RuntimeCommand:   runtimeCommand,
			RuntimeAvailable: runtimeOK,
			Usable:           usable,
			Recommended:      usable,
			Reason:           reason,
			SetupHint:        setupHint,
			Options:          discoveryModelOptions(provider),
		})
	}

	return llmDiscoveryResponse{
		Candidates: candidates,
		Notes: []string{
			"Discovery only checks installed runtimes and configured auth. It does not send prompts or spend provider credits.",
			"Environment variables are read from the backend server process, not unrelated terminal tabs opened after startup.",
		},
	}
}

// handleDiscoverLLMSetup reports local/provider LLM setup candidates without running model calls.
func (api *StreamingAPI) handleDiscoverLLMSetup(w http.ResponseWriter, r *http.Request) {
	response := buildLLMDiscovery(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode LLM discovery response", http.StatusInternalServerError)
		return
	}
}

// stripSecretsFromMap recursively removes api_key, endpoint, and other sensitive fields from m (for locked mode).
func stripSecretsFromMap(m map[string]interface{}) {
	delete(m, "api_key")
	delete(m, "endpoint")
	for _, v := range m {
		if nested, ok := v.(map[string]interface{}); ok {
			stripSecretsFromMap(nested)
		}
	}
}

// getDefaultPublishedLLMs returns the list of default published LLMs from env, file, or primary config.
// When locked is true, entries must not include api_key or endpoint (Azure tenant URL).
func getDefaultPublishedLLMs(locked bool, primaryConfig interface{}) []map[string]interface{} {
	stripEntrySecrets := func(entry map[string]interface{}) {
		delete(entry, "api_key")
		delete(entry, "endpoint")
	}
	// 1) Try DEFAULT_PUBLISHED_LLMS (JSON string)
	if s := os.Getenv("DEFAULT_PUBLISHED_LLMS"); s != "" {
		var list []map[string]interface{}
		if err := json.Unmarshal([]byte(s), &list); err == nil && len(list) > 0 {
			list = filterDefaultPublishedLLMs(list)
			for i := range list {
				provider, _ := list[i]["provider"].(string)
				if locked || isProviderLocked(provider) {
					stripEntrySecrets(list[i])
				}
			}
			return list
		}
	}
	// 2) Try DEFAULT_PUBLISHED_LLMS_PATH (path to JSON file)
	if path := os.Getenv("DEFAULT_PUBLISHED_LLMS_PATH"); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var list []map[string]interface{}
			if err := json.Unmarshal(data, &list); err == nil && len(list) > 0 {
				list = filterDefaultPublishedLLMs(list)
				for i := range list {
					provider, _ := list[i]["provider"].(string)
					if locked || isProviderLocked(provider) {
						stripEntrySecrets(list[i])
					}
				}
				return list
			}
		}
	}

	// 3) Auto-generate defaults from AvailableModels for locked providers
	var entries []map[string]interface{}
	defaults := llm.GetLLMDefaults()
	providers := getSupportedProviders()

	for _, p := range providers {
		// If provider is locked (or global lock is on), include its available models
		if isProviderLocked(p) || locked {
			models := defaults.AvailableModels[p]
			if len(models) == 0 {
				models = discoveryModelOptions(p)
			}
			for _, m := range models {
				entry := map[string]interface{}{
					"id":       "default-" + p + "-" + strings.ReplaceAll(m, "/", "-"),
					"name":     m, // Simple name
					"provider": p,
					"model_id": m,
				}
				// Secrets are stripped by default since we don't add them here
				entries = append(entries, entry)
			}
		}
	}

	if len(entries) > 0 {
		return entries
	}

	// 4) Fallback: Build one entry from primary config if nothing else found
	var provider, modelID string
	if pc, ok := primaryConfig.(map[string]interface{}); ok {
		if p, _ := pc["provider"].(string); p != "" {
			provider = p
		}
		if m, _ := pc["model_id"].(string); m != "" {
			modelID = m
		}
	}
	if !isPublishedLLMProviderAllowed(provider) {
		provider, modelID = defaultPublishedLLMProviderAndModel()
	}
	if provider == "" {
		provider, modelID = defaultPublishedLLMProviderAndModel()
	}
	if modelID == "" {
		modelID = llm.GetDefaultModel(llm.Provider(provider))
	}
	if strings.TrimSpace(modelID) == "" {
		provider, modelID = defaultPublishedLLMProviderAndModel()
	}
	entry := map[string]interface{}{
		"id":       "default-" + provider + "-" + strings.ReplaceAll(modelID, "/", "-"),
		"name":     "Default (" + provider + ")",
		"provider": provider,
		"model_id": modelID,
	}
	return []map[string]interface{}{entry}
}

func filterDefaultPublishedLLMs(list []map[string]interface{}) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(list))
	for _, entry := range list {
		provider, _ := entry["provider"].(string)
		if !isPublishedLLMProviderAllowed(provider) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// handleGetLLMDefaults returns default LLM configurations from environment variables.
// When LLM_CONFIG_LOCKED=true (or specific provider is locked), api_key and endpoint are stripped.
func (api *StreamingAPI) handleGetLLMDefaults(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received request for LLM defaults")
	defaults := llm.GetLLMDefaults()
	availableModels := make(map[string][]string, len(defaults.AvailableModels))
	for provider, models := range defaults.AvailableModels {
		if !isPublishedLLMProviderAllowed(provider) {
			continue
		}
		availableModels[provider] = models
	}
	for _, provider := range getSupportedProviders() {
		if _, exists := availableModels[provider]; exists || !isPublishedLLMProviderAllowed(provider) {
			continue
		}
		if models := discoveryModelOptions(provider); len(models) > 0 {
			availableModels[provider] = models
		}
	}

	globalLocked := isGlobalLLMConfigLocked()
	lockedProviders := getLockedProviders()
	primaryProvider, primaryModelID := getPrimaryProviderAndModelFromDefaults()
	primaryConfig := map[string]interface{}{
		"provider": primaryProvider,
		"model_id": primaryModelID,
	}

	// Build response (same shape as before)
	response := map[string]interface{}{
		"primary_config":        primaryConfig,
		"available_models":      availableModels,
		"provider_capabilities": buildProviderCapabilities(r.Context()),
		"supported_providers":   getSupportedProviders(),
		"locked_providers":      lockedProviders,
	}

	if globalLocked {
		stripSecretsFromMap(response)
	}

	response["llm_config_locked"] = globalLocked
	response["default_published_llms"] = getDefaultPublishedLLMs(globalLocked, defaults.PrimaryConfig)
	response["default_published_llms_locked"] = globalLocked

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleValidateAPIKey validates API keys for supported LLM providers and coding CLIs.
func (api *StreamingAPI) handleValidateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req llm.APIKeyValidationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode API key validation request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	api.populateValidationCredentialsFromMergedKeys(r.Context(), &req)
	log.Printf("Received API key validation request for provider: %s", req.Provider)
	response := validateProviderConfig(req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (api *StreamingAPI) populateValidationCredentialsFromMergedKeys(ctx context.Context, req *llm.APIKeyValidationRequest) {
	if req == nil {
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))

	keys := MergedProviderAPIKeys(ctx)
	if keys == nil {
		return
	}
	setAPIKey := func(key *string) {
		if req.APIKey == "" && key != nil {
			req.APIKey = strings.TrimSpace(*key)
		}
	}

	switch provider {
	case "codex-cli":
		setAPIKey(keys.CodexCLI)
	case "cursor-cli":
		setAPIKey(keys.CursorCLI)
	case "muse-cli":
		setAPIKey(keys.MuseCLI)
	case "pi-cli":
		if req.APIKey == "" {
			req.APIKey = selectPiAPIKeyForModel(keys, req.ModelID)
		}
	}
}

func validateProviderConfig(req llm.APIKeyValidationRequest) llm.APIKeyValidationResponse {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	switch provider {
	case "claude-code":
		return validateClaudeCodeCLI()
	case "codex-cli":
		return validateCodexCLI(req.APIKey)
	case "cursor-cli":
		return validateCursorCLI(req.APIKey, req.ModelID)
	case "muse-cli":
		return validateMuseCLI(req.APIKey)
	case "pi-cli":
		return validatePiCLI(req.APIKey, req.ModelID, req.Options)
	default:
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: unsupportedCodingAgentProviderMessage("LLM", req.Provider),
		}
	}
}

// validateClaudeCodeCLI validates the Claude Code CLI by checking it exists and
// then running a real adapter call through llm.InitializeLLM so the test
// exercises the same code path as a live workflow run (env vars, transport,
// model resolution all match production).
func validateClaudeCodeCLI() llm.APIKeyValidationResponse {
	log.Printf("[CLAUDE-CODE VALIDATION] Starting CLI validation")

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		log.Printf("[CLAUDE-CODE VALIDATION] CLI not found on PATH: %v", err)
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Claude Code CLI not found. Install it with: npm install -g @anthropic-ai/claude-code",
		}
	}
	log.Printf("[CLAUDE-CODE VALIDATION] CLI found at: %s", claudePath)

	workspaceDir, err := os.MkdirTemp("", "claude-code-validation-*")
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Could not create a temporary workspace for Claude Code validation.",
		}
	}
	defer os.RemoveAll(workspaceDir)

	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderClaudeCode,
		ModelID:  "claude-code",
		Context:  context.Background(),
	})
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Failed to initialize Claude Code: %v", err),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly: Claude Code is working."},
			},
		},
	}, llm.WithClaudeCodeWorkingDir(workspaceDir))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return llm.APIKeyValidationResponse{
				Valid:   false,
				Message: "Claude Code timed out after 90s. Check that you are authenticated (run 'claude' to log in).",
			}
		}
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Claude Code error: %s", strings.TrimSpace(err.Error())),
		}
	}

	responseText := ""
	if resp != nil && len(resp.Choices) > 0 {
		responseText = strings.TrimSpace(resp.Choices[0].Content)
	}
	if responseText == "" {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Claude Code returned an empty response. Check authentication with 'claude'.",
		}
	}

	log.Printf("[CLAUDE-CODE VALIDATION SUCCESS] Got response: %s", responseText)
	return llm.APIKeyValidationResponse{
		Valid:   true,
		Message: fmt.Sprintf("Claude Code is working. Response: %s", responseText),
	}
}

// validateCodexCLI validates the OpenAI Codex CLI by running a real adapter
// call through llm.InitializeLLM so the test exercises the same code path as
// a live workflow run.
func validateCodexCLI(apiKey string) llm.APIKeyValidationResponse {
	log.Printf("[CODEX-CLI VALIDATION] Starting CLI validation")

	codexPath, err := exec.LookPath("codex")
	if err != nil {
		log.Printf("[CODEX-CLI VALIDATION] CLI not found on PATH: %v", err)
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Codex CLI not found. Install it with: npm install -g @openai/codex",
		}
	}
	log.Printf("[CODEX-CLI VALIDATION] CLI found at: %s", codexPath)

	keys := &llm.ProviderAPIKeys{}
	if apiKey == "" {
		apiKey = os.Getenv("CODEX_API_KEY")
	}
	if strings.TrimSpace(apiKey) != "" {
		keys.CodexCLI = &apiKey
	}

	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderCodexCLI,
		ModelID:  "medium",
		APIKeys:  keys,
		Context:  context.Background(),
	})
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Failed to initialize Codex CLI: %v", err),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly: Codex CLI is working."},
			},
		},
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return llm.APIKeyValidationResponse{
				Valid:   false,
				Message: "Codex CLI timed out after 90s. Check that you are authenticated (run 'codex login').",
			}
		}
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Codex CLI error: %s", strings.TrimSpace(err.Error())),
		}
	}

	responseText := ""
	if resp != nil && len(resp.Choices) > 0 {
		responseText = strings.TrimSpace(resp.Choices[0].Content)
	}
	if responseText == "" {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Codex CLI returned an empty response. Check authentication with 'codex login'.",
		}
	}

	log.Printf("[CODEX-CLI VALIDATION SUCCESS] Got response: %s", responseText)
	return llm.APIKeyValidationResponse{
		Valid:   true,
		Message: fmt.Sprintf("Codex CLI is working. Response: %s", responseText),
	}
}

// validateMuseCLI validates Muse CLI through the real exec-lane adapter path:
// binary on PATH, init (explicit key, else META_API_KEY, else stored login),
// then one live canary turn. Mirrors validateCodexCLI.
func validateMuseCLI(apiKey string) llm.APIKeyValidationResponse {
	log.Printf("[MUSE-CLI VALIDATION] Starting CLI validation")

	musePath, err := exec.LookPath("muse")
	if err != nil {
		log.Printf("[MUSE-CLI VALIDATION] CLI not found on PATH: %v", err)
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Muse CLI not found. Install it so the muse command is available on the backend PATH.",
		}
	}
	log.Printf("[MUSE-CLI VALIDATION] CLI found at: %s", musePath)

	keys := &llm.ProviderAPIKeys{}
	if apiKey == "" {
		apiKey = os.Getenv("META_API_KEY")
	}
	if strings.TrimSpace(apiKey) != "" {
		keys.MuseCLI = &apiKey
	}

	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderMuseCLI,
		ModelID:  "muse-spark-1.3-contributor",
		APIKeys:  keys,
		Context:  context.Background(),
	})
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Failed to initialize Muse CLI: %v", err),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly: Muse CLI is working."},
			},
		},
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return llm.APIKeyValidationResponse{
				Valid:   false,
				Message: "Muse CLI timed out after 60s. Check authentication (run 'muse login' or set META_API_KEY).",
			}
		}
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Muse CLI error: %s", strings.TrimSpace(err.Error())),
		}
	}

	responseText := ""
	if resp != nil && len(resp.Choices) > 0 {
		responseText = strings.TrimSpace(resp.Choices[0].Content)
	}
	if responseText == "" {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Muse CLI returned an empty response. Check authentication with 'muse login' or META_API_KEY.",
		}
	}

	log.Printf("[MUSE-CLI VALIDATION SUCCESS] Got response: %s", responseText)
	return llm.APIKeyValidationResponse{
		Valid:   true,
		Message: fmt.Sprintf("Muse CLI is working. Response: %s", responseText),
	}
}

// validateCursorCLI validates Cursor Agent CLI through the real tmux adapter path.
func validateCursorCLI(apiKey, modelID string) llm.APIKeyValidationResponse {
	log.Printf("[CURSOR-CLI VALIDATION] Starting CLI validation")

	cursorPath, err := exec.LookPath("cursor-agent")
	if err != nil {
		log.Printf("[CURSOR-CLI VALIDATION] CLI not found on PATH: %v", err)
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Cursor Agent CLI not found. Install it with: curl https://cursor.com/install -fsS | bash",
		}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		log.Printf("[CURSOR-CLI VALIDATION] tmux not found on PATH: %v", err)
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "tmux not found. Cursor CLI integration requires tmux for interactive mode.",
		}
	}
	log.Printf("[CURSOR-CLI VALIDATION] CLI found at: %s", cursorPath)

	if modelID == "" {
		modelID = "cursor-cli"
	}
	if strings.TrimSpace(apiKey) == "" && strings.TrimSpace(os.Getenv("CURSOR_API_KEY")) == "" {
		authenticated, conclusive := cursorCLILocalAuthState()
		if conclusive && !authenticated {
			return llm.APIKeyValidationResponse{
				Valid:   false,
				Message: cursorCLILoginRequiredMessage(),
			}
		}
		if !conclusive {
			log.Printf("[CURSOR-CLI VALIDATION] Auth status probe was inconclusive; continuing with real adapter validation")
		}
	}

	workspaceDir, err := os.MkdirTemp("", "cursor-cli-validation-*")
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Could not create a temporary workspace for Cursor CLI validation.",
		}
	}
	defer os.RemoveAll(workspaceDir)

	keys := &llm.ProviderAPIKeys{}
	if apiKey == "" {
		apiKey = os.Getenv("CURSOR_API_KEY")
	}
	if strings.TrimSpace(apiKey) != "" {
		keys.CursorCLI = &apiKey
	}

	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderCursorCLI,
		ModelID:  modelID,
		APIKeys:  keys,
		Context:  context.Background(),
	})
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Failed to initialize Cursor CLI: %v", err),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly: Cursor CLI is working."},
			},
		},
	}, llm.WithCursorWorkingDir(workspaceDir))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return llm.APIKeyValidationResponse{
				Valid:   false,
				Message: "Cursor CLI timed out after 90s. Check that you are authenticated with Cursor Agent CLI.",
			}
		}
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Cursor CLI error: %s", strings.TrimSpace(err.Error())),
		}
	}

	responseText := ""
	if resp != nil && len(resp.Choices) > 0 {
		responseText = strings.TrimSpace(resp.Choices[0].Content)
	}
	if responseText == "" {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Cursor CLI returned an empty response. Check authentication with 'cursor-agent login'.",
		}
	}

	log.Printf("[CURSOR-CLI VALIDATION SUCCESS] Got response: %s", responseText)
	return llm.APIKeyValidationResponse{
		Valid:   true,
		Message: fmt.Sprintf("Cursor CLI is working. Response: %s", responseText),
	}
}

// validatePiCLI validates Pi CLI through the real tmux marker adapter path.
func validatePiCLI(apiKey, modelID string, options map[string]interface{}) llm.APIKeyValidationResponse {
	log.Printf("[PI-CLI VALIDATION] Starting CLI validation")

	runtimePath, err := runtimeAvailableForProvider("pi-cli")
	if err != nil {
		log.Printf("[PI-CLI VALIDATION] runtime not found: %v", err)
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Pi CLI not found. Install with: npm install -g @earendil-works/pi-coding-agent, or ensure npx is available.",
		}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		log.Printf("[PI-CLI VALIDATION] tmux not found on PATH: %v", err)
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "tmux not found. Pi CLI integration requires tmux for interactive mode.",
		}
	}
	log.Printf("[PI-CLI VALIDATION] runtime available: %s", runtimePath)

	if strings.TrimSpace(modelID) == "" || strings.EqualFold(strings.TrimSpace(modelID), "pi-cli") {
		modelID = "google/gemini-3.8-flash"
	}

	workspaceDir, err := os.MkdirTemp("", "pi-cli-validation-*")
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Could not create a temporary workspace for Pi CLI validation.",
		}
	}
	defer os.RemoveAll(workspaceDir)

	keys := &llm.ProviderAPIKeys{}
	if strings.TrimSpace(apiKey) == "" {
		apiKey = selectPiAPIKeyForModel(buildProviderAPIKeysFromEnv(), modelID)
	}
	if strings.TrimSpace(apiKey) != "" {
		piProvider := piProviderFromModelID(modelID)
		keys.PiProviderKeys = map[string]string{piProvider: strings.TrimSpace(apiKey)}
		if piProvider == "google" || piProvider == "google-vertex" {
			keys.PiCLI = &apiKey
		}
	}

	model, err := llm.InitializeLLM(llm.Config{
		Provider: llm.ProviderPiCLI,
		ModelID:  modelID,
		APIKeys:  keys,
		Context:  context.Background(),
	})
	if err != nil {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Failed to initialize Pi CLI: %v", err),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	callOpts := []llmtypes.CallOption{llm.WithPiWorkingDir(workspaceDir)}
	if options != nil {
		if provider, ok := options["pi_provider"].(string); ok && strings.TrimSpace(provider) != "" {
			callOpts = append(callOpts, llm.WithPiProvider(provider))
		}
	}

	resp, err := model.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly: Pi CLI is working."},
			},
		},
	}, callOpts...)
	if handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp); ok {
		llm.ClosePiCLIInteractiveSessionByTmux(handle.TmuxSession, "validation cleanup")
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return llm.APIKeyValidationResponse{
				Valid:   false,
				Message: "Pi CLI timed out after 120s. Check that the selected model and API key are valid.",
			}
		}
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: fmt.Sprintf("Pi CLI error: %s", strings.TrimSpace(err.Error())),
		}
	}

	responseText := ""
	if resp != nil && len(resp.Choices) > 0 {
		responseText = strings.TrimSpace(resp.Choices[0].Content)
	}
	if responseText == "" {
		return llm.APIKeyValidationResponse{
			Valid:   false,
			Message: "Pi CLI returned an empty response. Check Pi provider/model/API-key configuration.",
		}
	}

	log.Printf("[PI-CLI VALIDATION SUCCESS] Got response: %s", responseText)
	return llm.APIKeyValidationResponse{
		Valid:   true,
		Message: fmt.Sprintf("Pi CLI is working. Response: %s", responseText),
	}
}

// handleGetModelMetadata returns metadata for all available models across providers
func (api *StreamingAPI) handleGetModelMetadata(w http.ResponseWriter, r *http.Request) {
	models := allProviderModelMetadata()

	response := map[string]interface{}{
		"models": models,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// handleGetAzureDeployedModels is retained only because its route is still
// registered; Azure is no longer an offered provider.
func (api *StreamingAPI) handleGetAzureDeployedModels(w http.ResponseWriter, r *http.Request) {
	http.Error(w, unsupportedCodingAgentProviderMessage("LLM", "azure"), http.StatusGone)
}

// lockedPresetLLMConfig is a workflow's saved LLM config as it actually runs
// under LLM_CONFIG_LOCKED with an explicitly published list: every role --
// Builder, Pulse and the three execution tiers -- runs the published default
// unless the saved role is itself on the published list.
// Without the lock (or without a published list) the config is
// returned untouched. The result is runtime-only; nothing writes it back to
// workflow.json, so lifting the lock restores the workflow's own choices.
//
// Why here and not in handleQuery's primary-config check: the Builder chat,
// scheduled runs and step execution take their models from the manifest's
// llm_config, not from the request's primary config, so a locked deployment
// still ran a synced workflow on claude-sonnet-5 (RTS, 2026-09-03).
func lockedPresetLLMConfig(cfg *workflowtypes.PresetLLMConfig) *workflowtypes.PresetLLMConfig {
	if !isGlobalLLMConfigLocked() || !publishedLLMListConfigured() {
		return cfg
	}
	defaults := llm.GetLLMDefaults()
	published := getDefaultPublishedLLMs(true, defaults.PrimaryConfig)
	if len(published) == 0 {
		return cfg
	}
	defProvider, _ := published[0]["provider"].(string)
	defModel, _ := published[0]["model_id"].(string)
	defProvider, defModel = strings.TrimSpace(defProvider), strings.TrimSpace(defModel)
	if defProvider == "" || defModel == "" {
		return cfg
	}
	out := &workflowtypes.PresetLLMConfig{SchemaVersion: 2}
	if cfg != nil {
		copied := *cfg
		out = &copied
	}
	// A coding-agent provider (Cursor, Claude Code, Codex, Pi) carries its own
	// role profile -- Builder/High, Medium, Low and Pulse models chosen for
	// that provider (GetCodingAgentDefaultTierModels). Locking to such a
	// provider means locking to that profile, not flattening every role onto
	// one hardcoded model, so the tiers keep meaning something. Cursor's
	// profile is all-auto as of 2026-09-03: a live RTS run hit
	// quota_exhausted on the previously-pinned grok-4.6 with no fallback.
	if _, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(defProvider)); ok {
		out.Mode = workflowtypes.LLMConfigModeProviderProfile
		out.Provider = defProvider
		out.BuilderLLM, out.PulseLLM, out.TieredConfig = nil, nil, nil
		return out
	}
	// Any other published provider has no role profile: every role runs the
	// published model unless the saved role is itself on the published list.
	role := func(saved *workflowtypes.AgentLLMConfig) *workflowtypes.AgentLLMConfig {
		if saved != nil && publishedLLMListContains(strings.TrimSpace(saved.Provider), strings.TrimSpace(saved.ModelID), defaults.PrimaryConfig) {
			kept := *saved

			return &kept
		}
		return &workflowtypes.AgentLLMConfig{Provider: defProvider, ModelID: defModel}
	}
	var savedBuilder, savedPulse, t1, t2, t3 *workflowtypes.AgentLLMConfig
	if cfg != nil {
		savedBuilder, savedPulse = cfg.BuilderLLM, cfg.PulseLLM
		if cfg.TieredConfig != nil {
			t1, t2, t3 = cfg.TieredConfig.Tier1, cfg.TieredConfig.Tier2, cfg.TieredConfig.Tier3
		}
	}
	out.Mode = workflowtypes.LLMConfigModeExplicit
	out.Provider = ""
	out.BuilderLLM = role(savedBuilder)
	out.PulseLLM = role(savedPulse)
	out.TieredConfig = &workflowtypes.TieredLLMConfig{Tier1: role(t1), Tier2: role(t2), Tier3: role(t3)}
	return out
}
