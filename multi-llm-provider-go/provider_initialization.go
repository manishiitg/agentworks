package llmproviders

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/acpcli"
	agycli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	anthropicadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"
	azureadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/azure"
	bedrockadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/bedrock"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	codexcli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	cursorcli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	kimiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/kimi"
	minimaxadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/minimax"
	musecli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	openaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/openai"
	picli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
	zaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/zai"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	openaisdk "github.com/openai/openai-go/v3"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/openai/openai-go/v3/option"

	"google.golang.org/genai"
)

// initializeBedrock creates and configures a Bedrock LLM instance
func initializeBedrock(config Config) (llmtypes.Model, error) {
	// LLM Initialization event data - use typed structure directly
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    40000, // Will be set at call time
		TopP:         config.Temperature,
		User:         "bedrock_user",
		CustomFields: map[string]string{
			"provider":  "bedrock",
			"operation": "llm_initialization",
		},
	}

	var logger = config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Debug: Log AWS environment variables
	logger.Infof("Initializing Bedrock LLM with model: %s", config.ModelID)

	// Get region from config first, then environment (default to us-east-1)
	region := ""
	if config.APIKeys != nil && config.APIKeys.Bedrock != nil && config.APIKeys.Bedrock.Region != "" {
		region = config.APIKeys.Bedrock.Region
		logger.Infof("Using region from config: %s", region)
	} else {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
			logger.Infof("AWS_REGION not set, using default: %s", region)
		} else {
			logger.Infof("Using region from environment: %s", region)
		}
	}

	logger.Infof("AWS_REGION: %s", region)
	logger.Infof("AWS_ACCESS_KEY_ID: %s", os.Getenv("AWS_ACCESS_KEY_ID"))
	logger.Infof("AWS_SECRET_ACCESS_KEY: %s", os.Getenv("AWS_SECRET_ACCESS_KEY"))

	// Load AWS SDK configuration
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		logger.Errorf("Failed to load AWS config: %w", err)

		// Emit LLM initialization error event - use typed structure directly
		errorMetadata := LLMMetadata{
			ModelVersion: config.ModelID,
			User:         "bedrock_user",
			CustomFields: map[string]string{
				"provider":  "bedrock",
				"operation": OperationLLMInitialization,
				"error":     err.Error(),
				"status":    StatusLLMFailed,
			},
		}
		emitLLMInitializationError(config.EventEmitter, string(config.Provider), config.ModelID, OperationLLMInitialization, err, config.TraceID, errorMetadata)

		return nil, fmt.Errorf("load aws config: %w", err)
	}

	// Create Bedrock runtime client
	client := bedrockruntime.NewFromConfig(cfg)

	// Set default model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "us.anthropic.claude-3-sonnet-20240229-v1:0"
	}

	// Create Bedrock adapter
	llm := bedrockadapter.NewBedrockAdapter(client, modelID, logger)

	// Emit LLM initialization success event - use typed structure directly
	successMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		User:         "bedrock_user",
		CustomFields: map[string]string{
			"provider":     "bedrock",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), config.ModelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Bedrock LLM - model_id: %s", config.ModelID)
	return llm, nil
}

// IsO3O4Model detects o3/o4 models (OpenAI) for conditional logic in agent
func IsO3O4Model(modelID string) bool {
	// Covers gpt-4o, gpt-4.0, gpt-4.1, gpt-4, gpt-3.5, etc
	return strings.HasPrefix(modelID, "o3") ||
		strings.HasPrefix(modelID, "o4")
}

// Helper functions for event emission
func emitLLMInitializationStart(emitter interfaces.EventEmitter, provider string, modelID string, temperature float64, traceID interfaces.TraceID, metadata LLMMetadata) {
	if emitter != nil {
		emitter.EmitLLMInitializationStart(provider, modelID, temperature, traceID, metadata)
	}
}

func emitLLMInitializationSuccess(emitter interfaces.EventEmitter, provider string, modelID string, capabilities string, traceID interfaces.TraceID, metadata LLMMetadata) {
	if emitter != nil {
		emitter.EmitLLMInitializationSuccess(provider, modelID, capabilities, traceID, metadata)
	}
}

func emitLLMInitializationError(emitter interfaces.EventEmitter, provider string, modelID string, operation string, err error, traceID interfaces.TraceID, metadata LLMMetadata) {
	if emitter != nil {
		emitter.EmitLLMInitializationError(provider, modelID, operation, err, traceID, metadata)
	}
}

func emitLLMGenerationSuccess(emitter interfaces.EventEmitter, provider string, modelID string, operation string, messages int, temperature float64, messageContent string, responseLength int, choicesCount int, traceID interfaces.TraceID, metadata LLMMetadata) {
	if emitter != nil {
		emitter.EmitLLMGenerationSuccess(provider, modelID, operation, messages, temperature, messageContent, responseLength, choicesCount, traceID, metadata)
	}
}

func emitLLMGenerationError(emitter interfaces.EventEmitter, provider string, modelID string, operation string, messages int, temperature float64, messageContent string, err error, traceID interfaces.TraceID, metadata LLMMetadata) {
	if emitter != nil {
		emitter.EmitLLMGenerationError(provider, modelID, operation, messages, temperature, messageContent, err, traceID, metadata)
	}
}

