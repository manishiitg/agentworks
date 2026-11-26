package llmproviders

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	bedrockadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/bedrock"
	deepgramadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/deepgram"
	elevenlabsadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/elevenlabs"
	kimiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/kimi"
	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
	zaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/zai"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"google.golang.org/genai"
)

type LLMDefaultsResponse struct {
	PrimaryConfig           map[string]interface{} `json:"primary_config"`
	OpenrouterConfig        map[string]interface{} `json:"openrouter_config"`
	BedrockConfig           map[string]interface{} `json:"bedrock_config"`
	OpenaiConfig            map[string]interface{} `json:"openai_config"`
	AnthropicConfig         map[string]interface{} `json:"anthropic_config"`
	AzureConfig             map[string]interface{} `json:"azure_config"`
	ZAIConfig               map[string]interface{} `json:"zai_config"`
	KimiConfig              map[string]interface{} `json:"kimi_config"`
	MinimaxConfig           map[string]interface{} `json:"minimax_config"`
	MinimaxCodingPlanConfig map[string]interface{} `json:"minimax_coding_plan_config"`
	ElevenLabsConfig        map[string]interface{} `json:"elevenlabs_config"`
	DeepgramConfig          map[string]interface{} `json:"deepgram_config"`
	AvailableModels         map[string][]string    `json:"available_models"`
}

// APIKeyValidationRequest represents a request to validate an API key
type APIKeyValidationRequest struct {
	Provider string                 `json:"provider"`
	APIKey   string                 `json:"api_key"`
	ModelID  string                 `json:"model_id,omitempty"` // Optional model ID for Bedrock validation
	Options  map[string]interface{} `json:"options,omitempty"`
}

// APIKeyValidationResponse represents the response for API key validation
type APIKeyValidationResponse struct {
	Valid            bool                   `json:"valid"`
	Message          string                 `json:"message,omitempty"`
	Error            string                 `json:"error,omitempty"`
	CorrectedOptions map[string]interface{} `json:"corrected_options,omitempty"`
}

// GetLLMDefaults returns default LLM configurations from environment variables
func GetLLMDefaults() LLMDefaultsResponse {
	// Get primary configuration from environment
	defaultProvider := os.Getenv("AGENT_PROVIDER")
	if defaultProvider == "" {
		defaultProvider = "openrouter" // fallback default
	}

	defaultModel := os.Getenv("AGENT_MODEL")
	if defaultModel == "" {
		defaultModel = "x-ai/grok-code-fast-1" // fallback default
	}

	// Get API keys from environment for prefilling
	openrouterAPIKey := os.Getenv("OPENROUTER_API_KEY")
	if openrouterAPIKey == "" {
		openrouterAPIKey = os.Getenv("OPEN_ROUTER_API_KEY") // Fallback to old naming
	}
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")

	// Bedrock configuration
	bedrockModel := os.Getenv("BEDROCK_MODEL")
	if bedrockModel == "" {
		bedrockModel = os.Getenv("BEDROCK_PRIMARY_MODEL") // Fallback to old naming
	}
	if bedrockModel == "" {
		bedrockModel = "us.anthropic.claude-sonnet-4-20250514-v1:0" // fallback default
	}

	bedrockRegion := os.Getenv("BEDROCK_REGION")
	if bedrockRegion == "" {
		bedrockRegion = "us-east-1" // fallback default
	}

	// OpenAI configuration
	openaiModel := os.Getenv("OPENAI_MODEL")
	if openaiModel == "" {
		openaiModel = os.Getenv("OPENAI_PRIMARY_MODEL") // Fallback to old naming
	}
	if openaiModel == "" {
		openaiModel = "gpt-4o" // fallback default
	}

	// Anthropic configuration
	anthropicModel := os.Getenv("ANTHROPIC_PRIMARY_MODEL")
	if anthropicModel == "" {
		anthropicModel = "claude-sonnet-4-20250514"
	}
	anthropicAPIKey := os.Getenv("ANTHROPIC_API_KEY")

	// Azure configuration
	azureModel := os.Getenv("AZURE_PRIMARY_MODEL")
	azureAPIKey := os.Getenv("AZURE_AI_API_KEY")
	azureEndpoint := os.Getenv("AZURE_AI_ENDPOINT")

	// Z.AI configuration
	zaiModel := os.Getenv("ZAI_PRIMARY_MODEL")
	if zaiModel == "" {
		zaiModel = zaiadapter.ModelGLM51
	}
	zaiAPIKey := os.Getenv("ZAI_API_KEY")

	// Kimi configuration
	kimiModel := os.Getenv("KIMI_PRIMARY_MODEL")
	if kimiModel == "" {
		kimiModel = kimiadapter.ModelKimiK26
	}
	kimiAPIKey := os.Getenv("KIMI_API_KEY")

	// MiniMax configuration
	minimaxModel := os.Getenv("MINIMAX_PRIMARY_MODEL")
	if minimaxModel == "" {
		minimaxModel = "MiniMax-M2.7"
	}
	minimaxAPIKey := os.Getenv("MINIMAX_API_KEY")

	// MiniMax Coding Plan configuration (uses Anthropic model names)
	minimaxCodingPlanModel := os.Getenv("MINIMAX_CODING_PLAN_PRIMARY_MODEL")
	if minimaxCodingPlanModel == "" {
		minimaxCodingPlanModel = "claude-sonnet-4-5"
	}
	minimaxCodingPlanAPIKey := os.Getenv("MINIMAX_CODING_PLAN_API_KEY")

	// ElevenLabs configuration for media tools
	elevenLabsModel := os.Getenv("ELEVENLABS_PRIMARY_MODEL")
	if elevenLabsModel == "" {
		elevenLabsModel = elevenlabsadapter.DefaultModelID
	}
	elevenLabsAPIKey := os.Getenv("ELEVENLABS_API_KEY")

	// Deepgram configuration for media tools
	deepgramModel := os.Getenv("DEEPGRAM_PRIMARY_MODEL")
	if deepgramModel == "" {
		deepgramModel = deepgramadapter.DefaultTranscriptionModelID
	}
	deepgramAPIKey := os.Getenv("DEEPGRAM_API_KEY")

	// Build response
	return LLMDefaultsResponse{
		PrimaryConfig: map[string]interface{}{
			"provider": defaultProvider,
			"model_id": defaultModel,
		},
		OpenrouterConfig: map[string]interface{}{
			"provider": "openrouter",
			"model_id": defaultModel,
			"api_key":  openrouterAPIKey,
		},
		BedrockConfig: map[string]interface{}{
			"provider": "bedrock",
			"model_id": bedrockModel,
			"region":   bedrockRegion,
		},
		OpenaiConfig: map[string]interface{}{
			"provider": "openai",
			"model_id": openaiModel,
			"api_key":  openaiAPIKey,
		},
		AnthropicConfig: map[string]interface{}{
			"provider": "anthropic",
			"model_id": anthropicModel,
			"api_key":  anthropicAPIKey,
		},
		AzureConfig: map[string]interface{}{
			"provider": "azure",
			"model_id": azureModel,
			"api_key":  azureAPIKey,
			"endpoint": azureEndpoint,
		},
		ZAIConfig: map[string]interface{}{
			"provider": "z-ai",
			"model_id": zaiModel,
			"api_key":  zaiAPIKey,
		},
		KimiConfig: map[string]interface{}{
			"provider": "kimi",
			"model_id": kimiModel,
			"api_key":  kimiAPIKey,
		},
		MinimaxConfig: map[string]interface{}{
			"provider": "minimax",
			"model_id": minimaxModel,
			"api_key":  minimaxAPIKey,
		},
		MinimaxCodingPlanConfig: map[string]interface{}{
			"provider": "minimax-coding-plan",
			"model_id": minimaxCodingPlanModel,
			"api_key":  minimaxCodingPlanAPIKey,
		},
		ElevenLabsConfig: map[string]interface{}{
			"provider": "elevenlabs",
			"model_id": elevenLabsModel,
			"api_key":  elevenLabsAPIKey,
		},
		DeepgramConfig: map[string]interface{}{
			"provider": "deepgram",
			"model_id": deepgramModel,
			"api_key":  deepgramAPIKey,
		},
		AvailableModels: map[string][]string{
			"bedrock":             getBedrockAvailableModels(),
			"openrouter":          getOpenRouterAvailableModels(),
			"openai":              getOpenAIAvailableModels(),
			"anthropic":           getAnthropicAvailableModels(),
			"azure":               getAzureAvailableModels(),
			"z-ai":                getZAIAvailableModels(),
			"kimi":                getKimiAvailableModels(),
			"minimax":             getMiniMaxAvailableModels(),
			"minimax-coding-plan": getMiniMaxCodingPlanAvailableModels(),
			"elevenlabs":          getElevenLabsAvailableModels(),
			"deepgram":            getDeepgramAvailableModels(),
		},
	}
}

