package deepgram

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
	DefaultModelID              = "aura-2-thalia-en"
	DefaultTranscriptionModelID = "nova-3"
	defaultBaseURL              = "https://api.deepgram.com"
)

// DeepgramTTSAdapter implements llmtypes.AudioGenerationModel using Deepgram Speak.
type DeepgramTTSAdapter struct {
	apiKey     string
	modelID    string
	httpClient *http.Client
	logger     interfaces.Logger
	baseURL    string
}

// NewDeepgramTTSAdapter creates a new Deepgram TTS adapter.
func NewDeepgramTTSAdapter(apiKey, modelID string, logger interfaces.Logger) *DeepgramTTSAdapter {
	if strings.TrimSpace(modelID) == "" {
		modelID = DefaultModelID
	}
	return &DeepgramTTSAdapter{
		apiKey:     apiKey,
		modelID:    modelID,
		httpClient: http.DefaultClient,
		logger:     logger,
		baseURL:    defaultBaseURL,
	}
}

// GenerateAudio implements llmtypes.AudioGenerationModel.
func (a *DeepgramTTSAdapter) GenerateAudio(ctx context.Context, prompt string, options ...llmtypes.AudioGenerationOption) (*llmtypes.AudioGenerationResponse, error) {
	opts := &llmtypes.AudioGenerationOptions{}
	for _, opt := range options {
		opt(opts)
	}

	modelID := a.modelID
	if voiceName := strings.TrimSpace(opts.VoiceName); strings.HasPrefix(voiceName, "aura-") {
		modelID = voiceName
	}

	body, err := json.Marshal(map[string]string{"text": prompt})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Deepgram TTS request: %w", err)
	}

	endpoint, err := url.Parse(strings.TrimRight(a.baseURL, "/") + "/v1/speak")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("model", modelID)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	req.Header.Set("Authorization", "Token "+a.apiKey)

	if a.logger != nil {
		a.logger.Infof("Generating audio with Deepgram model %s", modelID)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepgram TTS request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Deepgram TTS response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("deepgram TTS failed with status %d: %s", resp.StatusCode, truncateForError(data))
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("deepgram TTS returned no audio data")
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

// TranscribeAudio implements llmtypes.AudioTranscriptionModel.
func (a *DeepgramTTSAdapter) TranscribeAudio(ctx context.Context, audio []byte, mimeType string, options ...llmtypes.AudioTranscriptionOption) (*llmtypes.AudioTranscriptionResponse, error) {
	if len(audio) == 0 {
		return nil, fmt.Errorf("audio is required")
	}

	opts := &llmtypes.AudioTranscriptionOptions{}
	for _, opt := range options {
		opt(opts)
	}

	modelID := a.modelID
	if strings.TrimSpace(modelID) == "" || strings.HasPrefix(modelID, "aura-") {
		modelID = DefaultTranscriptionModelID
	}

	endpoint, err := url.Parse(strings.TrimRight(a.baseURL, "/") + "/v1/listen")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("model", modelID)
	if strings.TrimSpace(opts.LanguageCode) != "" {
		q.Set("language", strings.TrimSpace(opts.LanguageCode))
	}
	if opts.SmartFormat == nil || *opts.SmartFormat {
		q.Set("smart_format", "true")
	}
	if opts.Punctuate == nil || *opts.Punctuate {
		q.Set("punctuate", "true")
	}
	if opts.Diarize != nil {
		q.Set("diarize", fmt.Sprintf("%t", *opts.Diarize))
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(audio))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+a.apiKey)

	if a.logger != nil {
		a.logger.Infof("Transcribing audio with Deepgram model %s", modelID)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepgram STT request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Deepgram STT response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("deepgram STT failed with status %d: %s", resp.StatusCode, truncateForError(data))
	}

	var parsed deepgramListenResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse Deepgram STT response: %w", err)
	}
	if len(parsed.Results.Channels) == 0 || len(parsed.Results.Channels[0].Alternatives) == 0 {
		return nil, fmt.Errorf("deepgram STT returned no transcription alternatives")
	}
	alt := parsed.Results.Channels[0].Alternatives[0]
	return &llmtypes.AudioTranscriptionResponse{
		Transcript: alt.Transcript,
		Confidence: alt.Confidence,
		Duration:   parsed.Metadata.Duration,
	}, nil
}

type deepgramListenResponse struct {
	Metadata struct {
		Duration float64 `json:"duration"`
	} `json:"metadata"`
	Results struct {
		Channels []struct {
			Alternatives []struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
		} `json:"channels"`
	} `json:"results"`
}

func truncateForError(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
