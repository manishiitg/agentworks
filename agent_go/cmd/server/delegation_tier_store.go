package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

const (
	delegationTierConfigFilePath      = "config/delegation-tier-config.json"
	delegationTierConfigSchemaVersion = 2
)

func sanitizeDelegationTierConfig(config *virtualtools.DelegationTierConfig) *virtualtools.DelegationTierConfig {
	if config == nil {
		return nil
	}

	mode := strings.TrimSpace(config.Mode)
	provider := strings.TrimSpace(config.Provider)
	if mode == "" {
		if provider != "" && config.Main == nil && config.High == nil && config.Medium == nil && config.Low == nil {
			mode = "provider_profile"
		} else {
			mode = "explicit"
		}
	}
	if mode == "provider_profile" && provider != "" {
		return &virtualtools.DelegationTierConfig{
			SchemaVersion: delegationTierConfigSchemaVersion,
			Mode:          "provider_profile",
			Provider:      provider,
		}
	}

	result := &virtualtools.DelegationTierConfig{
		SchemaVersion: delegationTierConfigSchemaVersion,
		Mode:          "explicit",
	}
	hasAny := false

	if main := sanitizeTierModel(config.Main); main != nil {
		result.Main = main
		hasAny = true
	}
	if high := sanitizeTierModel(config.High); high != nil {
		result.High = high
		hasAny = true
	}
	if medium := sanitizeTierModel(config.Medium); medium != nil {
		result.Medium = medium
		hasAny = true
	}
	if low := sanitizeTierModel(config.Low); low != nil {
		result.Low = low
		hasAny = true
	}

	if len(config.Custom) > 0 {
		custom := make(map[string]*virtualtools.CustomTierModel)
		for slug, tier := range config.Custom {
			if tier == nil {
				continue
			}
			cleanSlug := strings.TrimSpace(slug)
			provider := strings.TrimSpace(tier.Provider)
			modelID := strings.TrimSpace(tier.ModelID)
			if cleanSlug == "" || provider == "" || modelID == "" {
				continue
			}
			custom[cleanSlug] = &virtualtools.CustomTierModel{
				Description:  strings.TrimSpace(tier.Description),
				ConnectionID: tier.ConnectionID,
				Provider:     provider,
				ModelID:      modelID,
			}
		}
		if len(custom) > 0 {
			result.Custom = custom
			hasAny = true
		}
	}

	if !hasAny {
		return nil
	}
	return result
}

func mergeDelegationTierConfig(base, override *virtualtools.DelegationTierConfig) *virtualtools.DelegationTierConfig {
	base = sanitizeDelegationTierConfig(base)
	override = sanitizeDelegationTierConfig(override)

	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	result := &virtualtools.DelegationTierConfig{
		SchemaVersion: delegationTierConfigSchemaVersion,
		Mode:          "explicit",
		Main:          base.Main,
		High:          base.High,
		Medium:        base.Medium,
		Low:           base.Low,
	}

	if len(base.Custom) > 0 {
		result.Custom = make(map[string]*virtualtools.CustomTierModel, len(base.Custom))
		for slug, tier := range base.Custom {
			tierCopy := *tier
			result.Custom[slug] = &tierCopy
		}
	}
	if override.Mode == "provider_profile" {
		return override
	}
	if base.Mode == "provider_profile" {
		return override
	}

	if override.Main != nil {
		result.Main = override.Main
	}
	if override.High != nil {
		result.High = override.High
	}
	if override.Medium != nil {
		result.Medium = override.Medium
	}
	if override.Low != nil {
		result.Low = override.Low
	}
	if len(override.Custom) > 0 {
		if result.Custom == nil {
			result.Custom = make(map[string]*virtualtools.CustomTierModel, len(override.Custom))
		}
		for slug, tier := range override.Custom {
			tierCopy := *tier
			result.Custom[slug] = &tierCopy
		}
	}

	return result
}