func getZAIAvailableModels() []string {
	modelsStr := os.Getenv("ZAI_AVAILABLE_MODELS")
	if modelsStr != "" {
		var models []string
		for _, m := range strings.Split(modelsStr, ",") {
			if t := strings.TrimSpace(m); t != "" {
				models = append(models, t)
			}
		}
		if len(models) > 0 {
			return models
		}
	}

	return zaiadapter.GetDefaultVisibleZAIModelIDs()
}

func getKimiAvailableModels() []string {
	modelsStr := os.Getenv("KIMI_AVAILABLE_MODELS")
	if modelsStr != "" {
		var models []string
		for _, m := range strings.Split(modelsStr, ",") {
			if t := strings.TrimSpace(m); t != "" {
				models = append(models, t)
			}
		}
		if len(models) > 0 {
			return models
		}
	}

	return kimiadapter.GetDefaultVisibleKimiModelIDs()
}

// getMiniMaxCodingPlanAvailableModels returns Anthropic model names available via MiniMax coding plan
func getMiniMaxCodingPlanAvailableModels() []string {
	modelsStr := os.Getenv("MINIMAX_CODING_PLAN_AVAILABLE_MODELS")
	if modelsStr != "" {
		var models []string
		for _, m := range strings.Split(modelsStr, ",") {
			if t := strings.TrimSpace(m); t != "" {
				models = append(models, t)
			}
		}
		if len(models) > 0 {
			return models
		}
	}
	// Default: Anthropic model names that MiniMax coding plan supports
	return []string{
		"claude-sonnet-4-5",
		"claude-opus-4-6",
		"claude-haiku-4-5-20251001",
	}
}

func getElevenLabsAvailableModels() []string {
	modelsStr := os.Getenv("ELEVENLABS_AVAILABLE_MODELS")
	if modelsStr != "" {
		var models []string
		for _, m := range strings.Split(modelsStr, ",") {
			if t := strings.TrimSpace(m); t != "" {
				models = append(models, t)
			}
		}
		if len(models) > 0 {
			return models
		}
	}
	return []string{
		elevenlabsadapter.DefaultModelID,
		"eleven_turbo_v2_5",
		"eleven_flash_v2_5",
		"eleven_v3",
		elevenlabsadapter.DefaultMusicModelID,
	}
}

func getDeepgramAvailableModels() []string {
	modelsStr := os.Getenv("DEEPGRAM_AVAILABLE_MODELS")
	if modelsStr != "" {
		var models []string
		for _, m := range strings.Split(modelsStr, ",") {
			if t := strings.TrimSpace(m); t != "" {
				models = append(models, t)
			}
		}
		if len(models) > 0 {
			return models
		}
	}
	return []string{
		deepgramadapter.DefaultTranscriptionModelID,
		"nova-3-multilingual",
		"nova-2",
		"base",
		deepgramadapter.DefaultModelID,
		"aura-2-luna-en",
		"aura-2-asteria-en",
		"aura-2-apollo-en",
	}
}

// ValidateAPIKey validates API keys for OpenRouter, OpenAI, Bedrock, and Vertex
func ValidateAPIKey(req APIKeyValidationRequest) APIKeyValidationResponse {
	// Use fmt.Printf for logging in validation functions
	fmt.Printf("[API KEY VALIDATION] Request received for provider: %s\n", req.Provider)

	var isValid bool
	var message string
	var err error
	var correctedOptions map[string]interface{}

	fmt.Printf("[API KEY VALIDATION] Validating %s API key\n", req.Provider)
	switch req.Provider {
	case "openrouter":
		isValid, message, err = validateOpenRouterAPIKey(req.APIKey, req.ModelID, req.Options)
	case "openai":
		isValid, message, err = validateOpenAIAPIKey(req.APIKey, req.ModelID, req.Options)
	case "bedrock":
		// Bedrock uses AWS credentials, test them instead of API key
		fmt.Printf("[API KEY VALIDATION] Testing AWS Bedrock credentials\n")
		isValid, message, err = validateBedrockCredentials(req.ModelID, req.Options)
	case "vertex":
		// Vertex supports both API key and OAuth authentication
		if req.APIKey == "" {
			// Test OAuth authentication (gcloud/service account/ADC)
			fmt.Printf("[API KEY VALIDATION] Testing Vertex AI OAuth credentials\n")
			isValid, message, err = validateVertexCredentials(req.ModelID, req.Options)
		} else {
			// Test API key authentication
			fmt.Printf("[API KEY VALIDATION] Testing Vertex AI API key\n")
			isValid, message, err = validateVertexAPIKey(req.APIKey, req.ModelID, req.Options)
		}
	case "anthropic":
		// Anthropic validation with real GenerateContent call
		fmt.Printf("[API KEY VALIDATION] Testing Anthropic API key\n")
		isValid, message, err = validateAnthropicAPIKey(req.APIKey, req.ModelID, req.Options)
	case "minimax":
		// MiniMax validation with real GenerateContent call
		fmt.Printf("[API KEY VALIDATION] Testing MiniMax API key\n")
		isValid, message, err = validateMinimaxAPIKey(req.APIKey, req.ModelID, req.Options)
	case "minimax-coding-plan":
		// MiniMax Coding Plan validation — uses Anthropic model names
		fmt.Printf("[API KEY VALIDATION] Testing MiniMax Coding Plan API key\n")
		isValid, message, err = validateMinimaxCodingPlanAPIKey(req.APIKey, req.ModelID, req.Options)
	case "elevenlabs":
		fmt.Printf("[API KEY VALIDATION] Testing ElevenLabs API key\n")
		isValid, message, err = validateElevenLabsAPIKey(req.APIKey)
	case "deepgram":
		fmt.Printf("[API KEY VALIDATION] Testing Deepgram API key\n")
		isValid, message, err = validateDeepgramAPIKey(req.APIKey)
	case "azure":
		// Azure AI validation with real GenerateContent call
		fmt.Printf("[API KEY VALIDATION] Testing Azure AI API key\n")
		isValid, message, correctedOptions, err = validateAzureAPIKey(req.APIKey, req.ModelID, req.Options)
	case "z-ai":
		fmt.Printf("[API KEY VALIDATION] Testing Z.AI API key\n")
		isValid, message, err = validateZAIAPIKey(req.APIKey, req.ModelID, req.Options)
	case "kimi":
		fmt.Printf("[API KEY VALIDATION] Testing Kimi API key\n")
		isValid, message, err = validateKimiAPIKey(req.APIKey, req.ModelID, req.Options)
	default:
		fmt.Printf("[API KEY VALIDATION WARN] Unsupported provider: %s\n", req.Provider)
		return APIKeyValidationResponse{
			Valid: false,
			Error: "Unsupported provider",
		}
	}

	// Handle validation errors
	if err != nil {
		fmt.Printf("[API KEY VALIDATION ERROR] %s validation failed: %v\n", req.Provider, err)
		return APIKeyValidationResponse{
			Valid: false,
			Error: fmt.Sprintf("Validation failed: %v", err),
		}
	}

	// Return validation result
	if isValid {
		fmt.Printf("[API KEY VALIDATION SUCCESS] %s: %s\n", req.Provider, message)
	} else {
		fmt.Printf("[API KEY VALIDATION FAILED] %s: %s\n", req.Provider, message)
	}

	return APIKeyValidationResponse{
		Valid:            isValid,
		Message:          message,
		CorrectedOptions: correctedOptions,
	}
}

func validateZAIAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[ZAI VALIDATION] Starting API key validation\n")

	if apiKey == "" {
		return false, "Z.AI API key is required", nil
	}

	if modelID == "" {
		modelID = zaiadapter.ModelGLM51
		fmt.Printf("[ZAI VALIDATION] Using default model: %s\n", modelID)
	}

	originalKey := os.Getenv("ZAI_API_KEY")
	os.Setenv("ZAI_API_KEY", apiKey)
	defer func() {
		if originalKey != "" {
			os.Setenv("ZAI_API_KEY", originalKey)
		} else {
			os.Unsetenv("ZAI_API_KEY")
		}
	}()

	noopLog := &noopLoggerImpl{}
	temperature := extractTemperatureFromOptions(options)

	config := Config{
		Provider:    ProviderZAI,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
	}

	llm, err := initializeZAI(config)
	if err != nil {
		fmt.Printf("[ZAI VALIDATION ERROR] Failed to create LLM instance: %v\n", err)
		return false, fmt.Sprintf("Failed to create Z.AI LLM instance: %v", err), nil
	}

	callOptions := createCallOptionsFromMap(options)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Say hello in one word."}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[ZAI VALIDATION ERROR] %v\n", err)
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return false, "Invalid Z.AI API key", nil
		}
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
			return false, "Z.AI API rate limit exceeded", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "Z.AI service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("Z.AI test generation failed: %v", err), nil
	}

	fmt.Printf("[ZAI VALIDATION SUCCESS] Z.AI API key is valid\n")
	return true, fmt.Sprintf("Z.AI API key is valid for model %s", modelID), nil
}

func validateKimiAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[KIMI VALIDATION] Starting API key validation\n")

	if apiKey == "" {
		return false, "Kimi API key is required", nil
	}
	if !strings.HasPrefix(apiKey, "sk-kimi-") && !strings.HasPrefix(apiKey, "sk-") {
		return false, "Invalid Kimi API key format", nil
	}

	if modelID == "" {
		modelID = kimiadapter.ModelKimiK26
		fmt.Printf("[KIMI VALIDATION] Using default model: %s\n", modelID)
	}

	noopLog := &noopLoggerImpl{}
	temperature := extractTemperatureFromOptions(options)

	config := Config{
		Provider:    ProviderKimi,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
		APIKeys: &ProviderAPIKeys{
			Kimi: &apiKey,
		},
	}

	llm, err := initializeKimi(config)
	if err != nil {
		fmt.Printf("[KIMI VALIDATION ERROR] Failed to create LLM instance: %v\n", err)
		return false, fmt.Sprintf("Failed to create Kimi LLM instance: %v", err), nil
	}

	callOptions := createCallOptionsFromMap(options)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly: KIMI_OK"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[KIMI VALIDATION ERROR] %v\n", err)
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return false, "Invalid Kimi API key", nil
		}
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
			return false, "Kimi API rate limit exceeded", nil
		}
		if strings.Contains(err.Error(), "claude cli not found") {
			return false, "Claude Code CLI not found. Install it with: npm install -g @anthropic-ai/claude-code", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "Kimi service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("Kimi test generation failed: %v", err), nil
	}

	fmt.Printf("[KIMI VALIDATION SUCCESS] Kimi API key is valid\n")
	return true, fmt.Sprintf("Kimi API key is valid for model %s", modelID), nil
}

// validateOpenRouterAPIKey validates an OpenRouter API key by making a real GenerateContent call
func validateOpenRouterAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[OPENROUTER VALIDATION] Starting API key validation\n")

	// Basic format validation
	if !strings.HasPrefix(apiKey, "sk-or-") {
		fmt.Printf("[OPENROUTER VALIDATION WARN] Format validation failed - missing sk-or- prefix\n")
		return false, "Invalid OpenRouter API key format", nil
	}
	fmt.Printf("[OPENROUTER VALIDATION] Format validation passed\n")

	// Use a default model if none provided
	if modelID == "" {
		modelID = "moonshotai/kimi-k2"
		fmt.Printf("[OPENROUTER VALIDATION] Using default model: %s\n", modelID)
	}

	// Set API key in environment temporarily for initialization
	originalKey := os.Getenv("OPEN_ROUTER_API_KEY")
	os.Setenv("OPEN_ROUTER_API_KEY", apiKey)
	defer func() {
		if originalKey != "" {
			os.Setenv("OPEN_ROUTER_API_KEY", originalKey)
		} else {
			os.Unsetenv("OPEN_ROUTER_API_KEY")
		}
	}()

	// Create a no-op logger for validation
	noopLog := &noopLoggerImpl{}

	// Extract temperature from options (no default - let the model use its own default)
	temperature := extractTemperatureFromOptions(options)

	// Create OpenRouter LLM instance
	fmt.Printf("[OPENROUTER VALIDATION] Creating OpenRouter LLM instance (temperature: %v)\n", temperature)
	config := Config{
		Provider:    ProviderOpenRouter,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
	}

	llm, err := initializeOpenRouter(config)
	if err != nil {
		fmt.Printf("[OPENROUTER VALIDATION ERROR] Failed to create LLM instance: %v\n", err)
		return false, fmt.Sprintf("Failed to create OpenRouter LLM instance: %v", err), nil
	}

	// Create call options from map
	callOptions := createCallOptionsFromMap(options)

	// Test the LLM with a simple generation call
	fmt.Printf("[OPENROUTER VALIDATION] Making test generation call to OpenRouter\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[OPENROUTER VALIDATION ERROR] OpenRouter test generation failed: %v\n", err)
		// Check for specific error types
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return false, "Invalid OpenRouter API key", nil
		}
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
			return false, "OpenRouter API rate limit exceeded", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "OpenRouter service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("OpenRouter test generation failed: %v", err), nil
	}

	fmt.Printf("[OPENROUTER VALIDATION SUCCESS] OpenRouter API key is valid\n")
	return true, fmt.Sprintf("OpenRouter API key is valid for model %s", modelID), nil
}

