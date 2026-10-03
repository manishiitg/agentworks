package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var imageProviderModels = map[string][]string{
	// codex-cli's --model only selects which model orchestrates the tool
	// call (cost/latency); the native image_gen tool itself is the same
	// regardless, so there is no meaningfully different "model" to offer
	// here.
	"codex-cli": {"codex-cli"},
}

// ImageGenExecutorConfig holds configuration for the image generation executor
type ImageGenExecutorConfig struct {
	Provider        string // e.g. "codex-cli"
	ModelID         string // e.g. "gemini-3.1-flash-image"
	APIKey          string // optional; falls back to GEMINI_API_KEY env var on the server
	WorkspaceAPIURL string // workspace API base URL for saving generated images
	UserID          string // user ID for auth scoping
}

type imageGenContextKey string

const imageGenRuntimeOverrideKey imageGenContextKey = "image_gen_runtime_override"

type ImageGenRuntimeOverride struct {
	Provider string
	ModelID  string
	APIKey   string
}

// GetWorkspaceImageToolCategory returns the category name used by image tools.
// Image tools live inside workspace_advanced so step configs with workspace_advanced:*
// automatically get image_gen/image_edit — no separate category toggle needed.
func GetWorkspaceImageToolCategory() string {
	return "workspace_advanced"
}

// IsImageTool reports whether the given tool name is a workspace image tool
// (image_gen or image_edit). Used by override wiring that can't rely on
// category name anymore since image tools share the workspace_advanced category.
func IsImageTool(name string) bool {
	return name == "image_gen" || name == "image_edit"
}

// GetImageGenToolCategory returns the category name for the image gen tool.
func GetImageGenToolCategory() string {
	return GetWorkspaceImageToolCategory()
}

// GetImageGenToolDefinition returns the image_gen tool definition
func GetImageGenToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{
		Function: &llmtypes.FunctionDefinition{
			Name:        "image_gen",
			Description: "Generate images using AI from a text prompt. Requires a full absolute output_path under the workspace docs root so the caller decides exactly where the generated image files should be stored. See the image-prompting skill for how to write an effective prompt. Supports aspect ratio, resolution, number of images, and negative prompt options.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "Text prompt describing the image to generate from scratch. To edit an existing image instead, use the image_edit tool.",
					},
					"output_path": map[string]interface{}{
						"type":        "string",
						"description": "Required full absolute destination path under the workspace docs root for the generated image. Example: '/Users/.../workspace-docs/_users/default/Chats/generated-images/hero.png' or '/app/workspace-docs/Workflow/my-flow/assets/hero.png'. Workspace-relative paths are rejected. If number_of_images is greater than 1, this path is used as the base name and files are saved as '-1', '-2', etc. before the extension.",
					},
					"quality": map[string]interface{}{
						"type":        "string",
						"description": "Optional rendering quality hint (from GPT Image 2.5's own vocabulary). Not a guaranteed structured control — codex-cli has no real quality API parameter, so this is folded into the prompt as a plain-language instruction and honored on a best-effort basis by whatever model actually runs.",
						"enum":        []interface{}{"auto", "low", "medium", "high", "xhigh", "max"},
					},
					"size": map[string]interface{}{
						"type":        "string",
						"description": "Optional exact output size hint, e.g. '1024x1024', '1536x1024', '1024x1536', '2048x2048', '2048x1152', '3840x2160', '2160x3840', or 'auto'. Best-effort like quality — folded into the prompt as text, not a structured parameter; prefer aspect_ratio/resolution below when an exact size is not the point.",
					},
					"background": map[string]interface{}{
						"type":        "string",
						"description": "Optional background hint. 'transparent' only makes sense for formats that support alpha (PNG/WebP) — say so in output_path's extension too. Best-effort like quality — folded into the prompt as text, not a structured parameter.",
						"enum":        []interface{}{"auto", "opaque", "transparent"},
					},
					"aspect_ratio": map[string]interface{}{
						"type":        "string",
						"description": "The aspect ratio for the generated image.",
						"enum":        []interface{}{"1:1", "2:3", "3:2", "3:4", "4:3", "9:16", "16:9", "21:9"},
					},
					"resolution": map[string]interface{}{
						"type":        "string",
						"description": "Output image resolution. Defaults to '1K'.",
						"enum":        []interface{}{"1K", "2K", "4K"},
					},
					"number_of_images": map[string]interface{}{
						"type":        "integer",
						"description": "Number of images to generate (1-4). Defaults to 1.",
						"minimum":     1,
						"maximum":     4,
					},
					"negative_prompt": map[string]interface{}{
						"type":        "string",
						"description": "Optional description of what to exclude from the generated images.",
					},
				},
				"required": []interface{}{"prompt", "output_path"},
			}),
		},
	}
}

