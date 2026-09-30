package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/utils"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// providerManifestEntry is the per-provider metadata returned by the manifest endpoint.
type providerManifestEntry struct {
	ID                    string                            `json:"id"`
	DisplayName           string                            `json:"display_name"`
	Description           string                            `json:"description"`
	Kind                  string                            `json:"kind"`
	IntegrationKind       string                            `json:"integration_kind"`
	ModelSelectionMode    string                            `json:"model_selection_mode"`
	AuthDescription       string                            `json:"auth_description"`
	RuntimeCommand        string                            `json:"runtime_command,omitempty"`
	RuntimeAvailable      *bool                             `json:"runtime_available,omitempty"`
	InstallCommand        string                            `json:"install_command,omitempty"`
	InstalledVersion      string                            `json:"installed_version,omitempty"`
	MinSupportedVersion   string                            `json:"min_supported_version,omitempty"`
	UpdateStatus          string                            `json:"update_status,omitempty"`
	AuthConfigured        bool                              `json:"auth_configured"`
	AuthSource            string                            `json:"auth_source,omitempty"`
	Usable                bool                              `json:"usable"`
	SetupHint             string                            `json:"setup_hint,omitempty"`
	RequiresAPIKey        bool                              `json:"requires_api_key"`
	SupportsDynamicModels bool                              `json:"supports_dynamic_models"`
	DefaultModelID        string                            `json:"default_model_id"`
	DefaultTierModels     *llm.CodingAgentDefaultTierModels `json:"default_tier_models,omitempty"`
	Models                []*llmtypes.ModelMetadata         `json:"models"`
	Capabilities          []string                          `json:"capabilities"`
	CodingAgent           *providerCodingAgentInfo          `json:"coding_agent,omitempty"`
	APIKeyEnv             string                            `json:"api_key_env,omitempty"`
	APIKeyURL             string                            `json:"api_key_url,omitempty"`
}

type providerCodingAgentInfo struct {
	Transport               string `json:"transport"`
	SupportsLiveInput       bool   `json:"supports_live_input"`
	SupportsInterrupt       bool   `json:"supports_interrupt"`
	SupportsStatusLine      bool   `json:"supports_status_line"`
	UsesMCPBridge           bool   `json:"uses_mcp_bridge"`
	SupportsBridgeOnlyTools bool   `json:"supports_bridge_only_tools"`
	SupportsNativeResume    bool   `json:"supports_native_resume"`
	HandlesTmuxSessionLoss  bool   `json:"handles_tmux_session_loss"`
}

type integrationKindInfo struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type providerManifestResponse struct {
	Providers        []providerManifestEntry        `json:"providers"`
	IntegrationKinds map[string]integrationKindInfo `json:"integration_kinds"`
	ProviderOrder    []string                       `json:"provider_order"`
}

type dynamicModelEntry struct {
	ModelID       string  `json:"model_id"`
	ModelName     string  `json:"model_name"`
	Group         string  `json:"group,omitempty"`
	IsDefault     bool    `json:"is_default,omitempty"`
	IsFree        bool    `json:"is_free,omitempty"`
	ContextWindow int     `json:"context_window,omitempty"`
	CostInput     float64 `json:"cost_input,omitempty"`
	CostOutput    float64 `json:"cost_output,omitempty"`
}

type dynamicModelsResponse struct {
	Provider           string              `json:"provider"`
	ModelSelectionMode string              `json:"model_selection_mode"`
	Models             []dynamicModelEntry `json:"models"`
	Groups             []string            `json:"groups,omitempty"`
	SupportsCustom     bool                `json:"supports_custom_model,omitempty"`
	CustomModelHint    string              `json:"custom_model_hint,omitempty"`
	Source             string              `json:"source"`
	CachedAt           string              `json:"cached_at,omitempty"`
	CacheTTLSeconds    int                 `json:"cache_ttl_seconds,omitempty"`
	Error              string              `json:"error,omitempty"`
}

// --- Provider static metadata ---

type providerStaticInfo struct {
	displayName     string
	description     string
	integrationKind string
	authDescription string
	requiresAPIKey  bool
	apiKeyEnv       string
	apiKeyURL       string
}

