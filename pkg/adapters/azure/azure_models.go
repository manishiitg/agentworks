package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/modelspecs"
)

const (
	azureModelsCacheTTL = 1 * time.Hour // Cache models for 1 hour
)

var (
	// Cache for Azure models from API
	azureModelsCache     []AzureModel
	azureModelsCacheMu   sync.RWMutex
	azureModelsCacheTime time.Time
	azureModelsCacheKey  string // endpoint used for caching
)

// AzureModelsResponse represents the response from Azure's Models API
type AzureModelsResponse struct {
	Data []AzureModel `json:"data"`
}

// AzureModel represents a single model from Azure's API
type AzureModel struct {
	ID              string              `json:"id"`
	Object          string              `json:"object"`
	CreatedAt       int64               `json:"created_at"`
	Status          string              `json:"status"`
	LifecycleStatus string              `json:"lifecycle_status"`
	Capabilities    AzureCapabilities   `json:"capabilities"`
	Deprecation     AzureDeprecation    `json:"deprecation"`
}

// AzureCapabilities represents model capabilities from Azure
type AzureCapabilities struct {
	FineTune        bool `json:"fine_tune"`
	Inference       bool `json:"inference"`
	Completion      bool `json:"completion"`
	ChatCompletion  bool `json:"chat_completion"`
	Embeddings      bool `json:"embeddings"`
	GlobalFineTune  bool `json:"global_fine_tune"`
	DevtierFineTune bool `json:"devtier_fine_tune"`
}

// AzureDeprecation represents deprecation timestamps
type AzureDeprecation struct {
	Inference int64 `json:"inference"`
	FineTune  int64 `json:"fine_tune"`
}