// imageGenResult is the JSON structure returned to the LLM
type imageGenResult struct {
	Model         string   `json:"model"`
	CostPerImage  *float64 `json:"cost_per_image,omitempty"`
	TotalCost     *float64 `json:"total_cost_usd,omitempty"`
	CostNote      string   `json:"cost_note,omitempty"`
	Prompt        string   `json:"prompt"`
	SavedPaths    []string `json:"saved_paths,omitempty"`
	AbsolutePaths []string `json:"absolute_paths,omitempty"`
	Count         int      `json:"count"`
	Note          string   `json:"note,omitempty"`
}

const (
	defaultImageGenProvider = "codex-cli"
	defaultImageGenModelID  = "codex-cli"
)

// imageRequirementsSuffix folds quality/size/background into the prompt text
// itself. codex-cli has no real structured API parameter for any of these,
// so text is the only way to pass them through at all -- honored on a
// best-effort basis by whatever model actually runs, never guaranteed.
// "auto" on any of them means "no opinion", so it is left out rather than
// cluttering the prompt with a no-op.
func imageRequirementsSuffix(args map[string]interface{}) string {
	var parts []string
	if q, ok := args["quality"].(string); ok && q != "" && q != "auto" {
		parts = append(parts, "quality: "+q)
	}
	if s, ok := args["size"].(string); ok && s != "" && s != "auto" {
		parts = append(parts, "exact output size: "+s)
	}
	if b, ok := args["background"].(string); ok && b != "" && b != "auto" {
		parts = append(parts, "background: "+b)
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\nImage requirements — " + strings.Join(parts, ", ") + "."
}

func defaultImageModelForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex-cli":
		return "codex-cli"
	default:
		return defaultImageGenModelID
	}
}

func normalizeImageModelAlias(provider, modelID string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	normalizedModelID := strings.ToLower(strings.TrimSpace(modelID))
	if normalizedModelID == "" || normalizedModelID == provider {
		return defaultImageModelForProvider(provider)
	}
	return strings.TrimSpace(modelID)
}

func isSupportedImageModel(provider, modelID string) bool {
	models := imageProviderModels[strings.ToLower(strings.TrimSpace(provider))]
	normalizedModelID := strings.ToLower(strings.TrimSpace(modelID))
	for _, model := range models {
		if strings.ToLower(strings.TrimSpace(model)) == normalizedModelID {
			return true
		}
	}
	return false
}

func inferImageProviderFromModel(modelID string) string {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case modelID == "codex-cli", modelID == "gpt-5.4", modelID == "gpt-5.4-mini", modelID == "gpt-5.3-codex":
		return "codex-cli"
	default:
		return ""
	}
}

func normalizeImageProviderAndModel(provider, modelID string) (string, string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelID = strings.TrimSpace(modelID)

	if provider == "" && modelID != "" {
		provider = inferImageProviderFromModel(modelID)
	}
	if provider == "" {
		provider = defaultImageGenProvider
	}
	modelID = normalizeImageModelAlias(provider, modelID)

	switch provider {
	case "codex-cli":
		if !isSupportedImageModel(provider, modelID) {
			return "", "", fmt.Errorf("unsupported image generation model %q for provider %q. %s", modelID, provider, imageModelsSummaryForProvider(provider))
		}
		return provider, modelID, nil
	default:
		return "", "", fmt.Errorf("unsupported image generation provider %q. %s", provider, supportedImageProviderSummary())
	}
}

func hasImageProviderAuth(provider string, apiKeys *llm.ProviderAPIKeys) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex-cli":
		return apiKeys != nil && apiKeys.CodexCLI != nil && strings.TrimSpace(*apiKeys.CodexCLI) != ""
	default:
		return false
	}
}

func supportedImageProviderSummary() string {
	return "Supported image provider: codex-cli"
}

func imageModelsSummaryForProvider(provider string) string {
	models := imageProviderModels[strings.ToLower(strings.TrimSpace(provider))]
	if len(models) == 0 {
		return supportedImageProviderSummary()
	}
	return fmt.Sprintf("Supported models for provider %q: %s", provider, strings.Join(models, ", "))
}