func emitToolCallDetected(emitter interfaces.EventEmitter, provider string, modelID string, toolCallID string, toolName string, arguments string, traceID interfaces.TraceID, metadata LLMMetadata) {
	if emitter != nil {
		emitter.EmitToolCallDetected(provider, modelID, toolCallID, toolName, arguments, traceID, metadata)
	}
}

// initializeOpenAI creates and configures an OpenAI LLM instance
func initializeOpenAI(config Config) (llmtypes.Model, error) {
	// Check for API key from config first, then environment
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.OpenAI != nil && *config.APIKeys.OpenAI != "" {
		apiKey = *config.APIKeys.OpenAI
	} else {
		// Try environment variable
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required for OpenAI provider (not found in config or environment)")
	}

	// LLM Initialization event data - use typed structure directly
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0, // Will be set at call time
		TopP:         config.Temperature,
		User:         "openai_user",
		CustomFields: map[string]string{
			"provider":  "openai",
			"operation": "llm_initialization",
		},
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Set default model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "gpt-4.1"
	}

	// Create OpenAI client using official SDK
	client := openaisdk.NewClient(
		option.WithAPIKey(apiKey),
	)

	// Create OpenAI adapter
	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	llm := openaiadapter.NewOpenAIAdapter(&client, modelID, logger)

	// Emit LLM initialization success event - use typed structure directly
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "openai_user",
		CustomFields: map[string]string{
			"provider":     "openai",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized OpenAI LLM - model_id: %s", modelID)
	return llm, nil
}

// initializeZAI creates and configures a Z.AI LLM instance using the OpenAI-compatible
// Chat Completions API surface exposed at api.z.ai.
func initializeZAI(config Config) (llmtypes.Model, error) {
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.ZAI != nil && *config.APIKeys.ZAI != "" {
		apiKey = *config.APIKeys.ZAI
	} else {
		apiKey = os.Getenv("ZAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ZAI_API_KEY is required for Z.AI provider (not found in config or environment)")
	}

	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0,
		TopP:         config.Temperature,
		User:         "zai_user",
		CustomFields: map[string]string{
			"provider":  "z-ai",
			"operation": "llm_initialization",
		},
	}
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	modelID := config.ModelID
	if modelID == "" {
		modelID = zaiadapter.ModelGLM51
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	baseURL := os.Getenv("ZAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.z.ai/api/coding/paas/v4"
	}

	client := openaisdk.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)

	llm := openaiadapter.NewCompatibleOpenAIAdapter(&client, modelID, logger, openaiadapter.OpenAICompatibilityConfig{
		ProviderName:   "z-ai",
		MetadataLookup: zaiadapter.GetZAIModelMetadata,
		RequestExtraFields: func(modelID string, opts *llmtypes.CallOptions) map[string]interface{} {
			extra := map[string]interface{}{}

			thinkingType := "enabled"
			if strings.EqualFold(opts.ReasoningEffort, "none") {
				thinkingType = "disabled"
			}
			extra["thinking"] = map[string]interface{}{
				"type":           thinkingType,
				"clear_thinking": true,
			}

			if opts.StreamChan != nil && len(opts.Tools) > 0 {
				extra["tool_stream"] = true
			}

			return extra
		},
	})
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "zai_user",
		CustomFields: map[string]string{
			"provider":     "z-ai",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Z.AI LLM - model_id: %s, base_url: %s", modelID, baseURL)
	return llm, nil
}

