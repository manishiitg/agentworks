package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

const publishedLLMsFilePath = "config/published-llms.json"
const autoPublishedLLMSource = "auto_coding_agent"
const autoPublishedLLMIDPrefix = "auto:"

// gpt-5.3-codex-spark is not offered (owner, 2026-10-04): OpenAI refuses it for Codex signed in with a ChatGPT account.
var autoPublishedCodexCLIModelIDs = []string{
	"gpt-5.4-mini",
}

// StoredPublishedLLM is the workspace-backed published LLM record.
// Secrets are intentionally not stored here; provider auth lives in config/provider-api-keys.json.
type StoredPublishedLLM struct {
	ID                        string                 `json:"id"`
	Name                      string                 `json:"name"`
	Provider                  string                 `json:"provider"`
	ModelID                   string                 `json:"model_id"`
	SearchRole                string                 `json:"search_role,omitempty"`
	SearchPriority            *int                   `json:"search_priority,omitempty"`
	ModelName                 string                 `json:"model_name,omitempty"`
	AuthMethod                string                 `json:"auth_method,omitempty"`
	ContextWindow             *int                   `json:"context_window,omitempty"`
	InputCostPer1M            *float64               `json:"input_cost_per_1m,omitempty"`
	OutputCostPer1M           *float64               `json:"output_cost_per_1m,omitempty"`
	ReasoningCostPer1M        *float64               `json:"reasoning_cost_per_1m,omitempty"`
	CachedInputCostPer1M      *float64               `json:"cached_input_cost_per_1m,omitempty"`
	CachedInputCostWritePer1M *float64               `json:"cached_input_cost_write_per_1m,omitempty"`
	Options                   map[string]interface{} `json:"options,omitempty"`
	Source                    string                 `json:"source,omitempty"`
	CreatedAt                 string                 `json:"created_at,omitempty"`
}

func isAutoPublishedLLM(entry StoredPublishedLLM) bool {
	return strings.EqualFold(strings.TrimSpace(entry.Source), autoPublishedLLMSource) ||
		strings.HasPrefix(strings.TrimSpace(entry.ID), autoPublishedLLMIDPrefix)
}

