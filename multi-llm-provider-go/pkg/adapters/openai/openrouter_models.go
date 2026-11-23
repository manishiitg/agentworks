package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	// MaxLatestOpenRouterModels is the number of latest models to return from OpenRouter
	MaxLatestOpenRouterModels = 10
)

const (
	openRouterAPIBaseURL = "https://openrouter.ai/api/v1"
	openRouterModelsURL  = openRouterAPIBaseURL + "/models"
	cacheTTL             = 24 * time.Hour // Cache models for 24 hours
)

var (
	// Cache for raw OpenRouter models from API
	openRouterModelsCache     []OpenRouterModel
	openRouterModelsCacheMu   sync.RWMutex
	openRouterModelsCacheTime time.Time
)

// OpenRouterModelsResponse represents the response from OpenRouter's Models API
type OpenRouterModelsResponse struct {
	Data []OpenRouterModel `json:"data"`
}

// OpenRouterModel represents a single model from OpenRouter's API
type OpenRouterModel struct {
	ID            string                     `json:"id"`
	CanonicalSlug string                     `json:"canonical_slug"`
	Name          string                     `json:"name"`
	Created       int64                      `json:"created"`
	ContextLength int                        `json:"context_length"`
	Architecture  OpenRouterArchitecture     `json:"architecture"`
	Pricing       OpenRouterPricing          `json:"pricing"`
	TopProvider   OpenRouterTopProvider      `json:"top_provider"`
}

// OpenRouterArchitecture represents architecture information from OpenRouter
type OpenRouterArchitecture struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Tokenizer        string   `json:"tokenizer"`
	InstructType     string   `json:"instruct_type"`
}

// OpenRouterPricing represents pricing information from OpenRouter
type OpenRouterPricing struct {
	Prompt            string `json:"prompt"`             // Cost per input token (as string)
	Completion        string `json:"completion"`         // Cost per output token (as string)
	Request           string `json:"request"`            // Fixed cost per request
	Image             string `json:"image"`              // Cost per image
	WebSearch         string `json:"web_search"`         // Cost per web search
	InternalReasoning string `json:"internal_reasoning"` // Cost per reasoning token
	InputCacheRead    string `json:"input_cache_read"`   // Cost per cached input token read
	InputCacheWrite   string `json:"input_cache_write"`  // Cost per cached input token write
}

// OpenRouterTopProvider represents top provider information
type OpenRouterTopProvider struct {
	ContextLength       int  `json:"context_length"`
	MaxCompletionTokens int  `json:"max_completion_tokens"`
	IsModerated         bool `json:"is_moderated"`
}

// fetchOpenRouterModels fetches the list of models from OpenRouter's API
func fetchOpenRouterModels() (*OpenRouterModelsResponse, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", openRouterModelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OpenRouter models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenRouter API returned status %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp OpenRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenRouter models response: %w", err)
	}

	return &modelsResp, nil
}

// parsePriceString converts a price string (e.g., "0.000001" per token) to cost per 1M tokens
func parsePriceString(priceStr string) float64 {
	if priceStr == "" || priceStr == "0" {
		return 0.0
	}

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		// Log the error for debugging but don't fail - pricing is informational
		fmt.Printf("warning: failed to parse price string %q: %v\n", priceStr, err)
		return 0.0
	}

	// Convert from per-token to per-1M-tokens
	return price * 1_000_000
}

// convertOpenRouterModelToMetadata converts an OpenRouter model to our ModelMetadata format
func convertOpenRouterModelToMetadata(model OpenRouterModel) *llmtypes.ModelMetadata {
	// Use model context_length as primary, fallback to top_provider context_length if model context_length is 0
	// According to OpenRouter API semantics, model.ContextLength is the standard context window,
	// while TopProvider.ContextLength is provider-specific and should only be used if model.ContextLength is missing
	contextWindow := model.ContextLength
	if contextWindow == 0 && model.TopProvider.ContextLength > 0 {
		contextWindow = model.TopProvider.ContextLength
	}

	// Extract provider from model ID (format: "provider/model-name")
	provider := "openrouter"
	if parts := strings.Split(model.ID, "/"); len(parts) > 0 {
		provider = parts[0]
	}

	return &llmtypes.ModelMetadata{
		ModelID:                         model.ID,
		ModelName:                       model.Name,
		ContextWindow:                   contextWindow,
		InputCostPer1MTokens:            parsePriceString(model.Pricing.Prompt),
		OutputCostPer1MTokens:           parsePriceString(model.Pricing.Completion),
		ReasoningCostPer1MTokens:        parsePriceString(model.Pricing.InternalReasoning),
		CachedInputCostPer1MTokens:      parsePriceString(model.Pricing.InputCacheRead),
		CachedInputCostWritePer1MTokens: parsePriceString(model.Pricing.InputCacheWrite),
		Provider:                        provider,
	}
}