var providerStaticInfoMap = map[string]providerStaticInfo{
	"codex-cli": {
		displayName:     "OpenAI Codex CLI",
		description:     "Uses the locally installed codex CLI. Authentication via codex login or CODEX_API_KEY.",
		integrationKind: "coding_agent",
		authDescription: "Local CLI (API key optional)",
		requiresAPIKey:  false,
	},
	"cursor-cli": {
		displayName:     "Cursor CLI",
		description:     "Uses cursor-agent through tmux. Supports 100+ models via Cursor subscription.",
		integrationKind: "coding_agent",
		authDescription: "Local CLI (API key optional)",
		requiresAPIKey:  false,
	},
	"pi-cli": {
		displayName:     "Pi CLI",
		description:     "Uses Pi CLI through tmux marker transport for Pi model IDs across Gemini, Z.AI, Kimi, MiniMax, DeepSeek, and custom Pi providers.",
		integrationKind: "coding_agent",
		authDescription: "Local CLI (Pi provider API key)",
		requiresAPIKey:  false,
		apiKeyEnv:       "Provider-specific: GEMINI_API_KEY, ZAI_API_KEY, KIMI_API_KEY, MINIMAX_API_KEY, DEEPSEEK_API_KEY, etc.",
		apiKeyURL:       "https://pi.dev/docs/latest/providers",
	},
	"muse-cli": {
		displayName:     "Muse",
		description:     "Uses the locally installed muse CLI (Meta). Authentication via muse login or META_API_KEY.",
		integrationKind: "coding_agent",
		authDescription: "Local CLI (Meta login or API key)",
		requiresAPIKey:  false,
		apiKeyEnv:       "META_API_KEY",
	},
	"agy-cli": {
		displayName:     "Antigravity CLI (Alpha)",
		description:     "Alpha: uses the locally installed agy CLI (Google Antigravity). The terminal shows live progress; assistant text and tool receipts appear after each turn. Sign in with Google in the guided terminal, or use API-key mode for unattended setups (see the setup guide).",
		integrationKind: "coding_agent",
		authDescription: "Local CLI (Google sign-in)",
		requiresAPIKey:  false,
	},
	"claude-code": {
		displayName:     "Claude Code",
		description:     "Uses the locally installed claude CLI. Handles its own authentication, model selection, and tool execution.",
		integrationKind: "coding_agent",
		authDescription: "Local CLI (no API key)",
		requiresAPIKey:  false,
	},
}

func providerModelSelectionMode(provider string) string {
	switch provider {
	case "cursor-cli", "pi-cli":
		return "dynamic"
	default:
		return "fixed_tier"
	}
}

func providerKind(provider string) string {
	if common.IsCLIProvider(provider) {
		return "local_cli"
	}
	return "api"
}

func providerCodingAgentManifestInfo(provider, modelID string) *providerCodingAgentInfo {
	contract, ok := llm.GetCodingAgentProviderContract(llm.Provider(provider), modelID)
	if !ok {
		return nil
	}
	return &providerCodingAgentInfo{
		Transport:               string(contract.Transport),
		SupportsLiveInput:       contract.SupportsLiveInput,
		SupportsInterrupt:       contract.SupportsInterrupt,
		SupportsStatusLine:      contract.SupportsStatusLine,
		UsesMCPBridge:           contract.UsesMCPBridge,
		SupportsBridgeOnlyTools: contract.SupportsBridgeOnlyTools,
		SupportsNativeResume:    contract.SupportsNativeResume,
		HandlesTmuxSessionLoss:  contract.HandlesTmuxSessionLoss,
	}
}

// providerInstallCommand returns the SDK contract's allowlisted install
// command for coding providers, or "" for API providers.
func providerInstallCommand(provider string) string {
	contract, ok := llm.GetCodingAgentProviderContract(llm.Provider(provider), "")
	if !ok {
		return ""
	}
	return contract.InstallCommand
}

// codingCLIVersion describes one probed CLI for the manifest.
type codingCLIVersion struct {
	installed string
	floor     string
	status    string // supported|unsupported|unknown
}

// probeCodingCLIVersions probes installed versions for coding providers in
// parallel. "unknown" means the probe failed (missing binary, timeout);
// the page shows RuntimeAvailable=false alongside it.
func probeCodingCLIVersions(ctx context.Context, providers []string) map[string]codingCLIVersion {
	out := make(map[string]codingCLIVersion, len(providers))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, provider := range providers {
		contract, ok := llm.GetCodingAgentProviderContract(llm.Provider(provider), "")
		if !ok {
			continue
		}
		wg.Add(1)
		go func(provider, floor, binary string) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			version, err := cachedCodingCLIVersion(probeCtx, provider, binary)
			result := codingCLIVersion{floor: floor, status: "unknown"}
			if err == nil {
				result.installed = version
				result.status = cliUpdateStatus(version, floor)
			}
			mu.Lock()
			out[provider] = result
			mu.Unlock()
		}(provider, contract.MinCLIVersion, contract.RuntimeBinary)
	}
	wg.Wait()
	return out
}