func sanitizePublishedLLM(entry StoredPublishedLLM) (StoredPublishedLLM, bool) {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Name = strings.TrimSpace(entry.Name)
	entry.Provider = strings.TrimSpace(entry.Provider)
	entry.ModelID = strings.TrimSpace(entry.ModelID)
	entry.SearchRole = strings.ToLower(strings.TrimSpace(entry.SearchRole))
	entry.Source = strings.TrimSpace(entry.Source)
	entry.CreatedAt = strings.TrimSpace(entry.CreatedAt)

	// Published chat LLM entries are a routing registry, not a pricing/model
	// metadata cache. Keep legacy fields readable for old JSON files but do
	// not expose or rewrite them after sanitization.
	entry.ModelName = ""
	entry.AuthMethod = ""
	entry.ContextWindow = nil
	entry.InputCostPer1M = nil
	entry.OutputCostPer1M = nil
	entry.ReasoningCostPer1M = nil
	entry.CachedInputCostPer1M = nil
	entry.CachedInputCostWritePer1M = nil

	if entry.Provider == "" || entry.ModelID == "" || entry.Name == "" {
		return StoredPublishedLLM{}, false
	}
	if !isPublishedLLMProviderAllowed(entry.Provider) {
		return StoredPublishedLLM{}, false
	}

	if entry.ID == "" {
		entry.ID = fmt.Sprintf("%s:%s:%d", entry.Provider, entry.ModelID, time.Now().UnixNano())
	}
	if entry.CreatedAt == "" {
		entry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.Options != nil && len(entry.Options) == 0 {
		entry.Options = nil
	}

	return entry, true
}

func sanitizePersistedPublishedLLMs(llms []StoredPublishedLLM) []StoredPublishedLLM {
	sanitized := make([]StoredPublishedLLM, 0, len(llms))
	for _, entry := range llms {
		if isAutoPublishedLLM(entry) {
			log.Printf("[PUBLISHED_LLM] Skipping auto-published entry during persistence: id=%q provider=%q model_id=%q", entry.ID, entry.Provider, entry.ModelID)
			continue
		}
		clean, ok := sanitizePublishedLLM(entry)
		if !ok {
			log.Printf("[PUBLISHED_LLM] Dropping invalid entry during persistence: id=%q name=%q provider=%q model_id=%q", entry.ID, entry.Name, entry.Provider, entry.ModelID)
			continue
		}
		if isAutoPublishedLLM(clean) {
			continue
		}
		sanitized = append(sanitized, clean)
	}
	return sanitized
}

// SavePublishedLLMs saves published LLMs as plain JSON in the workspace config folder.
func SavePublishedLLMs(ctx context.Context, llms []StoredPublishedLLM) error {
	log.Printf("[PUBLISHED_LLM] SavePublishedLLMs called with %d entries", len(llms))
	sanitized := sanitizePersistedPublishedLLMs(llms)
	log.Printf("[PUBLISHED_LLM] Sanitized save payload contains %d entries", len(sanitized))

	data, err := json.Marshal(sanitized)
	if err != nil {
		return fmt.Errorf("failed to marshal published llms: %w", err)
	}
	if err := writeFileToWorkspace(ctx, publishedLLMsFilePath, string(data)); err != nil {
		log.Printf("[PUBLISHED_LLM] Failed writing %s: %v", publishedLLMsFilePath, err)
		return fmt.Errorf("failed to write published llms: %w", err)
	}
	log.Printf("[PUBLISHED_LLM] Wrote %d entries to %s", len(sanitized), publishedLLMsFilePath)
	return nil
}

// LoadPublishedLLMs reads published LLMs from the workspace config folder.
// Returns nil, nil if the file doesn't exist.
func LoadPublishedLLMs(ctx context.Context) ([]StoredPublishedLLM, error) {
	content, exists, err := readFileFromWorkspace(ctx, publishedLLMsFilePath)
	if err != nil {
		log.Printf("[PUBLISHED_LLM] Failed reading %s: %v", publishedLLMsFilePath, err)
		return nil, fmt.Errorf("failed to read published llms: %w", err)
	}
	if !exists {
		log.Printf("[PUBLISHED_LLM] %s does not exist", publishedLLMsFilePath)
		return nil, nil
	}
	log.Printf("[PUBLISHED_LLM] Loaded raw content from %s (%d bytes)", publishedLLMsFilePath, len(content))

	var llms []StoredPublishedLLM
	if err := json.Unmarshal([]byte(content), &llms); err != nil {
		log.Printf("[PUBLISHED_LLM] Failed unmarshalling %s: %v", publishedLLMsFilePath, err)
		return nil, fmt.Errorf("failed to unmarshal published llms: %w", err)
	}

	sanitized := make([]StoredPublishedLLM, 0, len(llms))
	for _, entry := range llms {
		clean, ok := sanitizePublishedLLM(entry)
		if !ok {
			log.Printf("[PUBLISHED_LLM] Dropping invalid entry during load: id=%q name=%q provider=%q model_id=%q", entry.ID, entry.Name, entry.Provider, entry.ModelID)
			continue
		}
		if isAutoPublishedLLM(clean) {
			log.Printf("[PUBLISHED_LLM] Dropping generated entry found in persisted file during load: id=%q provider=%q model_id=%q", clean.ID, clean.Provider, clean.ModelID)
			continue
		}
		sanitized = append(sanitized, clean)
	}
	log.Printf("[PUBLISHED_LLM] Returning %d published LLM entries from %s", len(sanitized), publishedLLMsFilePath)

	return sanitized, nil
}

func LoadPublishedLLMsWithAuto(ctx context.Context) ([]StoredPublishedLLM, error) {
	saved, err := LoadPublishedLLMs(ctx)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		saved = []StoredPublishedLLM{}
	}

	auto := buildAutoPublishedCodingAgentLLMs(ctx, saved)
	merged := make([]StoredPublishedLLM, 0, len(saved)+len(auto))
	merged = append(merged, saved...)
	merged = append(merged, auto...)
	log.Printf("[PUBLISHED_LLM] Returning %d entries (%d saved, %d auto)", len(merged), len(saved), len(auto))
	return merged, nil
}