// imageGenerationCostMetadata reports per-image cost. codex-cli is the only
// generation provider and is token-priced, so there is no fixed per-image
// cost to report.
func imageGenerationCostMetadata(_, _ string) (*float64, string) {
	return nil, "Token-priced via Codex CLI; fixed per-image cost is not available. This is not free."
}

func wrapImageGenerationSelectionError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(
		"image generation setup is incomplete: %w. Add workspace provider auth with set_provider_auth(provider=\"codex-cli\", api_key=\"...\"). %s",
		err,
		supportedImageProviderSummary(),
	)
}

func wrapImageGenerationInitializationError(provider, modelID string, err error) error {
	if err == nil {
		return nil
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "codex-cli":
		return fmt.Errorf(
			"image generation could not start for provider %q and model %q: %w. To fix this, set workspace auth with set_provider_auth(provider=\"codex-cli\", api_key=\"...\") or change the workspace image generation defaults to another provider with matching auth. %s",
			provider, modelID, err,
			imageModelsSummaryForProvider(provider),
		)
	default:
		return fmt.Errorf(
			"image generation could not start for provider %q and model %q: %w. Configure provider auth with set_provider_auth(...) or update the workspace image generation defaults. %s",
			provider, modelID, err, supportedImageProviderSummary(),
		)
	}
}

func imageExtensionForMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func resolveImageOutputPaths(outputPath string, count int, mimeType string) ([]string, error) {
	cleanPath := path.Clean(strings.TrimSpace(outputPath))
	if cleanPath == "" || cleanPath == "." {
		return nil, fmt.Errorf("output_path is required")
	}
	if strings.HasPrefix(cleanPath, "/") {
		return nil, fmt.Errorf("output_path must be normalized under the workspace docs root")
	}
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return nil, fmt.Errorf("output_path must stay inside the workspace")
	}

	dir := path.Dir(cleanPath)
	if dir == "." || dir == "" {
		return nil, fmt.Errorf("output_path must include a workspace folder, e.g. Chats/... or Workflow/...")
	}

	ext := "." + imageExtensionForMIME(mimeType)
	baseWithoutExt := strings.TrimSuffix(cleanPath, path.Ext(cleanPath))
	if baseWithoutExt == "" {
		return nil, fmt.Errorf("output_path must include a file name")
	}

	if count <= 1 {
		return []string{baseWithoutExt + ext}, nil
	}

	paths := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		paths = append(paths, fmt.Sprintf("%s-%d%s", baseWithoutExt, i, ext))
	}
	return paths, nil
}

func validateGuardedImageOutputPath(ctx context.Context, cfg ImageGenExecutorConfig, outputPath string) error {
	cleanOutputPath := path.Clean(strings.TrimSpace(outputPath))
	if _, err := resolveImageOutputPaths(cleanOutputPath, 1, "image/png"); err != nil {
		return err
	}
	if cfg.WorkspaceAPIURL == "" {
		return fmt.Errorf("image generation requires a workspace API URL so output_path can be saved in the workspace")
	}

	guardClient := workspace.NewClient(
		cfg.WorkspaceAPIURL,
		workspace.WithUserID(cfg.UserID),
	)
	if !guardClient.HasEffectiveWriteGuard(ctx) {
		return fmt.Errorf("output_path must stay inside the current session's writable folder, but no active session/workflow write guard was found")
	}

	outputDir := path.Dir(cleanOutputPath)
	if err := guardClient.ValidatePathWithContext(ctx, outputDir, true); err != nil {
		return fmt.Errorf("output_path directory is outside the current session's writable folder: %w", err)
	}
	if err := guardClient.ValidatePathWithContext(ctx, cleanOutputPath, true); err != nil {
		return fmt.Errorf("output_path is outside the current session's writable folder: %w", err)
	}
	return nil
}

func resolveImageGenerationTarget(ctx context.Context, cfg ImageGenExecutorConfig) (string, string, *llm.ProviderAPIKeys, error) {
	apiKeys := loadWorkspaceProviderAPIKeys(ctx, cfg.WorkspaceAPIURL)

	explicitProvider := strings.TrimSpace(cfg.Provider)
	explicitModelID := strings.TrimSpace(cfg.ModelID)
	if explicitProvider != "" || explicitModelID != "" {
		provider, modelID, err := normalizeImageProviderAndModel(explicitProvider, explicitModelID)
		return provider, modelID, apiKeys, err
	}

	if cfg.WorkspaceAPIURL != "" {
		imageCfg, exists, err := services.LoadImageGenerationConfig(ctx, cfg.WorkspaceAPIURL)
		if err != nil {
			log.Printf("[IMAGE_GEN] Failed to load image generation config: %v", err)
		} else if exists && imageCfg != nil {
			var candidates []services.ImageGenerationModelConfig
			if imageCfg.Primary != nil {
				candidates = append(candidates, *imageCfg.Primary)
			}

			var sawCandidate bool
			for _, candidate := range candidates {
				provider, modelID, err := normalizeImageProviderAndModel(candidate.Provider, candidate.ModelID)
				if err != nil {
					continue
				}
				sawCandidate = true
				if strings.TrimSpace(cfg.APIKey) != "" || hasImageProviderAuth(provider, apiKeys) {
					return provider, modelID, apiKeys, nil
				}
			}
			if sawCandidate {
				return "", "", apiKeys, fmt.Errorf("image generation defaults require matching workspace provider auth or an explicit api_key override")
			}
		}
	}

	return defaultImageGenProvider, defaultImageGenModelID, apiKeys, nil
}

