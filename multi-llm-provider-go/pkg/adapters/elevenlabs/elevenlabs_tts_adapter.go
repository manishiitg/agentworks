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
	DefaultVoiceID      = "JBFqnCBsd6RMkjVDRZzb"
	DefaultModelID      = "eleven_multilingual_v2"
	DefaultOutputFormat = "mp3_44100_128"
	defaultBaseURL      = "https://api.elevenlabs.io"
)

// ElevenLabsTTSAdapter implements llmtypes.AudioGenerationModel using ElevenLabs TTS.
type ElevenLabsTTSAdapter struct {
	apiKey       string
	modelID      string
	voiceID      string
	outputFormat string
	httpClient   *http.Client
	logger       interfaces.Logger
	baseURL      string
}

// NewElevenLabsTTSAdapter creates a new ElevenLabs TTS adapter.
func NewElevenLabsTTSAdapter(apiKey, modelID, voiceID, outputFormat string, logger interfaces.Logger) *ElevenLabsTTSAdapter {
	if strings.TrimSpace(modelID) == "" {
		modelID = DefaultModelID
	}
	if strings.TrimSpace(voiceID) == "" {
		voiceID = DefaultVoiceID
	}
	if strings.TrimSpace(outputFormat) == "" {
		outputFormat = DefaultOutputFormat
	}
	return &ElevenLabsTTSAdapter{
		apiKey:       apiKey,
		modelID:      modelID,
		voiceID:      voiceID,
		outputFormat: outputFormat,
		httpClient:   http.DefaultClient,
		logger:       logger,
		baseURL:      defaultBaseURL,
	}
}

// GenerateAudio implements llmtypes.AudioGenerationModel.
func (a *ElevenLabsTTSAdapter) GenerateAudio(ctx context.Context, prompt string, options ...llmtypes.AudioGenerationOption) (*llmtypes.AudioGenerationResponse, error) {
	opts := &llmtypes.AudioGenerationOptions{
		VoiceName: a.voiceID,
	}
	for _, opt := range options {
		opt(opts)
	}
	voiceID := strings.TrimSpace(opts.VoiceName)
	if voiceID == "" {
		voiceID = a.voiceID
	}

	payload := map[string]any{
		"text":     prompt,
		"model_id": a.modelID,
	}
	if strings.TrimSpace(opts.LanguageCode) != "" {
		payload["language_code"] = strings.TrimSpace(opts.LanguageCode)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ElevenLabs TTS request: %w", err)
	}

	endpoint, err := url.Parse(strings.TrimRight(a.baseURL, "/") + "/v1/text-to-speech/" + url.PathEscape(voiceID))
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("output_format", a.outputFormat)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	req.Header.Set("xi-api-key", a.apiKey)

	if a.logger != nil {
		a.logger.Infof("Generating audio with ElevenLabs model %s voice %s", a.modelID, voiceID)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs TTS request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read ElevenLabs TTS response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("elevenlabs TTS failed with status %d: %s", resp.StatusCode, truncateForError(data))
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("elevenlabs TTS returned no audio data")
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "audio/mpeg"
	}
	return &llmtypes.AudioGenerationResponse{
		Audio: []llmtypes.GeneratedAudio{{
			Data:     data,
			MimeType: mimeType,
		}},
	}, nil
}

func truncateForError(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
