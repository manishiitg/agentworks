package llmtypes

import "context"

// MusicGenerationModel is an interface for models that support text-to-music generation.
type MusicGenerationModel interface {
	// GenerateMusic generates music from a text prompt.
	GenerateMusic(ctx context.Context, prompt string, options ...MusicGenerationOption) (*MusicGenerationResponse, error)
}

// MusicGenerationOptions holds configuration for music generation requests.
type MusicGenerationOptions struct {
	// DurationMS is the requested music duration in milliseconds.
	DurationMS int
	// Instrumental requests an instrumental track when supported by the provider.
	Instrumental bool
	// Lyrics supplies explicit lyrics for providers that support vocal music.
	Lyrics string
	// LyricsOptimizer asks the provider to generate or improve lyrics from the prompt when supported.
	LyricsOptimizer bool
	// Seed controls deterministic generation when supported by the provider.
	Seed int
	// OutputFormat is a provider-specific audio output format such as mp3_44100_128 or mp3.
	OutputFormat string
	// CompositionPlan is a provider-specific structured plan for section-based music generation.
	CompositionPlan map[string]any
}

// MusicGenerationOption is a functional option for configuring music generation.
type MusicGenerationOption func(*MusicGenerationOptions)

// WithMusicDurationMS sets the requested music duration in milliseconds.
func WithMusicDurationMS(durationMS int) MusicGenerationOption {
	return func(opts *MusicGenerationOptions) {
		opts.DurationMS = durationMS
	}
}

// WithMusicInstrumental requests an instrumental track.
func WithMusicInstrumental(instrumental bool) MusicGenerationOption {
	return func(opts *MusicGenerationOptions) {
		opts.Instrumental = instrumental
	}
}

// WithMusicLyrics sets explicit lyrics for vocal music.
func WithMusicLyrics(lyrics string) MusicGenerationOption {
	return func(opts *MusicGenerationOptions) {
		opts.Lyrics = lyrics
	}
}

// WithMusicLyricsOptimizer enables provider-side lyric generation or optimization.
func WithMusicLyricsOptimizer(enabled bool) MusicGenerationOption {
	return func(opts *MusicGenerationOptions) {
		opts.LyricsOptimizer = enabled
	}
}

// WithMusicSeed sets a generation seed.
func WithMusicSeed(seed int) MusicGenerationOption {
	return func(opts *MusicGenerationOptions) {
		opts.Seed = seed
	}
}

// WithMusicOutputFormat sets the provider-specific output format.
func WithMusicOutputFormat(format string) MusicGenerationOption {
	return func(opts *MusicGenerationOptions) {
		opts.OutputFormat = format
	}
}

// WithMusicCompositionPlan sets an ElevenLabs-compatible composition plan.
func WithMusicCompositionPlan(plan map[string]any) MusicGenerationOption {
	return func(opts *MusicGenerationOptions) {
		opts.CompositionPlan = plan
	}
}

// MusicGenerationResponse holds the result of a music generation request.
type MusicGenerationResponse struct {
	Music []GeneratedMusic
}

// GeneratedMusic represents one generated music item.
type GeneratedMusic struct {
	Data     []byte
	MimeType string
	Metadata map[string]any
}
