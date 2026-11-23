package llmproviders

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	bedrockadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/bedrock"
	codexcli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	deepgramadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/deepgram"
	elevenlabsadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/elevenlabs"
	minimaxadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/minimax"
	openaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/openai"
	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"

	openaisdk "github.com/openai/openai-go/v3"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/openai/openai-go/v3/option"

	"google.golang.org/genai"
)

func InitializeEmbeddingModel(config Config) (llmtypes.EmbeddingModel, error) {
	var embeddingModel llmtypes.EmbeddingModel
	var err error

	switch config.Provider {
	case ProviderOpenAI:
		embeddingModel, err = initializeOpenAIEmbedding(config)
	case ProviderOpenRouter:
		// OpenRouter uses OpenAI-compatible API, so we can use OpenAI adapter
		embeddingModel, err = initializeOpenAIEmbedding(config)
	case ProviderVertex:
		embeddingModel, err = initializeVertexEmbedding(config)
	case ProviderBedrock:
		embeddingModel, err = initializeBedrockEmbedding(config)
	default:
		return nil, fmt.Errorf("embedding generation not supported for provider: %s. Supported providers: openai, openrouter, vertex, bedrock", config.Provider)
	}

	if err != nil {
		return nil, err
	}

	return embeddingModel, nil
}

const defaultGeminiImageModelID = "gemini-3.1-flash-image"

var legacyGeminiImageModelAliases = map[string]string{
	"gemini-3.1-flash-image-preview": defaultGeminiImageModelID,
	"gemini-3-pro-image-preview":     "gemini-3-pro-image",
}

// InitializeImageGenerationModel creates and initializes an image generation model.
// Supported providers:
//   - "gemini-*" models use GenerateContent with IMAGE response modality
//   - "minimax-coding-plan" uses MiniMax image generation with image-01
//   - "codex-cli" uses the native Codex CLI image generation flow
func InitializeImageGenerationModel(config Config) (llmtypes.ImageGenerationModel, error) {
	switch config.Provider {
	case ProviderVertex:
		return initializeVertexImagen(config)
	case ProviderMiniMaxCodingPlan:
		return initializeMiniMaxCodingPlanImagen(config)
	case ProviderCodexCLI:
		return initializeCodexCLIImage(config)
	default:
		return nil, fmt.Errorf("image generation not supported for provider: %s. Supported providers: vertex, minimax-coding-plan, codex-cli", config.Provider)
	}
}

// InitializeVideoGenerationModel creates and initializes a video generation model.
// Supported providers:
//   - "veo-*" models use Google's GenerateVideos API
//   - "gemini-omni-*" models use Google's Interactions API (video generation + conversational editing)
func InitializeVideoGenerationModel(config Config) (llmtypes.VideoGenerationModel, error) {
	switch config.Provider {
	case ProviderVertex:
		return initializeVertexVeo(config)
	default:
		return nil, fmt.Errorf("video generation not supported for provider: %s. Supported providers: vertex", config.Provider)
	}
}

// InitializeAudioGenerationModel creates and initializes an audio generation model.
// Supported providers:
//   - "gemini-*" models use GenerateContent with AUDIO response modality
func InitializeAudioGenerationModel(config Config) (llmtypes.AudioGenerationModel, error) {
	switch config.Provider {
	case ProviderVertex:
		return initializeVertexTTS(config)
	case ProviderMiniMax:
		return initializeMiniMaxTTS(config)
	case ProviderElevenLabs:
		return initializeElevenLabsTTS(config)
	case ProviderDeepgram:
		return initializeDeepgramTTS(config)
	default:
		return nil, fmt.Errorf("audio generation not supported for provider: %s. Supported providers: vertex, minimax, elevenlabs, deepgram", config.Provider)
	}
}

// InitializeAudioTranscriptionModel creates and initializes a speech-to-text model.
// Supported providers:
//   - "deepgram" models use Deepgram prerecorded transcription
func InitializeAudioTranscriptionModel(config Config) (llmtypes.AudioTranscriptionModel, error) {
	switch config.Provider {
	case ProviderDeepgram:
		return initializeDeepgramSTT(config)
	default:
		return nil, fmt.Errorf("audio transcription not supported for provider: %s. Supported providers: deepgram", config.Provider)
	}
}

