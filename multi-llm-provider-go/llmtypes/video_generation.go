package llmtypes

import "context"

// VideoGenerationModel is an interface for models that support video generation.
// This is separate from the Model interface since not all models support video generation.
type VideoGenerationModel interface {
	// GenerateVideos generates one or more videos from a text prompt and optional source media.
	GenerateVideos(ctx context.Context, prompt string, options ...VideoGenerationOption) (*VideoGenerationResponse, error)
}

// VideoGenerationOptions holds configuration for video generation requests.
type VideoGenerationOptions struct {
	// NumberOfVideos is the number of videos to generate (default: 1).
	NumberOfVideos int
	// AspectRatio controls output framing, e.g. "16:9" or "9:16".
	AspectRatio string
	// Resolution controls output size, e.g. "720p" or "1080p".
	Resolution string
	// NegativePrompt describes what to exclude from the generated video.
	NegativePrompt string
	// DurationSeconds controls output clip length. Supported values depend on the model.
	DurationSeconds int
	// GenerateAudio enables native audio generation when supported by the model.
	GenerateAudio *bool
	// PersonGeneration controls people/face generation policy, e.g. "allow_adult" or "dont_allow".
	PersonGeneration string
	// Seed makes generations deterministic when the provider supports it.
	Seed *int32
	// InputImage is an optional first-frame image for image-to-video generation.
	InputImage []byte
	// InputImageMIMEType is the MIME type of InputImage, e.g. "image/png".
	InputImageMIMEType string
	// LastFrame is an optional final frame image for first-frame/last-frame video generation.
	// It is only valid when InputImage is also provided.
	LastFrame []byte
	// LastFrameMIMEType is the MIME type of LastFrame, e.g. "image/png".
	LastFrameMIMEType string
	// PreviousInteractionID references a prior generation for conversational
	// video editing, when supported by the provider (e.g. Gemini Omni).
	PreviousInteractionID string
	// ReferenceImages are additional images for subject/style reference
	// generation, when supported by the provider (e.g. Gemini Omni composing
	// two distinct subjects, like a cat and a ball of yarn, into one scene).
	// Distinct from InputImage/InputImageMIMEType, which is a single
	// animate-this-image input. NOT a first-frame/last-frame interpolation
	// primitive — providers may not support that at all.
	ReferenceImages []VideoReferenceImage
}

// VideoReferenceImage is one image in a multi-image reference/subject-composition request.
type VideoReferenceImage struct {
	Data     []byte
	MimeType string
}

// VideoGenerationOption is a functional option for configuring video generation.
type VideoGenerationOption func(*VideoGenerationOptions)

// WithVideoNumberOfVideos sets the number of videos to generate.
func WithVideoNumberOfVideos(n int) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.NumberOfVideos = n
	}
}

// WithVideoAspectRatio sets the output video aspect ratio.
func WithVideoAspectRatio(ratio string) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.AspectRatio = ratio
	}
}

// WithVideoResolution sets the output video resolution.
func WithVideoResolution(resolution string) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.Resolution = resolution
	}
}

// WithVideoNegativePrompt sets the negative prompt.
func WithVideoNegativePrompt(prompt string) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.NegativePrompt = prompt
	}
}

// WithVideoDurationSeconds sets the requested clip duration.
func WithVideoDurationSeconds(seconds int) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.DurationSeconds = seconds
	}
}

// WithVideoGenerateAudio requests audio generation when supported.
func WithVideoGenerateAudio(enabled bool) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.GenerateAudio = &enabled
	}
}

// WithVideoPersonGeneration sets the people-generation safety policy.
func WithVideoPersonGeneration(policy string) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.PersonGeneration = policy
	}
}

// WithVideoSeed sets a deterministic RNG seed.
func WithVideoSeed(seed int32) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.Seed = &seed
	}
}

// WithVideoInputImage sets an input image for image-to-video generation.
func WithVideoInputImage(data []byte, mimeType string) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.InputImage = data
		opts.InputImageMIMEType = mimeType
	}
}

// WithVideoLastFrame sets the final image for first-frame/last-frame generation.
// Providers that support this require an input/first-frame image as well.
func WithVideoLastFrame(data []byte, mimeType string) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.LastFrame = data
		opts.LastFrameMIMEType = mimeType
	}
}

// WithVideoPreviousInteractionID chains this request onto a prior generation
// for conversational video editing, when supported by the provider.
func WithVideoPreviousInteractionID(id string) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.PreviousInteractionID = id
	}
}

// WithVideoReferenceImages sets two or more subject/style reference images
// to compose into one scene (e.g. "a cat" + "a ball of yarn" -> a cat batting
// at a ball of yarn), when supported by the provider. This is NOT a
// first-frame/last-frame interpolation control.
func WithVideoReferenceImages(images ...VideoReferenceImage) VideoGenerationOption {
	return func(opts *VideoGenerationOptions) {
		opts.ReferenceImages = images
	}
}

// VideoGenerationResponse holds the result of a video generation request.
type VideoGenerationResponse struct {
	// Videos contains the generated videos.
	Videos []GeneratedVideo
	// FilteredCount is the number of videos filtered by provider safety systems.
	FilteredCount int
	// FilterReasons contains any provider safety/filter reasons that were returned.
	FilterReasons []string
}

// GeneratedVideo represents a single generated video.
type GeneratedVideo struct {
	// Data contains the raw video bytes when downloaded successfully.
	Data []byte
	// MimeType is the MIME type of the video (e.g. "video/mp4").
	MimeType string
	// URI is the provider download URI when one was returned.
	URI string
}