// getCachedOpenRouterModels returns cached OpenRouter models, fetching from API if cache is expired
func getCachedOpenRouterModels() ([]OpenRouterModel, error) {
	openRouterModelsCacheMu.RLock()
	// Check if cache is still valid
	if time.Since(openRouterModelsCacheTime) < cacheTTL && len(openRouterModelsCache) > 0 {
		models := openRouterModelsCache
		openRouterModelsCacheMu.RUnlock()
		return models, nil
	}
	openRouterModelsCacheMu.RUnlock()

	// Cache expired or empty, fetch from API
	openRouterModelsCacheMu.Lock()
	defer openRouterModelsCacheMu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(openRouterModelsCacheTime) < cacheTTL && len(openRouterModelsCache) > 0 {
		return openRouterModelsCache, nil
	}

	// Fetch from API
	modelsResp, err := fetchOpenRouterModels()
	if err != nil {
		return nil, err
	}

	// Update cache with raw models
	openRouterModelsCache = modelsResp.Data
	openRouterModelsCacheTime = time.Now()

	return openRouterModelsCache, nil
}

// GetAllOpenRouterModels returns the latest OpenRouter models with their metadata (costs, context window, etc.)
// It fetches from OpenRouter's API, filters to text-only input models, sorts by creation date,
// and returns only the latest MaxLatestOpenRouterModels models.
func GetAllOpenRouterModels() []*llmtypes.ModelMetadata {
	models, err := getLatestOpenRouterModels()
	if err != nil {
		// Log error and return empty slice - don't fail the entire metadata request
		fmt.Printf("warning: failed to fetch OpenRouter models: %v\n", err)
		return []*llmtypes.ModelMetadata{}
	}

	result := make([]*llmtypes.ModelMetadata, 0, len(models))
	for _, metadata := range models {
		// Mark all as openrouter provider for consistency
		metadata.Provider = "openrouter"
		result = append(result, metadata)
	}
	return result
}

// getLatestOpenRouterModels fetches models from cache (or API if expired), filters to text-only, and returns latest N
func getLatestOpenRouterModels() ([]*llmtypes.ModelMetadata, error) {
	// Get models from cache (fetches from API if cache expired)
	allModels, err := getCachedOpenRouterModels()
	if err != nil {
		return nil, err
	}

	// Filter to text-only input modality models
	var textOnlyModels []OpenRouterModel
	for _, model := range allModels {
		if isTextOnlyInput(model.Architecture.InputModalities) {
			textOnlyModels = append(textOnlyModels, model)
		}
	}

	// Sort by created timestamp (descending - newest first)
	sort.Slice(textOnlyModels, func(i, j int) bool {
		return textOnlyModels[i].Created > textOnlyModels[j].Created
	})

	// Take only the latest N models
	limit := MaxLatestOpenRouterModels
	if len(textOnlyModels) < limit {
		limit = len(textOnlyModels)
	}
	latestModels := textOnlyModels[:limit]

	// Convert to metadata
	result := make([]*llmtypes.ModelMetadata, 0, len(latestModels))
	for _, model := range latestModels {
		metadata := convertOpenRouterModelToMetadata(model)
		result = append(result, metadata)
	}

	return result, nil
}

// isTextOnlyInput checks if the model only accepts text input
func isTextOnlyInput(inputModalities []string) bool {
	if len(inputModalities) == 0 {
		return false
	}
	// Check if the only input modality is "text"
	return len(inputModalities) == 1 && inputModalities[0] == "text"
}

// GetOpenRouterModelMetadata retrieves metadata for an OpenRouter model by fetching from the API
// It caches results for 24 hours to avoid excessive API calls
func GetOpenRouterModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if modelID == "" {
		return nil, fmt.Errorf("model ID cannot be empty")
	}

	// Check if this is an OpenRouter model ID (contains "/")
	if !strings.Contains(modelID, "/") {
		return nil, fmt.Errorf("model ID does not appear to be an OpenRouter model (expected format: provider/model-name)")
	}

	// Get models from cache (fetches from API if cache expired)
	models, err := getCachedOpenRouterModels()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OpenRouter models: %w", err)
	}

	// Look up the model by ID
	for _, model := range models {
		if model.ID == modelID {
			return convertOpenRouterModelToMetadata(model), nil
		}
	}

	return nil, fmt.Errorf("model not found in OpenRouter: %s", modelID)
}
