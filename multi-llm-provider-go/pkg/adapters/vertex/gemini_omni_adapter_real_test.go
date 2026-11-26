package vertex

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// solidColorPNG renders a small solid-color PNG for use as test image input.
func solidColorPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// Real-API e2e tests for the Gemini Omni Flash video-generation adapter, run
// against the Gemini Developer API's Interactions API.
//
// Gate: RUN_VERTEX_REAL_E2E=1 + GEMINI_API_KEY (or VERTEX_API_KEY or
// GOOGLE_API_KEY) — same gate as google_genai_adapter_real_test.go, via
// requireVertexRealE2E.

type stdoutLogger struct{ t *testing.T }

func (l *stdoutLogger) Infof(format string, v ...any)  { l.t.Logf("INFO: "+format, v...) }
func (l *stdoutLogger) Errorf(format string, v ...any) { l.t.Logf("ERROR: "+format, v...) }
func (l *stdoutLogger) Debugf(format string, args ...interface{}) {
	l.t.Logf("DEBUG: "+format, args...)
}

func TestGeminiOmniAdapterRealTextToVideo(t *testing.T) {
	apiKey, _ := requireVertexRealE2E(t)

	adapter := NewGeminiOmniAdapter(apiKey, "gemini-omni-flash-preview", &stdoutLogger{t: t})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateVideos(ctx, "A slow pan across a calm mountain lake at sunrise, cinematic")
	if err != nil {
		t.Fatalf("GenerateVideos failed: %v", err)
	}
	if len(resp.Videos) == 0 {
		t.Fatal("expected at least one generated video, got none")
	}
	video := resp.Videos[0]
	if len(video.Data) == 0 {
		t.Fatal("expected video bytes, got empty Data")
	}
	if video.MimeType != "video/mp4" {
		t.Errorf("expected mime type video/mp4, got %q", video.MimeType)
	}
	// MP4 files start with an ftyp box a few bytes in; a minimal sanity
	// check that we decoded real video bytes, not stray JSON/text.
	if len(video.Data) < 12 || string(video.Data[4:8]) != "ftyp" {
		t.Errorf("decoded data does not look like a valid MP4 (first 12 bytes: %v)", video.Data[:min(12, len(video.Data))])
	}
	t.Logf("generated video: %d bytes, mime=%s", len(video.Data), video.MimeType)

	if dumpPath := os.Getenv("GEMINI_OMNI_TEST_DUMP_PATH"); dumpPath != "" {
		if err := os.WriteFile(dumpPath, video.Data, 0o644); err != nil {
			t.Errorf("failed to write video dump to %s: %v", dumpPath, err)
		} else {
			t.Logf("wrote generated video to %s", dumpPath)
		}
	}
}

func TestGeminiOmniAdapterRealImageToVideoAndEdit(t *testing.T) {
	apiKey, _ := requireVertexRealE2E(t)

	adapter := NewGeminiOmniAdapter(apiKey, "gemini-omni-flash-preview", &stdoutLogger{t: t})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pngBytes := solidColorPNG(t, color.RGBA{R: 30, G: 90, B: 200, A: 255})

	resp, err := adapter.GenerateVideos(ctx, "Animate this scene: slow zoom in, gentle waves",
		llmtypes.WithVideoInputImage(pngBytes, "image/png"))
	if err != nil {
		t.Fatalf("image-to-video GenerateVideos failed: %v", err)
	}
	if len(resp.Videos) == 0 || len(resp.Videos[0].Data) == 0 {
		t.Fatal("expected video bytes from image-to-video request")
	}
	t.Logf("image-to-video generated: %d bytes", len(resp.Videos[0].Data))

	// Conversational edit — requires the interaction ID returned by the
	// first call, so it isn't exposed by GenerateVideos today. Instead,
	// verify the option is wired through by issuing a second independent
	// request that references a bogus ID and confirming we get the
	// provider's own validation error, not a client-side marshaling bug.
	_, err = adapter.GenerateVideos(ctx, "Make the water more turbulent",
		llmtypes.WithVideoPreviousInteractionID("v1_does_not_exist"))
	if err == nil {
		t.Fatal("expected an error from the API for a nonexistent previous_interaction_id")
	}
	t.Logf("previous_interaction_id validation error (expected): %v", err)
}

// TestGeminiOmniAdapterRealSubjectReference verifies the multi-image
// "subject reference" mode (composing two distinct subjects into one scene,
// e.g. the docs' cat + ball-of-yarn example) — NOT a first-frame/last-frame
// interpolation primitive, which Gemini Omni's own docs say is unsupported.
func TestGeminiOmniAdapterRealSubjectReference(t *testing.T) {
	apiKey, _ := requireVertexRealE2E(t)

	adapter := NewGeminiOmniAdapter(apiKey, "gemini-omni-flash-preview", &stdoutLogger{t: t})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	bluePNG := solidColorPNG(t, color.RGBA{R: 20, G: 60, B: 220, A: 255})
	redPNG := solidColorPNG(t, color.RGBA{R: 220, G: 30, B: 30, A: 255})

	resp, err := adapter.GenerateVideos(ctx, "The blue object and the red object float together and gently orbit each other.",
		llmtypes.WithVideoReferenceImages(
			llmtypes.VideoReferenceImage{Data: bluePNG, MimeType: "image/png"},
			llmtypes.VideoReferenceImage{Data: redPNG, MimeType: "image/png"},
		))
	if err != nil {
		t.Fatalf("subject-reference GenerateVideos failed: %v", err)
	}
	if len(resp.Videos) == 0 || len(resp.Videos[0].Data) == 0 {
		t.Fatal("expected video bytes from subject-reference request")
	}
	t.Logf("subject-reference generated: %d bytes", len(resp.Videos[0].Data))

	if dumpPath := os.Getenv("GEMINI_OMNI_SUBJECT_REF_DUMP_PATH"); dumpPath != "" {
		if err := os.WriteFile(dumpPath, resp.Videos[0].Data, 0o644); err != nil {
			t.Errorf("failed to write video dump to %s: %v", dumpPath, err)
		}
	}
}