// InitializeMusicGenerationModel creates and initializes a music generation model.
// Supported providers:
//   - "elevenlabs" models use the ElevenLabs Music API
//   - "minimax" models use the MiniMax Music Generation API
func InitializeMusicGenerationModel(config Config) (llmtypes.MusicGenerationModel, error) {
	switch config.Provider {
	case ProviderElevenLabs:
		return initializeElevenLabsMusic(config)
	case ProviderMiniMax:
		return initializeMiniMaxMusic(config)
	default:
		return nil, fmt.Errorf("music generation not supported for provider: %s. Supported providers: elevenlabs, minimax", config.Provider)
	}
}

// initializeMiniMaxCodingPlanImagen creates a MiniMax image generation adapter using the
// coding-plan credential, which is the canonical MiniMax non-text auth path.
func initializeMiniMaxCodingPlanImagen(config Config) (llmtypes.ImageGenerationModel, error) {
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.MiniMaxCodingPlan != nil && *config.APIKeys.MiniMaxCodingPlan != "" {
		apiKey = *config.APIKeys.MiniMaxCodingPlan
	} else {
		apiKey = os.Getenv("MINIMAX_CODING_PLAN_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("MINIMAX_CODING_PLAN_API_KEY is required for MiniMax coding plan image generation")
	}

	modelID := config.ModelID
	if modelID == "" {
		modelID = minimaxadapter.ModelMiniMaxImage01
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	logger.Infof("Initializing MiniMax Coding Plan Image Generation with model: %s", modelID)
	return minimaxadapter.NewMiniMaxImageAdapter(apiKey, modelID, logger), nil
}

func initializeCodexCLIImage(config Config) (llmtypes.ImageGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = "codex-cli"
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.CodexCLI != nil && *config.APIKeys.CodexCLI != "" {
		apiKey = *config.APIKeys.CodexCLI
	}
	if apiKey == "" {
		apiKey = os.Getenv("CODEX_API_KEY")
	}

	logger.Infof("Initializing Codex CLI Image Generation with model: %s", modelID)
	if apiKey == "" {
		logger.Infof("Codex CLI image generation: using Codex CLI local auth/session (CODEX_API_KEY not provided)")
	}
	return codexcli.NewCodexCLIImageAdapter(apiKey, modelID, logger), nil
}

// initializeVertexImagen creates an image generation adapter using the Gemini API.
// Gemini image models use GenerateContent with native image output.
// Uses GEMINI_API_KEY with the Gemini Developer API backend.
func initializeVertexImagen(config Config) (llmtypes.ImageGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = defaultGeminiImageModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}
	normalizedModelID := strings.ToLower(strings.TrimSpace(modelID))
	if alias, ok := legacyGeminiImageModelAliases[normalizedModelID]; ok {
		logger.Infof("Migrating legacy Gemini image model %s to %s", modelID, alias)
		modelID = alias
	} else if strings.HasPrefix(normalizedModelID, "imagen-") {
		logger.Infof("Migrating deprecated Imagen image model %s to %s", modelID, defaultGeminiImageModelID)
		modelID = defaultGeminiImageModelID
	}

	// Check config APIKeys first, then fall back to environment variables
	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Vertex != nil && *config.APIKeys.Vertex != "" {
		apiKey = *config.APIKeys.Vertex
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("VERTEX_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable is required for Gemini image generation (or provide api_key in config)")
	}

	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create GenAI client for Gemini image generation: %w", err)
	}

	logger.Infof("Initialized Gemini image model - model_id: %s", modelID)
	return vertexadapter.NewGeminiImageAdapter(client, modelID, logger), nil
}

const defaultGeminiTTSModelID = "gemini-3.1-flash-tts-preview"

// initializeVertexTTS creates an audio generation adapter using the Gemini API.
func initializeVertexTTS(config Config) (llmtypes.AudioGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = defaultGeminiTTSModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Vertex != nil && *config.APIKeys.Vertex != "" {
		apiKey = *config.APIKeys.Vertex
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("VERTEX_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable is required for Gemini TTS audio generation (or provide api_key in config)")
	}

	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create GenAI client for Gemini TTS: %w", err)
	}

	logger.Infof("Initialized Gemini TTS audio model - model_id: %s", modelID)
	return vertexadapter.NewGeminiTTSAdapter(client, modelID, logger), nil
}