// validateOpenAIAPIKey validates an OpenAI API key by making a real GenerateContent call
func validateOpenAIAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[OPENAI VALIDATION] Starting API key validation\n")
	// Basic format validation
	if !strings.HasPrefix(apiKey, "sk-") {
		fmt.Printf("[OPENAI VALIDATION WARN] Format validation failed - missing sk- prefix\n")
		return false, "Invalid OpenAI API key format", nil
	}
	fmt.Printf("[OPENAI VALIDATION] Format validation passed\n")

	// Use a default model if none provided
	if modelID == "" {
		modelID = "gpt-4o-mini"
		fmt.Printf("[OPENAI VALIDATION] Using default model: %s\n", modelID)
	}

	// Set API key in environment temporarily for initialization
	originalKey := os.Getenv("OPENAI_API_KEY")
	os.Setenv("OPENAI_API_KEY", apiKey)
	defer func() {
		if originalKey != "" {
			os.Setenv("OPENAI_API_KEY", originalKey)
		} else {
			os.Unsetenv("OPENAI_API_KEY")
		}
	}()

	// Create a no-op logger for validation
	noopLog := &noopLoggerImpl{}

	// Extract temperature from options (no default - let the model use its own default)
	temperature := extractTemperatureFromOptions(options)

	// Create OpenAI LLM instance
	fmt.Printf("[OPENAI VALIDATION] Creating OpenAI LLM instance (temperature: %v)\n", temperature)
	config := Config{
		Provider:    ProviderOpenAI,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
	}

	llm, err := initializeOpenAI(config)
	if err != nil {
		fmt.Printf("[OPENAI VALIDATION ERROR] Failed to create LLM instance: %v\n", err)
		return false, fmt.Sprintf("Failed to create OpenAI LLM instance: %v", err), nil
	}

	// Create call options from map
	callOptions := createCallOptionsFromMap(options)

	// Test the LLM with a simple generation call
	fmt.Printf("[OPENAI VALIDATION] Making test generation call to OpenAI\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[OPENAI VALIDATION ERROR] OpenAI test generation failed: %v\n", err)
		// Check for specific error types
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return false, "Invalid OpenAI API key", nil
		}
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
			return false, "OpenAI API rate limit exceeded", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "OpenAI service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("OpenAI test generation failed: %v", err), nil
	}

	fmt.Printf("[OPENAI VALIDATION SUCCESS] OpenAI API key is valid\n")
	return true, fmt.Sprintf("OpenAI API key is valid for model %s", modelID), nil
}

// validateAnthropicAPIKey validates an Anthropic API key by making a real GenerateContent call
func validateAnthropicAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[ANTHROPIC VALIDATION] Starting API key validation\n")

	// Basic format validation - Anthropic API keys start with "sk-ant-"
	if !strings.HasPrefix(apiKey, "sk-ant-") {
		fmt.Printf("[ANTHROPIC VALIDATION WARN] Format validation failed - missing sk-ant- prefix\n")
		return false, "Invalid Anthropic API key format", nil
	}
	fmt.Printf("[ANTHROPIC VALIDATION] Format validation passed\n")

	// Use a default model if none provided. Haiku 4.5 is the cheapest
	// current model — perfect for a low-cost auth-check call.
	if modelID == "" {
		modelID = "claude-haiku-4-5"
		fmt.Printf("[ANTHROPIC VALIDATION] Using default model: %s\n", modelID)
	}

	// Set API key in environment temporarily for initialization
	originalKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", apiKey)
	defer func() {
		if originalKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", originalKey)
		} else {
			os.Unsetenv("ANTHROPIC_API_KEY")
		}
	}()

	// Create a no-op logger for validation
	noopLog := &noopLoggerImpl{}

	// Extract temperature from options (no default - let the model use its own default)
	temperature := extractTemperatureFromOptions(options)

	// Create Anthropic LLM instance
	fmt.Printf("[ANTHROPIC VALIDATION] Creating Anthropic LLM instance (temperature: %v)\n", temperature)
	config := Config{
		Provider:    ProviderAnthropic,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
	}

	llm, err := initializeAnthropic(config)
	if err != nil {
		fmt.Printf("[ANTHROPIC VALIDATION ERROR] Failed to create LLM instance: %v\n", err)
		return false, fmt.Sprintf("Failed to create Anthropic LLM instance: %v", err), nil
	}

	// Create call options from map
	callOptions := createCallOptionsFromMap(options)

	// Test the LLM with a simple generation call
	fmt.Printf("[ANTHROPIC VALIDATION] Making test generation call to Anthropic\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[ANTHROPIC VALIDATION ERROR] Anthropic test generation failed: %v\n", err)
		// Check for specific error types
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return false, "Invalid Anthropic API key", nil
		}
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
			return false, "Anthropic API rate limit exceeded", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "Anthropic service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("Anthropic test generation failed: %v", err), nil
	}

	fmt.Printf("[ANTHROPIC VALIDATION SUCCESS] Anthropic API key is valid\n")
	return true, fmt.Sprintf("Anthropic API key is valid for model %s", modelID), nil
}

// validateMinimaxAPIKey validates a MiniMax API key by making a real GenerateContent call
func validateMinimaxAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[MINIMAX VALIDATION] Starting API key validation\n")

	if apiKey == "" {
		return false, "MiniMax API key is required", nil
	}

	// Use a default model if none provided
	if modelID == "" {
		modelID = "MiniMax-M2.7"
		fmt.Printf("[MINIMAX VALIDATION] Using default model: %s\n", modelID)
	}

	// Set API key in environment temporarily for initialization
	originalKey := os.Getenv("MINIMAX_API_KEY")
	os.Setenv("MINIMAX_API_KEY", apiKey)
	defer func() {
		if originalKey != "" {
			os.Setenv("MINIMAX_API_KEY", originalKey)
		} else {
			os.Unsetenv("MINIMAX_API_KEY")
		}
	}()

	noopLog := &noopLoggerImpl{}
	temperature := extractTemperatureFromOptions(options)

	config := Config{
		Provider:    ProviderMiniMax,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
	}

	llm, err := initializeMiniMax(config)
	if err != nil {
		fmt.Printf("[MINIMAX VALIDATION ERROR] Failed to create LLM instance: %v\n", err)
		return false, fmt.Sprintf("Failed to create MiniMax LLM instance: %v", err), nil
	}

	callOptions := createCallOptionsFromMap(options)

	fmt.Printf("[MINIMAX VALIDATION] Making test generation call\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[MINIMAX VALIDATION ERROR] MiniMax test generation failed: %v\n", err)
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "1004") {
			return false, "Invalid MiniMax API key", nil
		}
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
			return false, "MiniMax API rate limit exceeded", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "MiniMax service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("MiniMax test generation failed: %v", err), nil
	}

	fmt.Printf("[MINIMAX VALIDATION SUCCESS] MiniMax API key is valid\n")
	return true, fmt.Sprintf("MiniMax API key is valid for model %s", modelID), nil
}