func applyImageGenRuntimeOverride(ctx context.Context, cfg ImageGenExecutorConfig) ImageGenExecutorConfig {
	override, ok := ctx.Value(imageGenRuntimeOverrideKey).(ImageGenRuntimeOverride)
	if !ok {
		return cfg
	}
	if override.Provider != "" {
		cfg.Provider = override.Provider
	}
	if override.ModelID != "" {
		cfg.ModelID = override.ModelID
	}
	if override.APIKey != "" {
		cfg.APIKey = override.APIKey
	}
	return cfg
}

// CreateImageGenExecutor returns an executor that calls InitializeImageGenerationModel,
// then saves the generated images to the caller-provided workspace output path.
func CreateImageGenExecutor(cfg ImageGenExecutorConfig) func(ctx context.Context, args map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		cfg = applyImageGenRuntimeOverride(ctx, cfg)
		prompt, _ := args["prompt"].(string)
		if prompt == "" {
			return "", fmt.Errorf("prompt is required")
		}
		outputPath, _ := args["output_path"].(string)
		normalizedOutputPath, err := normalizeRequiredAbsoluteWorkspaceDocumentPath(outputPath, "output_path")
		if err != nil {
			return "", err
		}
		outputPath = normalizedOutputPath
		if err := validateGuardedImageOutputPath(ctx, cfg, outputPath); err != nil {
			return "", err
		}

		provider, modelID, workspaceAPIKeys, err := resolveImageGenerationTarget(ctx, cfg)
		if err != nil {
			return "", wrapImageGenerationSelectionError(err)
		}

		var apiKeyPtr *string
		if cfg.APIKey != "" {
			k := cfg.APIKey
			apiKeyPtr = &k
		}

		providerAPIKeys := workspaceAPIKeys
		if providerAPIKeys == nil {
			providerAPIKeys = &llm.ProviderAPIKeys{}
		}
		if apiKeyPtr != nil {
			providerAPIKeys.CodexCLI = apiKeyPtr
		}

		imageGenCfg := llm.Config{
			Provider: llm.Provider(provider),
			ModelID:  modelID,
			APIKeys:  providerAPIKeys,
			Context:  ctx,
		}

		log.Printf("[IMAGE_GEN] Initializing model: provider=%s model=%s apiKeyProvided=%v workspaceURL=%q userID=%q",
			provider, modelID, cfg.APIKey != "", cfg.WorkspaceAPIURL, cfg.UserID)

		model, err := llm.InitializeImageGenerationModel(imageGenCfg)
		if err != nil {
			log.Printf("[IMAGE_GEN] Failed to initialize image generation model: %v", err)
			return "", wrapImageGenerationInitializationError(provider, modelID, err)
		}
		log.Printf("[IMAGE_GEN] Model initialized successfully")

		// Build options from args
		var opts []llmtypes.ImageGenerationOption
		if ar, ok := args["aspect_ratio"].(string); ok && ar != "" {
			log.Printf("[IMAGE_GEN] aspect_ratio=%s", ar)
			opts = append(opts, llmtypes.WithAspectRatio(ar))
		}
		if res, ok := args["resolution"].(string); ok && res != "" {
			log.Printf("[IMAGE_GEN] resolution=%s", res)
			opts = append(opts, llmtypes.WithResolution(res))
		}
		if n, ok := args["number_of_images"].(float64); ok && n >= 1 {
			log.Printf("[IMAGE_GEN] number_of_images=%d", int(n))
			opts = append(opts, llmtypes.WithNumberOfImages(int(n)))
		}
		if np, ok := args["negative_prompt"].(string); ok && np != "" {
			log.Printf("[IMAGE_GEN] negative_prompt set (%d chars)", len(np))
			opts = append(opts, llmtypes.WithNegativePrompt(np))
		}
		if suffix := imageRequirementsSuffix(args); suffix != "" {
			log.Printf("[IMAGE_GEN] requirements folded into prompt:%s", suffix)
			prompt += suffix
		}

		log.Printf("[IMAGE_GEN] generate image: prompt=%q model=%s", prompt, modelID)
		resp, err := model.GenerateImages(ctx, prompt, opts...)
		if err != nil {
			log.Printf("[IMAGE_GEN] GenerateImages failed: %v", err)
			return "", fmt.Errorf("image generation failed: %w", err)
		}

		if len(resp.Images) == 0 {
			return "", fmt.Errorf("image generation returned no images (prompt may have been filtered by safety filters)")
		}

		log.Printf("[IMAGE_GEN] Generated %d image(s) with model=%s", len(resp.Images), modelID)

		var savedPaths []string
		var absolutePaths []string
		cleanOutputPath := path.Clean(strings.TrimSpace(outputPath))
		outputDir := path.Dir(cleanOutputPath)
		wsClient := workspace.NewClient(
			cfg.WorkspaceAPIURL,
			workspace.WithUserID(cfg.UserID),
			workspace.WithFolderGuard(&workspace.FolderGuardConfig{
				Enabled:    true,
				WritePaths: []string{outputDir + "/"},
			}),
		)
		if err := wsClient.CreateFolder(ctx, outputDir); err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "409") || strings.Contains(errStr, "already exists") {
				log.Printf("[IMAGE_GEN] %s folder already exists, proceeding", outputDir)
			} else {
				return "", fmt.Errorf("failed to prepare output folder %q: %w", outputDir, err)
			}
		}

		for i, img := range resp.Images {
			mimeType := img.MimeType
			if mimeType == "" {
				mimeType = "image/png"
			}

			targetPaths, pathErr := resolveImageOutputPaths(outputPath, len(resp.Images), mimeType)
			if pathErr != nil {
				return "", pathErr
			}
			targetPath := targetPaths[i]
			folderPath := path.Dir(targetPath)
			fileName := path.Base(targetPath)
			savedPath, saveErr := wsClient.UploadBinary(ctx, folderPath, fileName, img.Data)
			if saveErr != nil {
				return "", fmt.Errorf("failed to save generated image %d to workspace path %q: %w", i+1, targetPath, saveErr)
			}
			log.Printf("[IMAGE_GEN] Saved image %d to workspace: %s", i+1, savedPath)
			savedPaths = append(savedPaths, savedPath)
			absolutePaths = append(absolutePaths, workspaceAbsolutePath(savedPath))
		}

		costPerImage, costNote := imageGenerationCostMetadata(provider, modelID)
		var totalCost *float64
		if costPerImage != nil {
			cost := *costPerImage * float64(len(resp.Images))
			totalCost = &cost
			recordPricedToolCost(ctx, cfg.WorkspaceAPIURL, cfg.UserID, pricedToolCost{
				ToolName:    "image_gen",
				Capability:  "image_gen",
				Provider:    provider,
				ModelID:     modelID,
				Unit:        "image",
				Quantity:    float64(len(resp.Images)),
				Count:       len(resp.Images),
				TotalCost:   cost,
				OutputPaths: savedPaths,
			})
		}
		result := imageGenResult{
			Model:         modelID,
			CostPerImage:  costPerImage,
			TotalCost:     totalCost,
			CostNote:      costNote,
			Prompt:        prompt,
			SavedPaths:    savedPaths,
			AbsolutePaths: absolutePaths,
			Count:         len(resp.Images),
			Note:          "",
		}
		if costPerImage != nil {
			log.Printf("[IMAGE_GEN] Done: saved=%d costPerImage=$%.4f", len(savedPaths), *costPerImage)
		} else {
			log.Printf("[IMAGE_GEN] Done: saved=%d costNote=%q", len(savedPaths), costNote)
		}

		resultJSON, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("failed to marshal image generation result: %w", err)
		}

		return string(resultJSON), nil
	}
}