func buildAutoPublishedCodingAgentLLMs(ctx context.Context, saved []StoredPublishedLLM) []StoredPublishedLLM {
	supported := getSupportedProviders()
	supportedSet := make(map[string]bool, len(supported))
	for _, provider := range supported {
		supportedSet[provider] = true
	}

	existing := make(map[string]bool, len(saved))
	for _, entry := range saved {
		clean, ok := sanitizePublishedLLM(entry)
		if !ok {
			continue
		}
		existing[publishedLLMIdentityKey(clean)] = true
	}

	providers := []string{"claude-code", "codex-cli"}
	generated := make([]StoredPublishedLLM, 0, 16)
	for _, provider := range providers {
		if !supportedSet[provider] {
			continue
		}
		authConfigured, _ := providerAuthConfigured(provider, nil)
		usable, _, _ := providerUsable(provider, authConfigured)
		if !usable {
			continue
		}
		for _, entry := range autoPublishedCodingAgentLLMsForProvider(provider) {
			key := publishedLLMIdentityKey(entry)
			if existing[key] {
				continue
			}
			existing[key] = true
			generated = append(generated, entry)
		}
	}
	return generated
}

func autoPublishedCodingAgentLLMsForProvider(provider string) []StoredPublishedLLM {
	models := autoPublishedCodingAgentModelMetadata(provider)
	entries := make([]StoredPublishedLLM, 0, len(models)*2)
	for _, model := range models {
		for _, effort := range autoPublishedReasoningEfforts(provider, model) {
			options := map[string]interface{}{}
			if effort != "" {
				options["reasoning_effort"] = effort
			}
			if len(options) == 0 {
				options = nil
			}
			entry := StoredPublishedLLM{
				ID:       autoPublishedLLMID(provider, model.ModelID, effort),
				Name:     autoPublishedLLMName(provider, model, effort),
				Provider: provider,
				ModelID:  model.ModelID,
				Options:  options,
				Source:   autoPublishedLLMSource,
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

func autoPublishedCodingAgentModelMetadata(provider string) []*llmtypes.ModelMetadata {
	provider = strings.ToLower(strings.TrimSpace(provider))
	models := providerModelMetadata(provider)
	if provider == "codex-cli" {
		models = append(models, autoPublishedCodexCLIModels()...)
	}
	filtered := make([]*llmtypes.ModelMetadata, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		if model == nil {
			continue
		}
		modelID := strings.TrimSpace(model.ModelID)
		if modelID == "" || seen[modelID] || isCodingAgentAliasModel(provider, modelID) {
			continue
		}
		seen[modelID] = true
		filtered = append(filtered, model)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		leftRank := autoPublishedModelRank(provider, filtered[i].ModelID)
		rightRank := autoPublishedModelRank(provider, filtered[j].ModelID)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.ToLower(filtered[i].ModelID) < strings.ToLower(filtered[j].ModelID)
	})
	return filtered
}

func autoPublishedCodexCLIModels() []*llmtypes.ModelMetadata {
	adapter := codexcli.NewCodexCLIAdapter("", "", nil)
	models := make([]*llmtypes.ModelMetadata, 0, len(autoPublishedCodexCLIModelIDs))
	for _, modelID := range autoPublishedCodexCLIModelIDs {
		meta, err := adapter.GetModelMetadata(modelID)
		if err != nil || meta == nil {
			meta = &llmtypes.ModelMetadata{
				ModelID:                 modelID,
				ModelName:               modelID,
				Provider:                "codex-cli",
				SupportsReasoningEffort: true,
				ReasoningEffortLevels:   []string{"low", "medium", "high", "xhigh"},
			}
		}
		meta.ModelID = modelID
		meta.Provider = "codex-cli"
		models = append(models, meta)
	}
	return models
}

func isCodingAgentAliasModel(provider, modelID string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if provider == "codex-cli" {
		switch modelID {
		case "high", "medium", "low":
			return true
		}
	}
	if provider == "claude-code" && modelID == "claude-opus-4-7" {
		return true
	}
	return modelID == "" || modelID == provider || modelID == "auto" || modelID == "claude-code" || modelID == "codex-cli"
}

func autoPublishedModelRank(provider, modelID string) int {
	id := strings.ToLower(strings.TrimSpace(modelID))
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude-code":
		switch {
		case strings.Contains(id, "haiku"):
			return 10
		case strings.Contains(id, "sonnet-5"):
			return 20
		case strings.Contains(id, "sonnet"):
			return 30
		case strings.Contains(id, "opus-5"):
			return 40
		case strings.Contains(id, "opus"):
			return 50
		case strings.Contains(id, "fable"):
			return 60
		}
	case "codex-cli":
		switch {
		case strings.Contains(id, "spark"):
			return 10
		case strings.Contains(id, "gpt-5.3"):
			return 20
		case strings.Contains(id, "gpt-5.4-mini"):
			return 30
		case strings.Contains(id, "gpt-5.4"):
			return 40
		case strings.Contains(id, "gpt-5.5"):
			return 50
		}
	}
	return 1000
}

func autoPublishedReasoningEfforts(provider string, model *llmtypes.ModelMetadata) []string {
	if model == nil || !model.SupportsReasoningEffort {
		return []string{""}
	}

	levels := map[string]bool{}
	for _, level := range model.ReasoningEffortLevels {
		levels[strings.ToLower(strings.TrimSpace(level))] = true
	}

	desired := []string{"high", "xhigh"}
	if strings.EqualFold(provider, "claude-code") {
		desired = []string{"high", "max"}
	}

	result := make([]string, 0, len(desired))
	for _, level := range desired {
		if levels[level] {
			result = append(result, level)
		}
	}
	if len(result) == 0 && levels["high"] {
		result = append(result, "high")
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func autoPublishedLLMID(provider, modelID, effort string) string {
	parts := []string{autoPublishedLLMIDPrefix + strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(modelID)}
	if strings.TrimSpace(effort) != "" {
		parts = append(parts, strings.ToLower(strings.TrimSpace(effort)))
	}
	return strings.Join(parts, ":")
}

func autoPublishedLLMName(provider string, model *llmtypes.ModelMetadata, effort string) string {
	modelName := strings.TrimSpace(model.ModelName)
	if modelName == "" {
		modelName = strings.TrimSpace(model.ModelID)
	}
	if strings.EqualFold(provider, "claude-code") && !strings.HasPrefix(strings.ToLower(modelName), "claude ") {
		modelName = "Claude " + modelName
	}

	parts := []string{providerDisplayLabel(provider), modelName}
	if label := reasoningEffortDisplayName(effort); label != "" {
		parts = append(parts, label)
	}
	return strings.Join(parts, " · ")
}

func reasoningEffortDisplayName(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "xhigh", "max":
		return "Extra High"
	case "high":
		return "High"
	case "medium":
		return "Medium"
	case "low":
		return "Low"
	default:
		return ""
	}
}

func publishedLLMIdentityKey(entry StoredPublishedLLM) string {
	options := entry.Options
	if len(options) == 0 {
		options = nil
	}
	optionsJSON := "{}"
	if options != nil {
		if data, err := json.Marshal(options); err == nil {
			optionsJSON = string(data)
		}
	}
	return strings.ToLower(strings.TrimSpace(entry.Provider)) + "\x00" + strings.TrimSpace(entry.ModelID) + "\x00" + optionsJSON
}

// handleSavePublishedLLMs saves published LLMs to the workspace config folder.
// PUT /api/published-llms
func (api *StreamingAPI) handleSavePublishedLLMs(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var llms []StoredPublishedLLM
	if err := json.NewDecoder(r.Body).Decode(&llms); err != nil {
		log.Printf("[PUBLISHED_LLM] Invalid request body for save: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	log.Printf("[PUBLISHED_LLM] HTTP save request received with %d entries", len(llms))

	if err := SavePublishedLLMs(r.Context(), llms); err != nil {
		log.Printf("[PUBLISHED_LLM] Save request failed: %v", err)
		http.Error(w, fmt.Sprintf("Failed to save published llms: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("[PUBLISHED_LLM] Save request completed successfully")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// handleLoadPublishedLLMs loads published LLMs from the workspace config folder.
// GET /api/published-llms
func (api *StreamingAPI) handleLoadPublishedLLMs(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	log.Printf("[PUBLISHED_LLM] HTTP load request received")

	llms, err := LoadPublishedLLMsWithAuto(r.Context())
	if err != nil {
		log.Printf("[PUBLISHED_LLM] Load request failed: %v", err)
		http.Error(w, fmt.Sprintf("Failed to load published llms: %v", err), http.StatusInternalServerError)
		return
	}
	if llms == nil {
		llms = []StoredPublishedLLM{}
	}
	log.Printf("[PUBLISHED_LLM] Load request returning %d entries", len(llms))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(llms)
}
