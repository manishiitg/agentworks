package codexcli

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexCLIRealImagePathAnalysis proves codex's own native vision --
// pointed at a local workspace file path, the same mechanism
// wrapReadImageWithLLM (agent_go) uses for read_image(provider="codex-cli")
// -- actually works, including under the same bridge-lockdown constraint
// (WithDisableShellTool) a real workflow-step session runs under. No prior
// test in this package verified image *understanding* at all; the existing
// codexcli_image_adapter.go/_test.go pair is for image *generation*
// (image_gen/image_edit), a different capability entirely.
func TestCodexCLIRealImagePathAnalysis(t *testing.T) {
	requireRealCodexCLISearchWebE2E(t)

	workspaceDir := t.TempDir()
	imagePath := filepath.Join(workspaceDir, "sample.png")
	writeSolidCodexTestPNG(t, imagePath, color.RGBA{R: 255, A: 255})

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, quietCodexStreamLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	prompt := fmt.Sprintf("Inspect the local image file at this workspace path:\n%s\n\nQuestion: What is the dominant color? Reply with one lowercase English color word.", imagePath)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: prompt},
			},
		},
	}, WithProjectDirID(workspaceDir), WithDisableShellTool(), WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("GenerateContent() returned no choices")
	}
	content := strings.ToLower(strings.TrimSpace(resp.Choices[0].Content))
	if !strings.Contains(content, "red") {
		t.Fatalf("expected image analysis to mention red, got %q", content)
	}
}

func writeSolidCodexTestPNG(t *testing.T, path string, pixel color.RGBA) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 48, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			img.SetRGBA(x, y, pixel)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create test image: %v", err)
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode test image: %v", err)
	}
}

// TestCodexCLIRealImageGeneration proves codex's own native image
// generation -- GenerateImages() with no input image, the same mechanism
// agent_go's image_gen tool uses for provider="codex-cli" -- actually
// produces a real, content-matching image under the workspace-write sandbox
// (not the former full approvals/sandbox bypass). No prior test in this
// package drove GenerateImages against the real CLI at all.
func TestCodexCLIRealImageGeneration(t *testing.T) {
	requireRealCodexCLISearchWebE2E(t)

	adapter := NewCodexCLIImageAdapter("", codexCLIRealContractModel, quietCodexStreamLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateImages(ctx,
		"A single flat pure red square filling the entire frame, no gradients, no other colors, no text, no background elements.",
		llmtypes.WithNumberOfImages(1),
	)
	if err != nil {
		t.Fatalf("GenerateImages() error = %v", err)
	}
	if resp == nil || len(resp.Images) == 0 {
		t.Fatal("GenerateImages() returned no images")
	}
	img := resp.Images[0]
	if len(img.Data) == 0 {
		t.Fatal("generated image has no data")
	}
	if !dominantColorIsRed(t, img.Data) {
		t.Fatalf("expected generated image to be dominantly red")
	}
}

// TestCodexCLIRealImageEditing proves codex's own native image editing --
// GenerateImages() with WithInputImage set, the same mechanism agent_go's
// image_edit tool uses for provider="codex-cli" -- actually edits the
// supplied reference image. This exact call previously failed every time:
// runSingleImageCommand appended "--image" and the path as two argv
// entries, which codex's clap-variadic --image flag swallowed together with
// the prompt string that followed, leaving codex with no prompt at all
// ("No prompt provided via stdin", exit 1). Fixed by passing a single
// "--image=<path>" token.
func TestCodexCLIRealImageEditing(t *testing.T) {
	requireRealCodexCLISearchWebE2E(t)

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "input.png")
	writeSolidCodexTestPNG(t, inputPath, color.RGBA{R: 255, A: 255})
	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read input image: %v", err)
	}

	adapter := NewCodexCLIImageAdapter("", codexCLIRealContractModel, quietCodexStreamLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateImages(ctx,
		"Recolor this reference image to a single flat pure blue, filling the entire frame. Keep it a simple solid color image with no other elements.",
		llmtypes.WithNumberOfImages(1),
		llmtypes.WithInputImage(inputData, "image/png"),
	)
	if err != nil {
		t.Fatalf("GenerateImages() error = %v", err)
	}
	if resp == nil || len(resp.Images) == 0 {
		t.Fatal("GenerateImages() returned no images")
	}
	img := resp.Images[0]
	if len(img.Data) == 0 {
		t.Fatal("edited image has no data")
	}
	if !dominantColorIsBlue(t, img.Data) {
		t.Fatalf("expected edited image to be recolored to dominantly blue")
	}
}

func dominantColorIsRed(t *testing.T, data []byte) bool {
	t.Helper()
	r, g, b := averageRGB(t, data)
	return r > 120 && r > g+40 && r > b+40
}

func dominantColorIsBlue(t *testing.T, data []byte) bool {
	t.Helper()
	r, g, b := averageRGB(t, data)
	return b > 120 && b > r+40 && b > g+40
}

func averageRGB(t *testing.T, data []byte) (int, int, int) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode generated image: %v", err)
	}
	bounds := img.Bounds()
	var rSum, gSum, bSum, n int64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			rSum += int64(r >> 8)
			gSum += int64(g >> 8)
			bSum += int64(b >> 8)
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return int(rSum / n), int(gSum / n), int(bSum / n)
}

func TestExtractImagePathFromLastMessage(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "plain path",
			content: "/tmp/generated.png\n",
			want:    "/tmp/generated.png",
		},
		{
			name:    "quoted path",
			content: "\"/tmp/generated.png\"\n",
			want:    "/tmp/generated.png",
		},
		{
			name:    "json path",
			content: `{"saved_path":"/tmp/generated.png"}`,
			want:    "/tmp/generated.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastMessagePath := filepath.Join(tmpDir, tt.name+".txt")
			if err := os.WriteFile(lastMessagePath, []byte(tt.content), 0600); err != nil {
				t.Fatalf("write last message file: %v", err)
			}

			got, err := extractImagePathFromLastMessage(lastMessagePath)
			if err != nil {
				t.Fatalf("extractImagePathFromLastMessage returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("extractImagePathFromLastMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMimeTypeForImageFile(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/tmp/test.png", want: "image/png"},
		{path: "/tmp/test.jpg", want: "image/jpeg"},
		{path: "/tmp/test.jpeg", want: "image/jpeg"},
		{path: "/tmp/test.webp", want: "image/webp"},
	}

	for _, tt := range tests {
		if got := mimeTypeForImageFile(tt.path, nil); got != tt.want {
			t.Fatalf("mimeTypeForImageFile(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