// GetImageEditToolCategory returns the category name for the image edit tool.
func GetImageEditToolCategory() string {
	return GetWorkspaceImageToolCategory()
}

// CreateWorkspaceImageTools returns the LLM-visible workspace image tools.
func CreateWorkspaceImageTools() []llmtypes.Tool {
	return []llmtypes.Tool{
		GetImageGenToolDefinition(),
		GetImageEditToolDefinition(),
	}
}

// CreateWorkspaceImageToolExecutors creates workspace image executors with the provided config.
func CreateWorkspaceImageToolExecutors(cfg ImageGenExecutorConfig) map[string]func(ctx context.Context, args map[string]any) (string, error) {
	return map[string]func(ctx context.Context, args map[string]any) (string, error){
		GetImageGenToolDefinition().Function.Name:  CreateImageGenExecutor(cfg),
		GetImageEditToolDefinition().Function.Name: CreateImageEditExecutor(cfg),
	}
}

// MergeImageToolExecutors creates image tool executors from cfg and merges them
// into executors (typed map). If categories is non-nil, each tool is also categorized.
func MergeImageToolExecutors(cfg ImageGenExecutorConfig, executors map[string]func(ctx context.Context, args map[string]any) (string, error), categories map[string]string) {
	cat := GetWorkspaceImageToolCategory()
	for name, exec := range CreateWorkspaceImageToolExecutors(cfg) {
		executors[name] = exec
		if categories != nil {
			categories[name] = cat
		}
	}
}

