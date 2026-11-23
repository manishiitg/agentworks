package codexcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// CodexCLIImageAdapter implements llmtypes.ImageGenerationModel by driving the
// native Codex CLI image generation flow, then reading the generated file bytes
// back into the standard ImageGenerationResponse shape used by the rest of the
// package.
type CodexCLIImageAdapter struct {
	apiKey  string
	modelID string
	logger  interfaces.Logger
}

// NewCodexCLIImageAdapter creates a new Codex CLI image generation adapter.
func NewCodexCLIImageAdapter(apiKey, modelID string, logger interfaces.Logger) *CodexCLIImageAdapter {
	if modelID == "" {
		modelID = "codex-cli"
	}
	return &CodexCLIImageAdapter{
		apiKey:  apiKey,
		modelID: modelID,
		logger:  logger,
	}
}

// GenerateImages implements llmtypes.ImageGenerationModel.
func (a *CodexCLIImageAdapter) GenerateImages(ctx context.Context, prompt string, options ...llmtypes.ImageGenerationOption) (*llmtypes.ImageGenerationResponse, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return nil, fmt.Errorf("codex cli not found in PATH. Please install it first (npm install -g @openai/codex)")
	}

	opts := &llmtypes.ImageGenerationOptions{
		NumberOfImages: 1,
		AspectRatio:    "1:1",
		Resolution:     "1K",
	}
	for _, opt := range options {
		opt(opts)
	}

	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if opts.NumberOfImages <= 0 {
		opts.NumberOfImages = 1
	}
	if opts.InputImageURL != "" {
		return nil, fmt.Errorf("codex cli image generation does not support input_image_url; pass image bytes with WithInputImage instead")
	}

	workdir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve current working directory: %w", err)
	}

	tempDir, err := os.MkdirTemp(workdir, ".codex-image-*")
	if err != nil {
		return nil, fmt.Errorf("create codex image temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	var inputImagePath string
	if len(opts.InputImage) > 0 {
		inputImagePath = filepath.Join(tempDir, "input"+extensionForImageMIMEType(opts.InputImageMIMEType))
		if err := os.WriteFile(inputImagePath, opts.InputImage, 0600); err != nil {
			return nil, fmt.Errorf("write codex input image: %w", err)
		}
	}

	result := &llmtypes.ImageGenerationResponse{
		Images: make([]llmtypes.GeneratedImage, 0, opts.NumberOfImages),
	}
	for i := 0; i < opts.NumberOfImages; i++ {
		outputPath := filepath.Join(tempDir, fmt.Sprintf("generated-%02d.png", i+1))
		lastMessagePath := filepath.Join(tempDir, fmt.Sprintf("last-message-%02d.txt", i+1))

		if err := a.runSingleImageCommand(ctx, workdir, prompt, outputPath, lastMessagePath, inputImagePath, opts, i); err != nil {
			return nil, err
		}

		resolvedPath, err := extractImagePathFromLastMessage(lastMessagePath)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(workdir, resolvedPath)
		}
		if _, err := os.Stat(resolvedPath); err != nil {
			fallbackPath, fallbackErr := newestImageInDir(tempDir)
			if fallbackErr != nil {
				return nil, fmt.Errorf("codex cli reported image path %q but it does not exist: %w", resolvedPath, err)
			}
			resolvedPath = fallbackPath
		}

		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			return nil, fmt.Errorf("read generated codex image %q: %w", resolvedPath, err)
		}

		mimeType := mimeTypeForImageFile(resolvedPath, data)
		result.Images = append(result.Images, llmtypes.GeneratedImage{
			Data:     data,
			MimeType: mimeType,
		})
	}

	return result, nil
}

