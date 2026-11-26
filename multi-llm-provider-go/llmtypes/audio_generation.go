package llmtypes

import "context"

// AudioGenerationModel is an interface for models that support text-to-speech generation.
type AudioGenerationModel interface {
	// GenerateAudio generates audio from a text prompt.
	GenerateAudio(ctx context.Context, prompt string, options ...AudioGenerationOption) (*AudioGenerationResponse, error)
}

// AudioGenerationOptions holds configuration for audio generation requests.
type AudioGenerationOptions struct {
	// VoiceName is the prebuilt voice to use for single-speaker TTS.
	VoiceName string
	// LanguageCode is an optional BCP-47 language code for speech synthesis.
	LanguageCode string
	// SpeakerVoiceConfigs maps prompt speaker names to prebuilt voices for multi-speaker TTS.
	SpeakerVoiceConfigs []AudioSpeakerVoiceConfig
}

// AudioSpeakerVoiceConfig configures one speaker in a multi-speaker TTS prompt.
type AudioSpeakerVoiceConfig struct {
	Speaker   string
	VoiceName string
}

// AudioGenerationOption is a functional option for configuring audio generation.
type AudioGenerationOption func(*AudioGenerationOptions)

// WithAudioVoiceName sets the prebuilt voice for single-speaker generation.
func WithAudioVoiceName(voiceName string) AudioGenerationOption {
	return func(opts *AudioGenerationOptions) {
		opts.VoiceName = voiceName
	}
}

// WithAudioLanguageCode sets the optional speech language code.
func WithAudioLanguageCode(languageCode string) AudioGenerationOption {
	return func(opts *AudioGenerationOptions) {
		opts.LanguageCode = languageCode
	}
}

// WithAudioSpeakerVoiceConfigs sets the speaker-to-voice mapping for multi-speaker generation.
func WithAudioSpeakerVoiceConfigs(configs []AudioSpeakerVoiceConfig) AudioGenerationOption {
	return func(opts *AudioGenerationOptions) {
		opts.SpeakerVoiceConfigs = append([]AudioSpeakerVoiceConfig(nil), configs...)
	}
}

// AudioGenerationResponse holds the result of an audio generation request.
type AudioGenerationResponse struct {
	Audio []GeneratedAudio
}

// GeneratedAudio represents one generated audio item.
type GeneratedAudio struct {
	Data     []byte
	MimeType string
}