// Static pricing and context window data for known models
// This is merged with dynamic API data since Azure API doesn't provide pricing
var azureModelPricing = map[string]struct {
	ContextWindow            int
	InputCostPer1MTokens     float64
	OutputCostPer1MTokens    float64
	ReasoningCostPer1MTokens float64
	SupportsToolCalls        bool
	SupportsReasoningEffort  bool
	ReasoningEffortLevels    []string
}{
	// GPT-4 models
	"gpt-4": {8192, 30.0, 60.0, 0, true, false, nil},
	"gpt-4-0314": {8192, 30.0, 60.0, 0, true, false, nil},
	"gpt-4-0613": {8192, 30.0, 60.0, 0, true, false, nil},
	"gpt-4-32k": {32768, 60.0, 120.0, 0, true, false, nil},
	"gpt-4-32k-0314": {32768, 60.0, 120.0, 0, true, false, nil},
	"gpt-4-32k-0613": {32768, 60.0, 120.0, 0, true, false, nil},

	// GPT-4 Turbo models
	"gpt-4-turbo": {128000, 10.0, 30.0, 0, true, false, nil},
	"gpt-4-turbo-2024-04-09": {128000, 10.0, 30.0, 0, true, false, nil},
	"gpt-4-1106-preview": {128000, 10.0, 30.0, 0, true, false, nil},
	"gpt-4-0125-preview": {128000, 10.0, 30.0, 0, true, false, nil},
	"gpt-4-vision-preview": {128000, 10.0, 30.0, 0, true, false, nil},

	// GPT-4o models
	"gpt-4o": {128000, 5.0, 15.0, 0, true, false, nil},
	"gpt-4o-2024-05-13": {128000, 5.0, 15.0, 0, true, false, nil},
	"gpt-4o-2024-08-06": {128000, 2.5, 10.0, 0, true, false, nil},
	"gpt-4o-2024-11-20": {128000, 2.5, 10.0, 0, true, false, nil},
	"gpt-4o-mini": {128000, 0.15, 0.60, 0, true, false, nil},
	"gpt-4o-mini-2024-07-18": {128000, 0.15, 0.60, 0, true, false, nil},

	// GPT-4.1 models
	"gpt-4.1": {1047576, 2.0, 8.0, 0, true, false, nil},
	"gpt-4.1-mini": {1047576, 0.4, 1.6, 0, true, false, nil},
	"gpt-4.1-nano": {1047576, 0.1, 0.4, 0, true, false, nil},

	// GPT-3.5 models
	"gpt-3.5-turbo": {16385, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo": {16385, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo-0301": {4096, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo-0613": {4096, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo-1106": {16385, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo-0125": {16385, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo-16k": {16385, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo-16k-0613": {16385, 0.5, 1.5, 0, true, false, nil},
	"gpt-35-turbo-instruct": {4096, 0.5, 1.5, 0, false, false, nil},
	"gpt-35-turbo-instruct-0914": {4096, 0.5, 1.5, 0, false, false, nil},

	// o1 models (reasoning)
	"o1": {200000, 15.0, 60.0, 60.0, true, true, []string{"low", "medium", "high"}},
	"o1-mini": {128000, 3.0, 12.0, 12.0, true, true, []string{"low", "medium", "high"}},
	"o1-preview": {128000, 15.0, 60.0, 60.0, false, false, nil},

	// o3 models (reasoning)
	"o3": {200000, 10.0, 40.0, 40.0, true, true, []string{"low", "medium", "high"}},
	"o3-mini": {200000, 1.1, 4.4, 4.4, true, true, []string{"low", "medium", "high"}},

	// GPT-5 models
	"gpt-5": {200000, 5.0, 15.0, 0, true, true, []string{"low", "medium", "high"}},
	"gpt-5.1": {200000, 3.0, 12.0, 0, true, true, []string{"low", "medium", "high"}},
	"gpt-5.2": {200000, 2.0, 8.0, 0, true, true, []string{"low", "medium", "high"}},
	"gpt-5.2-codex": {200000, 2.0, 8.0, 0, true, true, []string{"low", "medium", "high"}},

	// Embedding models
	"text-embedding-ada-002": {8191, 0.10, 0.0, 0, false, false, nil},
	"text-embedding-3-small": {8191, 0.02, 0.0, 0, false, false, nil},
	"text-embedding-3-large": {8191, 0.13, 0.0, 0, false, false, nil},
}

// FetchAzureModels fetches models from Azure API
func FetchAzureModels(endpoint, apiKey string) ([]AzureModel, error) {
	// Build the models endpoint URL
	baseURL := endpoint
	if strings.Contains(baseURL, "services.ai.azure.com") {
		idx := strings.Index(baseURL, "services.ai.azure.com")
		if idx != -1 {
			baseURL = baseURL[:idx+len("services.ai.azure.com")]
		}
	}
	modelsURL := strings.TrimSuffix(baseURL, "/") + "/openai/v1/models"

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Azure models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Azure API returned status %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp AzureModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to decode Azure models response: %w", err)
	}

	return modelsResp.Data, nil
}

// GetCachedAzureModels returns cached Azure models, fetching from API if cache is expired
func GetCachedAzureModels(endpoint, apiKey string) ([]AzureModel, error) {
	azureModelsCacheMu.RLock()
	// Check if cache is still valid and for the same endpoint
	if time.Since(azureModelsCacheTime) < azureModelsCacheTTL &&
		len(azureModelsCache) > 0 &&
		azureModelsCacheKey == endpoint {
		models := azureModelsCache
		azureModelsCacheMu.RUnlock()
		return models, nil
	}
	azureModelsCacheMu.RUnlock()

	// Cache expired or empty, fetch from API
	azureModelsCacheMu.Lock()
	defer azureModelsCacheMu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(azureModelsCacheTime) < azureModelsCacheTTL &&
		len(azureModelsCache) > 0 &&
		azureModelsCacheKey == endpoint {
		return azureModelsCache, nil
	}

	// Fetch from API
	models, err := FetchAzureModels(endpoint, apiKey)
	if err != nil {
		return nil, err
	}

	// Update cache
	azureModelsCache = models
	azureModelsCacheTime = time.Now()
	azureModelsCacheKey = endpoint

	return azureModelsCache, nil
}

// convertAzureModelToMetadata converts an Azure API model to our ModelMetadata format
func convertAzureModelToMetadata(model AzureModel) *llmtypes.ModelMetadata {
	// Look up static pricing data
	pricing, hasPricing := azureModelPricing[model.ID]

	// Try to match by prefix for versioned models
	if !hasPricing {
		for key, p := range azureModelPricing {
			if strings.HasPrefix(model.ID, key) {
				pricing = p
				hasPricing = true
				break
			}
		}
	}

	// Default values for unknown models
	contextWindow := 128000
	inputCost := 0.0
	outputCost := 0.0
	reasoningCost := 0.0
	supportsToolCalls := model.Capabilities.ChatCompletion
	supportsReasoningEffort := false
	var reasoningLevels []string

	if hasPricing {
		contextWindow = pricing.ContextWindow
		inputCost = pricing.InputCostPer1MTokens
		outputCost = pricing.OutputCostPer1MTokens
		reasoningCost = pricing.ReasoningCostPer1MTokens
		supportsToolCalls = pricing.SupportsToolCalls
		supportsReasoningEffort = pricing.SupportsReasoningEffort
		reasoningLevels = pricing.ReasoningEffortLevels
	}

	return &llmtypes.ModelMetadata{
		ModelID:                  model.ID,
		ModelName:                model.ID,
		ContextWindow:            contextWindow,
		InputCostPer1MTokens:     inputCost,
		OutputCostPer1MTokens:    outputCost,
		ReasoningCostPer1MTokens: reasoningCost,
		Provider:                 "azure",
		SupportsToolCalls:        supportsToolCalls,
		SupportsJSONMode:         model.Capabilities.ChatCompletion,
		SupportsThinkingLevel:    false,
		SupportsReasoningEffort:  supportsReasoningEffort,
		ReasoningEffortLevels:    reasoningLevels,
		SupportsThinkingBudget:   false,
	}
}

// GetAzureModelMetadataFromAPI fetches model metadata from Azure API
func GetAzureModelMetadataFromAPI(endpoint, apiKey, modelID string) (*llmtypes.ModelMetadata, error) {
	models, err := GetCachedAzureModels(endpoint, apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Azure models: %w", err)
	}

	// Look up the model by ID
	for _, model := range models {
		if model.ID == modelID {
			return convertAzureModelToMetadata(model), nil
		}
	}

	// Try case-insensitive match
	for _, model := range models {
		if strings.EqualFold(model.ID, modelID) {
			return convertAzureModelToMetadata(model), nil
		}
	}

	return nil, fmt.Errorf("model not found in Azure: %s", modelID)
}

// GetAllAzureModelsFromAPI fetches all models from Azure API and returns metadata
func GetAllAzureModelsFromAPI(endpoint, apiKey string) ([]*llmtypes.ModelMetadata, error) {
	models, err := GetCachedAzureModels(endpoint, apiKey)
	if err != nil {
		return nil, err
	}

	result := make([]*llmtypes.ModelMetadata, 0, len(models))
	for _, model := range models {
		// Only include models that support inference
		if model.Capabilities.Inference && model.Status == "succeeded" {
			metadata := convertAzureModelToMetadata(model)
			result = append(result, metadata)
		}
	}
	return result, nil
}

// GetAzureModelMetadata returns metadata for a given Azure model ID
// Falls back to static data if API endpoint/key not provided
func GetAzureModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	m, err := azureModelMetadata(modelID)
	// A model the shared table knows (modelspecs) takes its name and context
	// window from there; Azure keeps its own prices.
	if err == nil && m != nil {
		if spec, ok := modelspecs.For(strings.ToLower(m.ModelID), "azure"); ok {
			m.ModelName, m.ContextWindow = spec.ModelName, spec.ContextWindow
		}
	}
	return m, err
}

func azureModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if modelID == "" {
		modelID = "gpt-4o" // default
	}

	// Look up static pricing data
	if pricing, exists := azureModelPricing[modelID]; exists {
		return &llmtypes.ModelMetadata{
			ModelID:                  modelID,
			ModelName:                modelID,
			ContextWindow:            pricing.ContextWindow,
			InputCostPer1MTokens:     pricing.InputCostPer1MTokens,
			OutputCostPer1MTokens:    pricing.OutputCostPer1MTokens,
			ReasoningCostPer1MTokens: pricing.ReasoningCostPer1MTokens,
			Provider:                 "azure",
			SupportsToolCalls:        pricing.SupportsToolCalls,
			SupportsJSONMode:         true,
			SupportsThinkingLevel:    false,
			SupportsReasoningEffort:  pricing.SupportsReasoningEffort,
			ReasoningEffortLevels:    pricing.ReasoningEffortLevels,
			SupportsThinkingBudget:   false,
		}, nil
	}

	// Try lowercase
	if pricing, exists := azureModelPricing[strings.ToLower(modelID)]; exists {
		return &llmtypes.ModelMetadata{
			ModelID:                  modelID,
			ModelName:                modelID,
			ContextWindow:            pricing.ContextWindow,
			InputCostPer1MTokens:     pricing.InputCostPer1MTokens,
			OutputCostPer1MTokens:    pricing.OutputCostPer1MTokens,
			ReasoningCostPer1MTokens: pricing.ReasoningCostPer1MTokens,
			Provider:                 "azure",
			SupportsToolCalls:        pricing.SupportsToolCalls,
			SupportsJSONMode:         true,
			SupportsThinkingLevel:    false,
			SupportsReasoningEffort:  pricing.SupportsReasoningEffort,
			ReasoningEffortLevels:    pricing.ReasoningEffortLevels,
			SupportsThinkingBudget:   false,
		}, nil
	}

	// Try to match by prefix for versioned models
	for key, pricing := range azureModelPricing {
		if strings.HasPrefix(strings.ToLower(modelID), key) {
			return &llmtypes.ModelMetadata{
				ModelID:                  modelID,
				ModelName:                modelID,
				ContextWindow:            pricing.ContextWindow,
				InputCostPer1MTokens:     pricing.InputCostPer1MTokens,
				OutputCostPer1MTokens:    pricing.OutputCostPer1MTokens,
				ReasoningCostPer1MTokens: pricing.ReasoningCostPer1MTokens,
				Provider:                 "azure",
				SupportsToolCalls:        pricing.SupportsToolCalls,
				SupportsJSONMode:         true,
				SupportsThinkingLevel:    false,
				SupportsReasoningEffort:  pricing.SupportsReasoningEffort,
				ReasoningEffortLevels:    pricing.ReasoningEffortLevels,
				SupportsThinkingBudget:   false,
			}, nil
		}
	}

	// Return a generic metadata for unknown models
	return &llmtypes.ModelMetadata{
		ModelID:                 modelID,
		ModelName:               modelID,
		ContextWindow:           128000, // Assume reasonable default
		InputCostPer1MTokens:    0.0,    // Unknown cost
		OutputCostPer1MTokens:   0.0,
		Provider:                "azure",
		SupportsToolCalls:       true,  // Assume supported
		SupportsJSONMode:        true,  // Assume supported
		SupportsThinkingLevel:   false,
		SupportsReasoningEffort: false,
		SupportsThinkingBudget:  false,
	}, nil
}

// GetAllAzureModels returns all known Azure model IDs from static data
func GetAllAzureModels() []string {
	models := make([]string, 0, len(azureModelPricing))
	for modelID := range azureModelPricing {
		models = append(models, modelID)
	}
	return models
}

// GetAllAzureModelMetadata returns all known Azure model metadata from static data
func GetAllAzureModelMetadata() []*llmtypes.ModelMetadata {
	result := make([]*llmtypes.ModelMetadata, 0, len(azureModelPricing))
	for modelID := range azureModelPricing {
		if m, err := GetAzureModelMetadata(modelID); err == nil && m != nil {
			result = append(result, m)
		}
	}
	return result
}

// IsAzureModelSupported checks if a model ID is supported by Azure
func IsAzureModelSupported(modelID string) bool {
	_, err := GetAzureModelMetadata(modelID)
	return err == nil
}

// AzureDeployment represents a deployed model in Azure
type AzureDeployment struct {
	ID         string                   `json:"id"`
	Model      string                   `json:"model"`
	Owner      string                   `json:"owner"`
	Object     string                   `json:"object"`
	Status     string                   `json:"status"`
	CreatedAt  int64                    `json:"created_at"`
	UpdatedAt  int64                    `json:"updated_at"`
	ScaleSettings map[string]interface{} `json:"scale_settings"`
}

// AzureDeploymentsResponse represents the response from Azure's Deployments API
type AzureDeploymentsResponse struct {
	Data []AzureDeployment `json:"data"`
}

// FetchAzureDeployments fetches available chat models from Azure API
// Returns models that support chat_completion and inference
// Works with both cognitiveservices.azure.com and services.ai.azure.com endpoints
func FetchAzureDeployments(endpoint, apiKey string) ([]AzureDeployment, error) {
	// Build the base URL
	baseURL := endpoint

	// Handle different Azure endpoint formats
	if strings.Contains(baseURL, "services.ai.azure.com") {
		// Azure AI Foundry format - extract base URL up to services.ai.azure.com
		idx := strings.Index(baseURL, "services.ai.azure.com")
		if idx != -1 {
			baseURL = baseURL[:idx+len("services.ai.azure.com")]
		}
	} else if strings.Contains(baseURL, "cognitiveservices.azure.com") {
		// Cognitive Services format - clean up path
		if idx := strings.Index(baseURL, "/openai"); idx != -1 {
			baseURL = baseURL[:idx]
		}
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	var modelsURL string

	// Both endpoint types use similar models API but with different paths
	if strings.Contains(baseURL, "services.ai.azure.com") {
		// Azure AI Foundry uses /openai/v1/models (no api-version needed)
		modelsURL = strings.TrimSuffix(baseURL, "/") + "/openai/v1/models"
	} else {
		// Cognitive Services uses /openai/models with api-version
		modelsURL = strings.TrimSuffix(baseURL, "/") + "/openai/models?api-version=2024-10-21"
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Azure models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Azure API returned status %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp AzureModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to decode Azure models response: %w", err)
	}

	// Convert models to deployments format (for consistency)
	// Filter for models that support inference and chat completion
	deployments := make([]AzureDeployment, 0, len(modelsResp.Data))
	for _, model := range modelsResp.Data {
		if model.Capabilities.Inference && model.Capabilities.ChatCompletion {
			deployments = append(deployments, AzureDeployment{
				ID:        model.ID,
				Model:     model.ID,
				Status:    model.Status,
				CreatedAt: model.CreatedAt,
			})
		}
	}

	return deployments, nil
}

// GetAzureDeployedModels fetches deployed models and returns them as ModelMetadata
func GetAzureDeployedModels(endpoint, apiKey string) ([]*llmtypes.ModelMetadata, error) {
	deployments, err := FetchAzureDeployments(endpoint, apiKey)
	if err != nil {
		return nil, err
	}

	result := make([]*llmtypes.ModelMetadata, 0, len(deployments))
	for _, deployment := range deployments {
		// Use deployment ID as the model ID (this is what you use to call the model)
		modelID := deployment.ID

		// Get metadata based on the underlying model
		metadata, _ := GetAzureModelMetadata(deployment.Model)
		if metadata == nil {
			metadata, _ = GetAzureModelMetadata(modelID)
		}

		if metadata != nil {
			// Override the model ID with the deployment name
			metadata.ModelID = modelID
			metadata.ModelName = fmt.Sprintf("%s (%s)", modelID, deployment.Model)
			result = append(result, metadata)
		} else {
			// Create basic metadata for unknown models
			result = append(result, &llmtypes.ModelMetadata{
				ModelID:           modelID,
				ModelName:         fmt.Sprintf("%s (%s)", modelID, deployment.Model),
				ContextWindow:     128000,
				Provider:          "azure",
				SupportsToolCalls: true,
				SupportsJSONMode:  true,
			})
		}
	}

	return result, nil
}

// GetAzureModelCapabilities returns a formatted string of model capabilities
func GetAzureModelCapabilities(modelID string) string {
	metadata, err := GetAzureModelMetadata(modelID)
	if err != nil {
		return "unknown"
	}

	capabilities := []string{}
	if metadata.SupportsToolCalls {
		capabilities = append(capabilities, "tool_calls")
	}
	if metadata.SupportsJSONMode {
		capabilities = append(capabilities, "json_mode")
	}
	if metadata.SupportsReasoningEffort {
		capabilities = append(capabilities, "reasoning_effort")
	}
	if metadata.SupportsThinkingLevel {
		capabilities = append(capabilities, "thinking_level")
	}
	if metadata.SupportsThinkingBudget {
		capabilities = append(capabilities, "thinking_budget")
	}

	if len(capabilities) == 0 {
		return "text_generation"
	}

	return fmt.Sprintf("text_generation,%s", strings.Join(capabilities, ","))
}
