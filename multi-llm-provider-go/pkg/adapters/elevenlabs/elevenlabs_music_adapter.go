package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	DefaultMusicModelID      = "music_v1"
	DefaultMusicOutputFormat = "mp3_44100_128"
)

// ElevenLabsMusicAdapter implements llmtypes.MusicGenerationModel using ElevenLabs Music.
type ElevenLabsMusicAdapter struct {
	apiKey       string
	modelID      string
	outputFormat string
	httpClient   *http.Client
	logger       interfaces.Logger
	baseURL      string
}

// NewElevenLabsMusicAdapter creates a new ElevenLabs Music adapter.
func NewElevenLabsMusicAdapter(apiKey, modelID, outputFormat string, logger interfaces.Logger) *ElevenLabsMusicAdapter {
	if strings.TrimSpace(modelID) == "" {
		modelID = DefaultMusicModelID
	}
	if strings.TrimSpace(outputFormat) == "" {
		outputFormat = DefaultMusicOutputFormat
	}
	return &ElevenLabsMusicAdapter{
		apiKey:       apiKey,
		modelID:      modelID,
		outputFormat: outputFormat,
		httpClient:   http.DefaultClient,
		logger:       logger,
		baseURL:      defaultBaseURL,
	}
}

// GenerateMusic implements llmtypes.MusicGenerationModel.
func (a *ElevenLabsMusicAdapter) GenerateMusic(ctx context.Context, prompt string, options ...llmtypes.MusicGenerationOption) (*llmtypes.MusicGenerationResponse, error) {
	opts := &llmtypes.MusicGenerationOptions{
		OutputFormat: a.outputFormat,
	}
	for _, opt := range options {
		opt(opts)
	}

	payload := map[string]any{
		"model_id": a.modelID,
	}
	if len(opts.CompositionPlan) > 0 {
		payload["composition_plan"] = opts.CompositionPlan
	} else {
		if strings.TrimSpace(prompt) == "" {
			return nil, fmt.Errorf("prompt is required for ElevenLabs music generation when composition_plan is not provided")
		}
		payload["prompt"] = prompt
		if opts.DurationMS > 0 {
			payload["music_length_ms"] = opts.DurationMS
		}
		payload["force_instrumental"] = opts.Instrumental
	}
	if opts.Seed > 0 {
		payload["seed"] = opts.Seed
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ElevenLabs music request: %w", err)
	}

	outputFormat := strings.TrimSpace(opts.OutputFormat)
	if outputFormat == "" {
		outputFormat = a.outputFormat
	}
	endpoint, err := url.Parse(strings.TrimRight(a.baseURL, "/") + "/v1/music")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("output_format", outputFormat)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	req.Header.Set("xi-api-key", a.apiKey)

	if a.logger != nil {
		a.logger.Infof("Generating music with ElevenLabs model %s", a.modelID)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs music request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read ElevenLabs music response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("elevenlabs music failed with status %d: %s", resp.StatusCode, truncateForError(data))
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("elevenlabs music returned no audio data")
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = mimeTypeForElevenLabsMusicOutput(outputFormat)
	}
	metadata := map[string]any{
		"model_id":      a.modelID,
		"output_format": outputFormat,
	}
	if songID := strings.TrimSpace(resp.Header.Get("song-id")); songID != "" {
		metadata["song_id"] = songID
	}

	return &llmtypes.MusicGenerationResponse{
		Music: []llmtypes.GeneratedMusic{{
			Data:     data,
			MimeType: mimeType,
			Metadata: metadata,
		}},
	}, nil
}

func mimeTypeForElevenLabsMusicOutput(outputFormat string) string {
	switch {
	case strings.HasPrefix(outputFormat, "pcm_"):
		return "audio/pcm"
	case strings.HasPrefix(outputFormat, "opus_"):
		return "audio/ogg"
	case strings.HasPrefix(outputFormat, "ulaw_"):
		return "audio/basic"
	case strings.HasPrefix(outputFormat, "alaw_"):
		return "audio/x-alaw-basic"
	default:
		return "audio/mpeg"
	}
}