func (a *CodexCLIImageAdapter) runSingleImageCommand(ctx context.Context, workdir, prompt, outputPath, lastMessagePath, inputImagePath string, opts *llmtypes.ImageGenerationOptions, index int) error {
	// --ask-for-approval is a GLOBAL flag and must precede "exec" (same
	// ordering constraint as buildCodexStructuredArgs). "never" replaces the
	// former --dangerously-bypass-approvals-and-sandbox: verified live that
	// codex's built-in image_gen tool needs no approval prompt at all, so
	// full bypass bought nothing here except an unnecessarily wide blast
	// radius (danger-full-access instead of workspace-write).
	args := []string{
		"--ask-for-approval", "never",
		"exec",
		"--ephemeral",
		"--skip-git-repo-check",
		// workspace-write, not the former full-bypass sandbox: codex's
		// image_gen skill writes the generated file under
		// $CODEX_HOME/generated_images/ (outside any sandbox, since that's
		// its own built-in tool storage) then shells out to `cp` it into
		// outputPath -- verified live that a workspace-write sandbox still
		// permits that copy because outputPath is inside workdir. Do NOT
		// also disable shell_tool (codexBridgeOnlyDisabledFeatures) here:
		// disabling it would leave the generated file stranded outside the
		// workspace with no way for codex to relocate it.
		"--sandbox", "workspace-write",
		"-C", workdir,
		"-o", lastMessagePath,
	}
	args = appendCodexDisableUpdateArgs(args)

	modelToUse := resolveCodexCLIModelID(a.modelID)
	if modelToUse != "" && modelToUse != "codex-cli" {
		args = append(args, "--model", modelToUse)
	}
	if inputImagePath != "" {
		// Must be a single "--image=<path>" token, not two argv entries:
		// codex's --image flag is clap-variadic (<FILE>...) and greedily
		// swallows the very next positional-looking argument too -- which
		// was the prompt string, appended right after this in the pre-fix
		// code. That left codex with an empty PROMPT positional, so it fell
		// back to reading stdin, found nothing, and exited 1
		// ("No prompt provided via stdin") on every image_edit call.
		// Verified live: this exact two-arg form reproduced that failure;
		// the "=" form here does not.
		args = append(args, "--image="+inputImagePath)
	}
	args = append(args, buildCodexImagePrompt(prompt, outputPath, opts, index, inputImagePath != ""))

	cmd := exec.CommandContext(ctx, "codex", args...)
	env := os.Environ()
	if strings.TrimSpace(a.apiKey) != "" {
		env = append(env, "CODEX_API_KEY="+a.apiKey)
	}
	cmd.Env = env

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if a.logger != nil {
		a.logger.Infof("[CODEX IMAGE] Running codex image generation model=%s output=%s", a.modelID, outputPath)
	}

	if err := cmd.Run(); err != nil {
		stderrOutput := strings.TrimSpace(stderrBuf.String())
		if stderrOutput != "" {
			return fmt.Errorf("codex cli image generation failed: %s", stderrOutput)
		}
		return fmt.Errorf("codex cli image generation failed: %w", err)
	}
	return nil
}

func buildCodexImagePrompt(prompt, outputPath string, opts *llmtypes.ImageGenerationOptions, index int, isEdit bool) string {
	var sb strings.Builder
	if isEdit {
		sb.WriteString("Use your native image generation or editing capability to create exactly one edited image based on the attached reference image.\n")
	} else {
		sb.WriteString("Use your native image generation capability to create exactly one image.\n")
	}
	sb.WriteString("Do not write code. Do not use apply_patch.\n")
	sb.WriteString(fmt.Sprintf("Save the final image to %s in the current workspace.\n", outputPath))
	sb.WriteString("After generation, reply with exactly the saved file path only.\n")
	sb.WriteString("\nPrompt:\n")
	sb.WriteString(strings.TrimSpace(prompt))
	sb.WriteString("\n")
	if strings.TrimSpace(opts.AspectRatio) != "" {
		sb.WriteString(fmt.Sprintf("Aspect ratio: %s\n", opts.AspectRatio))
	}
	if strings.TrimSpace(opts.Resolution) != "" {
		sb.WriteString(fmt.Sprintf("Resolution target: %s\n", opts.Resolution))
	}
	if strings.TrimSpace(opts.NegativePrompt) != "" {
		sb.WriteString(fmt.Sprintf("Avoid: %s\n", opts.NegativePrompt))
	}
	if opts.NumberOfImages > 1 {
		sb.WriteString(fmt.Sprintf("Variant number: %d of %d. Make it distinct while staying faithful to the same brief.\n", index+1, opts.NumberOfImages))
	}
	return sb.String()
}

func extractImagePathFromLastMessage(lastMessagePath string) (string, error) {
	data, err := os.ReadFile(lastMessagePath)
	if err != nil {
		return "", fmt.Errorf("read codex last message file: %w", err)
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return "", fmt.Errorf("codex last message file was empty")
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		for _, key := range []string{"saved_path", "path", "output_path", "file_path"} {
			if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value), nil
			}
		}
	}

	raw = strings.Trim(raw, "\"")
	return raw, nil
}

func newestImageInDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read codex temp dir: %w", err)
	}

	var newestPath string
	var newestModTime int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp":
		default:
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() > newestModTime {
			newestModTime = info.ModTime().UnixNano()
			newestPath = filepath.Join(dir, entry.Name())
		}
	}
	if newestPath == "" {
		return "", fmt.Errorf("no generated image files found in %s", dir)
	}
	return newestPath, nil
}

func extensionForImageMIMEType(mimeType string) string {
	switch strings.TrimSpace(strings.ToLower(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func mimeTypeForImageFile(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".png":
		return "image/png"
	}

	detected := http.DetectContentType(data)
	if strings.HasPrefix(detected, "image/") {
		return detected
	}
	return "image/png"
}
