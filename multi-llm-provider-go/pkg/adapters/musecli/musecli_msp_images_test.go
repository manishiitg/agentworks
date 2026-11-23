package musecli

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

type museImageFrameWriter struct {
	bytes.Buffer
	rpc     *museRPC
	largest int
	frames  int
}

func (w *museImageFrameWriter) Close() error                      { return nil }
func (w *museImageFrameWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *museImageFrameWriter) Write(p []byte) (int, error) {
	w.largest = max(w.largest, len(p))
	n, _ := w.Buffer.Write(p)
	if bytes.Contains(p, []byte{'\n'}) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Input []struct {
					Type       string `json:"type"`
					Text       string `json:"text"`
					MediaType  string `json:"mediaType"`
					Base64Data string `json:"base64Data"`
				} `json:"input"`
			} `json:"params"`
		}
		if err := json.Unmarshal(w.Bytes(), &request); err != nil {
			return 0, err
		}
		w.Reset()
		if len(request.Params.Input) != 2 || request.Params.Input[1].Type != "image" || request.Params.Input[1].MediaType != "image/png" {
			return 0, errors.New("missing MSP image block")
		}
		pixels, err := base64.StdEncoding.DecodeString(request.Params.Input[1].Base64Data)
		if err != nil || string(pixels) != strings.Repeat("pixels", 10000) {
			return 0, errors.New("wrong MSP image bytes")
		}
		w.frames++
		w.rpc.mu.Lock()
		ch := w.rpc.pending[string(request.ID)]
		w.rpc.mu.Unlock()
		ch <- museRPCMessage{Result: json.RawMessage(`{}`)}
	}
	return n, nil
}

// PLAT-746-1000: native parts stream without a pooled full-base64 copy;
// image-only input is valid, answered images are not new work, and a turn
// ending during preparation returns a definite not-sent result.
func TestMuseMSPImageInputBoundaries(t *testing.T) {
	owner := "muse-image-chat"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wire := &museImageFrameWriter{}
	rpc := &museRPC{stdin: wire, pending: map[string]chan museRPCMessage{}, done: make(chan struct{})}
	wire.rpc = rpc
	session := &museMSPSession{owner: owner, sessionID: "native-fixture", turnID: "active", dataHome: t.TempDir(), rpc: rpc}
	museMSPSessions.Lock()
	museMSPSessions.m[owner] = session
	museMSPSessions.Unlock()
	defer func() { museMSPSessions.Lock(); delete(museMSPSessions.m, owner); museMSPSessions.Unlock() }()
	data := strings.Repeat("pixels", 10000)
	input := llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		return io.NopCloser(strings.NewReader(data)), "image/png", int64(len(data)), nil
	}}
	imageCtx := llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{input})
	params, cleanup, err := museParamsWithImages(imageCtx, owner, map[string]any{"input": []map[string]any{{"type": "text", "text": "Inspect the path"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = rpc.call(ctx, "turn/start", params, nil); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if wire.largest >= len(data) || wire.frames != 1 {
		t.Fatal("image request buffered whole base64 payload or was lost")
	}
	retry, retryClose, err := museParamsWithImages(imageCtx, owner, map[string]any{"input": []map[string]any{{"type": "text", "text": "Retry"}}}, nil)
	retryClose()
	if _, ok := retry.(*museImageParams); err != nil || ok {
		t.Fatal("retry replayed an image", err)
	}
	late := llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		session.mu.Lock()
		session.turnID = ""
		session.mu.Unlock()
		return io.NopCloser(strings.NewReader(data)), "image/png", int64(len(data)), nil
	}}})
	if err = SendMuseMSPInput(late, owner, "Too late"); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatal("late input not queued safely", err)
	}
	if wire.frames != 1 || len(session.steers) != 0 {
		t.Fatal("late input left a native frame/receipt")
	}
	part := llmtypes.ImageContent{SourceType: "base64", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("pixels"))}
	pending := []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{part}}}
	if _, human, err := museMSPPrompt(ctx, owner, pending, true); err != nil || human != "" {
		t.Fatal("native image-only prompt rejected or stringified", err)
	}
	pending[0].Parts = append(pending[0].Parts, llmtypes.TextContent{Text: "first image path"})
	pending = append(pending, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "second image path"))
	if _, human, err := museMSPPrompt(ctx, owner, pending, true); err != nil || !strings.Contains(human, "first image path") || !strings.Contains(human, "second image path") || strings.Contains(human, part.Data) {
		t.Fatal("text/image pending boundary changed", err)
	}
	answered := append(pending, llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "answered"))
	if _, _, err := museMSPPrompt(ctx, owner, answered, true); err == nil {
		t.Fatal("answered image started empty work")
	}
}
