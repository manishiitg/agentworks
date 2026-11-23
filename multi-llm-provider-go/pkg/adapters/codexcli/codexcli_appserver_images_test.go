package codexcli

import (
	"bufio"
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

type imageFrameWriter struct {
	bytes.Buffer
	rpc          *codexRPC
	largestWrite int
	frames       []codexRPCMessage
}

func (w *imageFrameWriter) Close() error                      { return nil }
func (w *imageFrameWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *imageFrameWriter) Write(p []byte) (int, error) {
	w.largestWrite = max(w.largestWrite, len(p))
	n, _ := w.Buffer.Write(p)
	if bytes.Contains(p, []byte{'\n'}) {
		var message codexRPCMessage
		if err := json.Unmarshal(w.Bytes(), &message); err != nil {
			return 0, err
		}
		w.Reset()
		w.frames = append(w.frames, message)
		w.rpc.mu.Lock()
		ch := w.rpc.pending[string(message.ID)]
		w.rpc.mu.Unlock()
		if ch != nil {
			ch <- codexRPCMessage{Result: json.RawMessage(`{"turn":{"id":"turn"}}`)}
		}
	}
	return n, nil
}

type imageClosedReader struct {
	io.Reader
	closed *int
}

func (r *imageClosedReader) Close() error { *r.closed++; return nil }

// PLAT-746-1000: prompt and steer carry actual image blocks, while attachment
// capabilities cannot cross chats, preparation refusal writes no partial RPC,
// and large image payloads are streamed rather than marshaled in one buffer.
func TestCodexNativeImagesPromptSteerAndRefusedDelivery(t *testing.T) {
	owner := "image-chat"
	rpc, _ := goalFixtureRPC(nil)
	wire := &imageFrameWriter{rpc: rpc}
	rpc.stdin = wire
	data := strings.Repeat("pixels", 10000)
	closed, opened := 0, 0
	input := llmtypes.NativeImageInput{ID: "admitted", Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		opened++
		return &imageClosedReader{strings.NewReader(data), &closed}, "image/png", int64(len(data)), nil
	}}
	baseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := llmtypes.WithNativeImageInputs(baseCtx, owner, []llmtypes.NativeImageInput{input})
	params, cleanup, err := codexParamsWithImages(ctx, owner, codexAppTurnParams("thread", "Question", &llmtypes.CallOptions{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = startCodexGoalTurn(ctx, &codexAppSession{owner: owner}, rpc, "thread", params, false, func(llmtypes.StreamChunk) {}); err != nil {
		t.Fatal(err)
	}
	cleanup()
	session := &codexAppSession{owner: owner, rpc: rpc, threadID: "thread", turnID: "turn"}
	codexAppSessions.Lock()
	codexAppSessions.sessions[owner] = session
	codexAppSessions.Unlock()
	defer func() { codexAppSessions.Lock(); delete(codexAppSessions.sessions, owner); codexAppSessions.Unlock() }()
	retryParams, retryClose, retryErr := codexParamsWithImages(ctx, owner, codexAppTurnParams("thread", "retry", &llmtypes.CallOptions{}), nil)
	retryClose()
	if retryErr != nil {
		t.Fatal(retryErr)
	}
	if _, ok := retryParams.(*codexImageParams); ok {
		t.Fatal("images replayed on a model retry")
	}
	steerCtx := llmtypes.WithNativeImageInputs(baseCtx, owner, []llmtypes.NativeImageInput{input})
	if err = SendCodexAppServerInput(steerCtx, owner, "A follow-up"); err != nil {
		t.Fatal(err)
	}
	if closed != 2 || opened != 2 {
		t.Fatalf("delivery resources opened=%d closed=%d", opened, closed)
	}
	if len(wire.frames) != 2 || wire.frames[0].Method != "turn/start" || wire.frames[1].Method != "turn/steer" {
		t.Fatal("prompt or steer not delivered")
	}
	for _, frame := range wire.frames {
		var body struct {
			Input          []struct{ Type, Text, URL string }
			ExpectedTurnID string `json:"expectedTurnId"`
		}
		if err = json.Unmarshal(frame.Params, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Input) != 2 || body.Input[1].Type != "image" {
			t.Fatal("missing native image")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(body.Input[1].URL, "data:image/png;base64,"))
		if err != nil || string(decoded) != data {
			t.Fatal("native image changed")
		}
		if frame.Method == "turn/steer" && body.ExpectedTurnID != "turn" {
			t.Fatal("steer lost owned turn fence")
		}
	}
	if wire.largestWrite >= len(data) {
		t.Fatal("base64 request buffered as a full image")
	}
	if len(session.receipts) != 1 || strings.Contains(session.receipts[0].text, "base64") {
		t.Fatal("image bytes entered receipt history")
	}
	params, cleanup, err = codexParamsWithImages(ctx, "another-chat", codexAppTurnParams("other", "text", &llmtypes.CallOptions{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, ok := params.(*codexImageParams); ok || opened != 2 {
		t.Fatal("attachment capability crossed chats")
	}
	bad := llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{input, {Open: func(context.Context) (io.ReadCloser, string, int64, error) {
		return nil, "", 0, errors.New("access revoked")
	}}})
	if err = SendCodexAppServerInput(bad, owner, "Text survives a bad attachment"); err != nil {
		t.Fatal(err)
	}
	if len(wire.frames) != 3 || wire.Len() != 0 || closed != 3 || len(session.receipts) != 2 {
		t.Fatal("bad image swallowed text or leaked a resource")
	}
}

// A native echo can exceed the ordinary non-image frame limit. It must be
// stripped before entering the event queue, without changing text escapes or
// splitting the next RPC frame. No image data belongs in the host transcript.
func TestCodexImageEchoIsDiscardedBeforeEventQueue(t *testing.T) {
	wire := `{"method":"item/started","params":{"item":{"type":"userMessage","content":[{"type":"text","text":"Question \"quoted\""},{"type":"image","url":"data:image/png;base64,` + strings.Repeat("A", 17<<20) + `"}]}}}` + "\n" + `{"id":"next","result":{}}` + "\n"
	reader := bufio.NewReaderSize(strings.NewReader(wire), 4096)
	frame, err := readCodexImageSafeFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) > 1024 || bytes.Contains(frame, []byte("data:image")) || !json.Valid(frame) {
		t.Fatal("image echo retained or JSON changed")
	}
	short, err := readCodexImageSafeFrame(bufio.NewReader(strings.NewReader(`{"text":""}` + "\n")))
	if err != nil || !json.Valid(short) {
		t.Fatal("short final string framing failed", err)
	}
	next, err := readCodexImageSafeFrame(reader)
	if err != nil || !bytes.Contains(next, []byte("next")) {
		t.Fatal("next RPC frame lost", err)
	}
}

// PR176/4573583545: queued human inputs must keep images from their earlier
// pending message, but an assistant boundary must not replay an answered image.
func TestCodexNativeImagesFollowPendingHumanMessages(t *testing.T) {
	imageMessage := llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.ImageContent{SourceType: "base64", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("fixture"))}}}
	text := llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "What is in it?")
	answered := llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "Already answered")
	for _, tc := range []struct {
		name     string
		messages []llmtypes.MessageContent
		want     bool
	}{
		{"earlier_pending_image", []llmtypes.MessageContent{answered, imageMessage, text}, true},
		{"answered_image", []llmtypes.MessageContent{imageMessage, answered}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params, closeImages, err := codexParamsWithImages(context.Background(), "chat", codexAppTurnParams("thread", buildCodexInteractivePrompt(tc.messages), &llmtypes.CallOptions{}), tc.messages)
			if err != nil {
				t.Fatal(err)
			}
			defer closeImages()
			images, ok := params.(*codexImageParams)
			if ok != tc.want {
				t.Fatal("image selection diverged from pending text")
			}
			if !tc.want {
				_, err := (&CodexCLIAdapter{}).generateContentAppServer(context.Background(), tc.messages, &llmtypes.CallOptions{})
				if err == nil || !strings.Contains(err.Error(), "prompt is empty") {
					t.Fatal("answered image started an empty native turn", err)
				}
			}
			if ok {
				var wire bytes.Buffer
				if err = images.writeJSON(&wire); err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(wire.Bytes(), []byte("data:image/png;base64,")) {
					t.Fatal("pending image missing from native wire")
				}
			}
		})
	}
}
