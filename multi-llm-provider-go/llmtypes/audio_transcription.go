package llmtypes

import "context"

// AudioTranscriptionModel is an interface for models that support speech-to-text.
type AudioTranscriptionModel interface {
	// TranscribeAudio transcribes audio bytes into text.
	TranscribeAudio(ctx context.Context, audio []byte, mimeType string, options ...AudioTranscriptionOption) (*AudioTranscriptionResponse, error)
}

// AudioTranscriptionOptions holds configuration for speech-to-text requests.
type AudioTranscriptionOptions struct {
	LanguageCode string
	SmartFormat  *bool
	Punctuate    *bool
	Diarize      *bool
}

// AudioTranscriptionOption configures a speech-to-text request.
type AudioTranscriptionOption func(*AudioTranscriptionOptions)

// WithTranscriptionLanguageCode sets the optional language code.
func WithTranscriptionLanguageCode(languageCode string) AudioTranscriptionOption {
	return func(opts *AudioTranscriptionOptions) {
		opts.LanguageCode = languageCode
	}
}

// WithTranscriptionSmartFormat toggles smart formatting.
func WithTranscriptionSmartFormat(enabled bool) AudioTranscriptionOption {
	return func(opts *AudioTranscriptionOptions) {
		opts.SmartFormat = &enabled
	}
}

// WithTranscriptionPunctuate toggles punctuation.
func WithTranscriptionPunctuate(enabled bool) AudioTranscriptionOption {
	return func(opts *AudioTranscriptionOptions) {
		opts.Punctuate = &enabled
	}
}

// WithTranscriptionDiarize toggles speaker diarization.
func WithTranscriptionDiarize(enabled bool) AudioTranscriptionOption {
	return func(opts *AudioTranscriptionOptions) {
		opts.Diarize = &enabled
	}
}

// AudioTranscriptionResponse holds the result of a speech-to-text request.
type AudioTranscriptionResponse struct {
	Transcript string
	Confidence float64
	Duration   float64
}