func validateElevenLabsAPIKey(apiKey string) (bool, string, error) {
	fmt.Printf("[ELEVENLABS VALIDATION] Starting API key validation\n")
	if strings.TrimSpace(apiKey) == "" {
		return false, "ElevenLabs API key is required", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.elevenlabs.io/v1/user", nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("xi-api-key", strings.TrimSpace(apiKey))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "ElevenLabs service timeout - check network connectivity", nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, "ElevenLabs API key is valid", nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, "Invalid ElevenLabs API key", nil
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return false, "ElevenLabs API rate limit exceeded", nil
	}
	return false, fmt.Sprintf("ElevenLabs validation failed with status %d: %s", resp.StatusCode, truncateValidationBody(data)), nil
}

func validateDeepgramAPIKey(apiKey string) (bool, string, error) {
	fmt.Printf("[DEEPGRAM VALIDATION] Starting API key validation\n")
	if strings.TrimSpace(apiKey) == "" {
		return false, "Deepgram API key is required", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.deepgram.com/v1/projects", nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+strings.TrimSpace(apiKey))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "Deepgram service timeout - check network connectivity", nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, "Deepgram API key is valid", nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, "Invalid Deepgram API key", nil
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return false, "Deepgram API rate limit exceeded", nil
	}
	return false, fmt.Sprintf("Deepgram validation failed with status %d: %s", resp.StatusCode, truncateValidationBody(data)), nil
}

func truncateValidationBody(data []byte) string {
	msg := strings.TrimSpace(string(data))
	if len(msg) > 300 {
		return msg[:300] + "..."
	}
	return msg
}

// validateMinimaxCodingPlanAPIKey validates a MiniMax coding plan API key using an Anthropic model name.
func validateMinimaxCodingPlanAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[MINIMAX-CP VALIDATION] Starting coding plan API key validation\n")

	if apiKey == "" {
		return false, "MiniMax Coding Plan API key is required", nil
	}

	if modelID == "" {
		modelID = "claude-sonnet-4-5"
		fmt.Printf("[MINIMAX-CP VALIDATION] Using default model: %s\n", modelID)
	}

	noopLog := &noopLoggerImpl{}
	temperature := extractTemperatureFromOptions(options)

	config := Config{
		Provider:    ProviderMiniMaxCodingPlan,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
	}

	os.Setenv("MINIMAX_CODING_PLAN_API_KEY", apiKey)
	llm, err := initializeMiniMaxCodingPlan(config)
	if err != nil {
		return false, fmt.Sprintf("Failed to create MiniMax Coding Plan LLM instance: %v", err), nil
	}

	callOptions := createCallOptionsFromMap(options)
	_, err = llm.GenerateContent(context.Background(), []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Say hello in one word."}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[MINIMAX-CP VALIDATION ERROR] %v\n", err)
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return false, "Invalid MiniMax Coding Plan API key", nil
		}
		return false, fmt.Sprintf("MiniMax Coding Plan test generation failed: %v", err), nil
	}

	fmt.Printf("[MINIMAX-CP VALIDATION SUCCESS] MiniMax Coding Plan API key is valid\n")
	return true, fmt.Sprintf("MiniMax Coding Plan API key is valid for model %s", modelID), nil
}

// validateAzureAPIKey validates an Azure AI API key by making a real GenerateContent call
// Returns: isValid, message, correctedOptions, error
func validateAzureAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, map[string]interface{}, error) {
	fmt.Printf("[AZURE VALIDATION] Starting API key validation for model: %s\n", modelID)

	// Check for required endpoint in options
	var endpoint string
	if options != nil {
		if e, ok := options["endpoint"].(string); ok {
			endpoint = e
		}
	}
	if endpoint == "" {
		// Fallback to environment variable
		endpoint = os.Getenv("AZURE_AI_ENDPOINT")
	}
	if endpoint == "" {
		fmt.Printf("[AZURE VALIDATION WARN] No endpoint provided\n")
		return false, "Azure endpoint URL is required", nil, nil
	}
	fmt.Printf("[AZURE VALIDATION] Endpoint: %s\n", endpoint)

	// Basic validation - API key should not be empty
	if apiKey == "" {
		fmt.Printf("[AZURE VALIDATION WARN] API key is empty\n")
		return false, "Azure API key is required", nil, nil
	}
	fmt.Printf("[AZURE VALIDATION] API key format validation passed\n")

	// Use a default model if none provided
	if modelID == "" {
		modelID = "gpt-4o"
		fmt.Printf("[AZURE VALIDATION] Using default model: %s\n", modelID)
	}

	// Helper to try validation with a specific endpoint and modelID
	tryValidation := func(testEndpoint string, testModelID string) (bool, string, error) {
		// Extract optional fields from options
		var apiVersion, region string
		if options != nil {
			if v, ok := options["api_version"].(string); ok {
				apiVersion = v
			}
			if r, ok := options["region"].(string); ok {
				region = r
			}
		}

		// Set environment variables temporarily for initialization
		originalEndpoint := os.Getenv("AZURE_AI_ENDPOINT")
		originalKey := os.Getenv("AZURE_AI_API_KEY")
		originalVersion := os.Getenv("AZURE_AI_API_VERSION")
		originalRegion := os.Getenv("AZURE_AI_REGION")

		os.Setenv("AZURE_AI_ENDPOINT", testEndpoint)
		os.Setenv("AZURE_AI_API_KEY", apiKey)
		if apiVersion != "" {
			os.Setenv("AZURE_AI_API_VERSION", apiVersion)
		}
		if region != "" {
			os.Setenv("AZURE_AI_REGION", region)
		}
		defer func() {
			// Restore original environment variables
			if originalEndpoint != "" {
				os.Setenv("AZURE_AI_ENDPOINT", originalEndpoint)
			} else {
				os.Unsetenv("AZURE_AI_ENDPOINT")
			}
			if originalKey != "" {
				os.Setenv("AZURE_AI_API_KEY", originalKey)
			} else {
				os.Unsetenv("AZURE_AI_API_KEY")
			}
			if originalVersion != "" {
				os.Setenv("AZURE_AI_API_VERSION", originalVersion)
			} else {
				os.Unsetenv("AZURE_AI_API_VERSION")
			}
			if originalRegion != "" {
				os.Setenv("AZURE_AI_REGION", originalRegion)
			} else {
				os.Unsetenv("AZURE_AI_REGION")
			}
		}()

		// Create a no-op logger for validation
		noopLog := &noopLoggerImpl{}

		// Extract temperature from options
		temperature := extractTemperatureFromOptions(options)

		// Create Azure LLM instance
		fmt.Printf("[AZURE VALIDATION] Creating Azure LLM instance (endpoint: %s, model: %s)\n", testEndpoint, testModelID)
		config := Config{
			Provider:    ProviderAzure,
			ModelID:     testModelID,
			Temperature: temperature,
			Logger:      noopLog,
			Context:     context.Background(),
		}

		llm, err := initializeAzure(config)
		if err != nil {
			return false, fmt.Sprintf("Failed to create Azure LLM instance: %v", err), nil
		}

		// Create call options from map
		callOptions := createCallOptionsFromMap(options)

		// Test the LLM with a simple generation call
		fmt.Printf("[AZURE VALIDATION] Making test generation call to Azure AI\n")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
			{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}},
			},
		}, callOptions...)
		if err != nil {
			// Check for specific error types
			if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "Unauthorized") {
				return false, "Invalid Azure API key", nil
			}
			if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
				return false, "Azure API rate limit exceeded", nil
			}
			if strings.Contains(err.Error(), "timeout") {
				return false, "Azure service timeout - check network connectivity", nil
			}
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "unknown_model") || strings.Contains(err.Error(), "Unknown model") || strings.Contains(err.Error(), "DeploymentNotFound") {
				return false, fmt.Sprintf("Model '%s' not found or endpoint incorrect", testModelID), nil
			}
			return false, fmt.Sprintf("Azure test generation failed: %v", err), nil
		}

		return true, fmt.Sprintf("Azure API key is valid for model %s", testModelID), nil
	}

	// Clean Model ID: strip date suffix like -2026-01-14
	cleanModelID := modelID
	if parts := strings.Split(modelID, "-20"); len(parts) > 1 {
		cleanModelID = parts[0]
	}

	// Try combinations
	endpoints := []string{endpoint}

	// Case 1: services.ai.azure.com -> cognitiveservices.azure.com
	if strings.Contains(endpoint, "services.ai.azure.com") {
		parts := strings.Split(endpoint, "services.ai.azure.com")
		if len(parts) > 0 {
			prefix := parts[0]
			if strings.HasPrefix(prefix, "https://") {
				resourceName := strings.TrimPrefix(prefix, "https://")
				resourceName = strings.TrimSuffix(resourceName, ".")
				if resourceName != "" {
					derivedEndpoint := fmt.Sprintf("https://%s.cognitiveservices.azure.com/", resourceName)
					endpoints = append(endpoints, derivedEndpoint)
				}
			}
		}
	}

	models := []string{modelID}
	if cleanModelID != modelID {
		models = append(models, cleanModelID)
	}

	var lastMessage string
	var lastErr error

	for _, testEndpoint := range endpoints {
		for _, testModel := range models {
			isValid, message, err := tryValidation(testEndpoint, testModel)
			if isValid {
				// Success! Check if we need to return corrected options
				correctedOptions := make(map[string]interface{})
				if options != nil {
					for k, v := range options {
						correctedOptions[k] = v
					}
				}

				isCorrected := false
				if testEndpoint != endpoint {
					correctedOptions["endpoint"] = testEndpoint
					isCorrected = true
				}
				if testModel != modelID {
					correctedOptions["model_id"] = testModel
					isCorrected = true
				}

				if isCorrected {
					msg := fmt.Sprintf("%s (Note: We automatically optimized your configuration to endpoint: %s, model: %s)", message, testEndpoint, testModel)
					return true, msg, correctedOptions, nil
				}
				return true, message, nil, nil
			}
			lastMessage = message
			lastErr = err
		}
	}

	return false, lastMessage, nil, lastErr
}

