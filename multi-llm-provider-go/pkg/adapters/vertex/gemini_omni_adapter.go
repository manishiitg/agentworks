package vertex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	defaultOmniPollInterval = 5 * time.Second
	defaultOmniBaseURL      = "https://generativelanguage.googleapis.com/v1beta"
)

// GeminiOmniAdapter implements llmtypes.VideoGenerationModel using Gemini Omni Flash
// via the Gemini Developer API's Interactions API (POST /v1beta/interactions).
// There is no Go SDK support for this endpoint yet (google.golang.org/genai has no
// Interactions client as of v1.62.0), so this adapter speaks the REST contract directly.
type GeminiOmniAdapter struct {
	apiKey       string
	modelID      string
	baseURL      string
	httpClient   *http.Client
	logger       interfaces.Logger
	pollInterval time.Duration
}

// NewGeminiOmniAdapter creates a new Gemini Omni video-generation adapter.
func NewGeminiOmniAdapter(apiKey, modelID string, logger interfaces.Logger) *GeminiOmniAdapter {
	return &GeminiOmniAdapter{
		apiKey:       apiKey,
		modelID:      modelID,
		baseURL:      defaultOmniBaseURL,
		httpClient:   &http.Client{},
		logger:       logger,
		pollInterval: defaultOmniPollInterval,
	}
}

// omniContentItem is one item of an Interactions API `input` array, or one
// entry of a `model_output` step's `content` array in the response.
type omniContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type omniInteractionRequest struct {
	Model                 string      `json:"model"`
	Input                 interface{} `json:"input"`
	ResponseModalities    []string    `json:"response_modalities"`
	PreviousInteractionID string      `json:"previous_interaction_id,omitempty"`
}

type omniStep struct {
	Type    string            `json:"type"`
	Content []omniContentItem `json:"content,omitempty"`
}

type omniInteractionResponse struct {
	ID     string     `json:"id"`
	Status string     `json:"status"`
	Model  string     `json:"model"`
	Steps  []omniStep `json:"steps"`
}

type omniErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// GenerateVideos implements llmtypes.VideoGenerationModel.
func (a *GeminiOmniAdapter) GenerateVideos(ctx context.Context, prompt string, options ...llmtypes.VideoGenerationOption) (*llmtypes.VideoGenerationResponse, error) {
	opts := &llmtypes.VideoGenerationOptions{}
	for _, opt := range options {
		opt(opts)
	}
	if len(opts.LastFrame) > 0 {
		return nil, fmt.Errorf("gemini omni video generation does not support last-frame interpolation through this adapter; use a Veo 3.1 model for last-frame control")
	}

	req := omniInteractionRequest{
		Model:                 a.modelID,
		ResponseModalities:    []string{"video"},
		PreviousInteractionID: opts.PreviousInteractionID,
	}

	var images []omniContentItem
	if len(opts.InputImage) > 0 {
		mimeType := opts.InputImageMIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		images = append(images, omniContentItem{
			Type: "image", Data: base64.StdEncoding.EncodeToString(opts.InputImage), MimeType: mimeType,
		})
	}
	for _, ref := range opts.ReferenceImages {
		mimeType := ref.MimeType
		if mimeType == "" {
			mimeType = "image/png"
		}
		images = append(images, omniContentItem{
			Type: "image", Data: base64.StdEncoding.EncodeToString(ref.Data), MimeType: mimeType,
		})
	}

	if len(images) > 0 {
		// The Interactions API examples list images first, then the text
		// prompt describing how to use them.
		req.Input = append(images, omniContentItem{Type: "text", Text: prompt})
	} else {
		req.Input = prompt
	}

	a.logger.Infof("Generating video with Gemini Omni model %s, prompt: %q", a.modelID, prompt)
	interaction, err := a.createInteraction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("gemini omni GenerateVideos failed: %w", err)
	}

	interaction, err = a.waitForInteraction(ctx, interaction)
	if err != nil {
		return nil, err
	}

	if interaction.Status != "completed" {
		return nil, fmt.Errorf("gemini omni interaction %q ended with status %q", interaction.ID, interaction.Status)
	}

	result := &llmtypes.VideoGenerationResponse{}
	for _, step := range interaction.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, content := range step.Content {
			if content.Type != "video" {
				continue
			}
			mimeType := content.MimeType
			if mimeType == "" {
				mimeType = "video/mp4"
			}
			video := llmtypes.GeneratedVideo{MimeType: mimeType, URI: content.URI}
			if content.Data != "" {
				raw, decodeErr := base64.StdEncoding.DecodeString(content.Data)
				if decodeErr != nil {
					return nil, fmt.Errorf("gemini omni returned undecodable video data: %w", decodeErr)
				}
				video.Data = raw
			} else if content.URI != "" {
				raw, downloadErr := a.downloadURI(ctx, content.URI)
				if downloadErr != nil {
					return nil, fmt.Errorf("failed to download generated video %q: %w", content.URI, downloadErr)
				}
				video.Data = raw
			}
			result.Videos = append(result.Videos, video)
		}
	}

	if len(result.Videos) == 0 {
		return nil, fmt.Errorf("gemini omni returned no video data")
	}

	a.logger.Infof("Generated %d video(s) successfully via Gemini Omni (interaction %s)", len(result.Videos), interaction.ID)
	return result, nil
}

func (a *GeminiOmniAdapter) createInteraction(ctx context.Context, body omniInteractionRequest) (*omniInteractionResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal interaction request: %w", err)
	}
	return a.doRequest(ctx, http.MethodPost, a.baseURL+"/interactions", payload)
}

func (a *GeminiOmniAdapter) getInteraction(ctx context.Context, id string) (*omniInteractionResponse, error) {
	return a.doRequest(ctx, http.MethodGet, a.baseURL+"/interactions/"+id, nil)
}

func (a *GeminiOmniAdapter) doRequest(ctx context.Context, method, url string, payload []byte) (*omniInteractionResponse, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	httpReq.Header.Set("x-goog-api-key", a.apiKey)
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr omniErrorResponse
		if jsonErr := json.Unmarshal(respBody, &apiErr); jsonErr == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("gemini omni API error (%s): %s", apiErr.Error.Code, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("gemini omni API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var interaction omniInteractionResponse
	if err := json.Unmarshal(respBody, &interaction); err != nil {
		return nil, fmt.Errorf("failed to parse interaction response: %w", err)
	}
	return &interaction, nil
}

func (a *GeminiOmniAdapter) downloadURI(ctx context.Context, uri string) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build download request: %w", err)
	}
	httpReq.Header.Set("x-goog-api-key", a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download returned status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

// waitForInteraction polls a not-yet-terminal interaction until it reaches a
// terminal status. Simple text/image-to-video requests complete synchronously
// on the initial POST; this exists for the documented `background` mode.
func (a *GeminiOmniAdapter) waitForInteraction(ctx context.Context, interaction *omniInteractionResponse) (*omniInteractionResponse, error) {
	if interaction == nil {
		return nil, fmt.Errorf("gemini omni returned a nil interaction")
	}
	if isTerminalOmniStatus(interaction.Status) {
		return interaction, nil
	}

	interval := a.pollInterval
	if interval <= 0 {
		interval = defaultOmniPollInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	current := interaction
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			next, err := a.getInteraction(ctx, current.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to poll Gemini Omni interaction: %w", err)
			}
			current = next
			if isTerminalOmniStatus(current.Status) {
				return current, nil
			}
		}
	}
}

func isTerminalOmniStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "cancelled", "incomplete", "budget_exceeded":
		return true
	default:
		return false
	}
}