// MergeImageToolExecutorsUntyped is like MergeImageToolExecutors but accepts
// map[string]any (used by workflow/workshop paths where executor maps are untyped).
func MergeImageToolExecutorsUntyped(cfg ImageGenExecutorConfig, executors map[string]any, categories map[string]string) {
	cat := GetWorkspaceImageToolCategory()
	for name, exec := range CreateWorkspaceImageToolExecutors(cfg) {
		executors[name] = exec
		if categories != nil {
			categories[name] = cat
		}
	}
}

// WrapImageToolExecutorWithRuntimeOverride injects a per-session image provider/model override.
func WrapImageToolExecutorWithRuntimeOverride(
	inner func(ctx context.Context, args map[string]any) (string, error),
	override ImageGenRuntimeOverride,
) func(ctx context.Context, args map[string]any) (string, error) {
	override.Provider = strings.TrimSpace(override.Provider)
	override.ModelID = strings.TrimSpace(override.ModelID)
	override.APIKey = strings.TrimSpace(override.APIKey)
	if override.Provider == "" && override.ModelID == "" && override.APIKey == "" {
		return inner
	}
	return func(ctx context.Context, args map[string]any) (string, error) {
		ctx = context.WithValue(ctx, imageGenRuntimeOverrideKey, override)
		return inner(ctx, args)
	}
}

// GetImageEditToolDefinition returns the image_edit tool definition
func GetImageEditToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{
		Function: &llmtypes.FunctionDefinition{
			Name:        "image_edit",
			Description: "Edit an existing image from the workspace using a text instruction. Requires full absolute image_path and output_path values under the workspace docs root. See the image-prompting skill for how to write an effective edit instruction. Displays results inline.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"image_path": map[string]interface{}{
						"type":        "string",
						"description": "Full absolute workspace-docs path of the image to edit (e.g. '/Users/.../workspace-docs/_users/default/Chats/generated-images/image.png'). Use the absolute_paths value from a prior image_gen result. Workspace-relative paths are rejected.",
					},
					"output_path": map[string]interface{}{
						"type":        "string",
						"description": "Required full absolute destination path under the workspace docs root for the edited image. Example: '/Users/.../workspace-docs/_users/default/Chats/generated-images/edited.png'. Workspace-relative paths are rejected. If multiple images are returned, this path is used as the base name and files are saved as '-1', '-2', etc. before the extension.",
					},
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "Instruction describing how to edit the image. Be explicit — describe the full desired result rather than relative changes.",
					},
					"aspect_ratio": map[string]interface{}{
						"type":        "string",
						"description": "Output aspect ratio. Defaults to the input image's ratio.",
						"enum":        []interface{}{"1:1", "2:3", "3:2", "3:4", "4:3", "9:16", "16:9", "21:9"},
					},
					"resolution": map[string]interface{}{
						"type":        "string",
						"description": "Output resolution. Defaults to '1K'.",
						"enum":        []interface{}{"1K", "2K", "4K"},
					},
					"quality": map[string]interface{}{
						"type":        "string",
						"description": "Optional rendering quality hint (from GPT Image 2.5's own vocabulary). Not a guaranteed structured control — codex-cli has no real quality API parameter, so this is folded into the prompt as a plain-language instruction and honored on a best-effort basis by whatever model actually runs.",
						"enum":        []interface{}{"auto", "low", "medium", "high", "xhigh", "max"},
					},
					"size": map[string]interface{}{
						"type":        "string",
						"description": "Optional exact output size hint, e.g. '1024x1024', '1536x1024', '1024x1536', '2048x2048', '2048x1152', '3840x2160', '2160x3840', or 'auto'. Best-effort like quality — folded into the prompt as text, not a structured parameter; prefer aspect_ratio/resolution above when an exact size is not the point.",
					},
					"background": map[string]interface{}{
						"type":        "string",
						"description": "Optional background hint. 'transparent' only makes sense for formats that support alpha (PNG/WebP) — say so in output_path's extension too. Best-effort like quality — folded into the prompt as text, not a structured parameter.",
						"enum":        []interface{}{"auto", "opaque", "transparent"},
					},
				},
				"required": []interface{}{"image_path", "output_path", "prompt"},
			}),
		},
	}
}