// validateVertexCredentials validates Vertex AI OAuth credentials (gcloud/service account/ADC)
func validateVertexCredentials(modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[VERTEX VALIDATION] Starting OAuth credentials validation\n")

	// Check for required environment variables
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = os.Getenv("VERTEX_PROJECT_ID")
	}
	if projectID == "" {
		fmt.Printf("[VERTEX VALIDATION WARN] GOOGLE_CLOUD_PROJECT or VERTEX_PROJECT_ID not set\n")
		return false, "GOOGLE_CLOUD_PROJECT or VERTEX_PROJECT_ID environment variable is required for OAuth authentication", nil
	}

	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if location == "" {
		location = os.Getenv("VERTEX_LOCATION_ID")
	}
	if location == "" {
		location = "us-central1"
		fmt.Printf("[VERTEX VALIDATION] Using default location: %s\n", location)
	}
	// Vertex AI doesn't support "global" location
	if location == "global" {
		location = "us-central1"
		fmt.Printf("[VERTEX VALIDATION] Location 'global' is not valid for Vertex AI, using: %s\n", location)
	}

	fmt.Printf("[VERTEX VALIDATION] Testing OAuth with project: %s, location: %s\n", projectID, location)

	// Use a default model if none provided
	if modelID == "" {
		modelID = vertexadapter.ModelGemini35FlashLite
		fmt.Printf("[VERTEX VALIDATION] Using default model: %s\n", modelID)
	}

	// Test OAuth by creating an LLM instance and making a real API call
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a no-op logger for validation
	noopLog := &noopLoggerImpl{}

	// Detect if this is an Anthropic model (starts with "claude-")
	isAnthropicModel := strings.HasPrefix(modelID, "claude-")

	var llm llmtypes.Model
	var err error

	if isAnthropicModel {
		// Create Vertex Anthropic adapter for OAuth
		fmt.Printf("[VERTEX VALIDATION] Creating Vertex Anthropic adapter for model: %s\n", modelID)
		llm = vertexadapter.NewVertexAnthropicAdapter(projectID, location, modelID, noopLog)
	} else {
		// For Gemini models with OAuth, use Vertex AI backend (not Gemini Developer API)
		fmt.Printf("[VERTEX VALIDATION] Creating Vertex AI Gemini adapter for model: %s with OAuth\n", modelID)

		// Set environment variables for Vertex AI OAuth (genai library reads these)
		originalProject := os.Getenv("GOOGLE_CLOUD_PROJECT")
		originalLocation := os.Getenv("GOOGLE_CLOUD_LOCATION")
		originalUseVertex := os.Getenv("GOOGLE_GENAI_USE_VERTEXAI")

		os.Setenv("GOOGLE_CLOUD_PROJECT", projectID)
		os.Setenv("GOOGLE_CLOUD_LOCATION", location)
		os.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")

		defer func() {
			if originalProject != "" {
				os.Setenv("GOOGLE_CLOUD_PROJECT", originalProject)
			} else {
				os.Unsetenv("GOOGLE_CLOUD_PROJECT")
			}
			if originalLocation != "" {
				os.Setenv("GOOGLE_CLOUD_LOCATION", originalLocation)
			} else {
				os.Unsetenv("GOOGLE_CLOUD_LOCATION")
			}
			if originalUseVertex != "" {
				os.Setenv("GOOGLE_GENAI_USE_VERTEXAI", originalUseVertex)
			} else {
				os.Unsetenv("GOOGLE_GENAI_USE_VERTEXAI")
			}
		}()

		// Create Google GenAI client with OAuth (no API key, uses BackendVertexAI via env vars)
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			Backend: genai.BackendVertexAI,
		})
		if err != nil {
			fmt.Printf("[VERTEX VALIDATION ERROR] Failed to create Vertex AI client: %v\n", err)
			return false, fmt.Sprintf("Failed to create Vertex AI client for Gemini model '%s': %v", modelID, err), nil
		}

		// Create Gemini adapter with Vertex AI backend
		llm = vertexadapter.NewGoogleGenAIAdapter(client, modelID, noopLog)
	}

	// Create call options from map
	callOptions := createCallOptionsFromMap(options)

	// Test the LLM with a simple generation call
	fmt.Printf("[VERTEX VALIDATION] Making test generation call to Vertex AI\n")
	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[VERTEX VALIDATION ERROR] Vertex AI test generation failed: %v\n", err)
		// Check for specific error types
		if strings.Contains(err.Error(), "authentication") || strings.Contains(err.Error(), "unauthorized") {
			return false, "OAuth authentication failed. Make sure you have run 'gcloud auth application-default login' or set up service account credentials.", nil
		}
		if strings.Contains(err.Error(), "permission") || strings.Contains(err.Error(), "forbidden") {
			return false, "OAuth credentials do not have permission to access Vertex AI", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "Vertex AI service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("Vertex AI test generation failed: %v", err), nil
	}

	fmt.Printf("[VERTEX VALIDATION SUCCESS] Vertex AI OAuth credentials are valid\n")
	return true, fmt.Sprintf("Vertex AI OAuth authentication successful (project: %s, location: %s)", projectID, location), nil
}