// cliUpdateStatus maps an installed version against the certified floor.
func cliUpdateStatus(installed, floor string) string {
	if strings.TrimSpace(installed) == "" {
		return "unknown"
	}
	if llm.CompareCodingAgentCLIVersions(installed, floor) < 0 {
		return "unsupported"
	}
	return "supported"
}

func providerWorkflowTierDefaults(provider string) *llm.CodingAgentDefaultTierModels {
	if providerStaticInfoMap[provider].integrationKind != "coding_agent" {
		return nil
	}
	defaults, ok := llm.GetCodingAgentDefaultTierModels(llm.Provider(provider))
	if !ok {
		return nil
	}
	return defaults
}

func allProviderModelMetadata() []*llmtypes.ModelMetadata {
	base := utils.GetAllModelMetadata()
	out := make([]*llmtypes.ModelMetadata, 0, len(base)+8)
	seen := make(map[string]struct{}, len(base)+8)
	appendModel := func(model *llmtypes.ModelMetadata) {
		if model == nil || strings.TrimSpace(model.Provider) == "" || strings.TrimSpace(model.ModelID) == "" {
			return
		}
		key := strings.ToLower(strings.TrimSpace(model.Provider)) + "\x00" + strings.TrimSpace(model.ModelID)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	for _, model := range base {
		appendModel(model)
	}
	for _, model := range claudecode.GetAllClaudeCodeModels() {
		appendModel(model)
	}
	return out
}

func providerModelMetadata(provider string) []*llmtypes.ModelMetadata {
	provider = strings.ToLower(strings.TrimSpace(provider))
	var models []*llmtypes.ModelMetadata
	for _, model := range allProviderModelMetadata() {
		if model == nil || strings.ToLower(strings.TrimSpace(model.Provider)) != provider {
			continue
		}
		models = append(models, model)
	}
	return models
}

func providerModelIDs(provider string) []string {
	models := providerModelMetadata(provider)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		if model == nil || strings.TrimSpace(model.ModelID) == "" {
			continue
		}
		ids = append(ids, model.ModelID)
	}
	return ids
}

func claudeCodeCapabilityModels() []string {
	ids := providerModelIDs("claude-code")
	if len(ids) == 0 {
		// Mirrors claudecode.knownClaudeCodeModels (multi-llm-provider-go) as a
		// safety net if that metadata source is ever unavailable. Keep in sync
		// with it -- this fallback going stale is exactly what happened when
		// the 4.x model family was retired in favor of Claude 5 (this list
		// still named claude-opus-4-8/4-7/4-6 and claude-sonnet-4-6 for
		// months after they stopped being offered).
		return []string{
			"claude-code",
			"claude-fable-5-1",
			"claude-opus-5-5",
			"claude-sonnet-5-5",
			"claude-haiku-4-5-20251001",
		}
	}
	return ids
}

