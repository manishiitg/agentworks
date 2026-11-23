package vertex

import (
	"context"
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"google.golang.org/genai"
)

// GeminiImageAdapter implements llmtypes.ImageGenerationModel using Gemini's native
// image generation via GenerateContent (e.g. gemini-2.0-flash-preview-image-generation).
// Unlike Imagen, this uses the standard GenerateContent API and returns InlineData parts.
type GeminiImageAdapter struct {
	client  *genai.Client
	modelID string
	logger  interfaces.Logger
}

// NewGeminiImageAdapter creates a new Gemini image generation adapter.
// The client must be initialized with genai.BackendGeminiAPI and a valid API key.
func NewGeminiImageAdapter(client *genai.Client, modelID string, logger interfaces.Logger) *GeminiImageAdapter {
	return &GeminiImageAdapter{
		client:  client,
		modelID: modelID,
		logger:  logger,
	}
}

// GenerateImages implements llmtypes.ImageGenerationModel using GenerateContent.
// The Gemini image models return image data as InlineData parts in the response.
// When opts.InputImage is set, the model edits the input image instead of generating from scratch.
func (a *GeminiImageAdapter) GenerateImages(ctx context.Context, prompt string, options ...llmtypes.ImageGenerationOption) (*llmtypes.ImageGenerationResponse, error) {
	opts := &llmtypes.ImageGenerationOptions{
		NumberOfImages: 1,
	}
	for _, opt := range options {
		opt(opts)
	}

	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE", "TEXT"},
	}

	// Apply ImageConfig for aspect ratio and resolution
	if opts.AspectRatio != "" || opts.Resolution != "" {
		config.ImageConfig = &genai.ImageConfig{}
		if opts.AspectRatio != "" {
			config.ImageConfig.AspectRatio = opts.AspectRatio
		}
		if opts.Resolution != "" {
			config.ImageConfig.ImageSize = opts.Resolution
		}
	}

	// Build conversation contents
	var contents []*genai.Content

	// Prepend conversation history for multi-turn editing
	for _, turn := range opts.ConversationHistory {
		role := genai.Role(turn.Role)
		if role == "" {
			role = genai.RoleUser
		}
		var turnParts []*genai.Part
		if turn.Text != "" {
			turnParts = append(turnParts, genai.NewPartFromText(turn.Text))
		}
		for i, imgData := range turn.Images {
			mt := "image/png"
			if i < len(turn.MIMETypes) && turn.MIMETypes[i] != "" {
				mt = turn.MIMETypes[i]
			}
			turnParts = append(turnParts, &genai.Part{InlineData: &genai.Blob{MIMEType: mt, Data: imgData}})
		}
		if len(turnParts) > 0 {
			contents = append(contents, genai.NewContentFromParts(turnParts, role))
		}
	}

	// Build the current user turn
	if len(opts.InputImage) > 0 {
		// Edit mode: prompt + input image
		mimeType := opts.InputImageMIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		a.logger.Infof("Editing image with Gemini model %s, prompt: %q", a.modelID, prompt)
		parts := []*genai.Part{
			genai.NewPartFromText(prompt),
			{InlineData: &genai.Blob{MIMEType: mimeType, Data: opts.InputImage}},
		}
		contents = append(contents, genai.NewContentFromParts(parts, genai.RoleUser))
	} else {
		// Generation mode (or multi-turn follow-up): prompt only
		a.logger.Infof("Generating image with Gemini model %s, prompt: %q", a.modelID, prompt)
		contents = append(contents, genai.NewContentFromParts([]*genai.Part{genai.NewPartFromText(prompt)}, genai.RoleUser))
	}

	resp, err := a.client.Models.GenerateContent(ctx, a.modelID, contents, config)
	if err != nil {
		return nil, fmt.Errorf("gemini GenerateContent failed: %w", err)
	}

	result := &llmtypes.ImageGenerationResponse{
		Images: make([]llmtypes.GeneratedImage, 0),
	}

	if resp == nil || len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("gemini returned no candidates")
	}

	for _, candidate := range resp.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && len(part.InlineData.Data) > 0 {
				mimeType := part.InlineData.MIMEType
				if mimeType == "" {
					mimeType = "image/png"
				}
				result.Images = append(result.Images, llmtypes.GeneratedImage{
					Data:     part.InlineData.Data,
					MimeType: mimeType,
				})
			}
		}
	}

	if len(result.Images) == 0 {
		return nil, fmt.Errorf("gemini returned no image data (prompt may have been filtered or model does not support image output)")
	}

	a.logger.Infof("Generated %d image(s) successfully via Gemini", len(result.Images))
	return result, nil
}
