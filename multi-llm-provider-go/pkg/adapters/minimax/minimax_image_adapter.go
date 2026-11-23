package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	MiniMaxImageEndpoint = "https://api.minimax.io/v1/image_generation"

	// Image generation models
	ModelMiniMaxImage01 = "image-01"
)

// minimaxImageModels holds pricing metadata for MiniMax image generation models
var minimaxImageModels = map[string]llmtypes.ModelMetadata{
	ModelMiniMaxImage01: {
		ModelID:      ModelMiniMaxImage01,
		ModelName:    "MiniMax Image 01",
		Provider:     "minimax",
		CostPerImage: 0.0035,
	},
}

// MiniMaxImageAdapter implements llmtypes.ImageGenerationModel for MiniMax.
// Uses POST https://api.minimax.io/v1/image_generation
type MiniMaxImageAdapter struct {
	apiKey  string
	modelID string
	logger  interfaces.Logger
	client  *http.Client
}

// GetModelMetadata returns pricing metadata for the given MiniMax image model.
func (a *MiniMaxImageAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if modelID == "" {
		modelID = a.modelID
	}
	if meta, ok := minimaxImageModels[modelID]; ok {
		return &meta, nil
	}
	return nil, fmt.Errorf("no metadata found for MiniMax image model: %s", modelID)
}

// NewMiniMaxImageAdapter creates a new MiniMax image generation adapter.
func NewMiniMaxImageAdapter(apiKey, modelID string, logger interfaces.Logger) *MiniMaxImageAdapter {
	if modelID == "" {
		modelID = ModelMiniMaxImage01
	}
	return &MiniMaxImageAdapter{
		apiKey:  apiKey,
		modelID: modelID,
		logger:  logger,
		client:  &http.Client{},
	}
}

// imageSubjectReference is a reference image for subject-based editing.
// ImageFile is a URL string pointing to the reference image.
type imageSubjectReference struct {
	Type      string `json:"type"`
	ImageFile string `json:"image_file"`
}

// imageGenRequest is the request body for MiniMax image generation
type imageGenRequest struct {
	Model            string                  `json:"model"`
	Prompt           string                  `json:"prompt"`
	N                int                     `json:"n,omitempty"`
	AspectRatio      string                  `json:"aspect_ratio,omitempty"`
	ResponseFormat   string                  `json:"response_format,omitempty"`
	PromptOptimizer  bool                    `json:"prompt_optimizer,omitempty"`
	SubjectReference []imageSubjectReference `json:"subject_reference,omitempty"`
}

// imageGenResponse is the response body from MiniMax image generation.
// With response_format="url": data.image_urls contains signed URLs.
type imageGenResponse struct {
	ID   string `json:"id"`
	Data struct {
		ImageURLs []string `json:"image_urls"`
	} `json:"data"`
	Metadata *struct {
		SuccessCount string `json:"success_count"`
		FailedCount  string `json:"failed_count"`
	} `json:"metadata,omitempty"`
	BaseResp *struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp,omitempty"`
}