// handleGetProviderManifest returns the full provider manifest for the frontend.
func (api *StreamingAPI) handleGetProviderManifest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys := MergedProviderAPIKeys(ctx)
	allMetadata := allProviderModelMetadata()

	metadataByProvider := map[string][]*llmtypes.ModelMetadata{}
	for _, m := range allMetadata {
		if m == nil {
			continue
		}
		metadataByProvider[m.Provider] = append(metadataByProvider[m.Provider], m)
	}

	capabilitiesByProvider := buildProviderCapabilities(ctx)

	providerOrder := []string{
		"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli", "agy-cli",
	}

	supported := getSupportedProviders()
	supportedSet := make(map[string]bool, len(supported))
	for _, p := range supported {
		supportedSet[p] = true
	}

	cliVersions := probeCodingCLIVersions(ctx, providerOrder)

	entries := make([]providerManifestEntry, 0, len(providerOrder))
	for _, provider := range providerOrder {
		if !supportedSet[provider] {
			continue
		}

		info, ok := providerStaticInfoMap[provider]
		if !ok {
			continue
		}

		authConfigured, authSource := providerAuthConfigured(provider, keys)
		usable, runtimeCommand, runtimeOK := providerUsable(provider, authConfigured)

		setupHint := ""
		if runtimeOK != nil && !*runtimeOK {
			setupHint = discoverySetupHint(provider, true)
		} else if !usable {
			setupHint = discoverySetupHint(provider, false)
		}

		defaultModel := llm.GetDefaultModel(llm.Provider(provider))
		if defaultModel == "" {
			opts := discoveryModelOptions(provider)
			if len(opts) > 0 {
				defaultModel = opts[0]
			}
		}

		models := metadataByProvider[provider]
		if models == nil {
			models = []*llmtypes.ModelMetadata{}
		}

		selectionMode := providerModelSelectionMode(provider)

		caps := capabilitiesByProvider[provider]
		if caps == nil {
			caps = []string{}
		}

		entry := providerManifestEntry{
			ID:                    provider,
			DisplayName:           info.displayName,
			Description:           info.description,
			Kind:                  providerKind(provider),
			IntegrationKind:       info.integrationKind,
			ModelSelectionMode:    selectionMode,
			AuthDescription:       info.authDescription,
			RuntimeCommand:        runtimeCommand,
			RuntimeAvailable:      runtimeOK,
			InstallCommand:        providerInstallCommand(provider),
			InstalledVersion:      cliVersions[provider].installed,
			MinSupportedVersion:   cliVersions[provider].floor,
			UpdateStatus:          cliVersions[provider].status,
			AuthConfigured:        authConfigured,
			AuthSource:            authSource,
			Usable:                usable,
			SetupHint:             setupHint,
			RequiresAPIKey:        info.requiresAPIKey,
			SupportsDynamicModels: selectionMode == "dynamic",
			DefaultModelID:        defaultModel,
			DefaultTierModels:     providerWorkflowTierDefaults(provider),
			Models:                models,
			Capabilities:          caps,
			CodingAgent:           providerCodingAgentManifestInfo(provider, defaultModel),
			APIKeyEnv:             info.apiKeyEnv,
			APIKeyURL:             info.apiKeyURL,
		}
		entries = append(entries, entry)
	}

	resp := providerManifestResponse{
		Providers: entries,
		IntegrationKinds: map[string]integrationKindInfo{
			"coding_agent": {Label: "Coding Agents", Description: "Local agent CLI runtimes"},
		},
		ProviderOrder: providerOrder,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Dynamic model listing ---

var (
	dynamicModelCache   = map[string]*cachedDynamicModels{}
	dynamicModelCacheMu sync.RWMutex
)

type cachedDynamicModels struct {
	response  *dynamicModelsResponse
	fetchedAt time.Time
}

const dynamicModelCacheTTL = 5 * time.Minute

func (api *StreamingAPI) handleGetProviderModels(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	provider := vars["provider"]
	if provider == "" {
		http.Error(w, "provider is required", http.StatusBadRequest)
		return
	}

	mode := providerModelSelectionMode(provider)

	if mode == "dynamic" {
		full := r.URL.Query().Get("full") == "true"
		availableOnly := r.URL.Query().Get("available_only") == "true"
		resp := getDynamicModels(provider, full, availableOnly)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Fixed tier / API — return from metadata registry plus coding-agent model metadata.
	allMetadata := allProviderModelMetadata()
	models := make([]dynamicModelEntry, 0)
	for _, m := range allMetadata {
		if m == nil || m.Provider != provider {
			continue
		}
		defaultModel := llm.GetDefaultModel(llm.Provider(provider))
		models = append(models, dynamicModelEntry{
			ModelID:       m.ModelID,
			ModelName:     m.ModelName,
			IsDefault:     m.ModelID == defaultModel,
			ContextWindow: m.ContextWindow,
			CostInput:     m.InputCostPer1MTokens,
			CostOutput:    m.OutputCostPer1MTokens,
		})
	}

	resp := &dynamicModelsResponse{
		Provider:           provider,
		ModelSelectionMode: mode,
		Models:             models,
		Source:             "metadata_registry",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func getDynamicModels(provider string, full, availableOnly bool) *dynamicModelsResponse {
	cacheKey := provider
	if full {
		cacheKey = provider + ":full"
	}
	if availableOnly {
		cacheKey = provider + ":available"
	}

	dynamicModelCacheMu.RLock()
	cached, ok := dynamicModelCache[cacheKey]
	dynamicModelCacheMu.RUnlock()

	cacheTTL := dynamicModelCacheTTL
	if availableOnly {
		cacheTTL = 30 * time.Second
	}
	if ok && time.Since(cached.fetchedAt) < cacheTTL {
		return cached.response
	}

	var resp *dynamicModelsResponse
	switch provider {
	case "cursor-cli":
		resp = fetchCursorCLIModels()
	case "pi-cli":
		if availableOnly {
			resp = fetchAvailablePiCLIModels()
		} else {
			resp = fetchPiCLIModels(full)
		}
	default:
		resp = &dynamicModelsResponse{
			Provider:           provider,
			ModelSelectionMode: "dynamic",
			Models:             []dynamicModelEntry{},
			Source:             "unknown",
		}
	}

	dynamicModelCacheMu.Lock()
	dynamicModelCache[cacheKey] = &cachedDynamicModels{
		response:  resp,
		fetchedAt: time.Now(),
	}
	dynamicModelCacheMu.Unlock()

	return resp
}

func fetchAvailablePiCLIModels() *dynamicModelsResponse {
	resp := &dynamicModelsResponse{
		Provider:           "pi-cli",
		ModelSelectionMode: "dynamic",
		Models:             []dynamicModelEntry{},
		Source:             "cli_available",
		CacheTTLSeconds:    30,
		CachedAt:           time.Now().UTC().Format(time.RFC3339),
	}
	models, err := listPiCLIModelsFn()
	if err != nil {
		resp.Source = "cli_available_error"
		resp.Error = err.Error()
		return resp
	}
	resp.Models = models
	resp.Groups = dynamicModelGroups(models)
	return resp
}

func invalidatePiProviderCaches() {
	dynamicModelCacheMu.Lock()
	delete(dynamicModelCache, "pi-cli")
	delete(dynamicModelCache, "pi-cli:full")
	delete(dynamicModelCache, "pi-cli:available")
	dynamicModelCacheMu.Unlock()

	piCLIAuthProbeCache.Lock()
	piCLIAuthProbeCache.checkedAt = time.Time{}
	piCLIAuthProbeCache.authenticated = false
	piCLIAuthProbeCache.conclusive = false
	piCLIAuthProbeCache.Unlock()
}

func fetchCursorCLIModels() *dynamicModelsResponse {
	resp := &dynamicModelsResponse{
		Provider:           "cursor-cli",
		ModelSelectionMode: "dynamic",
		SupportsCustom:     true,
		CustomModelHint:    "Enter a model ID (e.g., gpt-5.5-high)",
		Source:             "cli_dynamic",
		CacheTTLSeconds:    300,
		CachedAt:           time.Now().UTC().Format(time.RFC3339),
	}

	binPath, err := runtimeAvailableForProvider("cursor-cli")
	if err != nil {
		resp.Source = "fallback_metadata"
		resp.Models = cursorFallbackModels()
		return resp
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	listCmd := exec.CommandContext(ctx, binPath, "--list-models")
	listCmd.Env = append(minimalChildEnv(), passthroughChildEnv("CURSOR_API_KEY")...)
	out, err := listCmd.Output()
	if err != nil {
		resp.Source = "fallback_metadata"
		resp.Models = cursorFallbackModels()
		return resp
	}

	models, groups := parseCursorModelList(string(out))
	if len(models) == 0 {
		resp.Source = "fallback_metadata"
		resp.Models = cursorFallbackModels()
		return resp
	}

	resp.Models = models
	resp.Groups = groups
	return resp
}

func parseCursorModelList(output string) ([]dynamicModelEntry, []string) {
	lines := strings.Split(output, "\n")
	models := make([]dynamicModelEntry, 0, len(lines))
	groupSet := map[string]bool{}
	groupOrder := []string{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Available") || strings.HasPrefix(line, "Tip:") {
			continue
		}

		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 {
			continue
		}

		modelID := strings.TrimSpace(parts[0])
		modelName := strings.TrimSpace(parts[1])
		group := inferCursorModelGroup(modelID, modelName)

		if !groupSet[group] {
			groupSet[group] = true
			groupOrder = append(groupOrder, group)
		}

		models = append(models, dynamicModelEntry{
			ModelID:   modelID,
			ModelName: modelName,
			Group:     group,
			IsDefault: modelID == "composer-2-fast" || modelID == "auto",
		})
	}

	return models, groupOrder
}

func inferCursorModelGroup(modelID, _ string) string {
	id := strings.ToLower(modelID)

	switch {
	case strings.HasPrefix(id, "composer"):
		return "Composer"
	case strings.Contains(id, "codex"):
		return "Codex"
	case strings.HasPrefix(id, "gpt-5.5"):
		return "GPT-5.5"
	case strings.HasPrefix(id, "gpt-5.4-nano"):
		return "GPT-5.4 Nano"
	case strings.HasPrefix(id, "gpt-5.4-mini"):
		return "GPT-5.4 Mini"
	case strings.HasPrefix(id, "gpt-5.4"):
		return "GPT-5.4"
	case strings.HasPrefix(id, "gpt-5.3"):
		return "GPT-5.3"
	case strings.HasPrefix(id, "gpt-5.2"):
		return "GPT-5.2"
	case strings.HasPrefix(id, "gpt-5.1"):
		return "GPT-5.1"
	case strings.HasPrefix(id, "gpt-5"):
		return "GPT-5"
	case strings.Contains(id, "claude-opus-5") || strings.Contains(id, "opus-5"):
		return "Claude Opus 5"
	case strings.Contains(id, "claude-opus-4-8") || strings.Contains(id, "opus-4-8"):
		return "Claude Opus 4.8"
	case strings.Contains(id, "claude-opus-4-7") || strings.Contains(id, "opus-4-7"):
		return "Claude Opus 4.7"
	case strings.Contains(id, "claude-4.6-opus") || strings.Contains(id, "opus-4-6"):
		return "Claude Opus 4.6"
	case strings.Contains(id, "claude-4.5-opus"):
		return "Claude Opus 4.5"
	case strings.Contains(id, "claude-sonnet-5-5"):
		return "Claude Sonnet 5.5"
	case strings.Contains(id, "claude-sonnet-5") || strings.Contains(id, "claude-5-sonnet"):
		return "Claude Sonnet 5"
	case strings.Contains(id, "claude-4.6-sonnet"):
		return "Claude Sonnet 4.6"
	case strings.Contains(id, "claude-4.5-sonnet"):
		return "Claude Sonnet 4.5"
	case strings.Contains(id, "claude-4-sonnet") || strings.Contains(id, "claude-sonnet-4"):
		return "Claude Sonnet 4"
	case strings.Contains(id, "claude"):
		return "Claude"
	case strings.Contains(id, "gemini"):
		return "Gemini"
	case strings.Contains(id, "grok"):
		return "Grok"
	case strings.Contains(id, "kimi"):
		return "Kimi"
	default:
		return "Other"
	}
}

func cursorFallbackModels() []dynamicModelEntry {
	allMetadata := utils.GetAllModelMetadata()
	models := make([]dynamicModelEntry, 0)
	for _, m := range allMetadata {
		if m == nil || m.Provider != "cursor-cli" {
			continue
		}
		models = append(models, dynamicModelEntry{
			ModelID:   m.ModelID,
			ModelName: m.ModelName,
			Group:     "Default",
			IsDefault: m.ModelID == "cursor-cli",
		})
	}
	return models
}

func fetchPiCLIModels(full bool) *dynamicModelsResponse {
	var models []dynamicModelEntry
	var source string

	if full {
		// Curated shortlist first (keeps pinned picks like the free Ox Alpha
		// model prominent), then the live OpenRouter catalog -- fetched fresh
		// from OpenRouter itself, so it carries real pricing/IsFree data --
		// and only then pi's own --list-models output. Order matters here:
		// mergePiModelEntries keeps the FIRST occurrence per ModelID, and
		// pi's own listing can legitimately include the same OpenRouter model
		// IDs with no pricing info at all (it doesn't know IsFree). Merging
		// cliModels in last means it can only ever ADD models neither other
		// source already covered, never silently mask a live-verified
		// IsFree=true with a pricing-blind duplicate.
		models = piFallbackModels()
		source = "curated_latest"
		if orModels, orErr := fetchOpenRouterCatalog(); orErr == nil && len(orModels) > 0 {
			models = mergePiModelEntries(models, orModels)
			source += "+openrouter_live"
		}
		if cliModels, err := listPiCLIModelsFn(); err == nil && len(cliModels) > 0 {
			models = mergePiModelEntries(models, cliModels)
			source += "+cli_full_catalog"
		}
	} else {
		models = piFallbackModels()
		source = "curated_latest"
	}

	if _, err := runtimeAvailableForProvider("pi-cli"); err == nil && !full {
		source = "curated_latest_runtime_available"
	}

	resp := &dynamicModelsResponse{
		Provider:           "pi-cli",
		ModelSelectionMode: "dynamic",
		Models:             models,
		Groups:             dynamicModelGroups(models),
		SupportsCustom:     true,
		CustomModelHint:    "Enter any Pi model as provider/model, e.g. google/gemini-3.8-flash, zai/glm-5.3, or openrouter/minimax/minimax-m3-20260531",
		Source:             source,
		CacheTTLSeconds:    300,
		CachedAt:           time.Now().UTC().Format(time.RFC3339),
	}
	return resp
}

// openRouterModelsAPIURL is a package-level var (not a const) so a test can
// point it at an httptest.NewServer instead of the real OpenRouter API.
var openRouterModelsAPIURL = "https://openrouter.ai/api/v1/models"

type openRouterModelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int    `json:"context_length"`
		Pricing       struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
	} `json:"data"`
}

// fetchOpenRouterCatalog fetches OpenRouter's public, unauthenticated model
// catalog and maps it onto Pi's own "openrouter/<vendor>/<model>" model ID
// convention (OpenRouter's own id is already "<vendor>/<model>"; Pi's own
// "openrouter/" prefix is prepended the same way the existing hardcoded
// OpenRouter entries in piFallbackModels already do). A model is only
// IsFree when OpenRouter's own pricing reports both prompt and completion
// cost as exactly zero -- distinct from cost fields simply being unset,
// which the existing hardcoded shortlist entries mostly are (unknown cost,
// not confirmed free).
func fetchOpenRouterCatalog() ([]dynamicModelEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openRouterModelsAPIURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter models API returned status %d", resp.StatusCode)
	}

	var parsed openRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	models := make([]dynamicModelEntry, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		promptCost, _ := strconv.ParseFloat(m.Pricing.Prompt, 64)
		completionCost, _ := strconv.ParseFloat(m.Pricing.Completion, 64)
		models = append(models, dynamicModelEntry{
			ModelID:       "openrouter/" + m.ID,
			ModelName:     m.Name,
			Group:         "OpenRouter",
			IsFree:        promptCost == 0 && completionCost == 0,
			ContextWindow: m.ContextLength,
			CostInput:     promptCost * 1_000_000,
			CostOutput:    completionCost * 1_000_000,
		})
	}
	return models, nil
}

// listPiCLIModelsFn is a package-level var (not a direct call) so a test can
// substitute a canned pi --list-models result without shelling out to a real
// pi binary.
var listPiCLIModelsFn = listPiCLIModels

func listPiCLIModels() ([]dynamicModelEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runtimePath, err := runtimeAvailableForProvider("pi-cli")
	if err != nil {
		return nil, err
	}
	args := []string{"--list-models"}
	if filepath.Base(runtimePath) == "npx" {
		args = []string{"--yes", "@earendil-works/pi-coding-agent", "--list-models"}
	}
	cmd := exec.CommandContext(ctx, runtimePath, args...)
	cmd.Env = append(minimalChildEnv(), passthroughChildEnv("PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY")...)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parsePiCLIModelList(string(output)), nil
}

func parsePiCLIModelList(output string) []dynamicModelEntry {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	models := make([]dynamicModelEntry, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.EqualFold(fields[0], "provider") || strings.EqualFold(fields[0], "no") {
			continue
		}
		provider := strings.TrimSpace(fields[0])
		model := strings.TrimSpace(fields[1])
		if provider == "" || model == "" {
			continue
		}
		modelID := provider + "/" + model
		if seen[modelID] {
			continue
		}
		seen[modelID] = true
		entry := dynamicModelEntry{
			ModelID:   modelID,
			ModelName: piModelDisplayName(provider, model),
			Group:     piModelGroup(provider),
			IsDefault: modelID == picli.DefaultModelID,
		}
		if len(fields) >= 3 {
			entry.ContextWindow = parsePiCompactCount(fields[2])
		}
		models = append(models, entry)
	}
	return models
}

func mergePiModelEntries(primary, secondary []dynamicModelEntry) []dynamicModelEntry {
	merged := make([]dynamicModelEntry, 0, len(primary)+len(secondary))
	seen := map[string]bool{}
	for _, group := range [][]dynamicModelEntry{primary, secondary} {
		for _, model := range group {
			modelID := strings.TrimSpace(model.ModelID)
			if modelID == "" || seen[modelID] {
				continue
			}
			seen[modelID] = true
			merged = append(merged, model)
		}
	}
	return merged
}

func parsePiCompactCount(value string) int {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 0
	}
	multiplier := 1.0
	switch {
	case strings.HasSuffix(value, "K"):
		multiplier = 1_000
		value = strings.TrimSuffix(value, "K")
	case strings.HasSuffix(value, "M"):
		multiplier = 1_000_000
		value = strings.TrimSuffix(value, "M")
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return int(n * multiplier)
}

func piModelDisplayName(provider, model string) string {
	return model + " (" + provider + ")"
}

func piModelGroup(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google":
		return "Gemini"
	case "google-vertex":
		return "Google Vertex"
	case "anthropic":
		return "Anthropic"
	case "openai":
		return "OpenAI"
	case "openrouter":
		return "OpenRouter"
	case "bedrock":
		return "Amazon Bedrock"
	case "deepseek":
		return "DeepSeek"
	case "zai", "zai-coding-cn":
		return "Z.AI"
	case "minimax", "minimax-cn":
		return "MiniMax"
	case "kimi-coding", "moonshotai", "moonshotai-cn":
		return "Kimi"
	case "xai":
		return "xAI"
	case "nvidia":
		return "NVIDIA"
	default:
		if provider == "" {
			return "Other"
		}
		return provider
	}
}