// validateVertexAPIKey validates a Vertex AI (Google Gemini) API key by making a real GenerateContent call
func validateVertexAPIKey(apiKey string, modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[VERTEX VALIDATION] Starting API key validation\n")
	// Basic validation - Google API keys don't have a specific prefix
	if apiKey == "" {
		fmt.Printf("[VERTEX VALIDATION WARN] API key is empty\n")
		return false, "API key is empty", nil
	}
	fmt.Printf("[VERTEX VALIDATION] API key format check passed\n")

	// Use a default model if none provided
	if modelID == "" {
		modelID = vertexadapter.ModelGemini35FlashLite
		fmt.Printf("[VERTEX VALIDATION] Using default model: %s\n", modelID)
	}

	// Set API key in environment temporarily for initialization
	originalKey := os.Getenv("VERTEX_API_KEY")
	originalGoogleKey := os.Getenv("GOOGLE_API_KEY")
	os.Setenv("VERTEX_API_KEY", apiKey)
	defer func() {
		if originalKey != "" {
			os.Setenv("VERTEX_API_KEY", originalKey)
		} else {
			os.Unsetenv("VERTEX_API_KEY")
		}
		if originalGoogleKey != "" {
			os.Setenv("GOOGLE_API_KEY", originalGoogleKey)
		}
	}()

	// Create a no-op logger for validation
	noopLog := &noopLoggerImpl{}

	// Extract temperature from options (no default - let the model use its own default)
	temperature := extractTemperatureFromOptions(options)

	// Create Vertex LLM instance (for Gemini models with API key)
	fmt.Printf("[VERTEX VALIDATION] Creating Vertex Gemini LLM instance (temperature: %v)\n", temperature)
	config := Config{
		Provider:    ProviderVertex,
		ModelID:     modelID,
		Temperature: temperature,
		Logger:      noopLog,
		Context:     context.Background(),
		APIKeys: &ProviderAPIKeys{
			Vertex: &apiKey,
		},
	}

	llm, err := initializeVertexGemini(config, modelID, noopLog)
	if err != nil {
		fmt.Printf("[VERTEX VALIDATION ERROR] Failed to create LLM instance: %v\n", err)
		return false, fmt.Sprintf("Failed to create Vertex LLM instance: %v", err), nil
	}

	// Create call options from map
	callOptions := createCallOptionsFromMap(options)

	// Test the LLM with a simple generation call
	fmt.Printf("[VERTEX VALIDATION] Making test generation call to Vertex AI\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Hi"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[VERTEX VALIDATION ERROR] Vertex AI test generation failed: %v\n", err)
		// Check for specific error types
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return false, "Invalid Vertex AI API key", nil
		}
		if strings.Contains(err.Error(), "permission") || strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "403") {
			return false, "API key lacks required permissions", nil
		}
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			return false, fmt.Sprintf("Model %s not found", modelID), nil
		}
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429") {
			return false, "Vertex AI API rate limit exceeded", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "Vertex AI service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("Vertex AI test generation failed: %v", err), nil
	}

	fmt.Printf("[VERTEX VALIDATION SUCCESS] Vertex AI API key is valid\n")
	return true, fmt.Sprintf("Vertex AI API key is valid for model %s", modelID), nil
}

// noopLoggerImpl is a no-op logger implementation for validation functions
type noopLoggerImpl struct{}

func (n *noopLoggerImpl) Infof(format string, v ...any)             {}
func (n *noopLoggerImpl) Errorf(format string, v ...any)            {}
func (n *noopLoggerImpl) Debugf(format string, args ...interface{}) {}

// DefaultLogger is a simple logger implementation that writes to stdout or a file
type DefaultLogger struct {
	output *os.File
	level  string
}

// NewDefaultLogger creates a new default logger instance
// If logFile is empty, logs to stdout. If logFile is provided, logs to that file.
// level can be "info" or "debug" - debug level enables Debugf output
func NewDefaultLogger(logFile string, level string) (interfaces.Logger, error) {
	var output *os.File
	var err error

	if logFile == "" {
		// Use stdout
		output = os.Stdout
	} else {
		// Create log directory if it doesn't exist
		logDir := filepath.Dir(logFile)
		if logDir != "." && logDir != "" {
			if err := os.MkdirAll(logDir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create log directory: %w", err)
			}
		}

		// Open log file
		output, err = os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
	}

	// Validate level
	if level != "info" && level != "debug" {
		level = "info" // Default to info if invalid
	}

	return &DefaultLogger{
		output: output,
		level:  level,
	}, nil
}

// Infof logs an info message
func (l *DefaultLogger) Infof(format string, v ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(l.output, "[%s] [INFO] %s\n", timestamp, fmt.Sprintf(format, v...))
}

// Errorf logs an error message
func (l *DefaultLogger) Errorf(format string, v ...any) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(l.output, "[%s] [ERROR] %s\n", timestamp, fmt.Sprintf(format, v...))
}

// Debugf logs a debug message (only if level is "debug")
func (l *DefaultLogger) Debugf(format string, args ...interface{}) {
	if l.level == "debug" {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(l.output, "[%s] [DEBUG] %s\n", timestamp, fmt.Sprintf(format, args...))
	}
}