// initializeElevenLabsTTS creates an audio generation adapter using ElevenLabs TTS.
func initializeElevenLabsTTS(config Config) (llmtypes.AudioGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = elevenlabsadapter.DefaultModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.ElevenLabs != nil && *config.APIKeys.ElevenLabs != "" {
		apiKey = *config.APIKeys.ElevenLabs
	}
	if apiKey == "" {
		apiKey = os.Getenv("ELEVENLABS_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ELEVENLABS_API_KEY environment variable is required for ElevenLabs audio generation (or provide api_key in config)")
	}

	logger.Infof("Initialized ElevenLabs TTS audio model - model_id: %s", modelID)
	return elevenlabsadapter.NewElevenLabsTTSAdapter(apiKey, modelID, elevenlabsadapter.DefaultVoiceID, elevenlabsadapter.DefaultOutputFormat, logger), nil
}

// initializeMiniMaxTTS creates an audio generation adapter using MiniMax T2A.
func initializeMiniMaxTTS(config Config) (llmtypes.AudioGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = minimaxadapter.DefaultTTSModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.MiniMax != nil && *config.APIKeys.MiniMax != "" {
		apiKey = *config.APIKeys.MiniMax
	}
	if apiKey == "" {
		apiKey = os.Getenv("MINIMAX_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("MINIMAX_API_KEY environment variable is required for MiniMax audio generation (or provide api_key in config)")
	}

	logger.Infof("Initialized MiniMax TTS audio model - model_id: %s", modelID)
	return minimaxadapter.NewMiniMaxTTSAdapter(apiKey, modelID, minimaxadapter.DefaultTTSVoiceID, logger), nil
}

// initializeDeepgramTTS creates an audio generation adapter using Deepgram Speak.
func initializeDeepgramTTS(config Config) (llmtypes.AudioGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = deepgramadapter.DefaultModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Deepgram != nil && *config.APIKeys.Deepgram != "" {
		apiKey = *config.APIKeys.Deepgram
	}
	if apiKey == "" {
		apiKey = os.Getenv("DEEPGRAM_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("DEEPGRAM_API_KEY environment variable is required for Deepgram audio generation (or provide api_key in config)")
	}

	logger.Infof("Initialized Deepgram TTS audio model - model_id: %s", modelID)
	return deepgramadapter.NewDeepgramTTSAdapter(apiKey, modelID, logger), nil
}

// initializeDeepgramSTT creates a speech-to-text adapter using Deepgram Listen.
func initializeDeepgramSTT(config Config) (llmtypes.AudioTranscriptionModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = deepgramadapter.DefaultTranscriptionModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Deepgram != nil && *config.APIKeys.Deepgram != "" {
		apiKey = *config.APIKeys.Deepgram
	}
	if apiKey == "" {
		apiKey = os.Getenv("DEEPGRAM_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("DEEPGRAM_API_KEY environment variable is required for Deepgram audio transcription (or provide api_key in config)")
	}

	logger.Infof("Initialized Deepgram STT audio model - model_id: %s", modelID)
	return deepgramadapter.NewDeepgramTTSAdapter(apiKey, modelID, logger), nil
}

// initializeElevenLabsMusic creates a music generation adapter using ElevenLabs Music.
func initializeElevenLabsMusic(config Config) (llmtypes.MusicGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = elevenlabsadapter.DefaultMusicModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.ElevenLabs != nil && *config.APIKeys.ElevenLabs != "" {
		apiKey = *config.APIKeys.ElevenLabs
	}
	if apiKey == "" {
		apiKey = os.Getenv("ELEVENLABS_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("ELEVENLABS_API_KEY environment variable is required for ElevenLabs music generation (or provide api_key in config)")
	}

	logger.Infof("Initialized ElevenLabs music model - model_id: %s", modelID)
	return elevenlabsadapter.NewElevenLabsMusicAdapter(apiKey, modelID, elevenlabsadapter.DefaultMusicOutputFormat, logger), nil
}

// initializeMiniMaxMusic creates a music generation adapter using MiniMax Music Generation.
func initializeMiniMaxMusic(config Config) (llmtypes.MusicGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = minimaxadapter.DefaultMusicModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.MiniMax != nil && *config.APIKeys.MiniMax != "" {
		apiKey = *config.APIKeys.MiniMax
	}
	if apiKey == "" {
		apiKey = os.Getenv("MINIMAX_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("MINIMAX_API_KEY environment variable is required for MiniMax music generation (or provide api_key in config)")
	}

	logger.Infof("Initialized MiniMax music model - model_id: %s", modelID)
	return minimaxadapter.NewMiniMaxMusicAdapter(apiKey, modelID, logger), nil
}

const (
	defaultGeminiVeoModelID = "veo-3.1-generate-preview"
	defaultVertexVeoModelID = "veo-3.1-generate-001"
	defaultVertexLocation   = "us-central1"
)

var vertexOnlyVeoModels = map[string]struct{}{
	"veo-3.1-generate-001":      {},
	"veo-3.1-fast-generate-001": {},
	"veo-3.1-lite-generate-001": {},
}

// initializeVertexVeo creates a video generation adapter using Google's GenerateVideos API.
// It supports both:
//   - Gemini Developer API with API-key auth for preview Veo models
//   - Vertex AI with ADC/OAuth for GA Vertex Veo models such as veo-3.1-generate-001
func initializeVertexVeo(config Config) (llmtypes.VideoGenerationModel, error) {
	modelID := config.ModelID

	if strings.HasPrefix(strings.TrimSpace(modelID), "gemini-omni") {
		return initializeGeminiOmni(config)
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Vertex != nil && *config.APIKeys.Vertex != "" {
		apiKey = *config.APIKeys.Vertex
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("VERTEX_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}

	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}

	projectID := firstNonEmpty(
		os.Getenv("GOOGLE_CLOUD_PROJECT"),
		os.Getenv("VERTEX_PROJECT_ID"),
	)
	locationID := firstNonEmpty(
		os.Getenv("GOOGLE_CLOUD_LOCATION"),
		os.Getenv("GOOGLE_CLOUD_REGION"),
		os.Getenv("VERTEX_LOCATION_ID"),
	)
	if locationID == "" {
		locationID = defaultVertexLocation
	}

	if modelID == "" {
		if apiKey != "" {
			modelID = defaultGeminiVeoModelID
		} else if projectID != "" {
			modelID = defaultVertexVeoModelID
		} else {
			modelID = defaultGeminiVeoModelID
		}
	}

	clientConfig := &genai.ClientConfig{}
	backendLabel := "Gemini API"

	if requiresVertexVeoBackend(modelID) {
		if projectID == "" {
			return nil, fmt.Errorf(
				"model %q requires the Vertex AI backend. Set GOOGLE_CLOUD_PROJECT or VERTEX_PROJECT_ID, optionally GOOGLE_CLOUD_LOCATION or VERTEX_LOCATION_ID, and authenticate with Application Default Credentials. For API-key auth, use %q or %q instead",
				modelID,
				defaultGeminiVeoModelID,
				"veo-3.1-fast-generate-preview",
			)
		}
		clientConfig.Backend = genai.BackendVertexAI
		clientConfig.Project = projectID
		clientConfig.Location = locationID
		backendLabel = "Vertex AI"
	} else if apiKey != "" {
		clientConfig.APIKey = apiKey
		clientConfig.Backend = genai.BackendGeminiAPI
	} else if projectID != "" {
		clientConfig.Backend = genai.BackendVertexAI
		clientConfig.Project = projectID
		clientConfig.Location = locationID
		backendLabel = "Vertex AI"
	} else {
		return nil, fmt.Errorf("Veo video generation requires either GEMINI_API_KEY / VERTEX_API_KEY / GOOGLE_API_KEY for Gemini API preview models, or GOOGLE_CLOUD_PROJECT / VERTEX_PROJECT_ID plus ADC for Vertex AI models")
	}

	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create GenAI client for Veo: %w", err)
	}

	logger.Infof("Initialized Veo video model - backend: %s, model_id: %s", backendLabel, modelID)
	return vertexadapter.NewVertexVeoAdapter(client, modelID, logger), nil
}

const defaultGeminiOmniModelID = "gemini-omni-flash-preview"

// initializeGeminiOmni creates a video generation adapter for Gemini Omni Flash,
// via the Gemini Developer API's Interactions API. There is no Vertex AI GA
// equivalent yet, so this is Gemini-API-key only (like Imagen and Gemini TTS).
func initializeGeminiOmni(config Config) (llmtypes.VideoGenerationModel, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = defaultGeminiOmniModelID
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	apiKey := ""
	if config.APIKeys != nil && config.APIKeys.Vertex != nil && *config.APIKeys.Vertex != "" {
		apiKey = *config.APIKeys.Vertex
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("VERTEX_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Gemini Omni video generation requires GEMINI_API_KEY, VERTEX_API_KEY, or GOOGLE_API_KEY (Gemini Developer API key)")
	}

	logger.Infof("Initialized Gemini Omni video model - backend: Gemini API (Interactions API), model_id: %s", modelID)
	return vertexadapter.NewGeminiOmniAdapter(apiKey, modelID, logger), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func requiresVertexVeoBackend(modelID string) bool {
	_, ok := vertexOnlyVeoModels[strings.TrimSpace(modelID)]
	return ok
}

// initializeOpenAIEmbedding creates and configures an OpenAI embedding model instance
func initializeOpenAIEmbedding(config Config) (llmtypes.EmbeddingModel, error) {
	// Check for API key
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required for OpenAI embedding provider")
	}

	// Set default embedding model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "text-embedding-3-small"
	}

	// Create OpenAI client using official SDK
	client := openaisdk.NewClient(
		option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
	)

	// Create OpenAI adapter (it implements both Model and EmbeddingModel interfaces)
	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	embeddingModel := openaiadapter.NewOpenAIAdapter(&client, modelID, logger)

	logger.Infof("Initialized OpenAI Embedding Model - model_id: %s", modelID)
	return embeddingModel, nil
}

// initializeVertexEmbedding creates and configures a Vertex AI embedding model instance
func initializeVertexEmbedding(config Config) (llmtypes.EmbeddingModel, error) {
	// Set default embedding model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "text-embedding-004" // Latest Vertex AI embedding model
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	logger.Infof("Initializing Vertex AI Embedding Model - model_id: %s", modelID)

	// Check for API key from environment
	apiKey := os.Getenv("VERTEX_API_KEY")
	if apiKey == "" {
		// Try alternative environment variable names
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("VERTEX_API_KEY or GOOGLE_API_KEY environment variable is required for Vertex AI embedding models")
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
		return nil, fmt.Errorf("failed to create GenAI client: %w", err)
	}

	// Create Vertex adapter (it implements both Model and EmbeddingModel interfaces)
	embeddingModel := vertexadapter.NewGoogleGenAIAdapter(client, modelID, logger)

	logger.Infof("Initialized Vertex AI Embedding Model - model_id: %s", modelID)
	return embeddingModel, nil
}

// initializeBedrockEmbedding creates and configures a Bedrock embedding model instance
func initializeBedrockEmbedding(config Config) (llmtypes.EmbeddingModel, error) {
	// Set default embedding model if not specified
	modelID := config.ModelID
	if modelID == "" {
		modelID = "amazon.titan-embed-text-v1" // Default Bedrock embedding model
	}

	logger := config.Logger
	if logger == nil {
		logger = &noopLoggerImpl{}
	}

	logger.Infof("Initializing Bedrock Embedding Model - model_id: %s", modelID)

	// Create AWS config
	cfg, err := awsconfig.LoadDefaultConfig(config.Context)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create Bedrock runtime client
	client := bedrockruntime.NewFromConfig(cfg)

	// Create Bedrock adapter (it implements both Model and EmbeddingModel interfaces)
	embeddingModel := bedrockadapter.NewBedrockAdapter(client, modelID, logger)

	logger.Infof("Initialized Bedrock Embedding Model - model_id: %s", modelID)
	return embeddingModel, nil
}
