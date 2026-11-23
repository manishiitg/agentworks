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
	DefaultMusicModelID = "music-2.6"
	defaultMusicBaseURL = "https://api.minimax.io"
)

// MiniMaxMusicAdapter implements llmtypes.MusicGenerationModel using MiniMax Music Generation.
type MiniMaxMusicAdapter struct {
	apiKey     string
	modelID    string
	httpClient *http.Client
	logger     interfaces.Logger
	baseURL    string
}

// NewMiniMaxMusicAdapter creates a new MiniMax Music adapter.
func NewMiniMaxMusicAdapter(apiKey, modelID string, logger interfaces.Logger) *MiniMaxMusicAdapter {
	if strings.TrimSpace(modelID) == "" {
		modelID = DefaultMusicModelID
	}
	return &MiniMaxMusicAdapter{
		apiKey:     apiKey,
		modelID:    modelID,
		httpClient: http.DefaultClient,
		logger:     logger,
		baseURL:    defaultMusicBaseURL,
	}
}

// GenerateMusic implements llmtypes.MusicGenerationModel.
func (a *MiniMaxMusicAdapter) GenerateMusic(ctx context.Context, prompt string, options ...llmtypes.MusicGenerationOption) (*llmtypes.MusicGenerationResponse, error) {
	opts := &llmtypes.MusicGenerationOptions{
		Instrumental: true,
		OutputFormat: "hex",
	}
	for _, opt := range options {
		opt(opts)
	}

	payload := map[string]any{
		"model":           a.modelID,
		"prompt":          prompt,
		"stream":          false,
		"output_format":   "hex",
		"is_instrumental": opts.Instrumental,
		"audio_setting": map[string]any{
			"sample_rate": 44100,
			"bitrate":     256000,
			"format":      "mp3",
		},
	}
	if strings.TrimSpace(opts.Lyrics) != "" {
		payload["lyrics"] = opts.Lyrics
	}
	if opts.LyricsOptimizer {
		payload["lyrics_optimizer"] = true
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal MiniMax music request: %w", err)
	}

	endpoint := strings.TrimRight(a.baseURL, "/") + "/v1/music_generation"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	if a.logger != nil {
		a.logger.Infof("Generating music with MiniMax model %s", a.modelID)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("minimax music request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MiniMax music response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("minimax music failed with status %d: %s", resp.StatusCode, truncateMiniMaxMusicError(data))
	}

	var parsed minimaxMusicResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse MiniMax music response: %w", err)
	}
	if parsed.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("minimax music failed with status %d: %s", parsed.BaseResp.StatusCode, parsed.BaseResp.StatusMsg)
	}
	audioHex := strings.TrimSpace(parsed.Data.Audio)
	if audioHex == "" {
		return nil, fmt.Errorf("minimax music returned no audio data")
	}
	audioBytes, err := hex.DecodeString(audioHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode MiniMax music audio hex: %w", err)
	}
	if len(audioBytes) == 0 {
		return nil, fmt.Errorf("minimax music returned empty audio data")
	}

	metadata := map[string]any{
		"model_id": a.modelID,
		"trace_id": parsed.TraceID,
		"status":   parsed.Data.Status,
	}
	if parsed.ExtraInfo.MusicDuration > 0 {
		metadata["music_duration_ms"] = parsed.ExtraInfo.MusicDuration
	}
	if parsed.ExtraInfo.MusicSampleRate > 0 {
		metadata["sample_rate"] = parsed.ExtraInfo.MusicSampleRate
	}
	if parsed.ExtraInfo.MusicChannel > 0 {
		metadata["channels"] = parsed.ExtraInfo.MusicChannel
	}
	if parsed.ExtraInfo.Bitrate > 0 {
		metadata["bitrate"] = parsed.ExtraInfo.Bitrate
	}
	if parsed.ExtraInfo.MusicSize > 0 {
		metadata["size_bytes"] = parsed.ExtraInfo.MusicSize
	}

	return &llmtypes.MusicGenerationResponse{
		Music: []llmtypes.GeneratedMusic{{
			Data:     audioBytes,
			MimeType: "audio/mpeg",
			Metadata: metadata,
		}},
	}, nil
}

type minimaxMusicResponse struct {
	Data struct {
		Audio  string `json:"audio"`
		Status int    `json:"status"`
	} `json:"data"`
	TraceID   string `json:"trace_id"`
	ExtraInfo struct {
		MusicDuration   int `json:"music_duration"`
		MusicSampleRate int `json:"music_sample_rate"`
		MusicChannel    int `json:"music_channel"`
		Bitrate         int `json:"bitrate"`
		MusicSize       int `json:"music_size"`
	} `json:"extra_info"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

func truncateMiniMaxMusicError(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