// SaveDelegationTierConfig saves delegation tier config as plain JSON to the workspace.
func SaveDelegationTierConfig(ctx context.Context, config *virtualtools.DelegationTierConfig) error {
	sanitized := sanitizeDelegationTierConfig(config)
	if sanitized == nil {
		sanitized = &virtualtools.DelegationTierConfig{}
	}

	data, err := json.Marshal(sanitized)
	if err != nil {
		return fmt.Errorf("failed to marshal tier config: %w", err)
	}
	if err := writeFileToWorkspace(ctx, delegationTierConfigFilePath, string(data)); err != nil {
		return fmt.Errorf("failed to write tier config: %w", err)
	}
	return nil
}

// LoadDelegationTierConfig reads delegation tier config from the workspace.
// Returns nil, nil if the file doesn't exist.
func LoadDelegationTierConfig(ctx context.Context) (*virtualtools.DelegationTierConfig, error) {
	content, exists, err := readFileFromWorkspace(ctx, delegationTierConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read tier config: %w", err)
	}
	if !exists {
		return nil, nil
	}
	var cfg virtualtools.DelegationTierConfig
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tier config: %w", err)
	}
	sanitized := sanitizeDelegationTierConfig(&cfg)
	if sanitized != nil && (cfg.SchemaVersion != delegationTierConfigSchemaVersion || strings.TrimSpace(cfg.Mode) == "") {
		if err := SaveDelegationTierConfig(ctx, sanitized); err != nil {
			return nil, fmt.Errorf("failed to persist tier config migration: %w", err)
		}
	}
	return sanitized, nil
}

// LoadAndResolveTierConfig loads tier config from the workspace file and merges with a request-level
// override (request config takes priority over file config, file over env vars).
// Use this at delegation spawn time so tier changes written mid-session take effect immediately.
func LoadAndResolveTierConfig(ctx context.Context, requestConfig *virtualtools.DelegationTierConfig) *virtualtools.DelegationTierConfig {
	fileConfig, err := LoadDelegationTierConfig(ctx)
	if err != nil {
		return constrainDelegationTierConfig(ctx, resolveDelegationTierConfig(requestConfig))
	}
	return constrainDelegationTierConfig(ctx, resolveDelegationTierConfig(mergeDelegationTierConfig(fileConfig, requestConfig)))
}

// constrainDelegationTierConfig moves a tier model its account does not allow
// onto the account's first allowed model. It returns a copy; the input may be
// shared.
func constrainDelegationTierConfig(ctx context.Context, cfg *virtualtools.DelegationTierConfig) *virtualtools.DelegationTierConfig {
	if cfg == nil {
		return nil
	}
	fix := func(provider, connectionID, model string) string {
		if resolved, changed, err := resolveAccountModel(ctx, provider, connectionID, model); err == nil && changed && strings.TrimSpace(model) != "" {
			return resolved
		}
		return model
	}
	fixTier := func(tier *virtualtools.TierModel) *virtualtools.TierModel {
		if tier == nil {
			return nil
		}
		copied := *tier
		copied.ModelID = fix(tier.Provider, tier.ConnectionID, tier.ModelID)
		return &copied
	}
	out := *cfg
	out.Main, out.High, out.Medium, out.Low = fixTier(cfg.Main), fixTier(cfg.High), fixTier(cfg.Medium), fixTier(cfg.Low)
	if cfg.Custom != nil {
		out.Custom = make(map[string]*virtualtools.CustomTierModel, len(cfg.Custom))
		for name, tier := range cfg.Custom {
			if tier == nil {
				out.Custom[name] = nil
				continue
			}
			copied := *tier
			copied.ModelID = fix(tier.Provider, tier.ConnectionID, tier.ModelID)
			out.Custom[name] = &copied
		}
	}
	return &out
}

// handleSaveDelegationTierConfig saves delegation tier config to the workspace filesystem.
// PUT /api/delegation-tier-config
func (api *StreamingAPI) handleSaveDelegationTierConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var cfg virtualtools.DelegationTierConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := SaveDelegationTierConfig(r.Context(), &cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save tier config: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// handleLoadDelegationTierConfig loads delegation tier config from the workspace filesystem.
// GET /api/delegation-tier-config
func (api *StreamingAPI) handleLoadDelegationTierConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	cfg, err := LoadDelegationTierConfig(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load tier config: %v", err), http.StatusInternalServerError)
		return
	}
	if cfg == nil {
		cfg = &virtualtools.DelegationTierConfig{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}