// CreateImageEditExecutor returns an executor that fetches an image from the workspace,
// edits it using the image model, and saves the result back to the workspace.
func CreateImageEditExecutor(cfg ImageGenExecutorConfig) func(ctx context.Context, args map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		cfg = applyImageGenRuntimeOverride(ctx, cfg)
		imagePath, _ := args["image_path"].(string)
		normalizedImagePath, err := normalizeRequiredAbsoluteWorkspaceDocumentPath(imagePath, "image_path")
		if err != nil {
			return "", err
		}
		imagePath = normalizedImagePath
		outputPath, _ := args["output_path"].(string)
		normalizedOutputPath, err := normalizeRequiredAbsoluteWorkspaceDocumentPath(outputPath, "output_path")
		if err != nil {
			return "", err
		}
		outputPath = normalizedOutputPath
		if err := validateGuardedImageOutputPath(ctx, cfg, outputPath); err != nil {
			return "", err
		}
		prompt, _ := args["prompt"].(string)
		if prompt == "" {
			return "", fmt.Errorf("prompt is required")
		}
		if suffix := imageRequirementsSuffix(args); suffix != "" {
			log.Printf("[IMAGE_EDIT] requirements folded into prompt:%s", suffix)
			prompt += suffix
		}

		provider, modelID, workspaceAPIKeys, err := resolveImageGenerationTarget(ctx, cfg)
		if err != nil {
			return "", wrapImageGenerationSelectionError(err)
		}

		var apiKeyPtr *string
		if cfg.APIKey != "" {
			k := cfg.APIKey
			apiKeyPtr = &k
		}

		providerAPIKeys := workspaceAPIKeys
		if providerAPIKeys == nil {
			providerAPIKeys = &llm.ProviderAPIKeys{}
		}
		if apiKeyPtr != nil {
			providerAPIKeys.CodexCLI = apiKeyPtr
		}

		imageGenCfg := llm.Config{
			Provider: llm.Provider(provider),
			ModelID:  modelID,
			APIKeys:  providerAPIKeys,
			Context:  ctx,
		}

		log.Printf("[IMAGE_EDIT] Initializing model: provider=%s model=%s apiKeyProvided=%v workspaceURL=%q userID=%q",
			provider, modelID, cfg.APIKey != "", cfg.WorkspaceAPIURL, cfg.UserID)

		model, err := llm.InitializeImageGenerationModel(imageGenCfg)
		if err != nil {
			log.Printf("[IMAGE_EDIT] Failed to initialize image generation model: %v", err)
			return "", wrapImageGenerationInitializationError(provider, modelID, err)
		}
		log.Printf("[IMAGE_EDIT] Model initialized successfully")

		// Fetch the source image from workspace
		if cfg.WorkspaceAPIURL == "" {
			return "", fmt.Errorf("workspace API URL is required for image editing")
		}
		wsClient := workspace.NewClient(
			cfg.WorkspaceAPIURL,
			workspace.WithUserID(cfg.UserID),
		)
		log.Printf("[IMAGE_EDIT] Fetching source image from workspace: %s", imagePath)
		imgBytes, err := wsClient.DownloadFile(ctx, imagePath)
		if err != nil {
			return "", fmt.Errorf("failed to fetch source image from workspace: %w", err)
		}
		log.Printf("[IMAGE_EDIT] Fetched %d bytes from %s", len(imgBytes), imagePath)

		// Detect MIME type from file extension
		mimeType := "image/png"
		if len(imagePath) > 4 {
			switch imagePath[len(imagePath)-4:] {
			case ".jpg", "jpeg":
				mimeType = "image/jpeg"
			case "webp":
				mimeType = "image/webp"
			}
		}

		// Build options
		var opts []llmtypes.ImageGenerationOption
		log.Printf("[IMAGE_EDIT] Source image: %d bytes, detected mime=%s", len(imgBytes), mimeType)
		opts = append(opts, llmtypes.WithInputImage(imgBytes, mimeType))
		if ar, ok := args["aspect_ratio"].(string); ok && ar != "" {
			log.Printf("[IMAGE_EDIT] aspect_ratio=%s", ar)
			opts = append(opts, llmtypes.WithAspectRatio(ar))
		}
		if res, ok := args["resolution"].(string); ok && res != "" {
			log.Printf("[IMAGE_EDIT] resolution=%s", res)
			opts = append(opts, llmtypes.WithResolution(res))
		}

		log.Printf("[IMAGE_EDIT] Editing image: prompt=%q model=%s source=%s", prompt, modelID, imagePath)
		resp, err := model.GenerateImages(ctx, prompt, opts...)
		if err != nil {
			log.Printf("[IMAGE_EDIT] GenerateImages failed: %v", err)
			return "", fmt.Errorf("image editing failed: %w", err)
		}
		if len(resp.Images) == 0 {
			log.Printf("[IMAGE_EDIT] GenerateImages returned no images (filtered or unsupported)")
			return "", fmt.Errorf("image editing returned no images (prompt may have been filtered)")
		}
		log.Printf("[IMAGE_EDIT] Received %d edited image(s) from model", len(resp.Images))

		// Save edited images to workspace
		var savedPaths []string
		var absolutePaths []string
		cleanOutputPath := path.Clean(strings.TrimSpace(outputPath))
		outputDir := path.Dir(cleanOutputPath)

		saveClient := workspace.NewClient(
			cfg.WorkspaceAPIURL,
			workspace.WithUserID(cfg.UserID),
			workspace.WithFolderGuard(&workspace.FolderGuardConfig{
				Enabled:    true,
				WritePaths: []string{outputDir + "/"},
			}),
		)
		if err := saveClient.CreateFolder(ctx, outputDir); err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "409") || strings.Contains(errStr, "already exists") {
				log.Printf("[IMAGE_EDIT] %s folder already exists, proceeding", outputDir)
			} else {
				log.Printf("[IMAGE_EDIT] Warning: failed to create output folder: %v", err)
			}
		}

		for i, img := range resp.Images {
			imgMIME := img.MimeType
			if imgMIME == "" {
				imgMIME = "image/png"
			}
			targetPaths, pathErr := resolveImageOutputPaths(outputPath, len(resp.Images), imgMIME)
			if pathErr != nil {
				return "", pathErr
			}
			targetPath := targetPaths[i]
			folderPath := path.Dir(targetPath)
			fileName := path.Base(targetPath)
			savedPath, saveErr := saveClient.UploadBinary(ctx, folderPath, fileName, img.Data)
			if saveErr != nil {
				return "", fmt.Errorf("failed to save edited image %d to workspace path %q: %w", i+1, targetPath, saveErr)
			}
			log.Printf("[IMAGE_EDIT] Saved edited image %d: %s", i+1, savedPath)
			savedPaths = append(savedPaths, savedPath)
			absolutePaths = append(absolutePaths, workspaceAbsolutePath(savedPath))
		}

		costPerImage, costNote := imageGenerationCostMetadata(provider, modelID)
		var totalCost *float64
		if costPerImage != nil {
			cost := *costPerImage * float64(len(resp.Images))
			totalCost = &cost
			recordPricedToolCost(ctx, cfg.WorkspaceAPIURL, cfg.UserID, pricedToolCost{
				ToolName:    "image_edit",
				Capability:  "image_edit",
				Provider:    provider,
				ModelID:     modelID,
				Unit:        "image",
				Quantity:    float64(len(resp.Images)),
				Count:       len(resp.Images),
				TotalCost:   cost,
				OutputPaths: savedPaths,
			})
		}
		result := imageGenResult{
			Model:         modelID,
			CostPerImage:  costPerImage,
			TotalCost:     totalCost,
			CostNote:      costNote,
			Prompt:        prompt,
			SavedPaths:    savedPaths,
			AbsolutePaths: absolutePaths,
			Count:         len(resp.Images),
			Note:          "",
		}
		if costPerImage != nil {
			log.Printf("[IMAGE_EDIT] Done: saved=%d costPerImage=$%.4f", len(savedPaths), *costPerImage)
		} else {
			log.Printf("[IMAGE_EDIT] Done: saved=%d costNote=%q", len(savedPaths), costNote)
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("failed to marshal result: %w", err)
		}
		return string(resultJSON), nil
	}
}
