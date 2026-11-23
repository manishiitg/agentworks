package llmtypes

import "context"

// ImageGenerationModel is an interface for models that support image generation.
// This is separate from the Model interface since not all models support image generation.
type ImageGenerationModel interface {
	// GenerateImages generates images from a text prompt.
	// Returns an ImageGenerationResponse containing one or more generated images.
	GenerateImages(ctx context.Context, prompt string, options ...ImageGenerationOption) (*ImageGenerationResponse, error)
}

// ImageGenerationOptions holds configuration for image generation requests
type ImageGenerationOptions struct {
	// NumberOfImages is the number of images to generate (default: 1)
	NumberOfImages int
	// AspectRatio controls image dimensions, e.g. "1:1", "16:9", "9:16", "4:3", "3:4"
	AspectRatio string
	// Resolution controls image size: "1K", "2K", "4K" (default: "1K")
	Resolution string
	// NegativePrompt describes what to exclude from the generated image
	NegativePrompt string
	// InputImage is an optional input image for editing (raw bytes)
	InputImage []byte
	// InputImageMIMEType is the MIME type of InputImage, e.g. "image/png"
	InputImageMIMEType string
	// ConversationHistory holds prior turns for multi-turn image editing
	ConversationHistory []*ImageTurn
	// InputImageURL is a URL to an image for subject-reference editing
	InputImageURL string
}

// ImageTurn represents a single turn in a multi-turn image conversation
type ImageTurn struct {
	Role    string // "user" or "model"
	Text    string
	Images  [][]byte // raw image bytes from this turn
	MIMETypes []string
}

// ImageGenerationOption is a functional option for configuring image generation
type ImageGenerationOption func(*ImageGenerationOptions)

// WithNumberOfImages sets the number of images to generate
func WithNumberOfImages(n int) ImageGenerationOption {
	return func(opts *ImageGenerationOptions) {
		opts.NumberOfImages = n
	}
}

// WithAspectRatio sets the aspect ratio for generated images
// Valid values: "1:1", "16:9", "9:16", "4:3", "3:4"
func WithAspectRatio(ratio string) ImageGenerationOption {
	return func(opts *ImageGenerationOptions) {
		opts.AspectRatio = ratio
	}
}

// WithNegativePrompt sets the negative prompt to exclude from generation
func WithNegativePrompt(prompt string) ImageGenerationOption {
	return func(opts *ImageGenerationOptions) {
		opts.NegativePrompt = prompt
	}
}

// WithResolution sets the output image resolution: "1K", "2K", or "4K".
func WithResolution(resolution string) ImageGenerationOption {
	return func(opts *ImageGenerationOptions) {
		opts.Resolution = resolution
	}
}

// WithInputImage sets an input image for editing mode.
// When provided, the model edits the input image according to the prompt
// instead of generating from scratch.
func WithInputImage(data []byte, mimeType string) ImageGenerationOption {
	return func(opts *ImageGenerationOptions) {
		opts.InputImage = data
		opts.InputImageMIMEType = mimeType
	}
}

// WithConversationHistory sets prior conversation turns for multi-turn image editing.
func WithConversationHistory(history []*ImageTurn) ImageGenerationOption {
	return func(opts *ImageGenerationOptions) {
		opts.ConversationHistory = history
	}
}

// WithInputImageURL sets a URL to a reference image for subject-based editing.
func WithInputImageURL(url string) ImageGenerationOption {
	return func(opts *ImageGenerationOptions) {
		opts.InputImageURL = url
	}
}

// ImageGenerationResponse holds the result of an image generation request
type ImageGenerationResponse struct {
	// Images contains the generated images
	Images []GeneratedImage
}

// GeneratedImage represents a single generated image
type GeneratedImage struct {
	// Data contains the raw image bytes (PNG format)
	Data []byte
	// MimeType is the MIME type of the image (e.g. "image/png")
	MimeType string
}