func dynamicModelGroups(models []dynamicModelEntry) []string {
	seen := map[string]bool{}
	groups := make([]string, 0)
	for _, model := range models {
		group := strings.TrimSpace(model.Group)
		if group == "" {
			group = "Other"
		}
		if seen[group] {
			continue
		}
		seen[group] = true
		groups = append(groups, group)
	}
	return groups
}

func piFallbackModels() []dynamicModelEntry {
	additional := []dynamicModelEntry{
		{
			ModelID:       "zai-coding-cn/glm-5.3",
			ModelName:     "GLM-5.3 (CN)",
			Group:         "Z.AI",
			ContextWindow: 1000000,
			CostInput:     1.4,
			CostOutput:    4.4,
		},
		{
			ModelID:       "minimax/MiniMax-M3",
			ModelName:     "MiniMax M3",
			Group:         "MiniMax",
			ContextWindow: 512000,
			CostInput:     0.6,
			CostOutput:    2.4,
		},
		{
			ModelID:       "minimax-cn/MiniMax-M3",
			ModelName:     "MiniMax M3 (CN)",
			Group:         "MiniMax",
			ContextWindow: 512000,
			CostInput:     0.6,
			CostOutput:    2.4,
		},
		{
			ModelID:       "kimi-coding/k3",
			ModelName:     "Kimi K3",
			Group:         "Kimi",
			ContextWindow: 1048576,
		},
		{
			ModelID:       "deepseek/deepseek-v4-pro",
			ModelName:     "DeepSeek V4 Pro",
			Group:         "DeepSeek",
			ContextWindow: 1048576,
			CostInput:     0.435,
			CostOutput:    0.87,
		},
		{
			ModelID:       "deepseek/deepseek-v4-flash",
			ModelName:     "DeepSeek V4 Flash",
			Group:         "DeepSeek",
			ContextWindow: 1048576,
			CostInput:     0.14,
			CostOutput:    0.28,
		},
		{
			ModelID:       "openrouter/stealth/ox-alpha",
			ModelName:     "Ox Alpha",
			Group:         "OpenRouter",
			IsFree:        true,
			ContextWindow: 1048576,
		},
		{
			ModelID:   "openrouter/minimax/minimax-m3-20260531",
			ModelName: "MiniMax M3",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/deepseek/deepseek-v4-flash-20260423",
			ModelName: "DeepSeek V4 Flash",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/deepseek/deepseek-v4-pro-20260423",
			ModelName: "DeepSeek V4 Pro",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/openrouter/owl-alpha",
			ModelName: "Owl Alpha",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/anthropic/claude-4.8-opus-20260528",
			ModelName: "Claude 4.8 Opus",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/anthropic/claude-4.6-sonnet-20260217",
			ModelName: "Claude 4.6 Sonnet",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/openai/gpt-5.5-20260423",
			ModelName: "GPT-5.5",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/xiaomi/mimo-v2.5-pro-20260422",
			ModelName: "MiMo V2.5 Pro",
			Group:     "OpenRouter",
		},
		{
			ModelID:   "openrouter/moonshotai/kimi-k3",
			ModelName: "Kimi K3",
			Group:     "OpenRouter",
		},
	}
	return mergePiModelEntries(piProviderCatalogModels(), additional)
}

func piProviderCatalogModels() []dynamicModelEntry {
	metadata := picli.GetAllPiCLIModels()
	models := make([]dynamicModelEntry, 0, len(metadata))
	for _, model := range metadata {
		if model == nil || strings.TrimSpace(model.ModelID) == "" {
			continue
		}
		provider, _, _ := strings.Cut(model.ModelID, "/")
		group := piModelGroup(provider)
		name := strings.TrimSpace(model.ModelName)
		if strings.HasPrefix(name, "Pi CLI (") && strings.HasSuffix(name, ")") {
			name = strings.TrimSuffix(strings.TrimPrefix(name, "Pi CLI ("), ")")
		}
		models = append(models, dynamicModelEntry{
			ModelID:       model.ModelID,
			ModelName:     name,
			Group:         group,
			IsDefault:     model.ModelID == picli.DefaultModelID,
			ContextWindow: model.ContextWindow,
			CostInput:     model.InputCostPer1MTokens,
			CostOutput:    model.OutputCostPer1MTokens,
		})
	}
	return models
}

func piCuratedModelIDs() []string {
	models := piFallbackModels()
	ids := make([]string, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model.ModelID) != "" {
			ids = append(ids, model.ModelID)
		}
	}
	return ids
}
