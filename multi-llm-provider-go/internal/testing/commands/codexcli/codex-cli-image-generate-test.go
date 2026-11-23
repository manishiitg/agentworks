package codexcli

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/internal/testing"
)

var CodexCLIImageGenerateTestCmd = &cobra.Command{
	Use:   "codex-cli-image-generate",
	Short: "Test Codex CLI native image generation",
	Run:   runCodexCLIImageGenerateTest,
}

var (
	codexImageModel     string
	codexImagePrompt    string
	codexImageAspect    string
	codexImageNum       int
	codexImageOutputDir string
)

func init() {
	CodexCLIImageGenerateTestCmd.Flags().StringVar(&codexImageModel, "model", "codex-cli", "Codex CLI image-capable model to use; codex-cli means use the CLI default without passing --model")
	CodexCLIImageGenerateTestCmd.Flags().StringVar(&codexImagePrompt, "prompt", "", "Text prompt for image generation")
	CodexCLIImageGenerateTestCmd.Flags().StringVar(&codexImageAspect, "aspect-ratio", "16:9", "Aspect ratio to request")
	CodexCLIImageGenerateTestCmd.Flags().IntVar(&codexImageNum, "num-images", 1, "Number of images to generate")
	CodexCLIImageGenerateTestCmd.Flags().StringVar(&codexImageOutputDir, "output-dir", ".", "Directory to save generated images")
}

func runCodexCLIImageGenerateTest(cmd *cobra.Command, args []string) {
	_ = godotenv.Load(".env")

	prompt := codexImagePrompt
	if prompt == "" {
		prompt = "A polished complex infographic about the modern LLM stack with clear labeled sections for models, tools, retrieval, agents, evaluation, guardrails, and deployment"
	}

	log.Printf("Testing Codex CLI image generation with model: %s", codexImageModel)
	log.Printf("Prompt: %s", prompt)

	logger := testing.GetTestLogger()
	imageGen, err := llmproviders.InitializeImageGenerationModel(llmproviders.Config{
		Provider: llmproviders.ProviderCodexCLI,
		ModelID:  codexImageModel,
		Logger:   logger,
		Context:  context.Background(),
	})
	if err != nil {
		log.Fatalf("Failed to initialize Codex CLI image model: %v", err)
	}

	var genOpts []llmproviders.ImageGenerationOption
	genOpts = append(genOpts, llmproviders.WithNumberOfImages(codexImageNum))
	genOpts = append(genOpts, llmproviders.WithAspectRatio(codexImageAspect))

	log.Printf("Calling GenerateImages...")
	resp, err := imageGen.GenerateImages(context.Background(), prompt, genOpts...)
	if err != nil {
		log.Fatalf("GenerateImages failed: %v", err)
	}

	log.Printf("Generated %d image(s)", len(resp.Images))
	if len(resp.Images) == 0 {
		log.Fatalf("GenerateImages returned no images")
	}

	if err := os.MkdirAll(codexImageOutputDir, 0750); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	for i, img := range resp.Images {
		if len(img.Data) == 0 {
			log.Fatalf("Image %d returned empty data", i+1)
		}
		if contentType := http.DetectContentType(img.Data); !strings.HasPrefix(contentType, "image/") {
			log.Fatalf("Image %d detected content type %q, want image/*", i+1, contentType)
		}

		ext := ".png"
		switch img.MimeType {
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		}

		filename := filepath.Join(codexImageOutputDir, fmt.Sprintf("codex_image_%d%s", i+1, ext))
		if err := os.WriteFile(filename, img.Data, 0600); err != nil {
			log.Printf("Failed to save image %d: %v", i+1, err)
			continue
		}
		log.Printf("Saved image %d (%s, %d bytes) -> %s", i+1, img.MimeType, len(img.Data), filename)
	}

	log.Printf("Codex CLI image generation test completed successfully")
}
