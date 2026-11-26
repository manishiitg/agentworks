package minimax

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	DefaultTTSModelID = "speech-2.8-turbo"
	DefaultTTSVoiceID = "English_expressive_narrator"
	defaultTTSBaseURL = "https://api.minimax.io"
)

// MiniMaxTTSAdapter implements llmtypes.AudioGenerationModel using MiniMax T2A.
type MiniMaxTTSAdapter struct {
	apiKey     string
	modelID    string
	voiceID    string
	httpClient *http.Client
	logger     interfaces.Logger
	baseURL    string
}

// NewMiniMaxTTSAdapter creates a new MiniMax TTS adapter.
func NewMiniMaxTTSAdapter(apiKey, modelID, voiceID string, logger interfaces.Logger) *MiniMaxTTSAdapter {
	if strings.TrimSpace(modelID) == "" {
		modelID = DefaultTTSModelID
	}
	if strings.TrimSpace(voiceID) == "" {
		voiceID = DefaultTTSVoiceID
	}
	return &MiniMaxTTSAdapter{
		apiKey:     apiKey,
		modelID:    modelID,
		voiceID:    voiceID,
		httpClient: http.DefaultClient,
		logger:     logger,
		baseURL:    defaultTTSBaseURL,
	}
}

// GenerateAudio implements llmtypes.AudioGenerationModel.
func (a *MiniMaxTTSAdapter) GenerateAudio(ctx context.Context, prompt string, options ...llmtypes.AudioGenerationOption) (*llmtypes.AudioGenerationResponse, error) {
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
		"model":          a.modelID,
		"text":           prompt,
		"stream":         false,
		"output_format":  "hex",
		"language_boost": "auto",
		"voice_setting": map[string]any{
			"voice_id": voiceID,
			"speed":    1,
			"vol":      1,
			"pitch":    0,
		},
		"audio_setting": map[string]any{
			"sample_rate": 32000,
			"bitrate":     128000,
			"format":      "mp3",
			"channel":     1,
		},
	}
	if strings.TrimSpace(opts.LanguageCode) != "" {
		payload["language_boost"] = strings.TrimSpace(opts.LanguageCode)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal MiniMax TTS request: %w", err)
	}

	endpoint := strings.TrimRight(a.baseURL, "/") + "/v1/t2a_v2"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	if a.logger != nil {
		a.logger.Infof("Generating audio with MiniMax model %s voice %s", a.modelID, voiceID)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("minimax TTS request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MiniMax TTS response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("minimax TTS failed with status %d: %s", resp.StatusCode, truncateMiniMaxTTSError(data))
	}

	var parsed minimaxTTSResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse MiniMax TTS response: %w", err)
	}
	if parsed.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("minimax TTS failed with status %d: %s", parsed.BaseResp.StatusCode, parsed.BaseResp.StatusMsg)
	}
	audioHex := strings.TrimSpace(parsed.Data.Audio)
	if audioHex == "" {
		return nil, fmt.Errorf("minimax TTS returned no audio data")
	}
	audioBytes, err := hex.DecodeString(audioHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode MiniMax TTS audio hex: %w", err)
	}
	if len(audioBytes) == 0 {
		return nil, fmt.Errorf("minimax TTS returned empty audio data")
	}

	mimeType := "audio/mpeg"
	if parsed.ExtraInfo.AudioFormat == "wav" {
		mimeType = "audio/wav"
	} else if parsed.ExtraInfo.AudioFormat == "flac" {
		mimeType = "audio/flac"
	} else if parsed.ExtraInfo.AudioFormat == "pcm" {
		mimeType = "audio/pcm"
	}

	return &llmtypes.AudioGenerationResponse{
		Audio: []llmtypes.GeneratedAudio{{
			Data:     audioBytes,
			MimeType: mimeType,
		}},
	}, nil
}

type minimaxTTSResponse struct {
	Data struct {
		Audio string `json:"audio"`
	} `json:"data"`
	ExtraInfo struct {
		AudioFormat string `json:"audio_format"`
	} `json:"extra_info"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

func truncateMiniMaxTTSError(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
