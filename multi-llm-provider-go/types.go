package llmproviders

import (
	"context"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// LLMCallFunc is a type-safe function signature for LLM calls.
type LLMCallFunc func(ctx context.Context, prompt string) (string, error)

// Re-export types from llmtypes for convenience
type Model = llmtypes.Model
type ChatMessageType = llmtypes.ChatMessageType
type ContentPart = llmtypes.ContentPart
type TextContent = llmtypes.TextContent
type ImageContent = llmtypes.ImageContent
type ToolCall = llmtypes.ToolCall
type FunctionCall = llmtypes.FunctionCall
type ToolCallResponse = llmtypes.ToolCallResponse
type MessageContent = llmtypes.MessageContent
type ContentResponse = llmtypes.ContentResponse
type ContentChoice = llmtypes.ContentChoice
type Usage = llmtypes.Usage
type Tool = llmtypes.Tool
type FunctionDefinition = llmtypes.FunctionDefinition
type ToolChoice = llmtypes.ToolChoice
type FunctionName = llmtypes.FunctionName
type CallOptions = llmtypes.CallOptions
type CallOption = llmtypes.CallOption

// Re-export image generation types
type ImageGenerationModel = llmtypes.ImageGenerationModel
type ImageGenerationResponse = llmtypes.ImageGenerationResponse
type GeneratedImage = llmtypes.GeneratedImage
type ImageGenerationOptions = llmtypes.ImageGenerationOptions
type ImageGenerationOption = llmtypes.ImageGenerationOption

// Re-export audio generation types
type AudioGenerationModel = llmtypes.AudioGenerationModel
type AudioGenerationResponse = llmtypes.AudioGenerationResponse
type GeneratedAudio = llmtypes.GeneratedAudio
type AudioGenerationOptions = llmtypes.AudioGenerationOptions
type AudioGenerationOption = llmtypes.AudioGenerationOption
type AudioSpeakerVoiceConfig = llmtypes.AudioSpeakerVoiceConfig
type AudioTranscriptionModel = llmtypes.AudioTranscriptionModel
type AudioTranscriptionResponse = llmtypes.AudioTranscriptionResponse
type AudioTranscriptionOptions = llmtypes.AudioTranscriptionOptions
type AudioTranscriptionOption = llmtypes.AudioTranscriptionOption

// Re-export music generation types
type MusicGenerationModel = llmtypes.MusicGenerationModel
type MusicGenerationResponse = llmtypes.MusicGenerationResponse
type GeneratedMusic = llmtypes.GeneratedMusic
type MusicGenerationOptions = llmtypes.MusicGenerationOptions
type MusicGenerationOption = llmtypes.MusicGenerationOption

// Re-export video generation types
type VideoGenerationModel = llmtypes.VideoGenerationModel
type VideoGenerationResponse = llmtypes.VideoGenerationResponse
type GeneratedVideo = llmtypes.GeneratedVideo
type VideoGenerationOptions = llmtypes.VideoGenerationOptions
type VideoGenerationOption = llmtypes.VideoGenerationOption

// Re-export embedding types
type EmbeddingModel = llmtypes.EmbeddingModel
type Embedding = llmtypes.Embedding
type EmbeddingResponse = llmtypes.EmbeddingResponse
type EmbeddingUsage = llmtypes.EmbeddingUsage
type EmbeddingOptions = llmtypes.EmbeddingOptions
type EmbeddingOption = llmtypes.EmbeddingOption

// Re-export constants
const (
	ChatMessageTypeSystem   = llmtypes.ChatMessageTypeSystem
	ChatMessageTypeHuman    = llmtypes.ChatMessageTypeHuman
	ChatMessageTypeAI       = llmtypes.ChatMessageTypeAI
	ChatMessageTypeTool     = llmtypes.ChatMessageTypeTool
	ChatMessageTypeGeneric  = llmtypes.ChatMessageTypeGeneric
	ChatMessageTypeFunction = llmtypes.ChatMessageTypeFunction
)

// Re-export functions
var (
	WithModel                     = llmtypes.WithModel
	WithTemperature               = llmtypes.WithTemperature
	WithMaxTokens                 = llmtypes.WithMaxTokens
	WithJSONMode                  = llmtypes.WithJSONMode
	WithTools                     = llmtypes.WithTools
	WithToolChoice                = llmtypes.WithToolChoice
	WithStreamingFunc             = llmtypes.WithStreamingFunc
	TextPart                      = llmtypes.TextPart
	TextParts                     = llmtypes.TextParts
	ImagePart                     = llmtypes.ImagePart
	ImagePartBase64               = llmtypes.ImagePartBase64
	ImagePartURL                  = llmtypes.ImagePartURL
	WithEmbeddingModel            = llmtypes.WithEmbeddingModel
	WithDimensions                = llmtypes.WithDimensions
	WithNumberOfImages            = llmtypes.WithNumberOfImages
	WithAspectRatio               = llmtypes.WithAspectRatio
	WithResolution                = llmtypes.WithResolution
	WithNegativePrompt            = llmtypes.WithNegativePrompt
	WithInputImage                = llmtypes.WithInputImage
	WithInputImageURL             = llmtypes.WithInputImageURL
	WithAudioVoiceName            = llmtypes.WithAudioVoiceName
	WithAudioLanguageCode         = llmtypes.WithAudioLanguageCode
	WithAudioSpeakerVoiceConfigs  = llmtypes.WithAudioSpeakerVoiceConfigs
	WithTranscriptionLanguageCode = llmtypes.WithTranscriptionLanguageCode
	WithTranscriptionSmartFormat  = llmtypes.WithTranscriptionSmartFormat
	WithTranscriptionPunctuate    = llmtypes.WithTranscriptionPunctuate
	WithTranscriptionDiarize      = llmtypes.WithTranscriptionDiarize
	WithMusicDurationMS           = llmtypes.WithMusicDurationMS
	WithMusicInstrumental         = llmtypes.WithMusicInstrumental
	WithMusicLyrics               = llmtypes.WithMusicLyrics
	WithMusicLyricsOptimizer      = llmtypes.WithMusicLyricsOptimizer
	WithMusicSeed                 = llmtypes.WithMusicSeed
	WithMusicOutputFormat         = llmtypes.WithMusicOutputFormat
	WithMusicCompositionPlan      = llmtypes.WithMusicCompositionPlan
	WithVideoNumberOfVideos       = llmtypes.WithVideoNumberOfVideos
	WithVideoAspectRatio          = llmtypes.WithVideoAspectRatio
	WithVideoResolution           = llmtypes.WithVideoResolution
	WithVideoNegativePrompt       = llmtypes.WithVideoNegativePrompt
	WithVideoDurationSeconds      = llmtypes.WithVideoDurationSeconds
	WithVideoGenerateAudio        = llmtypes.WithVideoGenerateAudio
	WithVideoPersonGeneration     = llmtypes.WithVideoPersonGeneration
	WithVideoSeed                 = llmtypes.WithVideoSeed
	WithVideoInputImage           = llmtypes.WithVideoInputImage
	WithVideoLastFrame            = llmtypes.WithVideoLastFrame
)
