package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"google.golang.org/genai"
)

const (
	defaultVeoPollInterval = 10 * time.Second
	defaultVeoModelID      = "veo-3.1-generate-preview"
)

// VertexVeoAdapter implements llmtypes.VideoGenerationModel using Google's Veo models.
type VertexVeoAdapter struct {
	client       *genai.Client
	modelID      string
	logger       interfaces.Logger
	pollInterval time.Duration
}

// NewVertexVeoAdapter creates a new Veo adapter.
func NewVertexVeoAdapter(client *genai.Client, modelID string, logger interfaces.Logger) *VertexVeoAdapter {
	return &VertexVeoAdapter{
		client:       client,
		modelID:      modelID,
		logger:       logger,
		pollInterval: defaultVeoPollInterval,
	}
}

// GenerateVideos implements llmtypes.VideoGenerationModel.
func (a *VertexVeoAdapter) GenerateVideos(ctx context.Context, prompt string, options ...llmtypes.VideoGenerationOption) (*llmtypes.VideoGenerationResponse, error) {
	opts := &llmtypes.VideoGenerationOptions{
		NumberOfVideos: 1,
	}
	for _, opt := range options {
		opt(opts)
	}
	if len(opts.LastFrame) > 0 && len(opts.InputImage) == 0 {
		return nil, fmt.Errorf("last frame requires an input image/first frame")
	}

	source := &genai.GenerateVideosSource{
		Prompt: prompt,
	}
	if len(opts.InputImage) > 0 {
		mimeType := opts.InputImageMIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		source.Image = &genai.Image{
			ImageBytes: opts.InputImage,
			MIMEType:   mimeType,
		}
	}

	config := &genai.GenerateVideosConfig{}
	if opts.NumberOfVideos > 0 {
		config.NumberOfVideos = int32(opts.NumberOfVideos)
	}
	if opts.AspectRatio != "" {
		config.AspectRatio = opts.AspectRatio
	}
	if opts.Resolution != "" {
		config.Resolution = opts.Resolution
	}
	if opts.NegativePrompt != "" {
		config.NegativePrompt = opts.NegativePrompt
	}
	if opts.DurationSeconds > 0 {
		duration := int32(opts.DurationSeconds)
		config.DurationSeconds = &duration
	}
	if opts.GenerateAudio != nil {
		config.GenerateAudio = opts.GenerateAudio
	}
	if len(opts.LastFrame) > 0 {
		mimeType := opts.LastFrameMIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		config.LastFrame = &genai.Image{
			ImageBytes: opts.LastFrame,
			MIMEType:   mimeType,
		}
	}
	if opts.PersonGeneration != "" {
		config.PersonGeneration = opts.PersonGeneration
	}
	if opts.Seed != nil {
		config.Seed = opts.Seed
	}

	a.logger.Infof("Generating video with Veo model %s, prompt: %q", a.modelID, prompt)
	operation, err := a.client.Models.GenerateVideosFromSource(ctx, a.modelID, source, config)
	if err != nil {
		return nil, fmt.Errorf("veo GenerateVideos failed: %w", err)
	}

	operation, err = a.waitForOperation(ctx, operation)
	if err != nil {
		return nil, err
	}
	if operation == nil || operation.Response == nil {
		return nil, fmt.Errorf("veo returned no operation response")
	}
	if len(operation.Error) > 0 {
		return nil, fmt.Errorf("veo operation failed: %s", marshalMap(operation.Error))
	}

	result := &llmtypes.VideoGenerationResponse{
		Videos:        make([]llmtypes.GeneratedVideo, 0, len(operation.Response.GeneratedVideos)),
		FilteredCount: int(operation.Response.RAIMediaFilteredCount),
		FilterReasons: append([]string(nil), operation.Response.RAIMediaFilteredReasons...),
	}

	for _, generated := range operation.Response.GeneratedVideos {
		if generated == nil || generated.Video == nil {
			continue
		}
		video := generated.Video
		if len(video.VideoBytes) == 0 && video.URI != "" {
			if _, err := a.client.Files.Download(ctx, genai.NewDownloadURIFromGeneratedVideo(generated), nil); err != nil {
				return nil, fmt.Errorf("failed to download generated video %q: %w", video.URI, err)
			}
		}

		mimeType := video.MIMEType
		if mimeType == "" {
			mimeType = "video/mp4"
		}
		result.Videos = append(result.Videos, llmtypes.GeneratedVideo{
			Data:     video.VideoBytes,
			MimeType: mimeType,
			URI:      video.URI,
		})
	}

	if len(result.Videos) == 0 {
		if result.FilteredCount > 0 {
			return nil, fmt.Errorf("veo returned no videos because all outputs were filtered: %v", result.FilterReasons)
		}
		return nil, fmt.Errorf("veo returned no video data")
	}

	a.logger.Infof("Generated %d video(s) successfully via Veo", len(result.Videos))
	return result, nil
}

func (a *VertexVeoAdapter) waitForOperation(ctx context.Context, operation *genai.GenerateVideosOperation) (*genai.GenerateVideosOperation, error) {
	if operation == nil {
		return nil, fmt.Errorf("veo returned a nil operation")
	}
	if operation.Done {
		return operation, nil
	}

	interval := a.pollInterval
	if interval <= 0 {
		interval = defaultVeoPollInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	current := operation
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			next, err := a.client.Operations.GetVideosOperation(ctx, current, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to poll Veo operation: %w", err)
			}
			current = next
			if current.Done {
				return current, nil
			}
		}
	}
}

func marshalMap(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}