// GenerateImages implements llmtypes.ImageGenerationModel
func (a *MiniMaxImageAdapter) GenerateImages(ctx context.Context, prompt string, options ...llmtypes.ImageGenerationOption) (*llmtypes.ImageGenerationResponse, error) {
	opts := &llmtypes.ImageGenerationOptions{
		NumberOfImages: 1,
		AspectRatio:    "1:1",
	}
	for _, opt := range options {
		opt(opts)
	}

	// Prefer the official MiniMax CLI whenever we can express the request locally.
	// Keep the HTTP API path for URL-based subject-reference editing and as a fallback
	// for plain generation when the CLI path is unavailable.
	if opts.InputImageURL == "" {
		if result, err := a.generateImagesViaCLI(ctx, prompt, opts); err == nil {
			return result, nil
		} else {
			if len(opts.InputImage) > 0 {
				return nil, fmt.Errorf("MiniMax CLI image editing failed: %w", err)
			}
			if a.logger != nil {
				a.logger.Infof("[MINIMAX IMAGE] Falling back to HTTP API after CLI failure: %v", err)
			}
		}
	}

	reqBody := imageGenRequest{
		Model:           a.modelID,
		Prompt:          prompt,
		N:               opts.NumberOfImages,
		AspectRatio:     opts.AspectRatio,
		ResponseFormat:  "url",
		PromptOptimizer: true,
	}

	// Support image editing via subject_reference when a reference image URL is provided
	if opts.InputImageURL != "" {
		reqBody.SubjectReference = []imageSubjectReference{
			{
				Type:      "character",
				ImageFile: opts.InputImageURL,
			},
		}
		if a.logger != nil {
			a.logger.Infof("[MINIMAX IMAGE] Using subject_reference URL for image editing")
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("minimax image gen: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, MiniMaxImageEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("minimax image gen: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	if a.logger != nil {
		a.logger.Infof("[MINIMAX IMAGE] Generating image model=%s aspect_ratio=%s", a.modelID, opts.AspectRatio)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("minimax image gen: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("minimax image gen: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("minimax image gen: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var genResp imageGenResponse
	if err := json.Unmarshal(respBytes, &genResp); err != nil {
		return nil, fmt.Errorf("minimax image gen: unmarshal response: %w", err)
	}

	if genResp.BaseResp != nil && genResp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("minimax image gen API error %d: %s", genResp.BaseResp.StatusCode, genResp.BaseResp.StatusMsg)
	}

	result := &llmtypes.ImageGenerationResponse{
		Images: make([]llmtypes.GeneratedImage, 0, len(genResp.Data.ImageURLs)),
	}

	for i, imgURL := range genResp.Data.ImageURLs {
		imgBytes, mimeType, dlErr := downloadImage(ctx, imgURL)
		if dlErr != nil {
			if a.logger != nil {
				a.logger.Errorf("[MINIMAX IMAGE] Failed to download image %d: %v", i, dlErr)
			}
			continue
		}
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		result.Images = append(result.Images, llmtypes.GeneratedImage{
			Data:     imgBytes,
			MimeType: mimeType,
		})
	}

	if a.logger != nil {
		a.logger.Infof("[MINIMAX IMAGE] Generated %d image(s) successfully", len(result.Images))
	}

	return result, nil
}

func (a *MiniMaxImageAdapter) generateImagesViaCLI(ctx context.Context, prompt string, opts *llmtypes.ImageGenerationOptions) (*llmtypes.ImageGenerationResponse, error) {
	mmxPath, err := exec.LookPath("mmx")
	if err != nil {
		return nil, fmt.Errorf("MiniMax CLI (mmx) not found on PATH")
	}

	outDir, err := os.MkdirTemp("", "minimax-image-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	args := []string{"image", "generate", "--prompt", prompt, "--out-dir", outDir, "--out-prefix", "image"}
	if opts.NumberOfImages > 0 {
		args = append(args, "--n", fmt.Sprintf("%d", opts.NumberOfImages))
	}
	if strings.TrimSpace(opts.AspectRatio) != "" {
		args = append(args, "--aspect-ratio", opts.AspectRatio)
	}
	if len(opts.InputImage) > 0 {
		referencePath := filepath.Join(outDir, "subject-reference.png")
		if err := os.WriteFile(referencePath, opts.InputImage, 0600); err != nil {
			return nil, fmt.Errorf("write MiniMax subject reference: %w", err)
		}
		args = append(args, "--subject-ref", fmt.Sprintf("type=character,image=%s", referencePath))
	}

	cmd := exec.CommandContext(ctx, mmxPath, args...)
	cmd.Env = buildMiniMaxSearchEnv(a.apiKey)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if a.logger != nil {
		a.logger.Infof("[MINIMAX IMAGE] Generating image via mmx CLI model=%s aspect_ratio=%s n=%d", a.modelID, opts.AspectRatio, opts.NumberOfImages)
	}

	if err := cmd.Run(); err != nil {
		if stderrOutput := strings.TrimSpace(stderrBuf.String()); stderrOutput != "" {
			return nil, fmt.Errorf("MiniMax CLI image generation failed: %s", stderrOutput)
		}
		return nil, fmt.Errorf("MiniMax CLI image generation failed: %w", err)
	}

	imagePaths, err := collectGeneratedImagePaths(outDir)
	if err != nil {
		return nil, err
	}

	result := &llmtypes.ImageGenerationResponse{
		Images: make([]llmtypes.GeneratedImage, 0, len(imagePaths)),
	}
	for _, path := range imagePaths {
		data, err := os.ReadFile(path)
		if err != nil {
			if a.logger != nil {
				a.logger.Errorf("[MINIMAX IMAGE] Failed to read generated image %s: %v", path, err)
			}
			continue
		}
		result.Images = append(result.Images, llmtypes.GeneratedImage{
			Data:     data,
			MimeType: mimeTypeFromPath(path),
		})
	}

	if len(result.Images) == 0 {
		if stderrOutput := strings.TrimSpace(stderrBuf.String()); stderrOutput != "" {
			return nil, fmt.Errorf("MiniMax CLI generated no readable images: %s", stderrOutput)
		}
		return nil, fmt.Errorf("MiniMax CLI generated no readable images")
	}

	if a.logger != nil {
		a.logger.Infof("[MINIMAX IMAGE] Generated %d image(s) successfully via mmx CLI", len(result.Images))
	}
	return result, nil
}

func collectGeneratedImagePaths(outDir string) ([]string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, fmt.Errorf("read generated image directory: %w", err)
	}

	var imagePaths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(outDir, entry.Name())
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".png", ".jpg", ".jpeg", ".webp":
			imagePaths = append(imagePaths, path)
		}
	}

	sort.Strings(imagePaths)
	if len(imagePaths) == 0 {
		return nil, fmt.Errorf("MiniMax CLI did not produce any image files")
	}
	return imagePaths, nil
}

func mimeTypeFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

// downloadImage fetches image bytes from a URL
func downloadImage(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return data, mimeType, nil
}