// initializeAnthropic creates and configures an Anthropic LLM instance
func initializeAnthropic(config Config) (llmtypes.Model, error) {
	// LLM Initialization event data - use typed structure directly
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0, // Will be set at call time
		TopP:         config.Temperature,
		User:         "anthropic_user",
		CustomFields: map[string]string{
			"provider":  "anthropic",
			"operation": "llm_initialization",
		},
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Check for API key from config first, then environment
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Anthropic != nil && *config.APIKeys.Anthropic != "" {
		apiKey = *config.APIKeys.Anthropic
	} else {
		// Try environment variable
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is required (not found in config or environment)")
	}

	// Use provided model or default. We default to the cheapest current
	// model on the active model line; the previous default
	// (claude-3-5-sonnet-20241022) is no longer accepted by the API and
	// any caller that did not explicitly set ModelID would 404.
	modelID := config.ModelID
	if modelID == "" {
		modelID = "claude-haiku-4-5"
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing Anthropic LLM with model: %s", modelID)

	// Create Anthropic SDK client
	// NewClient reads from environment by default, but we can explicitly set API key
	// Note: Beta header for prompt caching must be added per-request, not at client level
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey(apiKey),
	)

	// Create Anthropic adapter
	llm := anthropicadapter.NewAnthropicAdapter(client, modelID, logger)

	// Emit LLM initialization success event - use typed structure directly
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "anthropic_user",
		CustomFields: map[string]string{
			"provider":     "anthropic",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Anthropic LLM - model_id: %s", modelID)
	return llm, nil
}

// initializeMiniMax creates and configures a MiniMax LLM instance using the Anthropic-compatible API
func initializeMiniMax(config Config) (llmtypes.Model, error) {
	// Check for API key from config first, then environment
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.MiniMax != nil && *config.APIKeys.MiniMax != "" {
		apiKey = *config.APIKeys.MiniMax
	} else {
		apiKey = os.Getenv("MINIMAX_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("MINIMAX_API_KEY is required for MiniMax provider (not found in config or environment)")
	}

	modelID := config.ModelID
	if modelID == "" {
		modelID = minimaxadapter.ModelMiniMaxM27
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing MiniMax LLM with model: %s", modelID)

	llmMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "minimax_user",
		CustomFields: map[string]string{
			"provider":  "minimax",
			"operation": "llm_initialization",
		},
	}
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), modelID, config.Temperature, config.TraceID, llmMetadata)

	llm := minimaxadapter.NewMiniMaxAdapter(apiKey, modelID, logger)

	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "minimax_user",
		CustomFields: map[string]string{
			"provider":     "minimax",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized MiniMax LLM - model_id: %s", modelID)
	return llm, nil
}

// initializeMiniMaxCodingPlan creates a MiniMax coding plan adapter using Anthropic model names.
// The coding plan uses the same Anthropic-compatible endpoint but authenticates with a
// coding-plan-specific API key (MINIMAX_CODING_PLAN_API_KEY) and accepts Anthropic model names
// (e.g. claude-sonnet-4-5) which MiniMax maps to their equivalent models.
func initializeMiniMaxCodingPlan(config Config) (llmtypes.Model, error) {
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.MiniMaxCodingPlan != nil && *config.APIKeys.MiniMaxCodingPlan != "" {
		apiKey = *config.APIKeys.MiniMaxCodingPlan
	} else {
		apiKey = os.Getenv("MINIMAX_CODING_PLAN_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("MINIMAX_CODING_PLAN_API_KEY is required for MiniMax coding plan provider")
	}

	modelID := config.ModelID
	if modelID == "" {
		modelID = "claude-sonnet-4-5"
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing MiniMax Coding Plan LLM with model: %s", modelID)

	llm := minimaxadapter.NewMiniMaxCodingPlanAdapter(apiKey, modelID, logger)

	logger.Infof("Initialized MiniMax Coding Plan LLM - model_id: %s", modelID)
	return llm, nil
}

// initializeAzure creates and configures an Azure AI LLM instance
func initializeAzure(config Config) (llmtypes.Model, error) {
	// LLM Initialization event data
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0, // Will be set at call time
		TopP:         config.Temperature,
		User:         "azure_user",
		CustomFields: map[string]string{
			"provider":  "azure",
			"operation": OperationLLMInitialization,
		},
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Check for Azure config from APIKeys or environment
	var endpoint, apiKey, apiVersion, region string

	if config.APIKeys != nil && config.APIKeys.Azure != nil {
		endpoint = config.APIKeys.Azure.Endpoint
		apiKey = config.APIKeys.Azure.APIKey
		apiVersion = config.APIKeys.Azure.APIVersion
		region = config.APIKeys.Azure.Region
	}

	// Fallback to environment variables
	if endpoint == "" {
		endpoint = os.Getenv("AZURE_AI_ENDPOINT")
	}
	if apiKey == "" {
		apiKey = os.Getenv("AZURE_AI_API_KEY")
	}
	if apiVersion == "" {
		apiVersion = os.Getenv("AZURE_AI_API_VERSION")
	}
	if region == "" {
		region = os.Getenv("AZURE_AI_REGION")
	}

	// Validate required fields
	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_AI_ENDPOINT is required for Azure provider (not found in config or environment)")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("AZURE_AI_API_KEY is required for Azure provider (not found in config or environment)")
	}

	// Set default model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "gpt-4o"
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing Azure AI LLM - model_id: %s, endpoint: %s, region: %s", modelID, endpoint, region)

	// Create Azure adapter config
	azureConfig := azureadapter.AzureConfig{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		APIVersion: apiVersion,
		Region:     region,
	}

	// Create Azure adapter
	llm := azureadapter.NewAzureAdapter(azureConfig, modelID, logger)

	// Emit LLM initialization success event
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "azure_user",
		CustomFields: map[string]string{
			"provider":     "azure",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
			"endpoint":     endpoint,
			"region":       region,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Azure AI LLM - model_id: %s", modelID)
	return llm, nil
}

// initializeOpenRouter creates and configures an OpenRouter LLM instance
func initializeOpenRouter(config Config) (llmtypes.Model, error) {
	// LLM Initialization event data - use typed structure directly
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0, // Will be set at call time
		TopP:         config.Temperature,
		User:         "openrouter_user",
		CustomFields: map[string]string{
			"provider":  "openrouter",
			"operation": OperationLLMInitialization,
		},
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Check for API key from config first, then environment
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.OpenRouter != nil && *config.APIKeys.OpenRouter != "" {
		apiKey = *config.APIKeys.OpenRouter
	} else {
		// Try environment variables (check both naming conventions)
		apiKey = os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("OPEN_ROUTER_API_KEY")
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY or OPEN_ROUTER_API_KEY is required for OpenRouter provider (not found in config or environment)")
	}

	// Set default model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "moonshotai/kimi-k2"
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("🔧 Initializing OpenRouter LLM - model_id: %s, base_url: https://openrouter.ai/api/v1", modelID)

	// 🆕 DETAILED OPENROUTER INITIALIZATION LOGGING
	logger.Infof("🔧 [DEBUG] Creating OpenRouter LLM with OpenAI client...")
	logger.Infof("🔧 [DEBUG] Model: %s", modelID)
	logger.Infof("🔧 [DEBUG] Base URL: https://openrouter.ai/api/v1")
	logger.Infof("🔧 [DEBUG] API Key present: %v", apiKey != "")

	// Create OpenAI SDK client with OpenRouter base URL
	clientOptions := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL("https://openrouter.ai/api/v1"),
	}

	// Add optional OpenRouter headers if provided
	if httpReferer := os.Getenv("OPENROUTER_HTTP_REFERER"); httpReferer != "" {
		clientOptions = append(clientOptions, option.WithHeader("HTTP-Referer", httpReferer))
		logger.Infof("🔧 [DEBUG] Added HTTP-Referer header: %s", httpReferer)
	}
	if xTitle := os.Getenv("OPENROUTER_X_TITLE"); xTitle != "" {
		clientOptions = append(clientOptions, option.WithHeader("X-Title", xTitle))
		logger.Infof("🔧 [DEBUG] Added X-Title header: %s", xTitle)
	}

	client := openaisdk.NewClient(clientOptions...)

	// Create OpenAI adapter with OpenRouter configuration
	llm := openaiadapter.NewOpenAIAdapter(&client, modelID, logger)

	// 🆕 POST-INITIALIZATION LOGGING
	logger.Infof("🔧 [DEBUG] OpenRouter LLM creation completed - LLM: %v", llm != nil)

	// Emit LLM initialization success event - use typed structure directly
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "openrouter_user",
		CustomFields: map[string]string{
			"provider":     "openrouter",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("✅ Successfully initialized OpenRouter LLM - model_id: %s", modelID)
	return llm, nil
}

// initializeVertex creates and configures a Vertex AI LLM instance
// Supports both Gemini (via API key) and Anthropic (via OAuth2) models
func initializeVertex(config Config) (llmtypes.Model, error) {
	// LLM Initialization event data - use typed structure directly
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0, // Will be set at call time
		TopP:         config.Temperature,
		User:         "vertex_user",
		CustomFields: map[string]string{
			"provider":  "vertex",
			"operation": "llm_initialization",
		},
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Set default model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = vertexadapter.ModelGemini38Flash
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	// Detect if this is an Anthropic model (starts with "claude-\n")
	isAnthropicModel := strings.HasPrefix(modelID, "claude-")

	if isAnthropicModel {
		// Initialize Vertex AI Anthropic adapter
		return initializeVertexAnthropic(config, modelID, logger)
	}

	// Initialize Gemini adapter (existing implementation)
	return initializeVertexGemini(config, modelID, logger)
}

// initializeVertexAnthropic creates and configures a Vertex AI Anthropic LLM instance
func initializeVertexAnthropic(config Config, modelID string, logger interfaces.Logger) (llmtypes.Model, error) {
	logger.Infof("Initializing Vertex AI Anthropic LLM - model_id: %s", modelID)

	// Get required configuration
	projectID := os.Getenv("VERTEX_PROJECT_ID")
	if projectID == "" {
		return nil, fmt.Errorf("VERTEX_PROJECT_ID environment variable is required for Anthropic models")
	}

	locationID := os.Getenv("VERTEX_LOCATION_ID")
	if locationID == "" {
		locationID = "global" // Default location
		logger.Infof("VERTEX_LOCATION_ID not set, using default: %s", locationID)
	}

	// Create Vertex Anthropic adapter
	llm := vertexadapter.NewVertexAnthropicAdapter(projectID, locationID, modelID, logger)

	// Emit LLM initialization success event
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "vertex_user",
		CustomFields: map[string]string{
			"provider":     "vertex",
			"model_type":   "anthropic",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Vertex AI Anthropic LLM - model_id: %s, project: %s, location: %s", modelID, projectID, locationID)
	return llm, nil
}

// initializeVertexGemini creates and configures a Vertex AI Gemini LLM instance
func initializeVertexGemini(config Config, modelID string, logger interfaces.Logger) (llmtypes.Model, error) {
	logger.Infof("Initializing Vertex AI (Gemini) LLM with API key - model_id: %s", modelID)

	// Check for API key from config first, then environment
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Vertex != nil && *config.APIKeys.Vertex != "" {
		apiKey = *config.APIKeys.Vertex
		logger.Infof("🔑 [VERTEX AUTH] Using API key from config")
	} else {
		// Try environment variables (AI Studio key works under any of these names)
		apiKey = firstNonEmpty(
			os.Getenv("VERTEX_API_KEY"),
			os.Getenv("GEMINI_API_KEY"),
			os.Getenv("GOOGLE_API_KEY"),
		)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("an AI Studio API key is required for Gemini models — set VERTEX_API_KEY, GEMINI_API_KEY, or GOOGLE_API_KEY (or pass via config.APIKeys.Vertex)")
	}

	// Use provided context or use background context
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Create Google GenAI client with API key authentication
	// Using BackendGeminiAPI for Gemini Developer API
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		logger.Errorf("Failed to create GenAI client: %w", err)

		// Emit LLM initialization error event
		errorMetadata := LLMMetadata{
			ModelVersion: modelID,
			User:         "vertex_user",
			CustomFields: map[string]string{
				"provider":   "vertex",
				"model_type": "gemini",
				"operation":  OperationLLMInitialization,
				"error":      err.Error(),
				"status":     StatusLLMFailed,
			},
		}
		emitLLMInitializationError(config.EventEmitter, string(config.Provider), modelID, OperationLLMInitialization, err, config.TraceID, errorMetadata)

		return nil, fmt.Errorf("create genai client: %w", err)
	}

	// Create adapter wrapper that implements llmtypes.Model interface
	llm := vertexadapter.NewGoogleGenAIAdapter(client, modelID, logger)

	// Emit LLM initialization success event - use typed structure directly
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "vertex_user",
		CustomFields: map[string]string{
			"provider":     "vertex",
			"model_type":   "gemini",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Vertex AI Gemini LLM - model_id: %s", modelID)
	return llm, nil
}

// initializeClaudeCode creates and configures a Claude Code CLI adapter instance
func initializeClaudeCode(config Config) (llmtypes.Model, error) {
	// LLM Initialization event data
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0, // Will be set at call time or by CLI
		TopP:         config.Temperature,
		User:         "claude_code_user",
		CustomFields: map[string]string{
			"provider":  "claude-code",
			"operation": OperationLLMInitialization,
		},
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Set default model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "claude-code" // Default ID representing the CLI
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	transport, err := resolveClaudeCodeTransport(config.ClaudeCodeTransport)
	if err != nil {
		errorMetadata := LLMMetadata{
			ModelVersion: modelID,
			CustomFields: map[string]string{
				"provider":  "claude-code",
				"operation": OperationLLMInitialization,
			},
		}
		emitLLMInitializationError(config.EventEmitter, string(config.Provider), modelID, OperationLLMInitialization, err, config.TraceID, errorMetadata)
		return nil, err
	}

	logger.Infof("Initializing Claude Code %s adapter - model_id: %s", transport, modelID)

	// Claude Code authentication is intentionally limited to an explicitly
	// supplied workflow OAuth token or the CLI's saved login. Anthropic API keys
	// are never forwarded: doing so silently changes billing from the Claude
	// subscription to the Anthropic API account. The adapter also removes ambient
	// auth variables immediately before exec so a login shell cannot reintroduce
	// them from its startup files.
	var oauthToken string
	if config.APIKeys != nil && config.APIKeys.ClaudeCodeOAuthToken != nil {
		oauthToken = strings.TrimSpace(*config.APIKeys.ClaudeCodeOAuthToken)
	}
	if oauthToken != "" {
		logger.Infof("Claude Code: using an explicitly scoped OAuth token")
	} else {
		logger.Infof("Claude Code: using the CLI saved login")
	}

	// The selected transport is fixed for this adapter instance.
	llm := claudecodeadapter.NewClaudeCodeInteractiveAdapterWithOAuthToken(modelID, oauthToken, logger)
	llm.SetSDKTransport(transport == ClaudeCodeTransportSDK)

	// Emit LLM initialization success event
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "claude_code_user",
		CustomFields: map[string]string{
			"provider":     "claude-code",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
			"mode":         transport,
			"transport":    transport,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Claude Code %s adapter - model_id: %s", transport, modelID)
	return llm, nil
}

// CursorSDKEnabled reports the selected Cursor account and chat transport.
func CursorSDKEnabled() bool {
	transport, err := resolveCursorCLITransport()
	return err == nil && transport == CursorCLITransportSDK
}

func resolveCursorCLITransport() (string, error) {
	// Owner 2026-10-27: the newest transport is the default; older ones stay selectable.
	switch raw := strings.ToLower(strings.TrimSpace(os.Getenv(EnvCursorCLITransport))); raw {
	case "", CursorCLITransportSDK:
		return CursorCLITransportSDK, nil
	case CursorCLITransportTmux:
		return CursorCLITransportTmux, nil
	default:
		return "", fmt.Errorf("unsupported Cursor transport %q; use %s=sdk or tmux", raw, EnvCursorCLITransport)
	}
}

func resolveClaudeCodeTransport(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return normalizeClaudeCodeTransport(configured)
	}
	return normalizeClaudeCodeTransport(os.Getenv(EnvClaudeCodeTransport))
}

func normalizeClaudeCodeTransport(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ClaudeCodeTransportTmux:
		return ClaudeCodeTransportTmux, nil
	case "", ClaudeCodeTransportSDK:
		return ClaudeCodeTransportSDK, nil
	default:
		return "", fmt.Errorf("unsupported Claude Code transport %q; use %s=sdk or tmux", raw, EnvClaudeCodeTransport)
	}
}

// initializeMuseCLI creates and configures a Muse adapter instance.
// Auth is the stored `muse login` or META_API_KEY; no third-party key is
// ever forwarded.
func initializeMuseCLI(config Config) (llmtypes.Model, error) {
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0,
		TopP:         config.Temperature,
		User:         "muse_user",
		CustomFields: map[string]string{
			"provider":  "muse-cli",
			"operation": OperationLLMInitialization,
		},
	}

	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	modelID := config.ModelID
	if modelID == "" {
		modelID = DefaultMuseCLIModel
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing Muse adapter - model_id: %s", modelID)

	// Explicit per-call key wins (mcpagent forwards its MuseCLI key through
	// Config.APIKeys); otherwise META_API_KEY, otherwise the stored login.
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.MuseCLI != nil {
		apiKey = strings.TrimSpace(*config.APIKeys.MuseCLI)
	}
	if apiKey != "" {
		logger.Infof("Muse: using explicit API key (length=%d)", len(apiKey))
	} else if (config.APIKeys == nil || len(config.APIKeys.RuntimeEnvironment) == 0) && strings.TrimSpace(os.Getenv("META_API_KEY")) != "" {
		apiKey = strings.TrimSpace(os.Getenv("META_API_KEY"))
		logger.Infof("Muse: using API key from META_API_KEY env var (length=%d)", len(apiKey))
	} else {
		logger.Infof("Muse: no explicit API key, falling back to stored login")
	}

	llm := musecli.NewMuseCLIAdapter(apiKey, modelID, logger)

	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "muse_user",
		CustomFields: map[string]string{
			"provider":     "muse-cli",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Muse adapter (stub) - model_id: %s", modelID)
	return llm, nil
}

// initializeKimi creates and configures the Kimi API provider.
func initializeKimi(config Config) (llmtypes.Model, error) {
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0,
		TopP:         config.Temperature,
		User:         "kimi_user",
		CustomFields: map[string]string{
			"provider":  "kimi",
			"operation": OperationLLMInitialization,
		},
	}

	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	modelID := strings.TrimSpace(config.ModelID)
	if modelID == "" {
		modelID = kimiadapter.ModelKimiK26
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	if modelID == kimiadapter.ModelKimiCode {
		err := fmt.Errorf("kimi-code is no longer supported as a Kimi provider model; use kimi-k2.7-code for Kimi coding API workloads or Pi CLI for multi-model coding-agent plans")
		errorMetadata := LLMMetadata{
			ModelVersion: modelID,
			User:         "kimi_user",
			CustomFields: map[string]string{
				"provider":  "kimi",
				"operation": OperationLLMInitialization,
				"error":     err.Error(),
				"status":    StatusLLMFailed,
			},
		}
		emitLLMInitializationError(config.EventEmitter, string(config.Provider), modelID, OperationLLMInitialization, err, config.TraceID, errorMetadata)
		return nil, err
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Kimi != nil {
		apiKey = strings.TrimSpace(*config.APIKeys.Kimi)
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	}
	if apiKey == "" {
		err := fmt.Errorf("KIMI_API_KEY is required for Kimi provider (not found in config or environment)")
		errorMetadata := LLMMetadata{
			ModelVersion: modelID,
			User:         "kimi_user",
			CustomFields: map[string]string{
				"provider":  "kimi",
				"operation": OperationLLMInitialization,
				"error":     err.Error(),
				"status":    StatusLLMFailed,
			},
		}
		emitLLMInitializationError(config.EventEmitter, string(config.Provider), modelID, OperationLLMInitialization, err, config.TraceID, errorMetadata)
		return nil, err
	}

	baseURL := os.Getenv("KIMI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.moonshot.ai/v1"
	}

	client := openaisdk.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)

	llm := openaiadapter.NewCompatibleOpenAIAdapter(&client, modelID, logger, openaiadapter.OpenAICompatibilityConfig{
		ProviderName:   "kimi",
		MetadataLookup: kimiadapter.GetKimiModelMetadata,
	})

	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "kimi_user",
		CustomFields: map[string]string{
			"provider":     "kimi",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Kimi API provider - model_id: %s, base_url: %s", modelID, baseURL)
	return llm, nil
}

// initializeCodexCLI creates and configures an OpenAI Codex CLI adapter instance
func initializeCodexCLI(config Config) (llmtypes.Model, error) {
	transport, err := resolveCodexCLITransport()
	if err != nil {
		return nil, err
	}
	// LLM Initialization event data
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0,
		TopP:         config.Temperature,
		User:         "codex_cli_user",
		CustomFields: map[string]string{
			"provider":  "codex-cli",
			"operation": OperationLLMInitialization,
		},
	}

	// Emit LLM initialization start event
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	// Set default model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = DefaultCodexCLIModel
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing Codex CLI adapter - model_id: %s", modelID)

	// Resolve API key: explicit config > CODEX_API_KEY only.
	// Do NOT fall back to OPENAI_API_KEY — Codex CLI has its own auth
	// (via `codex login` stored in ~/.codex/auth.json) which should be preferred.
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.CodexCLI != nil {
		apiKey = *config.APIKeys.CodexCLI
		logger.Infof("Codex CLI: using API key from config (length=%d)", len(apiKey))
	} else if envKey := os.Getenv("CODEX_API_KEY"); envKey != "" {
		apiKey = envKey
		logger.Infof("Codex CLI: using API key from CODEX_API_KEY env var (length=%d)", len(apiKey))
	} else {
		logger.Infof("Codex CLI: no explicit API key — will use Codex CLI's own stored auth")
	}

	// Create Codex CLI adapter
	llm := codexcli.NewCodexCLIAdapter(apiKey, modelID, logger)
	llm.SetTmuxTransport(transport == CodexCLITransportTmux)

	// Emit LLM initialization success event
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "codex_cli_user",
		CustomFields: map[string]string{
			"provider":     "codex-cli",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Codex CLI adapter - model_id: %s", modelID)
	return llm, nil
}

// initializeCursorCLI creates and configures a Cursor Agent CLI adapter instance.
func initializeCursorCLI(config Config) (llmtypes.Model, error) {
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0,
		TopP:         config.Temperature,
		User:         "cursor_cli_user",
		CustomFields: map[string]string{
			"provider":  "cursor-cli",
			"operation": OperationLLMInitialization,
		},
	}

	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	modelID := config.ModelID
	if modelID == "" {
		modelID = DefaultCursorCLIModel
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing Cursor CLI adapter - model_id: %s", modelID)

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.CursorCLI != nil {
		apiKey = *config.APIKeys.CursorCLI
		logger.Infof("Cursor CLI: using API key from config (length=%d)", len(apiKey))
	} else if envKey := os.Getenv("CURSOR_API_KEY"); envKey != "" {
		apiKey = envKey
		logger.Infof("Cursor CLI: using API key from CURSOR_API_KEY env var (length=%d)", len(apiKey))
	} else {
		logger.Infof("Cursor CLI: no explicit API key — will use the selected transport's saved browser login")
	}

	transport, err := resolveCursorCLITransport()
	if err != nil {
		return nil, err
	}
	logger.Infof("Cursor CLI transport: %s", transport)
	llm := cursorcli.NewCursorCLIAdapter(apiKey, modelID, logger)
	llm.SetTmuxTransport(transport == CursorCLITransportTmux)

	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "cursor_cli_user",
		CustomFields: map[string]string{
			"provider":     "cursor-cli",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Cursor CLI adapter - model_id: %s", modelID)
	return llm, nil
}

// initializePiCLI creates and configures a Pi Coding Agent CLI adapter
// instance.
func initializePiCLI(config Config) (llmtypes.Model, error) {
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0,
		TopP:         config.Temperature,
		User:         "pi_cli_user",
		CustomFields: map[string]string{
			"provider":  "pi-cli",
			"operation": OperationLLMInitialization,
		},
	}

	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	modelID := config.ModelID
	if modelID == "" {
		modelID = DefaultPiCLIModel
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing Pi CLI adapter - model_id: %s", modelID)

	piProvider := piProviderFromModelID(modelID)
	if config.APIKeys != nil && len(config.APIKeys.RuntimeEnvironment) > 0 && piProviderKeyFromMap(config.APIKeys.PiProviderKeys, piProvider) == "" {
		return nil, fmt.Errorf("Pi connection does not provide credentials for selected model provider %s", piProvider)
	}
	apiKey, apiKeySource := piCLIAPIKeyForProvider(config.APIKeys, piProvider)
	if apiKey != "" {
		logger.Infof("Pi CLI: using API key from %s for provider %s (length=%d)", apiKeySource, piProvider, len(apiKey))
	} else {
		logger.Infof("Pi CLI: no explicit API key — will use Pi CLI/provider local auth if available")
	}

	llm := picli.NewPiCLIAdapter(apiKey, modelID, logger)
	if config.APIKeys != nil && config.APIKeys.PiCustomProvider != nil && config.APIKeys.PiCustomProvider.Name == piProvider {
		if err := llm.SetCustomProvider(config.APIKeys.PiCustomProvider); err != nil {
			return nil, err
		}
	}

	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "pi_cli_user",
		CustomFields: map[string]string{
			"provider":     "pi-cli",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Pi CLI adapter - model_id: %s", modelID)
	return llm, nil
}

func piProviderFromModelID(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || strings.EqualFold(modelID, string(ProviderPiCLI)) || strings.EqualFold(modelID, "auto") {
		return "google"
	}
	if slash := strings.Index(modelID, "/"); slash > 0 {
		return normalizePiProviderID(modelID[:slash])
	}
	return "google"
}

func normalizePiProviderID(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func piCLIAPIKeyForProvider(keys *ProviderAPIKeys, provider string) (string, string) {
	provider = normalizePiProviderID(provider)
	if provider == "" {
		provider = "google"
	}

	if keys != nil {
		if key := piProviderKeyFromMap(keys.PiProviderKeys, provider); key != "" {
			return key, "Pi provider workspace auth"
		}
		switch provider {
		case "google", "google-vertex":
			if key := derefTrim(keys.PiCLI); key != "" {
				return key, "Pi CLI workspace auth"
			}
			if key := derefTrim(keys.Vertex); key != "" {
				return key, "Gemini/Vertex workspace auth"
			}
		case "openai":
			if key := derefTrim(keys.OpenAI); key != "" {
				return key, "OpenAI workspace auth"
			}
		case "anthropic":
			if key := derefTrim(keys.Anthropic); key != "" {
				return key, "Anthropic workspace auth"
			}
		case "openrouter":
			if key := derefTrim(keys.OpenRouter); key != "" {
				return key, "OpenRouter workspace auth"
			}
		case "zai":
			if key := derefTrim(keys.ZAI); key != "" {
				return key, "Z.AI workspace auth"
			}
		case "kimi-coding", "moonshotai", "moonshotai-cn":
			if key := derefTrim(keys.Kimi); key != "" {
				return key, "Kimi workspace auth"
			}
		case "minimax":
			if key := derefTrim(keys.MiniMax); key != "" {
				return key, "MiniMax workspace auth"
			}
		}
	}

	for _, envName := range piProviderEnvNames(provider) {
		if envKey := strings.TrimSpace(os.Getenv(envName)); envKey != "" {
			return envKey, envName + " env var"
		}
	}
	return "", ""
}

func piProviderKeyFromMap(keys map[string]string, provider string) string {
	if keys == nil {
		return ""
	}
	for _, candidate := range piProviderKeyAliases(provider) {
		if key := strings.TrimSpace(keys[candidate]); key != "" {
			return key
		}
	}
	return ""
}

func piProviderKeyAliases(provider string) []string {
	switch normalizePiProviderID(provider) {
	case "google-vertex":
		return []string{"google-vertex", "google"}
	case "moonshotai", "moonshotai-cn":
		return []string{normalizePiProviderID(provider), "kimi-coding"}
	default:
		return []string{normalizePiProviderID(provider)}
	}
}

func piProviderEnvNames(provider string) []string {
	switch normalizePiProviderID(provider) {
	case "google", "google-vertex":
		return []string{"PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"}
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "anthropic":
		return []string{"ANTHROPIC_API_KEY"}
	case "openrouter":
		return []string{"OPENROUTER_API_KEY"}
	case "deepseek":
		return []string{"DEEPSEEK_API_KEY"}
	case "nvidia":
		return []string{"NVIDIA_API_KEY"}
	case "mistral":
		return []string{"MISTRAL_API_KEY"}
	case "groq":
		return []string{"GROQ_API_KEY"}
	case "cerebras":
		return []string{"CEREBRAS_API_KEY"}
	case "xai":
		return []string{"XAI_API_KEY"}
	case "zai":
		return []string{"ZAI_API_KEY"}
	case "zai-coding-cn":
		return []string{"ZAI_CODING_CN_API_KEY"}
	case "opencode", "opencode-go":
		return []string{"OPENCODE_API_KEY"}
	case "fireworks":
		return []string{"FIREWORKS_API_KEY"}
	case "together":
		return []string{"TOGETHER_API_KEY"}
	case "kimi-coding":
		return []string{"KIMI_API_KEY"}
	case "moonshotai", "moonshotai-cn":
		return []string{"MOONSHOT_API_KEY", "KIMI_API_KEY"}
	case "minimax":
		return []string{"MINIMAX_API_KEY"}
	case "minimax-cn":
		return []string{"MINIMAX_CN_API_KEY"}
	case "vercel-ai-gateway":
		return []string{"AI_GATEWAY_API_KEY"}
	default:
		normalized := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(normalizePiProviderID(provider)))
		if normalized == "" {
			return nil
		}
		return []string{normalized + "_API_KEY"}
	}
}

func derefTrim(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func initializeAgyCLI(config Config) (llmtypes.Model, error) {
	llmMetadata := LLMMetadata{
		ModelVersion: config.ModelID,
		MaxTokens:    0,
		TopP:         config.Temperature,
		User:         "agy_cli_user",
		CustomFields: map[string]string{
			"provider":  "agy-cli",
			"operation": OperationLLMInitialization,
		},
	}

	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), config.ModelID, config.Temperature, config.TraceID, llmMetadata)

	modelID := config.ModelID
	if modelID == "" {
		modelID = DefaultAgyCLIModel
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	logger.Infof("Initializing Agy adapter - model_id: %s", modelID)

	// Auth resolves inside the CLI: stored Google login by default, or the
	// GEMINI_API_KEY env when settings.json flips modelProvider to "gemini"
	// (key WITHOUT the flip is ignored — that is what the earlier "ignores
	// the key" probe actually proved). Config-supplied keys are neither read
	// nor exported; env is the only key path.
	if config.APIKeys != nil && config.APIKeys.AgyCLI != nil && strings.TrimSpace(*config.APIKeys.AgyCLI) != "" {
		logger.Infof("Agy: explicit API key not exported (key mode needs GEMINI_API_KEY env + modelProvider flip)")
	}
	logger.Infof("Agy: auth resolves in-CLI (stored `agy` Google login, or GEMINI_API_KEY when modelProvider is gemini)")

	llm := agycli.NewAgyCLIAdapter("", modelID, logger)

	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "agy_cli_user",
		CustomFields: map[string]string{
			"provider":     "agy-cli",
			"status":       StatusLLMInitialized,
			"capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling,
		},
	}

	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)

	logger.Infof("Initialized Agy adapter (stub) - model_id: %s", modelID)
	return llm, nil
}

func initializeACPCLI(config Config) (llmtypes.Model, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = DefaultACPCLIModel
	}
	llmMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "acp_cli_user",
		CustomFields: map[string]string{"provider": "acp-cli", "operation": OperationLLMInitialization},
	}
	emitLLMInitializationStart(config.EventEmitter, string(config.Provider), modelID, config.Temperature, config.TraceID, llmMetadata)
	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	// Each agent signs in on its own (stored login or its API key variable);
	// config-supplied keys are not used.
	llm, err := acpcli.NewAdapter(modelID, logger)
	if err != nil {
		emitLLMInitializationError(config.EventEmitter, string(config.Provider), modelID, OperationLLMInitialization, err, config.TraceID, llmMetadata)
		return nil, err
	}
	successMetadata := LLMMetadata{
		ModelVersion: modelID,
		User:         "acp_cli_user",
		CustomFields: map[string]string{"provider": "acp-cli", "status": StatusLLMInitialized, "capabilities": CapabilityTextGeneration + "," + CapabilityToolCalling},
	}
	emitLLMInitializationSuccess(config.EventEmitter, string(config.Provider), modelID, CapabilityTextGeneration+","+CapabilityToolCalling, config.TraceID, successMetadata)
	logger.Infof("Initialized ACP adapter - model_id: %s", modelID)
	return llm, nil
}

// GetDefaultModel returns the default model for each provider from environment variables

func resolveCodexCLITransport() (string, error) {
	switch raw := strings.ToLower(strings.TrimSpace(os.Getenv(EnvCodexCLITransport))); raw {
	case "", CodexCLITransportAppServer:
		return CodexCLITransportAppServer, nil
	case CodexCLITransportTmux:
		return CodexCLITransportTmux, nil
	default:
		return "", fmt.Errorf("unsupported Codex transport %q; use %s=app-server or tmux", raw, EnvCodexCLITransport)
	}
}