// validateBedrockCredentials validates AWS Bedrock credentials and region
func validateBedrockCredentials(modelID string, options map[string]interface{}) (bool, string, error) {
	fmt.Printf("[BEDROCK VALIDATION] Starting AWS Bedrock credentials validation\n")
	// Check if AWS region is configured
	region := os.Getenv("AWS_REGION")
	if region == "" {
		fmt.Printf("[BEDROCK VALIDATION WARN] AWS_REGION environment variable not set\n")
		return false, "AWS_REGION environment variable not set", nil
	}
	fmt.Printf("[BEDROCK VALIDATION] AWS region: %s", region)

	// Check if AWS credentials are configured
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	if accessKey == "" || secretKey == "" {
		fmt.Printf("[BEDROCK VALIDATION WARN] AWS credentials not configured\n")
		return false, "AWS credentials not configured (AWS_ACCESS_KEY_ID or AWS_SECRET_ACCESS_KEY missing)", nil
	}
	fmt.Printf("[BEDROCK VALIDATION] AWS credentials configured\n")
	// Use provided model ID or fallback to default
	if modelID == "" {
		modelID = "us.anthropic.claude-3-haiku-20240307-v1:0" // fallback default
		fmt.Printf("[BEDROCK VALIDATION] Using fallback model ID: %s\n", modelID)
	} else {
		fmt.Printf("[BEDROCK VALIDATION] Using provided model ID: %s\n", modelID)
	}

	// Test Bedrock access by creating a Bedrock LLM instance
	fmt.Printf("[BEDROCK VALIDATION] Testing Bedrock access by creating LLM instance\n")
	// Load AWS SDK configuration
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		fmt.Printf("[BEDROCK VALIDATION ERROR] Failed to load AWS config: %v\n", err)
		return false, "Failed to load AWS configuration", err
	}

	// Create Bedrock runtime client
	client := bedrockruntime.NewFromConfig(cfg)

	// Create a simple no-op logger for validation
	noopLog := &noopLoggerImpl{}
	// Create Bedrock adapter instance
	llm := bedrockadapter.NewBedrockAdapter(client, modelID, noopLog)

	// Create call options from map
	callOptions := createCallOptionsFromMap(options)

	// Test the LLM with a simple generation call
	fmt.Printf("[BEDROCK VALIDATION] Making test generation call to Bedrock\n")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err = llm.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "test"}},
		},
	}, callOptions...)
	if err != nil {
		fmt.Printf("[BEDROCK VALIDATION ERROR] Bedrock test generation failed: %v\n", err)
		// Check for specific error types
		if strings.Contains(err.Error(), "AccessDenied") {
			return false, "AWS credentials do not have permission to access Bedrock", nil
		}
		if strings.Contains(err.Error(), "InvalidUserID.NotFound") {
			return false, "AWS credentials are invalid", nil
		}
		if strings.Contains(err.Error(), "timeout") {
			return false, "Bedrock service timeout - check network connectivity", nil
		}
		return false, fmt.Sprintf("Bedrock test generation failed: %v", err), nil
	}

	fmt.Printf("[BEDROCK VALIDATION SUCCESS] AWS Bedrock credentials are valid\n")
	return true, "AWS Bedrock credentials are valid", nil
}

// Helper functions to get available models from environment variables

// getBedrockAvailableModels returns available Bedrock models from environment variables
func getBedrockAvailableModels() []string {
	// Get from environment variable
	modelsStr := os.Getenv("BEDROCK_AVAILABLE_MODELS")
	if modelsStr == "" {
		// Fallback to old naming
		modelsStr = os.Getenv("BEDROCK_MODELS")
	}
	if modelsStr == "" {
		// Return empty array if no environment variable is set
		return []string{}
	}

	// Parse comma-separated models
	models := strings.Split(modelsStr, ",")
	for i, model := range models {
		models[i] = strings.TrimSpace(model)
	}
	return models
}

// getOpenRouterAvailableModels returns available OpenRouter models from environment variables
func getOpenRouterAvailableModels() []string {
	// Get from environment variable
	modelsStr := os.Getenv("OPENROUTER_AVAILABLE_MODELS")
	if modelsStr == "" {
		// Fallback to old naming
		modelsStr = os.Getenv("OPEN_ROUTER_MODELS")
	}
	if modelsStr == "" {
		// Return empty array if no environment variable is set
		return []string{}
	}

	// Parse comma-separated models
	models := strings.Split(modelsStr, ",")
	for i, model := range models {
		models[i] = strings.TrimSpace(model)
	}
	return models
}

// getOpenAIAvailableModels returns available OpenAI models from environment variables
func getOpenAIAvailableModels() []string {
	// Get from environment variable
	modelsStr := os.Getenv("OPENAI_AVAILABLE_MODELS")
	if modelsStr == "" {
		// Fallback to old naming
		modelsStr = os.Getenv("OPENAI_MODELS")
	}
	if modelsStr == "" {
		// Return empty array if no environment variable is set
		return []string{}
	}

	// Parse comma-separated models
	models := strings.Split(modelsStr, ",")
	for i, model := range models {
		models[i] = strings.TrimSpace(model)
	}
	return models
}

// getAnthropicAvailableModels returns available Anthropic models from environment variables
func getAnthropicAvailableModels() []string {
	// Get from environment variable
	modelsStr := os.Getenv("ANTHROPIC_AVAILABLE_MODELS")
	if modelsStr == "" {
		modelsStr = os.Getenv("ANTHROPIC_MODELS")
	}
	if modelsStr == "" {
		return []string{}
	}

	// Parse comma-separated models
	models := strings.Split(modelsStr, ",")
	for i, model := range models {
		models[i] = strings.TrimSpace(model)
	}
	return models
}

// getAzureAvailableModels returns available Azure models from environment variables
func getAzureAvailableModels() []string {
	// Get from environment variable
	modelsStr := os.Getenv("AZURE_AVAILABLE_MODELS")
	if modelsStr == "" {
		modelsStr = os.Getenv("AZURE_MODELS")
	}
	if modelsStr == "" {
		return []string{}
	}

	// Parse comma-separated models
	models := strings.Split(modelsStr, ",")
	for i, model := range models {
		models[i] = strings.TrimSpace(model)
	}
	return models
}

// getMiniMaxAvailableModels returns available MiniMax models from environment variables
func getMiniMaxAvailableModels() []string {
	modelsStr := os.Getenv("MINIMAX_AVAILABLE_MODELS")
	if modelsStr == "" {
		modelsStr = os.Getenv("MINIMAX_MODELS")
	}
	if modelsStr == "" {
		return []string{}
	}
	models := strings.Split(modelsStr, ",")
	for i, model := range models {
		models[i] = strings.TrimSpace(model)
	}
	return models
}

// Helper to create call options from map
// extractTemperatureFromOptions extracts temperature from options map
// Returns 0 if not found (model will use its own default)
func extractTemperatureFromOptions(options map[string]interface{}) float64 {
	if options == nil {
		return 0
	}
	if temp, ok := options["temperature"].(float64); ok {
		return temp
	}
	if temp, ok := options["temperature"].(int); ok {
		return float64(temp)
	}
	return 0
}

func createCallOptionsFromMap(options map[string]interface{}) []llmtypes.CallOption {
	var callOptions []llmtypes.CallOption
	if options == nil {
		return callOptions
	}

	// Handle temperature
	if temp, ok := options["temperature"].(float64); ok && temp >= 0 {
		callOptions = append(callOptions, llmtypes.WithTemperature(temp))
	} else if temp, ok := options["temperature"].(int); ok && temp >= 0 {
		callOptions = append(callOptions, llmtypes.WithTemperature(float64(temp)))
	}

	if effort, ok := options["reasoning_effort"].(string); ok && effort != "" {
		callOptions = append(callOptions, llmtypes.WithReasoningEffort(effort))
	}
	if verbosity, ok := options["verbosity"].(string); ok && verbosity != "" {
		callOptions = append(callOptions, llmtypes.WithVerbosity(verbosity))
	}
	if thinkingLevel, ok := options["thinking_level"].(string); ok && thinkingLevel != "" {
		callOptions = append(callOptions, llmtypes.WithThinkingLevel(thinkingLevel))
	}
	if thinkingBudget, ok := options["thinking_budget"].(float64); ok && thinkingBudget > 0 {
		callOptions = append(callOptions, llmtypes.WithThinkingBudget(int(thinkingBudget)))
	} else if thinkingBudget, ok := options["thinking_budget"].(int); ok && thinkingBudget > 0 {
		callOptions = append(callOptions, llmtypes.WithThinkingBudget(thinkingBudget))
	}
	return callOptions
}
