package vertex

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestVertexVeoAdapterRejectsLastFrameWithoutInputImage(t *testing.T) {
	adapter := NewVertexVeoAdapter(nil, "veo-3.1-generate-preview", &MockLogger{})

	_, err := adapter.GenerateVideos(context.Background(), "animate between frames",
		llmtypes.WithVideoLastFrame([]byte("last-frame"), "image/png"))
	if err == nil {
		t.Fatal("expected last frame without input image to fail before provider call")
	}
	if !strings.Contains(err.Error(), "last frame requires an input image") {
		t.Fatalf("error = %q, want input image requirement", err.Error())
	}
}

func TestGeminiOmniAdapterRejectsLastFrame(t *testing.T) {
	adapter := NewGeminiOmniAdapter("fake-key", "gemini-omni-flash-preview", &MockLogger{})

	_, err := adapter.GenerateVideos(context.Background(), "animate between frames",
		llmtypes.WithVideoInputImage([]byte("first-frame"), "image/png"),
		llmtypes.WithVideoLastFrame([]byte("last-frame"), "image/png"))
	if err == nil {
		t.Fatal("expected Gemini Omni last frame to fail before provider call")
	}
	if !strings.Contains(err.Error(), "use a Veo 3.1 model") {
		t.Fatalf("error = %q, want Veo guidance", err.Error())
	}
}
