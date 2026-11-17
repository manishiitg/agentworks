package claudecode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type sdkImageFrameWriter struct {
	bytes.Buffer
	process *claudeSDKProcess
	frames  []map[string]any
	largest int
}

func (w *sdkImageFrameWriter) Close() error                      { return nil }
func (w *sdkImageFrameWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *sdkImageFrameWriter) Write(b []byte) (int, error) {
	w.largest = max(w.largest, len(b))
	n, _ := w.Buffer.Write(b)
	if bytes.Contains(b, []byte{'\n'}) {
		var request map[string]any
		if err := json.Unmarshal(w.Bytes(), &request); err != nil {
			return 0, err
		}
		w.Reset()
		w.frames = append(w.frames, request)
		if request["type"] == "steer" {
			w.process.acknowledgeControl(request["id"].(string), false, false)
		}
	}
	return n, nil
}

type sdkImageReader struct {
	io.Reader
	closed *int
}

func (r *sdkImageReader) Close() error { *r.closed++; return nil }

// PLAT-746-1000: prompt and active steer stream real image payloads, retaining
// only text receipts; retries do not replay images and a bad file keeps text.
func TestClaudeSDKImagesPromptSteerAndSkippedFile(t *testing.T) {
	owner := "image-chat"
	closed := 0
	data := strings.Repeat("pixels", 10000)
	input := llmtypes.NativeImageInput{ID: "admitted", Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		return &sdkImageReader{strings.NewReader(data), &closed}, "image/png", int64(len(data)), nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	withImage := func() context.Context {
		return llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{input})
	}
	wire := &sdkImageFrameWriter{}
	process := &claudeSDKProcess{session: &claudeSDKSession{owner: owner, input: &claudeSDKInput{w: wire}, kill: func() {}}, done: make(chan struct{})}
	wire.process = process
	promptCtx := withImage()
	params, closeImages, err := claudeSDKParamsWithImages(promptCtx, owner, map[string]any{"type": "prompt", "text": "Inspect this file"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = process.write(params); err != nil {
		t.Fatal(err)
	}
	closeImages()
	retry, closeRetry, err := claudeSDKParamsWithImages(promptCtx, owner, map[string]any{"type": "prompt", "text": "Retry"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	closeRetry()
	if _, ok := retry.(*claudeSDKImageParams); ok {
		t.Fatal("retry replayed images")
	}
	if err = process.steer(withImage(), "A follow-up"); err != nil {
		t.Fatal(err)
	}
	if closed != 2 || len(wire.frames) != 2 {
		t.Fatal("image resources leaked or a delivery was lost")
	}
	for _, frame := range wire.frames {
		images, ok := frame["images"].([]any)
		if !ok || len(images) != 1 {
			t.Fatal("missing native image")
		}
		image := images[0].(map[string]any)
		decoded, err := base64.StdEncoding.DecodeString(image["data"].(string))
		if err != nil || string(decoded) != data || image["mediaType"] != "image/png" {
			t.Fatal("native image changed")
		}
	}
	if wire.largest >= len(data) {
		t.Fatal("image marshaled as one full base64 buffer")
	}
	if strings.Contains(process.answering, "base64") || process.answering != "A follow-up" {
		t.Fatal("image payload entered receipt text")
	}
	skipped := false
	bad := llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		return nil, "", 0, errors.New("access revoked")
	}, Skipped: func(string) { skipped = true }}})
	if err = process.steer(bad, "Text survives"); err != nil {
		t.Fatal(err)
	}
	if !skipped || len(wire.frames) != 3 || wire.frames[2]["text"] != "Text survives" {
		t.Fatal("bad image swallowed text or notice")
	}
	// A turn that ends during decode must return a definite not-sent verdict.
	ended := llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		close(process.done)
		return io.NopCloser(strings.NewReader("pixels")), "image/png", 6, nil
	}}})
	if err = process.steer(ended, "Too late"); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatal("ended native turn was not queued safely", err)
	}
	if len(wire.frames) != 3 {
		t.Fatal("image written into ended turn")
	}
}

// #3031: drive the real control writer's end-before-ack branch. No provider
// process or model is needed; ending after the write is still ambiguous.
type sdkEndBeforeAckWriter struct{ done chan struct{} }

func (w *sdkEndBeforeAckWriter) Write(b []byte) (int, error) { close(w.done); return len(b), nil }
func (w *sdkEndBeforeAckWriter) Close() error                { return nil }
func TestClaudeSDKEndedBeforeAckIsTypedAndUncertain(t *testing.T) {
	done := make(chan struct{})
	process := &claudeSDKProcess{session: &claudeSDKSession{owner: "ended-before-ack", input: &claudeSDKInput{w: &sdkEndBeforeAckWriter{done: done}}, kill: func() {}}, done: done}
	err := process.steer(context.Background(), "follow up")
	if !errors.Is(err, llmtypes.ErrCodingAgentTurnEndedBeforeInputAck) || errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatalf("lost uncertain typed receipt: %v", err)
	}
}
